package api

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"rassvet-chat/service/internal/deviceauth"
)

type attachment struct {
	ID        string `json:"id"`
	RoomID    string `json:"room_id,omitempty"`
	Name      string `json:"name"`
	MimeType  string `json:"mime_type,omitempty"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	SentAt    string `json:"sent_at,omitempty"`
	ExpiresAt string `json:"expires_at"`
}

// ponytail: общий замок сериализует публикацию файла и уборку; разделить по ID при заметном ожидании загрузок.
var filePublishMu sync.Mutex

const absoluteMaxAttachmentBytes = 100 << 20

func validAttachmentName(name string) bool {
	return name != "" && name != "." && name != ".." && name != "/" &&
		filepath.Base(strings.ReplaceAll(name, "\\", "/")) == name &&
		len([]rune(name)) <= 200 && !strings.ContainsAny(name, "\x00\r\n")
}

func attachments(w http.ResponseWriter, r *http.Request, db *sql.DB, storageDir string) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/rooms/"), "/")
	if len(parts) < 2 || len(parts) > 3 || !validUUID(parts[0]) || parts[1] != "attachments" || (len(parts) == 3 && !validUUID(parts[2])) {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	if (len(parts) == 2 && r.Method != http.MethodGet) || (len(parts) == 3 && r.Method != http.MethodGet && r.Method != http.MethodPut) {
		errorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if storageDir == "" {
		errorJSON(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	roomID := parts[0]
	var actor caller
	if r.Method == http.MethodPut {
		userID, deviceID, role, key, ok := binding(db, r.Header.Get("Authorization"))
		if !ok || deviceauth.VerifyHeaders(r, key, time.Now()) != nil {
			errorJSON(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		actor = caller{userID: userID, role: role}
		if !attachmentAccess(w, db, actor, roomID, true) {
			return
		}
		putAttachment(w, r, db, storageDir, actor, deviceID, roomID, parts[2])
		return
	}
	var ok bool
	actor, ok = authenticate(w, r, db)
	if !ok || !attachmentAccess(w, db, actor, roomID, false) {
		return
	}
	if len(parts) == 2 {
		listAttachments(w, r, db, roomID)
	} else {
		getAttachment(w, db, storageDir, roomID, parts[2])
	}
}

func attachmentAccess(w http.ResponseWriter, db *sql.DB, actor caller, roomID string, adding bool) bool {
	var allowed int
	var err error
	column := "can_read"
	if adding {
		column = "can_add_attachment"
	}
	err = db.QueryRow("SELECT CASE WHEN ?='commander' THEN can_read ELSE "+column+" END FROM room_rights WHERE room_id=? AND user_id=?",
		actor.role, roomID, actor.userID).Scan(&allowed)
	if err != nil && err != sql.ErrNoRows {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return false
	}
	if err == sql.ErrNoRows || allowed != 1 {
		status, code := http.StatusNotFound, "not_found"
		if adding {
			status, code = http.StatusForbidden, "forbidden"
		}
		errorJSON(w, status, code)
		return false
	}
	return true
}

func putAttachment(w http.ResponseWriter, r *http.Request, db *sql.DB, dir string, actor caller, deviceID, roomID, id string) {
	var maxBytes, retention int64
	if err := db.QueryRow("SELECT attachment_max_bytes,retention_seconds FROM settings WHERE id=1").Scan(&maxBytes, &retention); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if maxBytes > absoluteMaxAttachmentBytes {
		maxBytes = absoluteMaxAttachmentBytes
	}
	if r.ContentLength < 0 {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if r.ContentLength > maxBytes {
		errorJSON(w, http.StatusRequestEntityTooLarge, "too_large")
		return
	}
	name := filepath.Base(strings.ReplaceAll(r.Header.Get("X-File-Name"), "\\", "/"))
	if !validAttachmentName(name) {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	mimeType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mimeType == "" {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	declared := r.Header.Get("X-Content-SHA256")
	if declared != r.Header.Get("X-Body-SHA256") {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	temp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := temp.Chmod(0600); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(r.Body, maxBytes+1))
	if err != nil || n != r.ContentLength {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if n > maxBytes {
		errorJSON(w, http.StatusRequestEntityTooLarge, "too_large")
		return
	}
	if hex.EncodeToString(hash.Sum(nil)) != declared {
		errorJSON(w, http.StatusUnprocessableEntity, "checksum_mismatch")
		return
	}
	if _, err := db.Exec("INSERT INTO request_nonces(device_id,nonce,seen_at) VALUES(?,?,?)", deviceID, r.Header.Get("X-Nonce"), time.Now().Unix()); err != nil {
		errorJSON(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if err := temp.Sync(); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	filePublishMu.Lock()
	defer filePublishMu.Unlock()
	if !attachmentAccess(w, db, actor, roomID, true) {
		return
	}
	final := filepath.Join(dir, id)
	linked := false
	if err := os.Link(temp.Name(), final); err != nil {
		if !os.IsExist(err) {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		var existing attachment
		var sender, storedRoom string
		var storedHash []byte
		var sent, expires int64
		err = db.QueryRow("SELECT room_id,sender_id,name,mime_type,size,sha256,sent_at,expires_at FROM attachments WHERE id=?", id).
			Scan(&storedRoom, &sender, &existing.Name, &existing.MimeType, &existing.Size, &storedHash, &sent, &expires)
		if err != nil && err != sql.ErrNoRows {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		if err == nil && expires > time.Now().Unix() && storedRoom == roomID && sender == actor.userID && existing.Name == name && existing.MimeType == mimeType && existing.Size == n && hex.EncodeToString(storedHash) == declared {
			existing.ID, existing.RoomID, existing.SHA256 = id, roomID, declared
			existing.SentAt, existing.ExpiresAt = time.Unix(sent, 0).UTC().Format(time.RFC3339), time.Unix(expires, 0).UTC().Format(time.RFC3339)
			writeJSON(w, http.StatusCreated, existing)
			return
		}
		if err != sql.ErrNoRows || !verifyPeerFile(dir, id, n, declared) {
			errorJSON(w, http.StatusConflict, "conflict")
			return
		}
	} else {
		linked = true
	}
	now := time.Now().UTC().Truncate(time.Second)
	result, err := db.Exec(`INSERT INTO attachments(id,room_id,sender_id,name,mime_type,size,sha256,storage_name,sent_at,expires_at)
		SELECT ?,?,?,?,?,?,?,?,?,? WHERE EXISTS
		(SELECT 1 FROM room_rights WHERE room_id=? AND user_id=? AND can_read=1
		 AND (?='commander' OR can_add_attachment=1))`,
		id, roomID, actor.userID, name, mimeType, n, hash.Sum(nil), id, now.Unix(), now.Unix()+retention,
		roomID, actor.userID, actor.role)
	if err != nil {
		if linked {
			os.Remove(final)
		}
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	inserted, err := result.RowsAffected()
	if err != nil || inserted != 1 {
		if linked {
			os.Remove(final)
		}
		if err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
		} else {
			errorJSON(w, http.StatusForbidden, "forbidden")
		}
		return
	}
	writeJSON(w, http.StatusCreated, attachment{ID: id, RoomID: roomID, Name: name, MimeType: mimeType, Size: n, SHA256: declared, SentAt: now.Format(time.RFC3339), ExpiresAt: now.Add(time.Duration(retention) * time.Second).Format(time.RFC3339)})
}

func getAttachment(w http.ResponseWriter, db *sql.DB, dir, roomID, id string) {
	var name, mimeType, storageName string
	var size int64
	var hash []byte
	err := db.QueryRow("SELECT name,mime_type,size,sha256,storage_name FROM attachments WHERE id=? AND room_id=? AND expires_at>?", id, roomID, time.Now().Unix()).Scan(&name, &mimeType, &size, &hash, &storageName)
	if err == sql.ErrNoRows {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil || !validUUID(storageName) {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	file, err := verifiedAttachmentFile(dir, storageName, size, hash)
	if err != nil {
		errorJSON(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("X-Content-SHA256", hex.EncodeToString(hash))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}

func verifiedAttachmentFile(dir, name string, size int64, expected []byte) (*os.File, error) {
	file, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() != size {
		file.Close()
		return nil, errors.New("attachment size mismatch")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil || !bytes.Equal(hash.Sum(nil), expected) {
		file.Close()
		return nil, errors.New("attachment checksum mismatch")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func listAttachments(w http.ResponseWriter, r *http.Request, db *sql.DB, roomID string) {
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
		var err error
		sinceRowID, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil || sinceRowID < 0 || (sinceRowID > 0 && !validUUID(sinceID)) || (sinceRowID == 0 && sinceID != "") {
			errorJSON(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if sinceRowID > 0 {
			var storedID string
			err = db.QueryRow("SELECT id FROM attachments WHERE rowid=?", sinceRowID).Scan(&storedID)
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
	statement := `SELECT rowid,id,name,mime_type,size,sha256,sent_at,expires_at FROM attachments
		WHERE room_id=? AND expires_at>? AND (sent_at>? OR (sent_at=? AND id>?))
		ORDER BY sent_at,id LIMIT ?`
	args := []any{roomID, time.Now().Unix(), afterTime, afterTime, afterID, limit + 1}
	if latest || backward {
		statement = `SELECT rowid,id,name,mime_type,size,sha256,sent_at,expires_at FROM attachments WHERE room_id=? AND expires_at>?`
		args = []any{roomID, time.Now().Unix()}
		if backward {
			statement += ` AND (sent_at<? OR (sent_at=? AND id<?))`
			args = append(args, afterTime, afterTime, afterID)
		}
		statement += ` ORDER BY sent_at DESC,id DESC LIMIT ?`
		args = append(args, limit+1)
	} else if updates {
		statement = `SELECT rowid,id,name,mime_type,size,sha256,sent_at,expires_at FROM attachments WHERE room_id=? AND expires_at>? AND rowid>? ORDER BY rowid LIMIT ?`
		args = []any{roomID, time.Now().Unix(), sinceRowID, limit + 1}
	}
	rows, err := db.Query(statement, args...)
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	defer rows.Close()
	items := make([]attachment, 0)
	var lastTime, lastRowID int64
	lastID := ""
	for rows.Next() {
		var a attachment
		var hash []byte
		var rowID, sent, expires int64
		if err := rows.Scan(&rowID, &a.ID, &a.Name, &a.MimeType, &a.Size, &hash, &sent, &expires); err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		a.SHA256 = hex.EncodeToString(hash)
		a.ExpiresAt = time.Unix(expires, 0).UTC().Format(time.RFC3339)
		items = append(items, a)
		if len(items) <= limit {
			lastTime = sent
			lastRowID = rowID
			lastID = a.ID
		}
		if latest && rowID > sinceRowID && len(items) <= limit {
			sinceRowID, sinceID = rowID, a.ID
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
