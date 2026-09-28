package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"rassvet-chat/service/internal/bootstrap"
	"rassvet-chat/service/internal/nodetls"
	"rassvet-chat/service/migrations"
)

func TestConfigurePeerSeedsAdminCreatedBeforeServe(t *testing.T) {
	db, err := migrations.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	adminID, _, err := bootstrap.Admin(db, "Администратор", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := ConfigurePeer(db, "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := ConfigurePeer(db, "node-a"); err != nil {
		t.Fatal(err)
	}
	var count int
	var payload string
	if err := db.QueryRow("SELECT count(*),min(payload) FROM peer_events WHERE kind='user' AND author_node_id='node-a'").Scan(&count, &payload); err != nil {
		t.Fatal(err)
	}
	if count != 1 || !strings.Contains(payload, adminID) || !strings.Contains(payload, `"role":"admin"`) {
		t.Fatalf("initial admin journal: count=%d payload=%s", count, payload)
	}
}

func TestPeerEventEqualityKeepsLargeIntegers(t *testing.T) {
	if jsonEqual([]byte(`{"at":9007199254740992}`), []byte(`{"at":9007199254740993}`)) {
		t.Fatal("distinct 64-bit values were treated as the same event")
	}
	if !jsonEqual([]byte(`{"at":1,"state":"read"}`), []byte(`{"state":"read","at":1}`)) {
		t.Fatal("equivalent JSON objects were treated as different events")
	}
}

func TestPeerReceiptForExpiredMessageDoesNotBlockJournal(t *testing.T) {
	db, err := migrations.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().Unix()
	userID := "00000000-0000-4000-8000-000000000001"
	roomID := "00000000-0000-4000-8000-000000000002"
	messageID := "00000000-0000-4000-8000-000000000003"
	if _, err := db.Exec("INSERT INTO users VALUES(?,?,?,?)", userID, "Анна", "user", now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO rooms VALUES(?,?,?,?)", roomID, "Группа", userID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO messages VALUES(?,?,?,?,?,?)", messageID, roomID, userID, "Старое", now-2, now-1); err != nil {
		t.Fatal(err)
	}
	if err := PruneExpired(db, time.Unix(now, 0)); err != nil {
		t.Fatal(err)
	}
	event := peerEvent{
		ID:           "00000000-0000-4000-8000-000000000004",
		Kind:         "receipt",
		AuthorNodeID: "node-a",
		CreatedAt:    time.Unix(now, 0).UTC().Format(time.RFC3339),
		Payload:      []byte(fmt.Sprintf(`{"message_id":%q,"user_id":%q,"state":"delivered","at":%d}`, messageID, userID, now)),
	}
	if err := applyPeerEvent(db, event, ""); err != nil {
		t.Fatal(err)
	}
	var receiptCount, eventCount int
	if err := db.QueryRow("SELECT count(*) FROM receipts").Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM peer_events WHERE id=?", event.ID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if receiptCount != 0 || eventCount != 1 {
		t.Fatalf("orphan receipt=%d journal event=%d", receiptCount, eventCount)
	}
	if err := PruneExpired(db, time.Unix(now, 0)); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err := db.QueryRow("SELECT payload FROM peer_events WHERE id=?", event.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if payload != "{}" {
		t.Fatalf("expired receipt remains in journal: %s", payload)
	}
}

func TestPeerIdentityRowsRejectConflictingDuplicates(t *testing.T) {
	db, err := migrations.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user := "00000000-0000-4000-8000-000000000001"
	room := "00000000-0000-4000-8000-000000000002"
	presence := "00000000-0000-4000-8000-000000000003"
	now := time.Now().Unix()
	key := []byte{1, 2, 3}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO users VALUES(?,?,?,?)", []any{user, "Анна", "user", now}},
		{"INSERT INTO device_bindings VALUES(?,?,?,?)", []any{user, "device-1", key, now}},
		{"INSERT INTO rooms VALUES(?,?,?,?)", []any{room, "Группа", user, now}},
		{"INSERT INTO room_events VALUES(?,?,?,?,?)", []any{presence, room, "commander_entered", user, now}},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	apply := func(kind, payload string) error {
		t.Helper()
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := applyPeerPayload(tx, kind, []byte(payload), ""); err != nil {
			return err
		}
		return tx.Commit()
	}
	for _, row := range []struct{ kind, same, conflict string }{
		{"user", fmt.Sprintf(`{"id":%q,"name":"Анна","role":"user","created_at":%d}`, user, now),
			fmt.Sprintf(`{"id":%q,"name":"Анна","role":"commander","created_at":%d}`, user, now)},
		{"binding", fmt.Sprintf(`{"user_id":%q,"device_id":"device-1","public_key_hex":%q,"activated_at":%d}`, user, hex.EncodeToString(key), now),
			fmt.Sprintf(`{"user_id":%q,"device_id":"device-2","public_key_hex":%q,"activated_at":%d}`, user, hex.EncodeToString(key), now)},
		{"room", fmt.Sprintf(`{"id":%q,"name":"Группа","created_by":%q,"created_at":%d}`, room, user, now),
			fmt.Sprintf(`{"id":%q,"name":"Другая","created_by":%q,"created_at":%d}`, room, user, now)},
		{"presence", fmt.Sprintf(`{"id":%q,"room_id":%q,"actor_id":%q,"created_at":%d}`, presence, room, user, now),
			fmt.Sprintf(`{"id":%q,"room_id":%q,"actor_id":%q,"created_at":%d}`, presence, room, user, now+1)},
	} {
		if err := apply(row.kind, row.same); err != nil {
			t.Fatalf("same %s: %v", row.kind, err)
		}
		if err := apply(row.kind, row.conflict); err == nil {
			t.Fatalf("conflicting %s was accepted", row.kind)
		}
	}
}

func TestPeerSameMessageIDConverges(t *testing.T) {
	db, err := migrations.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user := "00000000-0000-4000-8000-000000000001"
	room := "00000000-0000-4000-8000-000000000002"
	id := "00000000-0000-4000-8000-000000000003"
	now := time.Now().Unix()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO users VALUES(?,?,?,?)", []any{user, "Sender", "user", now}},
		{"INSERT INTO rooms VALUES(?,?,?,?)", []any{room, "Room", user, now}},
		{"INSERT INTO messages VALUES(?,?,?,?,?,?)", []any{id, room, user, "same text", now + 2, now + 3602}},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	apply := func(text string, sent, expires int64) error {
		t.Helper()
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		payload := []byte(fmt.Sprintf(`{"id":%q,"room_id":%q,"user_id":%q,"text":%q,"sent_at":%d,"expires_at":%d}`,
			id, room, user, text, sent, expires))
		if err := applyPeerPayload(tx, "message", payload, ""); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err := apply("same text", now+1, now+3601); err != nil {
		t.Fatalf("same ID from other node: %v", err)
	}
	if err := apply("same text", now+3, now+3603); err != nil {
		t.Fatalf("later retry: %v", err)
	}
	var count int
	var sent, expires int64
	if err := db.QueryRow("SELECT count(*),min(sent_at),min(expires_at) FROM messages WHERE id=?", id).Scan(&count, &sent, &expires); err != nil || count != 1 || sent != now+1 || expires != now+3601 {
		t.Fatalf("converged message count=%d sent=%d expires=%d err=%v", count, sent, expires, err)
	}
	if err := apply("different text", now+1, now+3601); err == nil {
		t.Fatal("conflicting message content was accepted")
	}
}

func TestPeerSameAttachmentIDConverges(t *testing.T) {
	db, err := migrations.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user := "00000000-0000-4000-8000-000000000001"
	room := "00000000-0000-4000-8000-000000000002"
	id := "00000000-0000-4000-8000-000000000003"
	now := time.Now().Unix()
	dir := t.TempDir()
	file := []byte("same file")
	hash := sha256.Sum256(file)
	if err := os.WriteFile(filepath.Join(dir, id), file, 0600); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO users VALUES(?,?,?,?)", []any{user, "Sender", "user", now}},
		{"INSERT INTO rooms VALUES(?,?,?,?)", []any{room, "Room", user, now}},
		{"INSERT INTO attachments VALUES(?,?,?,?,?,?,?,?,?,?)", []any{id, room, user, "file.txt", "text/plain", len(file), hash[:], id, now + 2, now + 3602}},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	apply := func(name string, sent, expires int64) error {
		t.Helper()
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		payload := []byte(fmt.Sprintf(`{"id":%q,"room_id":%q,"user_id":%q,"name":%q,"mime_type":"text/plain","size":%d,"sha256":%q,"sent_at":%d,"expires_at":%d}`,
			id, room, user, name, len(file), hex.EncodeToString(hash[:]), sent, expires))
		if err := applyPeerPayload(tx, "attachment", payload, dir); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err := apply("file.txt", now+1, now+3601); err != nil {
		t.Fatalf("same file from other node: %v", err)
	}
	if err := apply("file.txt", now+3, now+3603); err != nil {
		t.Fatalf("later retry: %v", err)
	}
	var count int
	var sent, expires int64
	if err := db.QueryRow("SELECT count(*),min(sent_at),min(expires_at) FROM attachments WHERE id=?", id).Scan(&count, &sent, &expires); err != nil || count != 1 || sent != now+1 || expires != now+3601 {
		t.Fatalf("converged attachment count=%d sent=%d expires=%d err=%v", count, sent, expires, err)
	}
	if err := apply("different.txt", now+1, now+3601); err == nil {
		t.Fatal("conflicting attachment metadata was accepted")
	}
}

func TestPeerHealthRequiresMutualTLS(t *testing.T) {
	aCert, aKey, err := nodetls.Generate("node-a")
	if err != nil {
		t.Fatal(err)
	}
	bCert, bKey, err := nodetls.Generate("node-b")
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, err := nodetls.OptionalPeerServer(aCert, aKey, bCert, "node-b")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(HandlerWithPeerStorage(nil, "", "node-a"))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()
	_, clientTLS, err := nodetls.Config(bCert, bKey, aCert, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}}
	defer client.CloseIdleConnections()
	response, err := client.Get(server.URL + "/v1/peer/health")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("trusted status=%d", response.StatusCode)
	}
	withoutCert := clientTLS.Clone()
	withoutCert.Certificates = nil
	plainClient := &http.Client{Transport: &http.Transport{TLSClientConfig: withoutCert}}
	defer plainClient.CloseIdleConnections()
	response, err = plainClient.Get(server.URL + "/v1/peer/health")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ordinary status=%d", response.StatusCode)
	}
}

func TestPeerSyncAndRetry(t *testing.T) {
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
	aCert, aKey, err := nodetls.Generate("node-a")
	if err != nil {
		t.Fatal(err)
	}
	bCert, bKey, err := nodetls.Generate("node-b")
	if err != nil {
		t.Fatal(err)
	}
	if err := ConfigurePeer(a, "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := ConfigurePeer(b, "node-b"); err != nil {
		t.Fatal(err)
	}
	aDir := filepath.Join(t.TempDir(), "a-files")
	bDir := filepath.Join(t.TempDir(), "b-files")
	for _, dir := range []string{aDir, bDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	aTLS, aClient, err := nodetls.Config(aCert, aKey, bCert, "node-b")
	if err != nil {
		t.Fatal(err)
	}
	bTLS, bClient, err := nodetls.Config(bCert, bKey, aCert, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	var failNext atomic.Bool
	aHandler := HandlerWithPeerStorage(a, aDir, "node-a")
	aServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/peer/events" && failNext.Swap(false) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		aHandler.ServeHTTP(w, r)
	}))
	aServer.TLS = aTLS
	aServer.StartTLS()
	defer aServer.Close()
	bServer := httptest.NewUnstartedServer(HandlerWithPeerStorage(b, bDir, "node-b"))
	bServer.TLS = bTLS
	bServer.StartTLS()
	defer bServer.Close()

	admin := "00000000-0000-4000-8000-000000000001"
	user := "00000000-0000-4000-8000-000000000002"
	room := "00000000-0000-4000-8000-000000000003"
	message := "00000000-0000-4000-8000-000000000004"
	now := time.Now().Unix()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO users VALUES(?,?,?,?)", []any{admin, "Администратор", "admin", now}},
		{"INSERT INTO users VALUES(?,?,?,?)", []any{user, "Анна", "user", now}},
		{"INSERT INTO device_bindings VALUES(?,?,?,?)", []any{user, "device-1", der, now}},
		{"INSERT INTO rooms VALUES(?,?,?,?)", []any{room, "Группа", admin, now}},
		{"INSERT INTO room_rights VALUES(?,?,?,?,?,?)", []any{room, user, 1, 1, 0, 0}},
		{"INSERT INTO messages VALUES(?,?,?,?,?,?)", []any{message, room, admin, "Привет", now, now + 3600}},
	} {
		if _, err := a.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	failNext.Store(true)
	if err := SyncPeerOnce(context.Background(), b, aServer.URL, "node-a", bDir, bClient); err == nil {
		t.Fatal("expected transient peer request failure")
	}
	var cursor int64
	if err := b.QueryRow("SELECT value FROM peer_cursor WHERE id=1").Scan(&cursor); err != nil || cursor != 0 {
		t.Fatalf("cursor advanced after failure: %d, %v", cursor, err)
	}
	for i := 0; i < 2; i++ {
		if i == 1 {
			if _, err := b.Exec("UPDATE peer_cursor SET value=0 WHERE id=1"); err != nil {
				t.Fatal(err)
			}
		}
		if err := SyncPeerOnce(context.Background(), b, aServer.URL, "node-a", bDir, bClient); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := b.QueryRow("SELECT count(*) FROM messages WHERE id=?", message).Scan(&count); err != nil || count != 1 {
		t.Fatalf("second node message count=%d err=%v", count, err)
	}
	var canRead bool
	if err := b.QueryRow("SELECT can_read FROM room_rights WHERE room_id=? AND user_id=?", room, user).Scan(&canRead); err != nil || !canRead {
		t.Fatalf("second node rights=%v err=%v", canRead, err)
	}
	var copiedKey []byte
	if err := b.QueryRow("SELECT public_key_p256 FROM device_bindings WHERE user_id=?", user).Scan(&copiedKey); err != nil || string(copiedKey) != string(der) {
		t.Fatalf("second node binding err=%v", err)
	}
	roomRequest := httptest.NewRequest(http.MethodGet, "/v1/rooms/"+room, nil)
	signTestRequest(t, roomRequest, user, "device-1", key, "peer-access-00000001")
	roomResponse := httptest.NewRecorder()
	Handler(b).ServeHTTP(roomResponse, roomRequest)
	if roomResponse.Code != http.StatusOK {
		t.Fatalf("second node room access=%d %s", roomResponse.Code, roomResponse.Body)
	}
	messageRequest := httptest.NewRequest(http.MethodGet, "/v1/rooms/"+room+"/messages", nil)
	signTestRequest(t, messageRequest, user, "device-1", key, "peer-access-00000002")
	messageResponse := httptest.NewRecorder()
	Handler(b).ServeHTTP(messageResponse, messageRequest)
	if messageResponse.Code != http.StatusOK || !strings.Contains(messageResponse.Body.String(), "Привет") {
		t.Fatalf("second node history=%d %s", messageResponse.Code, messageResponse.Body)
	}
	if _, err := a.Exec("UPDATE rooms SET name='Новое имя' WHERE id=?", room); err != nil {
		t.Fatal(err)
	}
	if err := SyncPeerOnce(context.Background(), b, aServer.URL, "node-a", bDir, bClient); err != nil {
		t.Fatal(err)
	}
	var renamed string
	if err := b.QueryRow("SELECT name FROM rooms WHERE id=?", room).Scan(&renamed); err != nil || renamed != "Новое имя" {
		t.Fatalf("second node room name=%q err=%v", renamed, err)
	}
	attachmentID := "00000000-0000-4000-8000-000000000005"
	contents := []byte(strings.Repeat("проверка-файла-", 1024))
	checksum := sha256.Sum256(contents)
	if err := os.WriteFile(filepath.Join(aDir, attachmentID), contents, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Exec("INSERT INTO attachments VALUES(?,?,?,?,?,?,?,?,?,?)", attachmentID, room, admin, "test.txt", "text/plain", len(contents), checksum[:], attachmentID, now, now+3600); err != nil {
		t.Fatal(err)
	}
	if err := SyncPeerOnce(context.Background(), b, aServer.URL, "node-a", bDir, bClient); err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(filepath.Join(bDir, attachmentID))
	if err != nil || sha256.Sum256(copied) != checksum {
		t.Fatalf("second node attachment hash mismatch: %v", err)
	}
	var storedHash []byte
	if err := b.QueryRow("SELECT sha256 FROM attachments WHERE id=?", attachmentID).Scan(&storedHash); err != nil || hex.EncodeToString(storedHash) != hex.EncodeToString(checksum[:]) {
		t.Fatalf("second node attachment metadata err=%v", err)
	}
	attachmentRequest := httptest.NewRequest(http.MethodGet, "/v1/rooms/"+room+"/attachments/"+attachmentID, nil)
	signTestRequest(t, attachmentRequest, user, "device-1", key, "peer-access-00000004")
	attachmentResponse := httptest.NewRecorder()
	HandlerWithStorage(b, bDir).ServeHTTP(attachmentResponse, attachmentRequest)
	if attachmentResponse.Code != http.StatusOK || sha256.Sum256(attachmentResponse.Body.Bytes()) != checksum {
		t.Fatalf("second node attachment download=%d", attachmentResponse.Code)
	}
	tamperedID := "00000000-0000-4000-8000-000000000006"
	original := []byte("good")
	originalHash := sha256.Sum256(original)
	if err := os.WriteFile(filepath.Join(aDir, tamperedID), original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Exec("INSERT INTO attachments VALUES(?,?,?,?,?,?,?,?,?,?)", tamperedID, room, admin, "proof.txt", "text/plain", len(original), originalHash[:], tamperedID, now, now+3600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(aDir, tamperedID), []byte("evil"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SyncPeerOnce(context.Background(), b, aServer.URL, "node-a", bDir, bClient); err == nil {
		t.Fatal("corrupt peer attachment was accepted")
	}
	if err := b.QueryRow("SELECT count(*) FROM attachments WHERE id=?", tamperedID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("corrupt peer attachment metadata=%d err=%v", count, err)
	}
	if err := os.WriteFile(filepath.Join(aDir, tamperedID), original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := SyncPeerOnce(context.Background(), b, aServer.URL, "node-a", bDir, bClient); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Exec("INSERT INTO receipts VALUES(?,?,?,?)", message, user, "delivered", now); err != nil {
		t.Fatal(err)
	}
	if err := SyncPeerOnce(context.Background(), a, bServer.URL, "node-b", aDir, aClient); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := a.QueryRow("SELECT state FROM receipts WHERE message_id=? AND user_id=?", message, user).Scan(&state); err != nil || state != "delivered" {
		t.Fatalf("first node receipt=%q err=%v", state, err)
	}
	if _, err := b.Exec("UPDATE receipts SET state='read' WHERE message_id=? AND user_id=?", message, user); err != nil {
		t.Fatal(err)
	}
	if err := SyncPeerOnce(context.Background(), a, bServer.URL, "node-b", aDir, aClient); err != nil {
		t.Fatal(err)
	}
	if err := a.QueryRow("SELECT state FROM receipts WHERE message_id=? AND user_id=?", message, user).Scan(&state); err != nil || state != "read" {
		t.Fatalf("first node read receipt=%q err=%v", state, err)
	}
	if _, err := a.Exec("DELETE FROM room_rights WHERE room_id=? AND user_id=?", room, user); err != nil {
		t.Fatal(err)
	}
	if err := SyncPeerOnce(context.Background(), b, aServer.URL, "node-a", bDir, bClient); err != nil {
		t.Fatal(err)
	}
	if err := b.QueryRow("SELECT count(*) FROM room_rights WHERE room_id=? AND user_id=?", room, user).Scan(&count); err != nil || count != 0 {
		t.Fatalf("revoked rights count=%d err=%v", count, err)
	}
	revokedRequest := httptest.NewRequest(http.MethodGet, "/v1/rooms/"+room, nil)
	signTestRequest(t, revokedRequest, user, "device-1", key, "peer-access-00000003")
	revokedResponse := httptest.NewRecorder()
	Handler(b).ServeHTTP(revokedResponse, revokedRequest)
	if revokedResponse.Code != http.StatusNotFound {
		t.Fatalf("second node revoked room access=%d", revokedResponse.Code)
	}
	if err := b.QueryRow("SELECT count(*) FROM peer_events WHERE author_node_id='node-b' AND kind='message'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("echoed message events=%d err=%v", count, err)
	}
	if err := PruneExpired(b, time.Unix(now+3601, 0), bDir); err != nil {
		t.Fatal(err)
	}
	var retained string
	if err := b.QueryRow("SELECT CAST(payload AS TEXT) FROM peer_events WHERE kind='message'").Scan(&retained); err != nil || retained != "{}" {
		t.Fatalf("expired peer payload=%q err=%v", retained, err)
	}
	if _, err := os.Stat(filepath.Join(bDir, attachmentID)); !os.IsNotExist(err) {
		t.Fatalf("expired attachment file remains: %v", err)
	}
}
