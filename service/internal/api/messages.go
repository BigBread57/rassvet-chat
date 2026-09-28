package api

import (
	"database/sql"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type message struct {
	ID        string    `json:"id"`
	RoomID    string    `json:"room_id,omitempty"`
	SenderID  string    `json:"sender_id"`
	Text      string    `json:"text"`
	SentAt    string    `json:"sent_at"`
	ExpiresAt string    `json:"expires_at"`
	State     string    `json:"state,omitempty"`
	Receipts  []receipt `json:"receipts,omitempty"`
}

func messages(w http.ResponseWriter, r *http.Request, db *sql.DB, actor caller) {
	roomID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/rooms/"), "/messages")
	if roomID == "" || strings.Contains(roomID, "/") {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var allowed int
	column := "can_read"
	if r.Method == http.MethodPost {
		column = "can_send_text"
	}
	// Column is a fixed server-side literal, never client input.
	err := db.QueryRow("SELECT CASE WHEN ?='commander' THEN can_read ELSE "+column+" END FROM room_rights WHERE room_id=? AND user_id=?",
		actor.role, roomID, actor.userID).Scan(&allowed)
	if err != nil && err != sql.ErrNoRows {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if err == sql.ErrNoRows || allowed != 1 {
		status, code := http.StatusNotFound, "not_found"
		if r.Method == http.MethodPost {
			status, code = http.StatusForbidden, "forbidden"
		}
		errorJSON(w, status, code)
		return
	}
	if r.Method == http.MethodPost {
		postMessage(w, db, actor, roomID)
		return
	}
	listMessages(w, r, db, roomID)
}

func postMessage(w http.ResponseWriter, db *sql.DB, actor caller, roomID string) {
	var input struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	if !decodeJSON(actor.body, &input) || !validUUID(input.ID) || strings.TrimSpace(input.Text) == "" {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	var retention int64
	if err := db.QueryRow("SELECT retention_seconds FROM settings WHERE id=1").Scan(&retention); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	result, err := db.Exec(`INSERT OR IGNORE INTO messages(id,room_id,sender_id,text,sent_at,expires_at)
		SELECT ?,?,?,?,?,? WHERE EXISTS
		(SELECT 1 FROM room_rights WHERE room_id=? AND user_id=? AND can_read=1 AND (?='commander' OR can_send_text=1))`,
		input.ID, roomID, actor.userID, input.Text, now.Unix(), now.Unix()+retention,
		roomID, actor.userID, actor.role)
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	var stored message
	var sent, expires int64
	if err := db.QueryRow("SELECT id,room_id,sender_id,text,sent_at,expires_at FROM messages WHERE id=?", input.ID).
		Scan(&stored.ID, &stored.RoomID, &stored.SenderID, &stored.Text, &sent, &expires); err != nil {
		if err == sql.ErrNoRows && inserted == 0 {
			errorJSON(w, http.StatusForbidden, "forbidden")
			return
		}
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if inserted == 0 && (stored.RoomID != roomID || stored.SenderID != actor.userID || stored.Text != input.Text) {
		errorJSON(w, http.StatusConflict, "conflict")
		return
	}
	stored.SentAt = time.Unix(sent, 0).UTC().Format(time.RFC3339)
	stored.ExpiresAt = time.Unix(expires, 0).UTC().Format(time.RFC3339)
	stored.State = "saved"
	writeJSON(w, http.StatusCreated, stored)
}

func listMessages(w http.ResponseWriter, r *http.Request, db *sql.DB, roomID string) {
	if r.URL.Query().Has("q") {
		searchMessages(w, r, db, roomID)
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	query := r.URL.Query()
	latest := query.Get("latest") == "1"
	updates := query.Has("since_rowid")
	receiptsOnly := query.Get("receipts_only") == "1"
	if (query.Has("latest") && !latest) || (query.Has("receipts_only") && !receiptsOnly) ||
		(updates && (latest || query.Has("cursor") || receiptsOnly)) || (latest && query.Has("cursor")) {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var sinceRowID int64
	sinceID := ""
	if updates {
		var err error
		parts := strings.SplitN(query.Get("since_rowid"), ":", 2)
		if len(parts) == 2 {
			sinceID = parts[1]
		}
		sinceRowID, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil || sinceRowID < 0 || (sinceRowID > 0 && !validUUID(sinceID)) || (sinceRowID == 0 && sinceID != "") {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if sinceRowID > 0 {
			var storedID string
			err = db.QueryRow("SELECT id FROM messages WHERE rowid=?", sinceRowID).Scan(&storedID)
			if err != nil && err != sql.ErrNoRows {
				errorJSON(w, http.StatusInternalServerError, "internal_error")
				return
			}
			if err == sql.ErrNoRows || storedID != sinceID {
				sinceRowID = 0 // SQLite can reuse a rowid after retention deletes it.
				sinceID = ""
			}
		}
	}
	var afterTime int64
	afterID := ""
	backward := false
	if raw := query.Get("cursor"); raw != "" {
		backward = strings.HasPrefix(raw, "b:")
		if backward {
			raw = strings.TrimPrefix(raw, "b:")
		}
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 || !validUUID(parts[1]) {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
		afterTime, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil || afterTime < 0 {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
		afterID = parts[1]
	}
	statement := `SELECT rowid,id,sender_id,text,sent_at,expires_at FROM messages
		WHERE room_id=? AND expires_at>? AND (sent_at>? OR (sent_at=? AND id>?))
		ORDER BY sent_at,id LIMIT ?`
	args := []any{roomID, time.Now().Unix(), afterTime, afterTime, afterID, limit + 1}
	if latest || backward {
		statement = `SELECT rowid,id,sender_id,text,sent_at,expires_at FROM messages
			WHERE room_id=? AND expires_at>?`
		args = []any{roomID, time.Now().Unix()}
		if backward {
			statement += ` AND (sent_at<? OR (sent_at=? AND id<?))`
			args = append(args, afterTime, afterTime, afterID)
		}
		statement += ` ORDER BY sent_at DESC,id DESC LIMIT ?`
		args = append(args, limit+1)
	} else if updates {
		statement = `SELECT rowid,id,sender_id,text,sent_at,expires_at FROM messages
			WHERE room_id=? AND expires_at>? AND rowid>? ORDER BY rowid LIMIT ?`
		args = []any{roomID, time.Now().Unix(), sinceRowID, limit + 1}
	}
	rows, err := db.Query(statement, args...)
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	defer rows.Close()
	items := make([]message, 0)
	var lastTime, lastRowID int64
	lastID := ""
	for rows.Next() {
		var item message
		var rowID, sent, expires int64
		if err := rows.Scan(&rowID, &item.ID, &item.SenderID, &item.Text, &sent, &expires); err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		item.SentAt = time.Unix(sent, 0).UTC().Format(time.RFC3339)
		item.ExpiresAt = time.Unix(expires, 0).UTC().Format(time.RFC3339)
		items = append(items, item)
		if len(items) <= limit {
			lastTime = sent
			lastRowID = rowID
			lastID = item.ID
		}
		if latest && rowID > sinceRowID && len(items) <= limit {
			sinceRowID = rowID
			sinceID = item.ID
		}
	}
	if rows.Err() != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if err := rows.Close(); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	var next any
	if len(items) > limit {
		if latest || backward {
			next = "b:" + base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(lastTime, 10)+":"+items[limit-1].ID))
		} else if updates {
			next = strconv.FormatInt(lastRowID, 10) + ":" + lastID
		} else {
			next = base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(lastTime, 10) + ":" + items[limit-1].ID))
		}
		items = items[:limit]
	}
	if updates && len(items) > 0 {
		sinceRowID = lastRowID
		sinceID = lastID
	}
	if latest || backward {
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
	}
	for i := range items {
		items[i].Receipts = make([]receipt, 0)
		receiptRows, err := db.Query("SELECT user_id,state,at FROM receipts WHERE message_id=? ORDER BY user_id", items[i].ID)
		if err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		for receiptRows.Next() {
			var entry receipt
			var at int64
			if err := receiptRows.Scan(&entry.UserID, &entry.State, &at); err != nil {
				receiptRows.Close()
				errorJSON(w, http.StatusInternalServerError, "internal_error")
				return
			}
			entry.At = time.Unix(at, 0).UTC().Format(time.RFC3339)
			items[i].Receipts = append(items[i].Receipts, entry)
		}
		if err := receiptRows.Err(); err != nil {
			receiptRows.Close()
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		receiptRows.Close()
	}
	var responseItems any = items
	if receiptsOnly {
		brief := make([]map[string]any, 0, len(items))
		for _, item := range items {
			brief = append(brief, map[string]any{"id": item.ID, "receipts": item.Receipts})
		}
		responseItems = brief
	}
	response := map[string]any{"items": responseItems, "next_cursor": next}
	if latest || updates {
		response["cursor"] = strconv.FormatInt(sinceRowID, 10)
		if sinceRowID > 0 {
			response["cursor"] = strconv.FormatInt(sinceRowID, 10) + ":" + sinceID
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func searchMessages(w http.ResponseWriter, r *http.Request, db *sql.DB, roomID string) {
	query := r.URL.Query()
	q := strings.TrimSpace(query.Get("q"))
	if q == "" || len([]rune(q)) > 200 || len(query) > 2 {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	for key := range query {
		if key != "q" && key != "cursor" {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	statement := `SELECT id,sender_id,text,sent_at,expires_at FROM messages
		WHERE room_id=? AND expires_at>?`
	args := []any{roomID, time.Now().Unix()}
	if raw := query.Get("cursor"); raw != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 || !validUUID(parts[1]) {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
		sent, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || sent < 0 {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
		statement += ` AND (sent_at<? OR (sent_at=? AND id<?))`
		args = append(args, sent, sent, parts[1])
	} else if query.Has("cursor") {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	statement += ` ORDER BY sent_at DESC,id DESC`
	rows, err := db.Query(statement, args...)
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	defer rows.Close()
	items := make([]message, 0)
	var lastTime int64
	needle := strings.ToLower(q)
	for rows.Next() {
		var item message
		var sent, expires int64
		if err := rows.Scan(&item.ID, &item.SenderID, &item.Text, &sent, &expires); err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		// ponytail: scan one room in order; add a Unicode search index if history search becomes slow.
		if !strings.Contains(strings.ToLower(item.Text), needle) {
			continue
		}
		item.SentAt = time.Unix(sent, 0).UTC().Format(time.RFC3339)
		item.ExpiresAt = time.Unix(expires, 0).UTC().Format(time.RFC3339)
		items = append(items, item)
		if len(items) == 50 {
			lastTime = sent
		}
		if len(items) == 51 {
			break
		}
	}
	if rows.Err() != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	var next any
	if len(items) > 50 {
		items = items[:50]
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(lastTime, 10) + ":" + items[49].ID))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
