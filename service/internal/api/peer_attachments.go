package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type peerAttachmentPayload struct {
	ID        string `json:"id"`
	RoomID    string `json:"room_id"`
	UserID    string `json:"user_id"`
	Name      string `json:"name"`
	MimeType  string `json:"mime_type"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	SentAt    int64  `json:"sent_at"`
	ExpiresAt int64  `json:"expires_at"`
}

func peerAttachment(w http.ResponseWriter, r *http.Request, db *sql.DB, dir string) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/peer/attachments/")
	if !validUUID(id) {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	if db == nil || dir == "" {
		errorJSON(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if r.Method == http.MethodGet {
		var size int64
		var hash []byte
		err := db.QueryRow("SELECT size,sha256 FROM attachments WHERE id=? AND expires_at>?", id, time.Now().Unix()).Scan(&size, &hash)
		if err == sql.ErrNoRows {
			errorJSON(w, http.StatusNotFound, "not_found")
			return
		}
		if err != nil {
			errorJSON(w, http.StatusInternalServerError, "internal_error")
			return
		}
		file, err := verifiedAttachmentFile(dir, id, size, hash)
		if err != nil {
			errorJSON(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
		defer file.Close()
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.Header().Set("X-Content-SHA256", hex.EncodeToString(hash))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, file)
		return
	}
	if r.Method != http.MethodPut {
		errorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var maxBytes int64
	if err := db.QueryRow("SELECT attachment_max_bytes FROM settings WHERE id=1").Scan(&maxBytes); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if maxBytes > absoluteMaxAttachmentBytes {
		maxBytes = absoluteMaxAttachmentBytes
	}
	if r.ContentLength < 0 || r.ContentLength > maxBytes {
		errorJSON(w, http.StatusRequestEntityTooLarge, "too_large")
		return
	}
	declared := r.Header.Get("X-Content-SHA256")
	if hash, err := hex.DecodeString(declared); err != nil || len(hash) != sha256.Size {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	temp, err := os.CreateTemp(dir, ".peer-upload-*")
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
	hasher := sha256.New()
	n, err := io.Copy(io.MultiWriter(temp, hasher), io.LimitReader(r.Body, maxBytes+1))
	if err != nil || n != r.ContentLength {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if hex.EncodeToString(hasher.Sum(nil)) != declared {
		errorJSON(w, http.StatusUnprocessableEntity, "checksum_mismatch")
		return
	}
	if err := temp.Sync(); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	filePublishMu.Lock()
	defer filePublishMu.Unlock()
	if err := os.Link(temp.Name(), filepath.Join(dir, id)); err != nil && !os.IsExist(err) {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if !verifyPeerFile(dir, id, n, declared) {
		errorJSON(w, http.StatusConflict, "conflict")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "size": n, "sha256": declared})
}

func verifyPeerFile(dir, id string, size int64, declared string) bool {
	file, err := os.Open(filepath.Join(dir, id))
	if err != nil {
		return false
	}
	defer file.Close()
	hasher := sha256.New()
	n, err := io.Copy(hasher, file)
	return err == nil && n == size && hex.EncodeToString(hasher.Sum(nil)) == declared
}

func fetchPeerFile(ctx context.Context, client *http.Client, peerURL, dir string, p peerAttachmentPayload) error {
	if dir == "" || !validUUID(p.ID) || p.Size < 0 || p.Size > absoluteMaxAttachmentBytes || p.ExpiresAt <= time.Now().Unix() {
		return errInvalidPeerEvent
	}
	if verifyPeerFile(dir, p.ID, p.Size, p.SHA256) {
		return nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, peerURL+"/v1/peer/attachments/"+p.ID, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength != p.Size || response.Header.Get("X-Content-SHA256") != p.SHA256 {
		return errors.New("peer attachment metadata mismatch")
	}
	temp, err := os.CreateTemp(dir, ".peer-upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := temp.Chmod(0600); err != nil {
		return err
	}
	hasher := sha256.New()
	n, err := io.Copy(io.MultiWriter(temp, hasher), io.LimitReader(response.Body, p.Size+1))
	if err != nil || n != p.Size || hex.EncodeToString(hasher.Sum(nil)) != p.SHA256 {
		return errors.New("peer attachment checksum mismatch")
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	filePublishMu.Lock()
	err = os.Link(temp.Name(), filepath.Join(dir, p.ID))
	filePublishMu.Unlock()
	if err != nil && !os.IsExist(err) {
		return err
	}
	if !verifyPeerFile(dir, p.ID, p.Size, p.SHA256) {
		return errors.New("peer attachment file collision")
	}
	return nil
}
