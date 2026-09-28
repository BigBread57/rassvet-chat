package api

import (
	"database/sql"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type roomEvent struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	CommanderID string `json:"commander_id"`
	EnteredAt   string `json:"entered_at"`
}

func roomEvents(w http.ResponseWriter, r *http.Request, db *sql.DB, actor caller) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/rooms/"), "/")
	if len(parts) != 2 || !validUUID(parts[0]) {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	roomID := parts[0]
	if parts[1] == "presence" {
		if r.Method != http.MethodPost {
			errorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		if actor.role != "commander" {
			errorJSON(w, http.StatusForbidden, "forbidden")
			return
		}
		var input struct {
			Kind string `json:"kind"`
		}
		if !decodeJSON(actor.body, &input) || input.Kind != "commander_entered" {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var count int
		if err := db.QueryRow("SELECT count(*) FROM room_rights WHERE room_id=? AND user_id=? AND can_read=1",
			roomID, actor.userID).Scan(&count); err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		if count == 0 {
			errorJSON(w, http.StatusNotFound, "not_found")
			return
		}
		id, err := newID()
		if err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		now := time.Now().UTC().Truncate(time.Second)
		if _, err := db.Exec("INSERT INTO room_events(id,room_id,kind,actor_id,created_at) VALUES(?,?,'commander_entered',?,?)", id, roomID, actor.userID, now.Unix()); err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		writeJSON(w, http.StatusCreated, roomEvent{id, "commander_entered", actor.userID, now.Format(time.RFC3339)})
		return
	}
	if parts[1] != "events" {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodGet {
		errorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var allowed int
	var err error
	err = db.QueryRow("SELECT can_read FROM room_rights WHERE room_id=? AND user_id=?", roomID, actor.userID).Scan(&allowed)
	if err == sql.ErrNoRows || (err == nil && allowed != 1) {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	query := r.URL.Query()
	latest := query.Get("latest") == "1"
	updates := query.Has("since_rowid")
	if (query.Has("latest") && !latest) || (updates && (latest || query.Has("cursor"))) || (latest && query.Has("cursor")) {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var sinceRowID int64
	sinceID := ""
	if updates {
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
			err = db.QueryRow("SELECT id FROM room_events WHERE rowid=?", sinceRowID).Scan(&storedID)
			if err != nil && err != sql.ErrNoRows {
				errorJSON(w, http.StatusInternalServerError, "internal_error")
				return
			}
			if err == sql.ErrNoRows || storedID != sinceID {
				sinceRowID, sinceID = 0, ""
			}
		}
	}
	var afterTime int64
	var afterID string
	backward := false
	if raw := query.Get("cursor"); raw != "" {
		backward = strings.HasPrefix(raw, "b:")
		if backward {
			raw = strings.TrimPrefix(raw, "b:")
		}
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		parts := strings.SplitN(string(decoded), ":", 2)
		if err != nil || len(parts) != 2 || !validUUID(parts[1]) {
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
	var retention int64
	if err := db.QueryRow("SELECT retention_seconds FROM settings WHERE id=1").Scan(&retention); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	statement := `SELECT rowid,id,actor_id,created_at FROM room_events WHERE room_id=? AND created_at>?
		AND (created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?`
	args := []any{roomID, time.Now().Unix() - retention, afterTime, afterTime, afterID, limit + 1}
	if latest || backward {
		statement = `SELECT rowid,id,actor_id,created_at FROM room_events WHERE room_id=? AND created_at>?`
		args = []any{roomID, time.Now().Unix() - retention}
		if backward {
			statement += ` AND (created_at<? OR (created_at=? AND id<?))`
			args = append(args, afterTime, afterTime, afterID)
		}
		statement += ` ORDER BY created_at DESC,id DESC LIMIT ?`
		args = append(args, limit+1)
	} else if updates {
		statement = `SELECT rowid,id,actor_id,created_at FROM room_events WHERE room_id=? AND created_at>? AND rowid>? ORDER BY rowid LIMIT ?`
		args = []any{roomID, time.Now().Unix() - retention, sinceRowID, limit + 1}
	}
	rows, err := db.Query(statement, args...)
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	defer rows.Close()
	items := make([]roomEvent, 0)
	var lastTime, lastRowID int64
	lastID := ""
	for rows.Next() {
		var event roomEvent
		var rowID, at int64
		if err := rows.Scan(&rowID, &event.ID, &event.CommanderID, &at); err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		event.Kind = "commander_entered"
		event.EnteredAt = time.Unix(at, 0).UTC().Format(time.RFC3339)
		items = append(items, event)
		if len(items) <= limit {
			lastTime = at
			lastRowID = rowID
			lastID = event.ID
		}
		if latest && rowID > sinceRowID && len(items) <= limit {
			sinceRowID, sinceID = rowID, event.ID
		}
	}
	if rows.Err() != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	var next any
	if len(items) > limit {
		if latest || backward {
			next = "b:" + base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(lastTime, 10)+":"+lastID))
		} else if updates {
			next = strconv.FormatInt(lastRowID, 10) + ":" + lastID
		} else {
			next = base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(lastTime, 10) + ":" + lastID))
		}
		items = items[:limit]
	}
	if updates && len(items) > 0 {
		sinceRowID, sinceID = lastRowID, lastID
	}
	if latest || backward {
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
	}
	response := map[string]any{"items": items, "next_cursor": next}
	if latest || updates {
		response["cursor"] = strconv.FormatInt(sinceRowID, 10)
		if sinceRowID > 0 {
			response["cursor"] = strconv.FormatInt(sinceRowID, 10) + ":" + sinceID
		}
	}
	writeJSON(w, http.StatusOK, response)
}
