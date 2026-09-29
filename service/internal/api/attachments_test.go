package api

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rassvet-chat/service/migrations"
)

type hookedReader struct {
	io.Reader
	before func()
}

func TestValidAttachmentName(t *testing.T) {
	for _, name := range []string{"photo.jpg", "файл.txt"} {
		if !validAttachmentName(name) {
			t.Fatalf("rejected %q", name)
		}
	}
	for _, name := range []string{"", ".", "..", "../secret", `..\secret`, "bad\nname"} {
		if validAttachmentName(name) {
			t.Fatalf("accepted %q", name)
		}
	}
}

func (r *hookedReader) Read(p []byte) (int, error) {
	if r.before != nil {
		before := r.before
		r.before = nil
		before()
	}
	return r.Reader.Read(p)
}

func TestAttachmentsStreamAndRights(t *testing.T) {
	db, err := migrations.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dir := t.TempDir()
	for _, id := range []string{"sender", "reader", "outsider", "commander"} {
		role := "user"
		if id == "commander" {
			role = "commander"
		}
		if _, err := db.Exec("INSERT INTO users(id,name,role,created_at) VALUES(?,?,?,?)", id, id, role, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	roomID := "11111111-1111-4111-8111-111111111111"
	fileID := "22222222-2222-4222-8222-222222222222"
	if _, err := db.Exec("INSERT INTO rooms(id,name,created_by,created_at) VALUES(?,'Room','sender',?)", roomID, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"sender", "reader", "commander"} {
		if _, err := db.Exec("INSERT INTO room_rights(room_id,user_id,can_read,can_add_attachment) VALUES(?,?,1,?)", roomID, id, id == "sender"); err != nil {
			t.Fatal(err)
		}
	}
	keys := map[string]*ecdsa.PrivateKey{}
	for _, id := range []string{"sender", "reader", "outsider", "commander"} {
		keys[id], err = bindTestDevice(db, id, id+"-device")
		if err != nil {
			t.Fatal(err)
		}
	}
	handler := HandlerWithStorage(db, dir)
	path := "/v1/rooms/" + roomID + "/attachments/" + fileID
	nonce := 0
	call := func(method, target, body, user, name, digest string) *httptest.ResponseRecorder {
		t.Helper()
		nonce++
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		if method == http.MethodPut {
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("X-File-Name", name)
			req.Header.Set("X-Content-SHA256", digest)
		}
		signTestRequest(t, req, user, user+"-device", keys[user], "attachment-nonce-"+string(rune('a'+nonce)))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	payload := "hello attachment"
	hash := sha256.Sum256([]byte(payload))
	digest := hex.EncodeToString(hash[:])
	if got := call(http.MethodPut, path, payload, "reader", "x.bin", digest).Code; got != http.StatusForbidden {
		t.Fatalf("reader upload=%d", got)
	}
	commanderID := "99999999-9999-4999-8999-999999999999"
	commanderPath := "/v1/rooms/" + roomID + "/attachments/" + commanderID
	if got := call(http.MethodPut, commanderPath, payload, "commander", "x.bin", digest).Code; got != http.StatusCreated {
		t.Fatalf("captain upload=%d", got)
	}
	if _, err := db.Exec("DELETE FROM attachments WHERE id=?", commanderID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, commanderID)); err != nil {
		t.Fatal(err)
	}
	commanderReq := httptest.NewRequest(http.MethodPut, commanderPath, strings.NewReader(payload))
	commanderReq.Header.Set("Content-Type", "application/octet-stream")
	commanderReq.Header.Set("X-File-Name", "x.bin")
	commanderReq.Header.Set("X-Content-SHA256", digest)
	signTestRequest(t, commanderReq, "commander", "commander-device", keys["commander"], "captain-revoked-during-upload")
	commanderReq.Body = io.NopCloser(&hookedReader{Reader: strings.NewReader(payload), before: func() {
		if _, err := db.Exec("DELETE FROM room_rights WHERE room_id=? AND user_id='commander'", roomID); err != nil {
			t.Fatal(err)
		}
	}})
	commanderResponse := httptest.NewRecorder()
	handler.ServeHTTP(commanderResponse, commanderReq)
	if commanderResponse.Code != http.StatusForbidden {
		t.Fatalf("captain upload after removal during stream=%d", commanderResponse.Code)
	}
	if got := call(http.MethodPut, commanderPath, payload, "commander", "x.bin", digest).Code; got != http.StatusForbidden {
		t.Fatalf("removed captain upload=%d", got)
	}
	if got := call(http.MethodPut, path, payload, "sender", "x.bin", strings.Repeat("0", 64)).Code; got != http.StatusBadRequest {
		t.Fatalf("bad signed digest=%d", got)
	}
	badID := "44444444-4444-4444-8444-444444444444"
	badPath := "/v1/rooms/" + roomID + "/attachments/" + badID
	badReq := httptest.NewRequest(http.MethodPut, badPath, strings.NewReader(payload))
	badReq.Header.Set("Content-Type", "application/octet-stream")
	badReq.Header.Set("X-File-Name", "broken.bin")
	badReq.Header.Set("X-Content-SHA256", digest)
	signTestRequest(t, badReq, "sender", "sender-device", keys["sender"], "attachment-bad-hash-1")
	badReq.Body = io.NopCloser(strings.NewReader("j" + payload[1:]))
	badResponse := httptest.NewRecorder()
	handler.ServeHTTP(badResponse, badReq)
	if badResponse.Code != http.StatusUnprocessableEntity {
		t.Fatalf("corrupted stream=%d %s", badResponse.Code, badResponse.Body)
	}
	shortReq := httptest.NewRequest(http.MethodPut, badPath, strings.NewReader(payload))
	shortReq.Header.Set("Content-Type", "application/octet-stream")
	shortReq.Header.Set("X-File-Name", "broken.bin")
	shortReq.Header.Set("X-Content-SHA256", digest)
	signTestRequest(t, shortReq, "sender", "sender-device", keys["sender"], "attachment-short-body-1")
	shortReq.Body = io.NopCloser(strings.NewReader(payload[:4]))
	shortResponse := httptest.NewRecorder()
	handler.ServeHTTP(shortResponse, shortReq)
	if shortResponse.Code != http.StatusBadRequest {
		t.Fatalf("interrupted stream=%d %s", shortResponse.Code, shortResponse.Body)
	}
	var badCount int
	if err := db.QueryRow("SELECT count(*) FROM attachments WHERE id=?", badID).Scan(&badCount); err != nil || badCount != 0 {
		t.Fatalf("corrupt attachment published=%d err=%v", badCount, err)
	}
	if _, err := os.Stat(filepath.Join(dir, badID)); !os.IsNotExist(err) {
		t.Fatalf("corrupt file published: %v", err)
	}
	revokedReq := httptest.NewRequest(http.MethodPut, badPath, strings.NewReader(payload))
	revokedReq.Header.Set("Content-Type", "application/octet-stream")
	revokedReq.Header.Set("X-File-Name", "x.bin")
	revokedReq.Header.Set("X-Content-SHA256", digest)
	signTestRequest(t, revokedReq, "sender", "sender-device", keys["sender"], "attachment-revoked-1")
	revokedReq.Body = io.NopCloser(&hookedReader{Reader: strings.NewReader(payload), before: func() {
		if _, err := db.Exec("UPDATE room_rights SET can_add_attachment=0 WHERE room_id=? AND user_id='sender'", roomID); err != nil {
			t.Fatal(err)
		}
	}})
	revokedResponse := httptest.NewRecorder()
	handler.ServeHTTP(revokedResponse, revokedReq)
	if revokedResponse.Code != http.StatusForbidden {
		t.Fatalf("upload after revoked right=%d", revokedResponse.Code)
	}
	if err := db.QueryRow("SELECT count(*) FROM attachments WHERE id=?", badID).Scan(&badCount); err != nil || badCount != 0 {
		t.Fatalf("revoked attachment published=%d err=%v", badCount, err)
	}
	if _, err := os.Stat(filepath.Join(dir, badID)); !os.IsNotExist(err) {
		t.Fatalf("revoked file published: %v", err)
	}
	if _, err := db.Exec("UPDATE room_rights SET can_add_attachment=1 WHERE room_id=? AND user_id='sender'", roomID); err != nil {
		t.Fatal(err)
	}
	if got := call(http.MethodPut, path, payload, "sender", "x.bin", digest); got.Code != http.StatusCreated || !strings.Contains(got.Body.String(), `"name":"x.bin"`) {
		t.Fatalf("upload=%d %s", got.Code, got.Body)
	}
	if got := call(http.MethodPut, path, payload, "sender", "x.bin", digest).Code; got != http.StatusCreated {
		t.Fatalf("repeat=%d", got)
	}
	if got := call(http.MethodPut, path, payload, "sender", "other.bin", digest).Code; got != http.StatusConflict {
		t.Fatalf("conflict=%d", got)
	}
	if got := call(http.MethodGet, path, "", "outsider", "", "").Code; got != http.StatusNotFound {
		t.Fatalf("outsider download=%d", got)
	}
	download := call(http.MethodGet, path, "", "reader", "", "")
	if download.Code != http.StatusOK || download.Body.String() != payload || download.Header().Get("X-Content-SHA256") != digest {
		t.Fatalf("download=%d %q", download.Code, download.Body)
	}
	if err := os.WriteFile(filepath.Join(dir, fileID), []byte("j"+payload[1:]), 0600); err != nil {
		t.Fatal(err)
	}
	if got := call(http.MethodGet, path, "", "reader", "", "").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("corrupted stored file=%d", got)
	}
	if err := os.WriteFile(filepath.Join(dir, fileID), []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	list := call(http.MethodGet, "/v1/rooms/"+roomID+"/attachments", "", "reader", "", "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), fileID) || strings.Contains(list.Body.String(), payload) {
		t.Fatalf("list=%d %s", list.Code, list.Body)
	}
	secondID := "55555555-5555-4555-8555-555555555555"
	if got := call(http.MethodPut, "/v1/rooms/"+roomID+"/attachments/"+secondID, payload, "sender", "x.bin", digest); got.Code != http.StatusCreated {
		t.Fatalf("second upload=%d %s", got.Code, got.Body)
	}
	listPath := "/v1/rooms/" + roomID + "/attachments"
	latest := call(http.MethodGet, listPath+"?latest=1&limit=1", "", "reader", "", "")
	var page struct {
		Items      []attachment `json:"items"`
		NextCursor *string      `json:"next_cursor"`
		Cursor     string       `json:"cursor"`
	}
	if err := json.Unmarshal(latest.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if latest.Code != http.StatusOK || len(page.Items) != 1 || page.Items[0].ID != secondID || page.NextCursor == nil || page.Cursor == "" {
		t.Fatalf("latest=%d %s", latest.Code, latest.Body)
	}
	initialCursor := page.Cursor
	older := call(http.MethodGet, listPath+"?cursor="+*page.NextCursor, "", "reader", "", "")
	if err := json.Unmarshal(older.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if older.Code != http.StatusOK || len(page.Items) != 1 || page.Items[0].ID != fileID || page.NextCursor != nil {
		t.Fatalf("older=%d %s", older.Code, older.Body)
	}
	lateID := "66666666-6666-4666-8666-666666666666"
	if _, err := db.Exec(`INSERT INTO attachments(id,room_id,sender_id,name,mime_type,size,sha256,storage_name,sent_at,expires_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, lateID, roomID, "sender", "late.bin", "application/octet-stream", len(payload), hash[:], lateID,
		time.Now().Add(-time.Hour).Unix(), time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	updates := call(http.MethodGet, listPath+"?since_rowid="+initialCursor, "", "reader", "", "")
	if updates.Code != http.StatusOK || !strings.Contains(updates.Body.String(), lateID) {
		t.Fatalf("attachment updates=%d %s", updates.Code, updates.Body)
	}
	if _, err := db.Exec("DELETE FROM attachments WHERE id=?", lateID); err != nil {
		t.Fatal(err)
	}
	orphanID := "77777777-7777-4777-8777-777777777777"
	if err := os.WriteFile(filepath.Join(dir, orphanID), []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	orphanPath := "/v1/rooms/" + roomID + "/attachments/" + orphanID
	if got := call(http.MethodPut, orphanPath, payload, "sender", "x.bin", digest); got.Code != http.StatusCreated {
		t.Fatalf("matching orphan retry=%d %s", got.Code, got.Body)
	}
	var orphanCount int
	if err := db.QueryRow("SELECT count(*) FROM attachments WHERE id=?", orphanID).Scan(&orphanCount); err != nil || orphanCount != 1 {
		t.Fatalf("matching orphan metadata=%d err=%v", orphanCount, err)
	}
	wrongOrphanID := "88888888-8888-4888-8888-888888888888"
	if err := os.WriteFile(filepath.Join(dir, wrongOrphanID), []byte("wrong"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := call(http.MethodPut, "/v1/rooms/"+roomID+"/attachments/"+wrongOrphanID, payload, "sender", "x.bin", digest).Code; got != http.StatusConflict {
		t.Fatalf("mismatched orphan retry=%d", got)
	}
	if err := db.QueryRow("SELECT count(*) FROM attachments WHERE id=?", wrongOrphanID).Scan(&orphanCount); err != nil || orphanCount != 0 {
		t.Fatalf("mismatched orphan published=%d err=%v", orphanCount, err)
	}
	if _, err := db.Exec("UPDATE settings SET attachment_max_bytes=2 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	otherID := "33333333-3333-4333-8333-333333333333"
	if got := call(http.MethodPut, "/v1/rooms/"+roomID+"/attachments/"+otherID, payload, "sender", "x.bin", digest).Code; got != http.StatusRequestEntityTooLarge {
		t.Fatalf("limit=%d", got)
	}
	if _, err := os.Stat(filepath.Join(dir, otherID)); !os.IsNotExist(err) {
		t.Fatalf("oversized file published: %v", err)
	}
	if _, err := db.Exec("UPDATE room_rights SET can_read=0 WHERE room_id=? AND user_id='reader'", roomID); err != nil {
		t.Fatal(err)
	}
	if got := call(http.MethodGet, path, "", "reader", "", "").Code; got != http.StatusNotFound {
		t.Fatalf("revoked download=%d", got)
	}
	if _, err := db.Exec("UPDATE attachments SET sent_at=?, expires_at=? WHERE id=?", time.Now().Add(-3*time.Second).Unix(), time.Now().Add(-2*time.Second).Unix(), fileID); err != nil {
		t.Fatal(err)
	}
	if got := call(http.MethodGet, path, "", "sender", "", "").Code; got != http.StatusNotFound {
		t.Fatalf("expired download=%d", got)
	}
	if err := PruneExpired(db, time.Now(), dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, fileID)); !os.IsNotExist(err) {
		t.Fatalf("expired file remains: %v", err)
	}
	var expiredCount int
	if err := db.QueryRow("SELECT count(*) FROM attachments WHERE id=?", fileID).Scan(&expiredCount); err != nil || expiredCount != 0 {
		t.Fatalf("expired metadata=%d err=%v", expiredCount, err)
	}
}
