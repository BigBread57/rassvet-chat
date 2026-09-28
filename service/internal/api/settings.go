package api

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type settings struct {
	AttachmentMaxBytes int64 `json:"attachment_max_bytes"`
	RetentionSeconds   int64 `json:"retention_seconds"`
}

const maxRetentionSeconds = (1<<63 - 1) / int64(time.Second)

func adminSettings(w http.ResponseWriter, r *http.Request, db *sql.DB, actor caller) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		errorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if actor.role != "admin" {
		errorJSON(w, http.StatusForbidden, "forbidden")
		return
	}
	if r.Method == http.MethodPut {
		var input settings
		if !decodeJSON(actor.body, &input) || input.AttachmentMaxBytes < 1 || input.AttachmentMaxBytes > absoluteMaxAttachmentBytes || input.RetentionSeconds < 1 || input.RetentionSeconds > maxRetentionSeconds {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if _, err := db.Exec("UPDATE settings SET attachment_max_bytes=?, retention_seconds=? WHERE id=1", input.AttachmentMaxBytes, input.RetentionSeconds); err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
	}
	var current settings
	if err := db.QueryRow("SELECT attachment_max_bytes, retention_seconds FROM settings WHERE id=1").Scan(&current.AttachmentMaxBytes, &current.RetentionSeconds); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeJSON(w, http.StatusOK, current)
}

// PruneExpired удаляет подтверждения до текста, на который они ссылаются.
func PruneExpired(db *sql.DB, now time.Time, storageDir ...string) error {
	filePublishMu.Lock()
	defer filePublishMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM receipts WHERE message_id IN (SELECT id FROM messages WHERE expires_at<=?)", now.Unix()); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM messages WHERE expires_at<=?", now.Unix()); err != nil {
		return err
	}
	expiredFiles := make(map[string]bool)
	rows, err := tx.Query("SELECT storage_name FROM attachments WHERE expires_at<=?", now.Unix())
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		expiredFiles[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if _, err := tx.Exec("DELETE FROM attachments WHERE expires_at<=?", now.Unix()); err != nil {
		return err
	}
	var retention int64
	if err := tx.QueryRow("SELECT retention_seconds FROM settings WHERE id=1").Scan(&retention); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM room_events WHERE created_at<=?", now.Unix()-retention); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE peer_events SET payload='{}' WHERE length(payload)>2 AND
		((kind='message' AND CAST(json_extract(CAST(payload AS TEXT),'$.expires_at') AS INTEGER)<=?) OR
		 (kind='attachment' AND CAST(json_extract(CAST(payload AS TEXT),'$.expires_at') AS INTEGER)<=?) OR
		 (kind='presence' AND CAST(json_extract(CAST(payload AS TEXT),'$.created_at') AS INTEGER)<=?) OR
		 (kind='receipt' AND NOT EXISTS (SELECT 1 FROM messages WHERE id=json_extract(CAST(peer_events.payload AS TEXT),'$.message_id'))))`,
		now.Unix(), now.Unix(), now.Unix()-retention); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM request_nonces WHERE seen_at<?", now.Add(-10*time.Minute).Unix()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if len(storageDir) == 0 || storageDir[0] == "" {
		return nil
	}
	entries, err := os.ReadDir(storageDir[0])
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && (strings.HasPrefix(entry.Name(), ".upload-") || strings.HasPrefix(entry.Name(), ".peer-upload-")) {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.ModTime().Before(now.Add(-24 * time.Hour)) {
				if err := os.Remove(filepath.Join(storageDir[0], entry.Name())); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
			continue
		}
		if !entry.Type().IsRegular() || !validUUID(entry.Name()) {
			continue
		}
		var count int
		if err := db.QueryRow("SELECT count(*) FROM attachments WHERE storage_name=?", entry.Name()).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !expiredFiles[entry.Name()] && !info.ModTime().Before(now.Add(-time.Hour)) {
				continue
			}
			if err := os.Remove(filepath.Join(storageDir[0], entry.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}
