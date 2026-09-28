package api

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-sqlite3"
	"rassvet-chat/service/internal/deviceauth"
	"rassvet-chat/service/internal/uuid7"
)

type caller struct {
	userID string
	role   string
	body   []byte
}

func Handler(db *sql.DB, nodes ...Node) http.Handler {
	return HandlerWithStorage(db, "", nodes...)
}

func HandlerWithStorage(db *sql.DB, storageDir string, nodes ...Node) http.Handler {
	return HandlerWithPeerStorage(db, storageDir, "", nodes...)
}

func HandlerWithPeerStorage(db *sql.DB, storageDir, nodeID string, nodes ...Node) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "Рассвет: узел работает. Для подключения откройте приложение и используйте код активации или QR-код.\n")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/peer/") {
			if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
				errorJSON(w, http.StatusUnauthorized, "unauthenticated")
				return
			}
			if r.URL.Path == "/v1/peer/health" && r.Method == http.MethodGet {
				writeJSON(w, http.StatusOK, map[string]any{"node_id": nodeID, "protocol": 1})
				return
			}
			if r.URL.Path == "/v1/peer/events" {
				peerEvents(w, r, db, nodeID, storageDir)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/v1/peer/attachments/") {
				peerAttachment(w, r, db, storageDir)
				return
			}
			errorJSON(w, http.StatusNotFound, "not_found")
			return
		}
		if r.URL.Path == "/v1/activation" {
			activate(w, r, db, nodes)
			return
		}
		if !knownRoute(r.URL.Path) {
			errorJSON(w, http.StatusNotFound, "not_found")
			return
		}
		if strings.Contains(r.URL.Path, "/attachments") && strings.HasPrefix(r.URL.Path, "/v1/rooms/") {
			attachments(w, r, db, storageDir)
			return
		}
		actor, ok := authenticate(w, r, db)
		if !ok {
			return
		}
		switch {
		case r.URL.Path == "/v1/me":
			me(w, r, db, actor)
		case r.URL.Path == "/v1/admin/users":
			adminUsers(w, r, db, actor)
		case strings.HasPrefix(r.URL.Path, "/v1/admin/users/"):
			adminUser(w, r, db, actor, nodes)
		case r.URL.Path == "/v1/admin/settings":
			adminSettings(w, r, db, actor)
		case r.URL.Path == "/v1/admin/rooms":
			adminRooms(w, r, db, actor)
		case strings.HasPrefix(r.URL.Path, "/v1/admin/rooms/"):
			if strings.Contains(r.URL.Path, "/members") {
				adminMembers(w, r, db, actor)
			} else {
				adminRoom(w, r, db, actor)
			}
		case strings.HasPrefix(r.URL.Path, "/v1/rooms"):
			rooms(w, r, db, actor)
		}
	})
}

func knownRoute(path string) bool {
	return path == "/v1/me" || path == "/v1/admin/users" || strings.HasPrefix(path, "/v1/admin/users/") || path == "/v1/admin/settings" || path == "/v1/admin/rooms" || strings.HasPrefix(path, "/v1/admin/rooms/") || path == "/v1/rooms" || strings.HasPrefix(path, "/v1/rooms/")
}

func me(w http.ResponseWriter, r *http.Request, db *sql.DB, actor caller) {
	if r.Method != http.MethodGet {
		errorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var name string
	if err := db.QueryRow("SELECT name FROM users WHERE id=?", actor.userID).Scan(&name); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": actor.userID, "name": name, "role": actor.role})
}

func authenticate(w http.ResponseWriter, r *http.Request, db *sql.DB) (caller, bool) {
	userID, deviceID, role, key, ok := binding(db, r.Header.Get("Authorization"))
	if !ok || deviceauth.VerifyHeaders(r, key, time.Now()) != nil {
		errorJSON(w, http.StatusUnauthorized, "unauthenticated")
		return caller{}, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return caller{}, false
	}
	if len(body) > 1<<20 {
		errorJSON(w, http.StatusRequestEntityTooLarge, "too_large")
		return caller{}, false
	}
	hash := sha256.Sum256(body)
	if hex.EncodeToString(hash[:]) != r.Header.Get("X-Body-SHA256") {
		errorJSON(w, http.StatusUnauthorized, "unauthenticated")
		return caller{}, false
	}
	if _, err := db.Exec("INSERT INTO request_nonces(device_id, nonce, seen_at) VALUES (?, ?, ?)", deviceID, r.Header.Get("X-Nonce"), time.Now().Unix()); err != nil {
		errorJSON(w, http.StatusUnauthorized, "unauthenticated")
		return caller{}, false
	}
	return caller{userID: userID, role: role, body: body}, true
}

func adminUsers(w http.ResponseWriter, r *http.Request, db *sql.DB, actor caller) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		errorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if actor.role != "admin" {
		errorJSON(w, http.StatusForbidden, "forbidden")
		return
	}
	if r.Method == http.MethodGet {
		limit, cursor, ok := adminPage(w, r)
		if !ok {
			return
		}
		rows, err := db.Query(`SELECT id,name,role,EXISTS(SELECT 1 FROM device_bindings b WHERE b.user_id=u.id)
			FROM users u WHERE id>? AND NOT EXISTS
			(SELECT 1 FROM user_deletions d WHERE d.user_id=u.id) ORDER BY id LIMIT ?`, cursor, limit+1)
		if err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		defer rows.Close()
		items := make([]map[string]any, 0)
		for rows.Next() {
			var id, name, role string
			var activated bool
			if rows.Scan(&id, &name, &role, &activated) != nil {
				errorJSON(w, http.StatusInternalServerError, "internal_error")
				return
			}
			items = append(items, map[string]any{"id": id, "name": name, "role": role, "activated": activated})
		}
		if rows.Err() != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		var next any
		if len(items) > limit {
			next = items[limit-1]["id"]
			items = items[:limit]
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
		return
	}
	var input struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if !decodeJSON(actor.body, &input) || len([]rune(strings.TrimSpace(input.Name))) < 1 || len([]rune(strings.TrimSpace(input.Name))) > 200 || (input.Role != "user" && input.Role != "admin" && input.Role != "commander") {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	tx, err := db.Begin()
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	defer tx.Rollback()
	taken, err := nameExists(tx, "users", input.Name)
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if taken {
		errorJSON(w, http.StatusConflict, "name_taken")
		return
	}
	id, err := newID()
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if _, err := tx.Exec("INSERT INTO users(id, name, role, created_at) VALUES (?, ?, ?, ?)", id, input.Name, input.Role, time.Now().Unix()); err != nil {
		writeInsertError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "name": input.Name, "role": input.Role})
}

func adminUser(w http.ResponseWriter, r *http.Request, db *sql.DB, actor caller, nodes []Node) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/admin/users/"), "/")
	if len(parts) == 2 && parts[1] == "activation" {
		adminActivation(w, r, db, actor, nodes)
		return
	}
	if len(parts) != 1 || !validUUID(parts[0]) {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodDelete && r.Method != http.MethodPut {
		errorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if actor.role != "admin" {
		errorJSON(w, http.StatusForbidden, "forbidden")
		return
	}
	if parts[0] == actor.userID {
		errorJSON(w, http.StatusConflict, "conflict")
		return
	}
	if r.Method == http.MethodPut {
		var input struct {
			Role string `json:"role"`
		}
		if !decodeJSON(actor.body, &input) || (input.Role != "user" && input.Role != "commander" && input.Role != "admin") {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
		result, err := db.Exec(`UPDATE users SET role=? WHERE id=? AND NOT EXISTS
			(SELECT 1 FROM user_deletions d WHERE d.user_id=users.id)`, input.Role, parts[0])
		if err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		if count, _ := result.RowsAffected(); count == 0 {
			errorJSON(w, http.StatusNotFound, "not_found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": parts[0], "role": input.Role})
		return
	}
	tx, err := db.Begin()
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT count(*) FROM users u WHERE u.id=? AND NOT EXISTS
		(SELECT 1 FROM user_deletions d WHERE d.user_id=u.id)`, parts[0]).Scan(&exists); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if exists == 0 {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	queries := []string{
		"INSERT INTO user_deletions(user_id,deleted_at) VALUES(?,unixepoch())",
		"DELETE FROM activation_codes WHERE user_id=?",
		"DELETE FROM room_rights WHERE user_id=?",
		"DELETE FROM request_nonces WHERE device_id IN (SELECT device_id FROM device_bindings WHERE user_id=?)",
		"DELETE FROM device_bindings WHERE user_id=?",
	}
	for _, query := range queries {
		if _, err := tx.Exec(query, parts[0]); err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func nameExists(tx *sql.Tx, table, name string) (bool, error) {
	rows, err := tx.Query("SELECT name FROM " + table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var existing string
		if err := rows.Scan(&existing); err != nil {
			return false, err
		}
		if strings.EqualFold(existing, name) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func writeInsertError(w http.ResponseWriter, err error) {
	var constraint sqlite3.Error
	if errors.As(err, &constraint) && constraint.Code == sqlite3.ErrConstraint {
		errorJSON(w, http.StatusConflict, "name_taken")
	} else {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
	}
}

func adminPage(w http.ResponseWriter, r *http.Request) (int, string, bool) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return 0, "", false
		}
	}
	cursor := r.URL.Query().Get("cursor")
	if cursor != "" && !validUUID(cursor) {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return 0, "", false
	}
	return limit, cursor, true
}

func decodeJSON(body []byte, dst any) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst) == nil && decoder.Decode(new(any)) == io.EOF
}

func newID() (string, error) {
	return uuid7.New(time.Now())
}

func binding(db *sql.DB, authorization string) (userID, deviceID, role string, publicKey []byte, ok bool) {
	value, found := strings.CutPrefix(authorization, "Device ")
	if !found {
		return
	}
	userID, deviceID, found = strings.Cut(value, ":")
	if !found || userID == "" || deviceID == "" || strings.Contains(deviceID, ":") {
		return
	}
	err := db.QueryRow(`SELECT u.role, d.public_key_p256 FROM device_bindings d JOIN users u ON u.id=d.user_id
		WHERE d.user_id=? AND d.device_id=? AND NOT EXISTS
		(SELECT 1 FROM user_deletions x WHERE x.user_id=u.id)`, userID, deviceID).Scan(&role, &publicKey)
	return userID, deviceID, role, publicKey, err == nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func errorJSON(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": code}})
}
