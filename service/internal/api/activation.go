package api

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Node struct {
	URL           string `json:"url"`
	TLSSPKISHA256 string `json:"tls_spki_sha256"`
}

func adminActivation(w http.ResponseWriter, r *http.Request, db *sql.DB, actor caller, nodes []Node) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/admin/users/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "activation" {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if actor.role != "admin" {
		errorJSON(w, http.StatusForbidden, "forbidden")
		return
	}
	var input struct {
		TTLSeconds int `json:"ttl_seconds"`
	}
	if !decodeJSON(actor.body, &input) || input.TTLSeconds < 1 || input.TTLSeconds > 600 {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	issuer := ""
	for _, node := range nodes {
		address, err := url.Parse(node.URL)
		if err == nil && address.Host == r.Host && address.Scheme == "https" {
			issuer = node.URL
		}
	}
	if issuer == "" {
		errorJSON(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	var raw [20]byte
	if _, err := rand.Read(raw[:]); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
	hash := sha256.Sum256([]byte(code))
	tx, err := db.Begin()
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM users u WHERE u.id=? AND NOT EXISTS
		(SELECT 1 FROM user_deletions d WHERE d.user_id=u.id)`, parts[0]).Scan(&count); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if count == 0 {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	if err := tx.QueryRow("SELECT count(*) FROM device_bindings WHERE user_id=?", parts[0]).Scan(&count); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if count != 0 {
		errorJSON(w, http.StatusConflict, "conflict")
		return
	}
	if _, err := tx.Exec("DELETE FROM activation_codes WHERE user_id=? AND used_at IS NULL", parts[0]); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	expiresAt := time.Now().Add(time.Duration(input.TTLSeconds+1) * time.Second).Truncate(time.Second)
	if _, err := tx.Exec("INSERT INTO activation_codes(code_hash,user_id,expires_at) VALUES(?,?,?)", hash[:], parts[0], expiresAt.Unix()); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if err := tx.Commit(); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"code": code, "expires_at": expiresAt.UTC().Format(time.RFC3339), "issuer": issuer, "nodes": nodes})
}

// ParseNodes принимает ровно два доверенных адреса для выдачи телефону.
func ParseNodes(data []byte) ([]Node, error) {
	var nodes []Node
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, err
	}
	if len(nodes) != 2 || nodes[0].URL == nodes[1].URL || nodes[0].TLSSPKISHA256 == nodes[1].TLSSPKISHA256 {
		return nil, fmt.Errorf("expected two different trusted nodes")
	}
	for _, node := range nodes {
		address, err := url.Parse(node.URL)
		if err != nil || address.Scheme != "https" || address.Host == "" || address.User != nil || address.Path != "" || address.RawQuery != "" || address.Fragment != "" {
			return nil, fmt.Errorf("invalid node URL")
		}
		hash, err := base64.StdEncoding.DecodeString(node.TLSSPKISHA256)
		if err != nil || len(hash) != sha256.Size {
			return nil, fmt.Errorf("invalid node key hash")
		}
	}
	return nodes, nil
}

// PeerURL связывает оба адреса выдачи с сертификатами этого и второго узла.
func PeerURL(nodes []Node, ownPin, peerPin string) (string, error) {
	if len(nodes) != 2 || ownPin == "" || peerPin == "" || ownPin == peerPin {
		return "", fmt.Errorf("invalid node keys")
	}
	var ownCount int
	peerURL := ""
	for _, node := range nodes {
		switch node.TLSSPKISHA256 {
		case ownPin:
			ownCount++
		case peerPin:
			if peerURL != "" {
				return "", fmt.Errorf("duplicate peer key in nodes.json")
			}
			peerURL = node.URL
		}
	}
	if ownCount != 1 || peerURL == "" {
		return "", fmt.Errorf("node keys do not match nodes.json")
	}
	return peerURL, nil
}

// activate погашает код и привязывает один телефон в одной транзакции.
func activate(w http.ResponseWriter, r *http.Request, db *sql.DB, nodes []Node) {
	if len(nodes) != 2 {
		errorJSON(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if len(body) > 1<<20 {
		errorJSON(w, http.StatusRequestEntityTooLarge, "too_large")
		return
	}
	var input struct {
		Code      string `json:"code"`
		DeviceID  string `json:"device_id"`
		PublicKey string `json:"public_key_p256"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || input.Code == "" || input.DeviceID == "" {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	publicDER, err := base64.StdEncoding.DecodeString(input.PublicKey)
	if err != nil || len(publicDER) > 512 {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	parsed, err := x509.ParsePKIXPublicKey(publicDER)
	key, ok := parsed.(*ecdsa.PublicKey)
	if err != nil || !ok || key.Curve.Params().Name != "P-256" {
		errorJSON(w, http.StatusBadRequest, "invalid_request")
		return
	}
	hash := sha256.Sum256([]byte(input.Code))
	tx, err := db.Begin()
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	defer tx.Rollback()
	var userID, role string
	var expiresAt int64
	var usedAt sql.NullInt64
	err = tx.QueryRow("SELECT a.user_id, u.role, a.expires_at, a.used_at FROM activation_codes a JOIN users u ON u.id = a.user_id WHERE a.code_hash = ?", hash[:]).Scan(&userID, &role, &expiresAt, &usedAt)
	if err == sql.ErrNoRows {
		errorJSON(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	now := time.Now().Unix()
	if usedAt.Valid || expiresAt <= now {
		errorJSON(w, http.StatusConflict, "conflict")
		return
	}
	var bound int
	if err := tx.QueryRow("SELECT count(*) FROM device_bindings WHERE user_id = ?", userID).Scan(&bound); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if bound != 0 {
		errorJSON(w, http.StatusConflict, "conflict")
		return
	}
	result, err := tx.Exec("UPDATE activation_codes SET used_at = ? WHERE code_hash = ? AND used_at IS NULL AND expires_at > ?", now, hash[:], now)
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		errorJSON(w, http.StatusConflict, "conflict")
		return
	}
	if _, err := tx.Exec("INSERT INTO device_bindings(user_id, device_id, public_key_p256, activated_at) VALUES (?, ?, ?, ?)", userID, input.DeviceID, publicDER, now); err != nil {
		errorJSON(w, http.StatusConflict, "conflict")
		return
	}
	if err := tx.Commit(); err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"user_id": userID, "device_id": input.DeviceID, "role": role, "nodes": nodes})
}
