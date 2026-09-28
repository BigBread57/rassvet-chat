package api

import (
	"database/sql"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type rights struct {
	Read           bool `json:"read"`
	SendText       bool `json:"send_text"`
	AddAttachment  bool `json:"add_attachment"`
	SaveAttachment bool `json:"save_attachment"`
}

func adminRooms(w http.ResponseWriter, r *http.Request, db *sql.DB, actor caller) {
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
		rows, err := db.Query("SELECT id,name FROM rooms WHERE id>? ORDER BY id LIMIT ?", cursor, limit+1)
		if err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		defer rows.Close()
		items := make([]map[string]string, 0)
		for rows.Next() {
			var id, name string
			if rows.Scan(&id, &name) != nil {
				errorJSON(w, http.StatusInternalServerError, "internal_error")
				return
			}
			items = append(items, map[string]string{"id": id, "name": name})
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
	}
	if !decodeJSON(actor.body, &input) || len([]rune(strings.TrimSpace(input.Name))) < 1 || len([]rune(strings.TrimSpace(input.Name))) > 200 {
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
	taken, err := nameExists(tx, "rooms", input.Name)
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
	if _, err := tx.Exec("INSERT INTO rooms(id, name, created_by, created_at) VALUES (?, ?, ?, ?)", id, input.Name, actor.userID, time.Now().Unix()); err != nil {
		writeInsertError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "name": input.Name})
}

func adminRoom(w http.ResponseWriter, r *http.Request, db *sql.DB, actor caller) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/admin/rooms/")
	if !validUUID(id) {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodPut {
		errorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if actor.role != "admin" {
		errorJSON(w, http.StatusForbidden, "forbidden")
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(actor.body, &input) {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if len([]rune(input.Name)) < 1 || len([]rune(input.Name)) > 200 {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	tx, err := db.Begin()
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	defer tx.Rollback()
	var current string
	if err := tx.QueryRow("SELECT name FROM rooms WHERE id=?", id).Scan(&current); err != nil {
		if err == sql.ErrNoRows {
			errorJSON(w, http.StatusNotFound, "not_found")
		} else {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
		}
		return
	}
	if !strings.EqualFold(current, input.Name) {
		taken, err := nameExists(tx, "rooms", input.Name)
		if err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		if taken {
			errorJSON(w, http.StatusConflict, "name_taken")
			return
		}
	}
	result, err := tx.Exec("UPDATE rooms SET name=? WHERE id=?", input.Name, id)
	if err != nil {
		writeInsertError(w, err)
		return
	}
	changed, err := result.RowsAffected()
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
	} else if changed == 0 {
		errorJSON(w, http.StatusNotFound, "not_found")
	} else {
		if err := tx.Commit(); err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": id, "name": input.Name})
	}
}

func adminMembers(w http.ResponseWriter, r *http.Request, db *sql.DB, actor caller) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/admin/rooms/"), "/")
	if (len(parts) != 2 && len(parts) != 3) || parts[0] == "" || parts[1] != "members" || (len(parts) == 3 && parts[2] == "") {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	if (len(parts) == 2 && r.Method != http.MethodGet) || (len(parts) == 3 && r.Method != http.MethodPut && r.Method != http.MethodDelete) {
		errorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if actor.role != "admin" {
		errorJSON(w, http.StatusForbidden, "forbidden")
		return
	}
	roomID := parts[0]
	if len(parts) == 2 {
		limit, cursor, ok := adminPage(w, r)
		if !ok {
			return
		}
		var exists int
		if err := db.QueryRow("SELECT count(*) FROM rooms WHERE id=?", roomID).Scan(&exists); err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		if exists == 0 {
			errorJSON(w, http.StatusNotFound, "not_found")
			return
		}
		rows, err := db.Query(`SELECT u.id,u.name,u.role,rr.can_read,rr.can_send_text,rr.can_add_attachment,rr.can_save_attachment
			FROM room_rights rr JOIN users u ON u.id=rr.user_id WHERE rr.room_id=? AND u.id>?
			AND NOT EXISTS (SELECT 1 FROM user_deletions d WHERE d.user_id=u.id) ORDER BY u.id LIMIT ?`, roomID, cursor, limit+1)
		if err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		defer rows.Close()
		type member struct {
			ID     string `json:"user_id"`
			Name   string `json:"name"`
			Role   string `json:"role"`
			Rights rights `json:"rights"`
		}
		items := make([]member, 0)
		for rows.Next() {
			var m member
			if rows.Scan(&m.ID, &m.Name, &m.Role, &m.Rights.Read, &m.Rights.SendText, &m.Rights.AddAttachment, &m.Rights.SaveAttachment) != nil {
				errorJSON(w, http.StatusInternalServerError, "internal_error")
				return
			}
			items = append(items, m)
		}
		if rows.Err() != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		var next any
		if len(items) > limit {
			next = items[limit-1].ID
			items = items[:limit]
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
		return
	}
	userID := parts[2]
	var exists, users int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM rooms WHERE id=?),
		(SELECT count(*) FROM users u WHERE u.id=? AND NOT EXISTS
		(SELECT 1 FROM user_deletions d WHERE d.user_id=u.id))`, roomID, userID).Scan(&exists, &users); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if exists == 0 || users == 0 {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method == http.MethodDelete {
		if _, err := db.Exec("DELETE FROM room_rights WHERE room_id=? AND user_id=?", roomID, userID); err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var input struct {
		Read           *bool `json:"read"`
		SendText       *bool `json:"send_text"`
		AddAttachment  *bool `json:"add_attachment"`
		SaveAttachment *bool `json:"save_attachment"`
	}
	if !decodeJSON(actor.body, &input) || input.Read == nil || input.SendText == nil || input.AddAttachment == nil || input.SaveAttachment == nil {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	_, err := db.Exec(`INSERT INTO room_rights(room_id,user_id,can_read,can_send_text,can_add_attachment,can_save_attachment)
		VALUES(?,?,?,?,?,?) ON CONFLICT(room_id,user_id) DO UPDATE SET
		can_read=excluded.can_read,can_send_text=excluded.can_send_text,
		can_add_attachment=excluded.can_add_attachment,can_save_attachment=excluded.can_save_attachment`,
		roomID, userID, *input.Read, *input.SendText, *input.AddAttachment, *input.SaveAttachment)
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": userID, "rights": rights{*input.Read, *input.SendText, *input.AddAttachment, *input.SaveAttachment}})
}

func rooms(w http.ResponseWriter, r *http.Request, db *sql.DB, actor caller) {
	if strings.HasSuffix(r.URL.Path, "/presence") || strings.HasSuffix(r.URL.Path, "/events") {
		roomEvents(w, r, db, actor)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/receipts") {
		postReceipt(w, r, db, actor)
		return
	}
	if strings.Contains(r.URL.Path, "/messages/") && (r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodDelete) {
		errorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/messages") {
		messages(w, r, db, actor)
		return
	}
	if r.Method != http.MethodGet {
		errorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if r.URL.Path == "/v1/rooms" {
		listRooms(w, r, db, actor)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/rooms/")
	if id == "" || strings.Contains(id, "/") {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	var name string
	var p rights
	var err error
	if actor.role == "commander" {
		err = db.QueryRow(`SELECT r.name FROM rooms r JOIN room_rights rr ON rr.room_id=r.id
			WHERE r.id=? AND rr.user_id=? AND rr.can_read=1`, id, actor.userID).Scan(&name)
		p = rights{true, true, true, true}
	} else {
		err = db.QueryRow(`SELECT r.name, rr.can_read, rr.can_send_text, rr.can_add_attachment, rr.can_save_attachment
			FROM rooms r JOIN room_rights rr ON rr.room_id=r.id WHERE r.id=? AND rr.user_id=? AND rr.can_read=1`, id, actor.userID).
			Scan(&name, &p.Read, &p.SendText, &p.AddAttachment, &p.SaveAttachment)
	}
	if err == sql.ErrNoRows {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	members := make([]map[string]string, 0)
	rows, err := db.Query(`SELECT u.id,u.name FROM users u JOIN room_rights rr ON rr.user_id=u.id
		WHERE rr.room_id=? AND rr.can_read=1 AND NOT EXISTS
		(SELECT 1 FROM user_deletions d WHERE d.user_id=u.id) ORDER BY u.id`, id)
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var uid, uname string
		if err := rows.Scan(&uid, &uname); err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		members = append(members, map[string]string{"user_id": uid, "name": uname})
	}
	if rows.Err() != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "name": name, "rights": p, "members": members})
}

func listRooms(w http.ResponseWriter, r *http.Request, db *sql.DB, actor caller) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	cursor := ""
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || len(decoded) != 36 {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
		cursor = string(decoded)
	}
	var rows *sql.Rows
	var err error
	if actor.role == "commander" {
		rows, err = db.Query(`SELECT r.id,r.name,1,1,1,1 FROM rooms r JOIN room_rights rr ON rr.room_id=r.id
			WHERE rr.user_id=? AND rr.can_read=1 AND r.id>? ORDER BY r.id LIMIT ?`, actor.userID, cursor, limit+1)
	} else {
		rows, err = db.Query(`SELECT r.id,r.name,rr.can_read,rr.can_send_text,rr.can_add_attachment,rr.can_save_attachment FROM rooms r JOIN room_rights rr ON rr.room_id=r.id
			WHERE rr.user_id=? AND rr.can_read=1 AND r.id>? ORDER BY r.id LIMIT ?`, actor.userID, cursor, limit+1)
	}
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, name string
		var p rights
		if err := rows.Scan(&id, &name, &p.Read, &p.SendText, &p.AddAttachment, &p.SaveAttachment); err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		items = append(items, map[string]any{"id": id, "name": name, "rights": p})
	}
	if rows.Err() != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	var next any
	if len(items) > limit {
		next = base64.RawURLEncoding.EncodeToString([]byte(items[limit-1]["id"].(string)))
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}
