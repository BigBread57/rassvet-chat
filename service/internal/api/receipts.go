package api

import (
	"database/sql"
	"net/http"
	"strings"
	"time"
)

type receipt struct {
	UserID string `json:"user_id"`
	State  string `json:"state"`
	At     string `json:"at"`
}

func postReceipt(w http.ResponseWriter, r *http.Request, db *sql.DB, actor caller) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/rooms/"), "/")
	if len(parts) != 4 || parts[0] == "" || parts[1] != "messages" || !validUUID(parts[2]) || parts[3] != "receipts" {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	roomID, messageID := parts[0], parts[2]
	var allowed int
	err := db.QueryRow("SELECT can_read FROM room_rights WHERE room_id=? AND user_id=?", roomID, actor.userID).Scan(&allowed)
	if err != nil && err != sql.ErrNoRows {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if err == sql.ErrNoRows || allowed != 1 {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	var input struct {
		State string `json:"state"`
	}
	if !decodeJSON(actor.body, &input) || (input.State != "delivered" && input.State != "read") {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var sender string
	err = db.QueryRow("SELECT sender_id FROM messages WHERE id=? AND room_id=? AND expires_at>?", messageID, roomID, time.Now().Unix()).Scan(&sender)
	if err == sql.ErrNoRows {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if sender == actor.userID {
		errorJSON(w, http.StatusForbidden, "forbidden")
		return
	}
	now := time.Now().Unix()
	_, err = db.Exec(`INSERT INTO receipts(message_id,user_id,state,at) VALUES(?,?,?,?)
		ON CONFLICT(message_id,user_id) DO UPDATE SET state=excluded.state,at=excluded.at
		WHERE receipts.state='delivered' AND excluded.state='read'`, messageID, actor.userID, input.State, now)
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	var current receipt
	var at int64
	if err := db.QueryRow("SELECT user_id,state,at FROM receipts WHERE message_id=? AND user_id=?", messageID, actor.userID).Scan(&current.UserID, &current.State, &at); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	current.At = time.Unix(at, 0).UTC().Format(time.RFC3339)
	writeJSON(w, http.StatusOK, map[string]any{"message_id": messageID, "user_id": current.UserID, "state": current.State, "at": current.At})
}
