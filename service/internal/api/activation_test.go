package api

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rassvet-chat/service/internal/bootstrap"
	"rassvet-chat/service/migrations"
)

func TestActivationIsAtomicAndOneTime(t *testing.T) {
	db, err := migrations.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	userID, code, err := bootstrap.Admin(db, "Администратор", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	trustedHash := base64.StdEncoding.EncodeToString(make([]byte, 32))
	handler := Handler(db, Node{URL: "https://node-a", TLSSPKISHA256: trustedHash}, Node{URL: "https://node-b", TLSSPKISHA256: trustedHash})
	call := func(code, device string) int {
		body, err := json.Marshal(map[string]string{"code": code, "device_id": device, "public_key_p256": base64.StdEncoding.EncodeToString(publicDER)})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/activation", bytes.NewReader(body)))
		return w.Code
	}
	if got := call(code, "first-phone"); got != http.StatusOK {
		t.Fatalf("first activation=%d", got)
	}
	if got := call(code, "second-phone"); got != http.StatusConflict {
		t.Fatalf("reused code=%d", got)
	}
	var boundDevice string
	if err := db.QueryRow("SELECT device_id FROM device_bindings WHERE user_id = ?", userID).Scan(&boundDevice); err != nil || boundDevice != "first-phone" {
		t.Fatalf("binding=%q err=%v", boundDevice, err)
	}
	secondHash := sha256.Sum256([]byte("second-code"))
	if _, err := db.Exec("INSERT INTO activation_codes(code_hash, user_id, expires_at) VALUES (?, ?, ?)", secondHash[:], userID, time.Now().Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	if got := call("second-code", "second-phone"); got != http.StatusConflict {
		t.Fatalf("second phone=%d", got)
	}
	expiredHash := sha256.Sum256([]byte("expired-code"))
	if _, err := db.Exec("INSERT INTO activation_codes(code_hash, user_id, expires_at) VALUES (?, ?, ?)", expiredHash[:], userID, time.Now().Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	if got := call("expired-code", "second-phone"); got != http.StatusConflict {
		t.Fatalf("expired code=%d", got)
	}
	if err := db.QueryRow("SELECT device_id FROM device_bindings WHERE user_id = ?", userID).Scan(&boundDevice); err != nil || boundDevice != "first-phone" {
		t.Fatalf("binding changed=%q err=%v", boundDevice, err)
	}
}

func TestParseNodes(t *testing.T) {
	pinA := base64.StdEncoding.EncodeToString(make([]byte, 32))
	pinB := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	nodes := []Node{{URL: "https://node-a", TLSSPKISHA256: pinA}, {URL: "https://node-b", TLSSPKISHA256: pinB}}
	data, err := json.Marshal(nodes)
	if err != nil {
		t.Fatal(err)
	}
	if nodes, err := ParseNodes(data); err != nil || len(nodes) != 2 {
		t.Fatalf("valid nodes: %v", err)
	}
	if url, err := PeerURL(nodes, pinA, pinB); err != nil || url != "https://node-b" {
		t.Fatalf("valid peer URL: %q %v", url, err)
	}
	if _, err := PeerURL(nodes, pinB, pinA); err != nil {
		t.Fatalf("reversed node order: %v", err)
	}
	if _, err := PeerURL(nodes, "unknown-own-key", pinB); err == nil {
		t.Fatal("missing own key accepted")
	}
	if _, err := PeerURL(nodes, pinA, "unknown-peer-key"); err == nil {
		t.Fatal("missing peer key accepted")
	}
	if _, err := ParseNodes([]byte(`[{"url":"http://node-a","tls_spki_sha256":"bad"}]`)); err == nil {
		t.Fatal("invalid trust accepted")
	}
	duplicatePin, _ := json.Marshal([]Node{{URL: "https://node-a", TLSSPKISHA256: pinA}, {URL: "https://node-b", TLSSPKISHA256: pinA}})
	if _, err := ParseNodes(duplicatePin); err == nil {
		t.Fatal("duplicate node key accepted")
	}
	withPath, _ := json.Marshal([]Node{{URL: "https://node-a/path", TLSSPKISHA256: pinA}, {URL: "https://node-b", TLSSPKISHA256: pinB}})
	if _, err := ParseNodes(withPath); err == nil {
		t.Fatal("node URL with path accepted")
	}
}

func TestAdminIssuesActivationCode(t *testing.T) {
	db, err := migrations.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	adminID, _, err := bootstrap.Admin(db, "Админ", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO users(id,name,role,created_at) VALUES ('recipient','Получатель','user',?)", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	adminKey, err := bindTestDevice(db, adminID, "admin-device")
	if err != nil {
		t.Fatal(err)
	}
	pin := base64.StdEncoding.EncodeToString(make([]byte, 32))
	handler := Handler(db, Node{URL: "https://node-a", TLSSPKISHA256: pin}, Node{URL: "https://node-b", TLSSPKISHA256: pin})
	issue := func(nonce string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "https://node-a/v1/admin/users/recipient/activation", strings.NewReader(`{"ttl_seconds":600}`))
		signTestRequest(t, req, adminID, "admin-device", adminKey, nonce)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	first := issue("issue-nonce-000001")
	if first.Code != http.StatusCreated {
		t.Fatalf("issue=%d %s", first.Code, first.Body)
	}
	var issued struct {
		Code, Issuer string
		Nodes        []Node
	}
	if err := json.Unmarshal(first.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	if issued.Code == "" || issued.Issuer != "https://node-a" || len(issued.Nodes) != 2 {
		t.Fatalf("issue response=%s", first.Body)
	}
	second := issue("issue-nonce-000002")
	if second.Code != http.StatusCreated {
		t.Fatalf("reissue=%d %s", second.Code, second.Body)
	}
	var oldCount int
	oldHash := sha256.Sum256([]byte(issued.Code))
	if err := db.QueryRow("SELECT count(*) FROM activation_codes WHERE code_hash=?", oldHash[:]).Scan(&oldCount); err != nil || oldCount != 0 {
		t.Fatalf("old code count=%d err=%v", oldCount, err)
	}
}
