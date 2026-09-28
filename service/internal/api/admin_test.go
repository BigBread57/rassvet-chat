package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rassvet-chat/service/internal/bootstrap"
	"rassvet-chat/service/migrations"
)

func TestDeleteUserRevokesAndReplicates(t *testing.T) {
	a, err := migrations.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := migrations.Open(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	adminID, _, err := bootstrap.Admin(a, "Администратор", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := ConfigurePeer(a, "node-a"); err != nil {
		t.Fatal(err)
	}
	adminKey, err := bindTestDevice(a, adminID, "admin-device")
	if err != nil {
		t.Fatal(err)
	}
	userID, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	roomID, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	var userKey *ecdsa.PrivateKey
	for _, db := range []*sql.DB{a, b} {
		if _, err := db.Exec("INSERT INTO users(id,name,role,created_at) VALUES(?,'Участник','user',?)", userID, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
		key, err := bindTestDevice(db, userID, "user-device")
		if err != nil {
			t.Fatal(err)
		}
		if db == a {
			userKey = key
		}
		if _, err := db.Exec("INSERT INTO rooms(id,name,created_by,created_at) VALUES(?,'История',?,?)", roomID, userID, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO room_rights(room_id,user_id,can_read) VALUES(?,?,1)", roomID, userID); err != nil {
			t.Fatal(err)
		}
	}
	handler := Handler(a, Node{URL: "https://example.com"}, Node{URL: "https://other.example.com"})
	before := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	signTestRequest(t, before, userID, "user-device", userKey, "user-before-delete")
	beforeResult := httptest.NewRecorder()
	handler.ServeHTTP(beforeResult, before)
	if beforeResult.Code != http.StatusOK {
		t.Fatalf("before deletion=%d %s", beforeResult.Code, beforeResult.Body)
	}
	request := func(method, path, nonce string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		signTestRequest(t, req, adminID, "admin-device", adminKey, nonce)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	if got := request(http.MethodDelete, "/v1/admin/users/"+adminID, "nonce-for-self-delete"); got.Code != http.StatusConflict {
		t.Fatalf("self delete=%d %s", got.Code, got.Body)
	}
	if got := request(http.MethodDelete, "/v1/admin/users/"+userID, "nonce-for-delete-user"); got.Code != http.StatusNoContent {
		t.Fatalf("delete=%d %s", got.Code, got.Body)
	}
	after := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	signTestRequest(t, after, userID, "user-device", userKey, "user-after-delete")
	afterResult := httptest.NewRecorder()
	handler.ServeHTTP(afterResult, after)
	if afterResult.Code != http.StatusUnauthorized {
		t.Fatalf("after deletion=%d %s", afterResult.Code, afterResult.Body)
	}
	if got := request(http.MethodGet, "/v1/admin/users", "nonce-for-list-users"); got.Code != http.StatusOK || strings.Contains(got.Body.String(), userID) {
		t.Fatalf("list=%d %s", got.Code, got.Body)
	}
	activation := httptest.NewRequest(http.MethodPost, "/v1/admin/users/"+userID+"/activation", strings.NewReader(`{"ttl_seconds":600}`))
	signTestRequest(t, activation, adminID, "admin-device", adminKey, "nonce-after-user-deleted")
	activationResult := httptest.NewRecorder()
	handler.ServeHTTP(activationResult, activation)
	if activationResult.Code != http.StatusNotFound {
		t.Fatalf("activation after deletion=%d %s", activationResult.Code, activationResult.Body)
	}
	grant := httptest.NewRequest(http.MethodPut, "/v1/admin/rooms/"+roomID+"/members/"+userID,
		strings.NewReader(`{"read":true,"send_text":true,"add_attachment":false,"save_attachment":false}`))
	signTestRequest(t, grant, adminID, "admin-device", adminKey, "nonce-grant-deleted-user")
	grantResult := httptest.NewRecorder()
	handler.ServeHTTP(grantResult, grant)
	if grantResult.Code != http.StatusNotFound {
		t.Fatalf("grant after deletion=%d %s", grantResult.Code, grantResult.Body)
	}
	var count int
	if err := a.QueryRow("SELECT count(*) FROM rooms WHERE id=? AND created_by=?", roomID, userID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("history count=%d err=%v", count, err)
	}
	if err := a.QueryRow("SELECT count(*) FROM device_bindings WHERE user_id=?", userID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("binding count=%d err=%v", count, err)
	}
	var peerKey []byte
	if err := b.QueryRow("SELECT public_key_p256 FROM device_bindings WHERE user_id=?", userID).Scan(&peerKey); err != nil {
		t.Fatal(err)
	}
	var event peerEvent
	var createdAt int64
	var payload string
	if err := a.QueryRow("SELECT id,created_at,payload FROM peer_events WHERE kind='user_deleted'").Scan(&event.ID, &createdAt, &payload); err != nil {
		t.Fatal(err)
	}
	event.Payload = json.RawMessage(payload)
	event.Kind, event.AuthorNodeID = "user_deleted", "node-a"
	event.CreatedAt = time.Unix(createdAt, 0).UTC().Format(time.RFC3339)
	if !json.Valid(event.Payload) || applyPeerEvent(b, event, "") != nil {
		t.Fatal("delete event did not replicate")
	}
	if err := b.QueryRow("SELECT count(*) FROM user_deletions WHERE user_id=?", userID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("peer deletion count=%d err=%v", count, err)
	}
	if err := b.QueryRow("SELECT count(*) FROM device_bindings WHERE user_id=?", userID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("peer binding count=%d err=%v", count, err)
	}
	staleID, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	stalePayload, err := json.Marshal(map[string]any{"user_id": userID, "device_id": "user-device",
		"public_key_hex": hex.EncodeToString(peerKey), "activated_at": time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if err := applyPeerEvent(b, peerEvent{ID: staleID, Kind: "binding", AuthorNodeID: "node-a",
		CreatedAt: time.Now().UTC().Format(time.RFC3339), Payload: stalePayload}, ""); err != nil {
		t.Fatal(err)
	}
	if err := b.QueryRow("SELECT count(*) FROM device_bindings WHERE user_id=?", userID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale binding restored access: count=%d err=%v", count, err)
	}
}

func TestAdminAccess(t *testing.T) {
	db, err := migrations.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	adminID, _, err := bootstrap.Admin(db, "Администратор", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO users(id, name, role, created_at) VALUES ('ordinary', 'Обычный', 'user', ?)", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	adminKey, err := bindTestDevice(db, adminID, "admin-device")
	if err != nil {
		t.Fatal(err)
	}
	userKey, err := bindTestDevice(db, "ordinary", "user-device")
	if err != nil {
		t.Fatal(err)
	}
	handler := Handler(db)
	landing := httptest.NewRecorder()
	handler.ServeHTTP(landing, httptest.NewRequest(http.MethodGet, "/", nil))
	if landing.Code != http.StatusOK || !strings.Contains(landing.Body.String(), "код активации") {
		t.Fatalf("node landing=%d %s", landing.Code, landing.Body)
	}
	call := func(req *http.Request) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	newRequest := func() *http.Request {
		return httptest.NewRequest(http.MethodPost, "/v1/admin/users", strings.NewReader(`{"name":"Новый","role":"user"}`))
	}
	if got := call(newRequest()).Code; got != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", got)
	}
	userReq := newRequest()
	signTestRequest(t, userReq, "ordinary", "user-device", userKey, "nonce-for-ordinary-1")
	if got := call(userReq).Code; got != http.StatusForbidden {
		t.Fatalf("user status=%d", got)
	}
	adminReq := newRequest()
	signTestRequest(t, adminReq, adminID, "admin-device", adminKey, "nonce-for-admin-0001")
	if got := call(adminReq).Code; got != http.StatusCreated {
		t.Fatalf("admin status=%d", got)
	}
	meReq := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	signTestRequest(t, meReq, adminID, "admin-device", adminKey, "nonce-for-admin-me-1")
	if got := call(meReq); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"name":"Администратор"`) {
		t.Fatalf("me=%d %s", got.Code, got.Body)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM users WHERE name='Новый'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("created users=%d err=%v", count, err)
	}
	replay := newRequest()
	signTestRequest(t, replay, adminID, "admin-device", adminKey, "nonce-for-admin-0001")
	if got := call(replay).Code; got != http.StatusUnauthorized {
		t.Fatalf("replay status=%d", got)
	}
	wrong := newRequest()
	signTestRequest(t, wrong, adminID, "admin-device", userKey, "nonce-for-admin-0002")
	if got := call(wrong).Code; got != http.StatusUnauthorized {
		t.Fatalf("wrong key status=%d", got)
	}
	tooLarge := httptest.NewRequest(http.MethodPost, "/v1/admin/users", strings.NewReader(strings.Repeat("a", (1<<20)+1)))
	signTestRequest(t, tooLarge, adminID, "admin-device", adminKey, "nonce-for-admin-0003")
	if got := call(tooLarge).Code; got != http.StatusRequestEntityTooLarge {
		t.Fatalf("large body status=%d", got)
	}
}

func TestUniqueRussianNamesAndRoleReplication(t *testing.T) {
	a, err := migrations.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := migrations.Open(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	adminID, _, err := bootstrap.Admin(a, "Администратор", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := ConfigurePeer(a, "node-a"); err != nil {
		t.Fatal(err)
	}
	key, err := bindTestDevice(a, adminID, "admin-device")
	if err != nil {
		t.Fatal(err)
	}
	handler := Handler(a)
	call := func(method, path, body, nonce string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		signTestRequest(t, req, adminID, "admin-device", key, nonce)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	created := call(http.MethodPost, "/v1/admin/users", `{"name":"Мария","role":"user"}`, "nonce-for-new-maria")
	if created.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", created.Code, created.Body)
	}
	var user struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	if duplicate := call(http.MethodPost, "/v1/admin/users", `{"name":"мария","role":"user"}`, "nonce-for-duplicate-maria"); duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate user: %d %s", duplicate.Code, duplicate.Body)
	}
	room := call(http.MethodPost, "/v1/admin/rooms", `{"name":"Группа"}`, "nonce-for-new-group")
	if room.Code != http.StatusCreated {
		t.Fatalf("create room: %d %s", room.Code, room.Body)
	}
	var createdRoom struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(room.Body.Bytes(), &createdRoom); err != nil {
		t.Fatal(err)
	}
	if duplicate := call(http.MethodPost, "/v1/admin/rooms", `{"name":"группа"}`, "nonce-for-duplicate-group"); duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate room: %d %s", duplicate.Code, duplicate.Body)
	}
	if changed := call(http.MethodPut, "/v1/admin/users/"+user.ID, `{"role":"commander"}`, "nonce-for-promote-maria"); changed.Code != http.StatusOK {
		t.Fatalf("change role: %d %s", changed.Code, changed.Body)
	}
	grant := call(http.MethodPut, "/v1/admin/rooms/"+createdRoom.ID+"/members/"+user.ID,
		`{"read":true,"send_text":true,"add_attachment":false,"save_attachment":false}`, "nonce-for-grant-maria")
	if grant.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", grant.Code, grant.Body)
	}
	members := call(http.MethodGet, "/v1/admin/rooms/"+createdRoom.ID+"/members", "", "nonce-for-members-list")
	if members.Code != http.StatusOK || !strings.Contains(members.Body.String(), `"role":"commander"`) {
		t.Fatalf("captain member: %d %s", members.Code, members.Body)
	}
	rows, err := a.Query("SELECT id,kind,created_at,payload FROM peer_events WHERE kind IN ('user','user_role') ORDER BY rowid")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var event peerEvent
		var seconds int64
		var payload string
		if err := rows.Scan(&event.ID, &event.Kind, &seconds, &payload); err != nil {
			t.Fatal(err)
		}
		event.Payload = json.RawMessage(payload)
		event.AuthorNodeID = "node-a"
		event.CreatedAt = time.Unix(seconds, 0).UTC().Format(time.RFC3339)
		if err := applyPeerEvent(b, event, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var role string
	if err := b.QueryRow("SELECT role FROM users WHERE id=?", user.ID).Scan(&role); err != nil || role != "commander" {
		t.Fatalf("replicated role=%q err=%v", role, err)
	}
}

func bindTestDevice(db *sql.DB, userID, deviceID string) (*ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec("INSERT INTO device_bindings(user_id, device_id, public_key_p256, activated_at) VALUES (?, ?, ?, ?)", userID, deviceID, publicDER, time.Now().Unix())
	return key, err
}

func signTestRequest(t *testing.T, req *http.Request, userID, deviceID string, key *ecdsa.PrivateKey, nonce string) {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	req.Body = io.NopCloser(strings.NewReader(string(body)))
	hash := sha256.Sum256(body)
	req.Header.Set("Authorization", "Device "+userID+":"+deviceID)
	req.Header.Set("X-Time", time.Now().UTC().Format(time.RFC3339))
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Body-SHA256", hex.EncodeToString(hash[:]))
	message := strings.Join([]string{req.Method, req.URL.RequestURI(), req.Header.Get("Authorization"), req.Header.Get("X-Time"), nonce, req.Header.Get("X-Body-SHA256")}, "\n")
	digest := sha256.Sum256([]byte(message))
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(signature))
}
