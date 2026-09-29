package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type peerEvent struct {
	ID           string          `json:"id"`
	Kind         string          `json:"kind"`
	AuthorNodeID string          `json:"author_node_id"`
	CreatedAt    string          `json:"created_at"`
	Payload      json.RawMessage `json:"payload"`
}

func peerEvents(w http.ResponseWriter, r *http.Request, db *sql.DB, nodeID, storageDir string) {
	if db == nil || nodeID == "" {
		errorJSON(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		cursor, err := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
		if r.URL.Query().Get("cursor") == "" {
			cursor, err = 0, nil
		}
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			limit, err = strconv.Atoi(raw)
		}
		if err != nil || cursor < 0 || limit < 1 || limit > 100 {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
		rows, err := db.Query("SELECT rowid,id,kind,author_node_id,created_at,payload FROM peer_events WHERE rowid>? AND author_node_id=? AND length(payload)>2 ORDER BY rowid LIMIT ?", cursor, nodeID, limit+1)
		if err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		defer rows.Close()
		items := make([]peerEvent, 0)
		var last int64
		for rows.Next() {
			var event peerEvent
			var rowid, at int64
			var payload []byte
			if err := rows.Scan(&rowid, &event.ID, &event.Kind, &event.AuthorNodeID, &at, &payload); err != nil {
				errorJSON(w, http.StatusInternalServerError, "internal_error")
				return
			}
			event.Payload = payload
			event.CreatedAt = time.Unix(at, 0).UTC().Format(time.RFC3339)
			items = append(items, event)
			if len(items) <= limit {
				last = rowid
			}
		}
		if rows.Err() != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		var next any
		if len(items) > limit {
			items = items[:limit]
			next = strconv.FormatInt(last, 10)
		}
		if len(items) == 0 {
			last = cursor
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next, "cursor": strconv.FormatInt(last, 10)})
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
		if err != nil || len(body) > 1<<20 {
			errorJSON(w, http.StatusRequestEntityTooLarge, "too_large")
			return
		}
		var input struct {
			Events []peerEvent `json:"events"`
		}
		if !decodeJSON(body, &input) || len(input.Events) < 1 || len(input.Events) > 100 {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
		accepted := make([]string, 0, len(input.Events))
		for _, event := range input.Events {
			if event.AuthorNodeID != r.TLS.PeerCertificates[0].Subject.CommonName || !validUUID(event.ID) {
				errorJSON(w, http.StatusBadRequest, "invalid_request")
				return
			}
			if event.Kind == "attachment" {
				filePublishMu.Lock()
			}
			err := applyPeerEvent(db, event, storageDir)
			if event.Kind == "attachment" {
				filePublishMu.Unlock()
			}
			if err != nil {
				status, code := http.StatusConflict, "conflict"
				if errors.Is(err, errInvalidPeerEvent) {
					status, code = http.StatusBadRequest, "invalid_request"
				}
				errorJSON(w, status, code)
				return
			}
			accepted = append(accepted, event.ID)
		}
		writeJSON(w, http.StatusOK, map[string]any{"accepted": accepted, "cursor": ""})
	default:
		errorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

var errInvalidPeerEvent = errors.New("invalid peer event")

func applyPeerEvent(db *sql.DB, event peerEvent, storageDir string) error {
	at, err := time.Parse(time.RFC3339, event.CreatedAt)
	if err != nil || at.After(time.Now().Add(10*time.Minute)) || len(event.Payload) == 0 || len(event.Payload) > 1<<20 {
		return errInvalidPeerEvent
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var kind, author string
	var storedAt int64
	var payload []byte
	err = tx.QueryRow("SELECT kind,author_node_id,created_at,payload FROM peer_events WHERE id=?", event.ID).Scan(&kind, &author, &storedAt, &payload)
	if err == nil {
		if kind != event.Kind || author != event.AuthorNodeID || storedAt != at.Unix() || (string(payload) != "{}" && !jsonEqual(payload, event.Payload)) {
			return errors.New("event ID collision")
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	if _, err := tx.Exec("UPDATE peer_runtime SET applying=1 WHERE id=1"); err != nil {
		return err
	}
	if err := applyPeerPayload(tx, event.Kind, event.Payload, storageDir); err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO peer_events(id,kind,author_node_id,created_at,payload) VALUES(?,?,?,?,?)", event.ID, event.Kind, event.AuthorNodeID, at.Unix(), []byte(event.Payload))
	if err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE peer_runtime SET applying=0 WHERE id=1"); err != nil {
		return err
	}
	return tx.Commit()
}

// ConfigurePeer fixes the node identity before local writes can enter the journal.
func ConfigurePeer(db *sql.DB, nodeID string) error {
	if nodeID == "" {
		return errInvalidPeerEvent
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing string
	if err := tx.QueryRow("SELECT node_id FROM peer_runtime WHERE id=1").Scan(&existing); err != nil {
		return err
	}
	if existing != "" && existing != nodeID {
		return errors.New("database belongs to a different node")
	}
	var count int
	if err := tx.QueryRow("SELECT count(*) FROM peer_events").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		rows, err := tx.Query("SELECT id,name,role,created_at FROM users ORDER BY created_at,id")
		if err != nil {
			return err
		}
		var seeds [][]byte
		for rows.Next() {
			var id, name, role string
			var created int64
			if err := rows.Scan(&id, &name, &role, &created); err != nil {
				rows.Close()
				return err
			}
			payload, _ := json.Marshal(map[string]any{"id": id, "name": name, "role": role, "created_at": created})
			seeds = append(seeds, payload)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, payload := range seeds {
			id, err := newID()
			if err != nil {
				return err
			}
			if _, err := tx.Exec("INSERT INTO peer_events(id,kind,author_node_id,created_at,payload) VALUES(?,'user',?,?,?)", id, nodeID, time.Now().Unix(), payload); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec("UPDATE peer_runtime SET node_id=? WHERE id=1", nodeID); err != nil {
		return err
	}
	return tx.Commit()
}

// SyncPeerOnce pulls all currently available pages. A failed page keeps its cursor for retry.
func SyncPeerOnce(ctx context.Context, db *sql.DB, peerURL, peerID, storageDir string, tlsConfig *tls.Config) error {
	var cursor int64
	if err := db.QueryRow("SELECT value FROM peer_cursor WHERE id=1").Scan(&cursor); err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}}
	defer client.CloseIdleConnections()
	for {
		requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, peerURL+"/v1/peer/events?limit=5&cursor="+strconv.FormatInt(cursor, 10), nil)
		if err != nil {
			cancel()
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			cancel()
			return err
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
		response.Body.Close()
		cancel()
		if err != nil || len(body) > 8<<20 || response.StatusCode != http.StatusOK {
			return fmt.Errorf("peer event request failed: status=%d size=%d read=%v", response.StatusCode, len(body), err)
		}
		var page struct {
			Items      []peerEvent `json:"items"`
			NextCursor *string     `json:"next_cursor"`
			Cursor     string      `json:"cursor"`
		}
		if !decodeJSON(body, &page) || len(page.Items) > 5 {
			return errInvalidPeerEvent
		}
		next, err := strconv.ParseInt(page.Cursor, 10, 64)
		if err != nil || next < cursor || (len(page.Items) > 0 && next == cursor) {
			return errInvalidPeerEvent
		}
		for _, event := range page.Items {
			if event.AuthorNodeID != peerID || !validUUID(event.ID) {
				return errInvalidPeerEvent
			}
			if event.Kind == "attachment" {
				var attachment peerAttachmentPayload
				if !decodeJSON(event.Payload, &attachment) {
					return errInvalidPeerEvent
				}
				if attachment.ExpiresAt > time.Now().Unix() {
					err := fetchPeerFile(ctx, client, peerURL, storageDir, attachment)
					if err == nil {
						filePublishMu.Lock()
						err = applyPeerEvent(db, event, storageDir)
						filePublishMu.Unlock()
					}
					if err != nil {
						return err
					}
					continue
				}
			}
			if err := applyPeerEvent(db, event, storageDir); err != nil {
				return err
			}
		}
		if _, err := db.Exec("UPDATE peer_cursor SET value=? WHERE id=1", next); err != nil {
			return err
		}
		cursor = next
		if page.NextCursor == nil {
			return nil
		}
		if *page.NextCursor != page.Cursor {
			return errInvalidPeerEvent
		}
	}
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	first, second := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	first.UseNumber()
	second.UseNumber()
	return first.Decode(&x) == nil && first.Decode(new(any)) == io.EOF &&
		second.Decode(&y) == nil && second.Decode(new(any)) == io.EOF &&
		bytes.Equal(mustJSON(x), mustJSON(y))
}

func mustJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func applyPeerPayload(tx *sql.Tx, kind string, raw []byte, storageDir string) error {
	var p struct {
		ID                 string `json:"id"`
		UserID             string `json:"user_id"`
		RoomID             string `json:"room_id"`
		MessageID          string `json:"message_id"`
		Name               string `json:"name"`
		MimeType           string `json:"mime_type"`
		Size               int64  `json:"size"`
		SHA256             string `json:"sha256"`
		Role               string `json:"role"`
		DeviceID           string `json:"device_id"`
		PublicKey          string `json:"public_key_hex"`
		Text               string `json:"text"`
		State              string `json:"state"`
		CreatedBy          string `json:"created_by"`
		ActorID            string `json:"actor_id"`
		CreatedAt          int64  `json:"created_at"`
		ActivatedAt        int64  `json:"activated_at"`
		SentAt             int64  `json:"sent_at"`
		ExpiresAt          int64  `json:"expires_at"`
		At                 int64  `json:"at"`
		Read               bool   `json:"read"`
		SendText           bool   `json:"send_text"`
		AddAttachment      bool   `json:"add_attachment"`
		SaveAttachment     bool   `json:"save_attachment"`
		Deleted            bool   `json:"deleted"`
		DeletedAt          int64  `json:"deleted_at"`
		AttachmentMaxBytes int64  `json:"attachment_max_bytes"`
		RetentionSeconds   int64  `json:"retention_seconds"`
	}
	if !decodeJSON(raw, &p) {
		return errInvalidPeerEvent
	}
	var result sql.Result
	var err error
	switch kind {
	case "user":
		if !validUUID(p.ID) || p.Name == "" || (p.Role != "admin" && p.Role != "user" && p.Role != "commander") || p.CreatedAt < 1 {
			return errInvalidPeerEvent
		}
		result, err = tx.Exec(`INSERT INTO users(id,name,role,created_at) VALUES(?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET id=excluded.id
			WHERE users.name=excluded.name AND users.role=excluded.role AND users.created_at=excluded.created_at`,
			p.ID, p.Name, p.Role, p.CreatedAt)
	case "user_role":
		if !validUUID(p.ID) || (p.Role != "admin" && p.Role != "user" && p.Role != "commander") {
			return errInvalidPeerEvent
		}
		result, err = tx.Exec(`UPDATE users SET role=? WHERE id=? AND NOT EXISTS
			(SELECT 1 FROM user_deletions d WHERE d.user_id=users.id)`, p.Role, p.ID)
	case "user_deleted":
		if !validUUID(p.UserID) || p.DeletedAt < 1 {
			return errInvalidPeerEvent
		}
		if _, err = tx.Exec("INSERT OR IGNORE INTO user_deletions(user_id,deleted_at) VALUES(?,?)", p.UserID, p.DeletedAt); err != nil {
			return err
		}
		for _, query := range []string{
			"DELETE FROM activation_codes WHERE user_id=?",
			"DELETE FROM room_rights WHERE user_id=?",
			"DELETE FROM request_nonces WHERE device_id IN (SELECT device_id FROM device_bindings WHERE user_id=?)",
			"DELETE FROM device_bindings WHERE user_id=?",
		} {
			if _, err = tx.Exec(query, p.UserID); err != nil {
				return err
			}
		}
	case "binding":
		key, decodeErr := hex.DecodeString(p.PublicKey)
		if !validUUID(p.UserID) || p.DeviceID == "" || decodeErr != nil || len(key) == 0 || len(key) > 512 || p.ActivatedAt < 1 {
			return errInvalidPeerEvent
		}
		var deleted int
		if err := tx.QueryRow("SELECT count(*) FROM user_deletions WHERE user_id=?", p.UserID).Scan(&deleted); err != nil {
			return err
		}
		if deleted != 0 {
			return nil
		}
		result, err = tx.Exec(`INSERT INTO device_bindings(user_id,device_id,public_key_p256,activated_at) VALUES(?,?,?,?)
			ON CONFLICT(user_id) DO UPDATE SET user_id=excluded.user_id
			WHERE device_bindings.device_id=excluded.device_id AND device_bindings.public_key_p256=excluded.public_key_p256
			AND device_bindings.activated_at=excluded.activated_at`, p.UserID, p.DeviceID, key, p.ActivatedAt)
	case "room":
		if !validUUID(p.ID) || !validUUID(p.CreatedBy) || p.Name == "" || p.CreatedAt < 1 {
			return errInvalidPeerEvent
		}
		result, err = tx.Exec(`INSERT INTO rooms(id,name,created_by,created_at) VALUES(?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET id=excluded.id
			WHERE rooms.name=excluded.name AND rooms.created_by=excluded.created_by AND rooms.created_at=excluded.created_at`,
			p.ID, p.Name, p.CreatedBy, p.CreatedAt)
	case "room_rename":
		if !validUUID(p.ID) || strings.TrimSpace(p.Name) != p.Name || len([]rune(p.Name)) < 1 || len([]rune(p.Name)) > 200 {
			return errInvalidPeerEvent
		}
		result, err = tx.Exec("UPDATE rooms SET name=? WHERE id=?", p.Name, p.ID)
	case "rights":
		if !validUUID(p.RoomID) || !validUUID(p.UserID) {
			return errInvalidPeerEvent
		}
		var deleted int
		if err := tx.QueryRow("SELECT count(*) FROM user_deletions WHERE user_id=?", p.UserID).Scan(&deleted); err != nil {
			return err
		}
		if deleted != 0 {
			return nil
		}
		if p.Deleted {
			_, err = tx.Exec("DELETE FROM room_rights WHERE room_id=? AND user_id=?", p.RoomID, p.UserID)
		} else {
			_, err = tx.Exec(`INSERT INTO room_rights(room_id,user_id,can_read,can_send_text,can_add_attachment,can_save_attachment) VALUES(?,?,?,?,?,?)
				ON CONFLICT(room_id,user_id) DO UPDATE SET can_read=excluded.can_read,can_send_text=excluded.can_send_text,
				can_add_attachment=excluded.can_add_attachment,can_save_attachment=excluded.can_save_attachment`,
				p.RoomID, p.UserID, p.Read, p.SendText, p.AddAttachment, p.SaveAttachment)
		}
	case "message":
		if !validUUID(p.ID) || !validUUID(p.RoomID) || !validUUID(p.UserID) || p.Text == "" || p.SentAt < 1 || p.ExpiresAt <= p.SentAt {
			return errInvalidPeerEvent
		}
		if p.ExpiresAt <= time.Now().Unix() {
			return nil
		}
		result, err = tx.Exec(`INSERT INTO messages(id,room_id,sender_id,text,sent_at,expires_at) VALUES(?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET sent_at=min(messages.sent_at,excluded.sent_at),
			expires_at=min(messages.expires_at,excluded.expires_at)
			WHERE messages.room_id=excluded.room_id AND messages.sender_id=excluded.sender_id AND messages.text=excluded.text`,
			p.ID, p.RoomID, p.UserID, p.Text, p.SentAt, p.ExpiresAt)
	case "receipt":
		if !validUUID(p.MessageID) || !validUUID(p.UserID) || (p.State != "delivered" && p.State != "read") || p.At < 1 {
			return errInvalidPeerEvent
		}
		var retention int64
		if err := tx.QueryRow("SELECT retention_seconds FROM settings WHERE id=1").Scan(&retention); err != nil {
			return err
		}
		if p.At <= time.Now().Unix()-retention {
			return nil
		}
		var live int
		if err := tx.QueryRow("SELECT count(*) FROM messages WHERE id=? AND expires_at>?", p.MessageID, time.Now().Unix()).Scan(&live); err != nil {
			return err
		}
		if live == 0 {
			return nil
		}
		_, err = tx.Exec(`INSERT INTO receipts(message_id,user_id,state,at) VALUES(?,?,?,?)
			ON CONFLICT(message_id,user_id) DO UPDATE SET state=excluded.state,at=excluded.at
			WHERE receipts.state='delivered' AND excluded.state='read'`, p.MessageID, p.UserID, p.State, p.At)
	case "presence":
		if !validUUID(p.ID) || !validUUID(p.RoomID) || !validUUID(p.ActorID) || p.CreatedAt < 1 {
			return errInvalidPeerEvent
		}
		result, err = tx.Exec(`INSERT INTO room_events(id,room_id,kind,actor_id,created_at) VALUES(?,?,'commander_entered',?,?)
			ON CONFLICT(id) DO UPDATE SET id=excluded.id
			WHERE room_events.room_id=excluded.room_id AND room_events.kind=excluded.kind
			AND room_events.actor_id=excluded.actor_id AND room_events.created_at=excluded.created_at`,
			p.ID, p.RoomID, p.ActorID, p.CreatedAt)
	case "attachment":
		hash, decodeErr := hex.DecodeString(p.SHA256)
		if !validUUID(p.ID) || !validUUID(p.RoomID) || !validUUID(p.UserID) || !validAttachmentName(p.Name) || p.MimeType == "" || p.Size < 0 || p.Size > absoluteMaxAttachmentBytes || decodeErr != nil || len(hash) != 32 || p.SentAt < 1 || p.ExpiresAt <= p.SentAt {
			return errInvalidPeerEvent
		}
		if p.ExpiresAt <= time.Now().Unix() {
			return nil
		}
		if storageDir == "" || !verifyPeerFile(storageDir, p.ID, p.Size, p.SHA256) {
			return errors.New("peer attachment missing or corrupt")
		}
		result, err = tx.Exec(`INSERT INTO attachments(id,room_id,sender_id,name,mime_type,size,sha256,storage_name,sent_at,expires_at)
			VALUES(?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET sent_at=min(attachments.sent_at,excluded.sent_at),
			expires_at=min(attachments.expires_at,excluded.expires_at)
			WHERE attachments.room_id=excluded.room_id AND attachments.sender_id=excluded.sender_id
			AND attachments.name=excluded.name AND attachments.mime_type=excluded.mime_type
			AND attachments.size=excluded.size AND attachments.sha256=excluded.sha256
			AND attachments.storage_name=excluded.storage_name`,
			p.ID, p.RoomID, p.UserID, p.Name, p.MimeType, p.Size, hash, p.ID, p.SentAt, p.ExpiresAt)
	case "settings":
		if p.AttachmentMaxBytes < 1 || p.AttachmentMaxBytes > absoluteMaxAttachmentBytes || p.RetentionSeconds < 1 || p.RetentionSeconds > maxRetentionSeconds {
			return errInvalidPeerEvent
		}
		_, err = tx.Exec("UPDATE settings SET attachment_max_bytes=?,retention_seconds=? WHERE id=1", p.AttachmentMaxBytes, p.RetentionSeconds)
	default:
		return errInvalidPeerEvent
	}
	if err != nil {
		return err
	}
	if result != nil {
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			return errors.New("conflicting existing row")
		}
	}
	return nil
}
