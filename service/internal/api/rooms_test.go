package api

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rassvet-chat/service/internal/bootstrap"
	"rassvet-chat/service/migrations"
)

func TestMessageWriteChecksCurrentRight(t *testing.T) {
	db, err := migrations.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	userID := "00000000-0000-4000-8000-000000000001"
	roomID := "00000000-0000-4000-8000-000000000002"
	messageID := "00000000-0000-4000-8000-000000000003"
	now := time.Now().Unix()
	if _, err := db.Exec("INSERT INTO users VALUES(?,?,?,?)", userID, "Анна", "user", now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO rooms VALUES(?,?,?,?)", roomID, "Группа", userID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO room_rights(room_id,user_id,can_read,can_send_text) VALUES(?,?,1,0)", roomID, userID); err != nil {
		t.Fatal(err)
	}
	actor := caller{userID: userID, role: "user", body: []byte(fmt.Sprintf(`{"id":%q,"text":"Привет"}`, messageID))}
	call := func() int {
		response := httptest.NewRecorder()
		postMessage(response, db, actor, roomID)
		return response.Code
	}
	if got := call(); got != http.StatusForbidden {
		t.Fatalf("revoked write=%d", got)
	}
	if _, err := db.Exec("UPDATE room_rights SET can_send_text=1 WHERE room_id=? AND user_id=?", roomID, userID); err != nil {
		t.Fatal(err)
	}
	if got := call(); got != http.StatusCreated {
		t.Fatalf("allowed write=%d", got)
	}
}

func TestRoomPermissions(t *testing.T) {
	db, err := migrations.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	adminID, _, err := bootstrap.Admin(db, "Админ", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []struct{ id, role string }{{"member", "user"}, {"outsider", "user"}, {"commander", "commander"}} {
		if _, err := db.Exec("INSERT INTO users(id,name,role,created_at) VALUES (?,?,?,?)", u.id, u.id, u.role, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	adminKey, err := bindTestDevice(db, adminID, "admin-device")
	if err != nil {
		t.Fatal(err)
	}
	memberKey, err := bindTestDevice(db, "member", "member-device")
	if err != nil {
		t.Fatal(err)
	}
	outsiderKey, err := bindTestDevice(db, "outsider", "outsider-device")
	if err != nil {
		t.Fatal(err)
	}
	commanderKey, err := bindTestDevice(db, "commander", "commander-device")
	if err != nil {
		t.Fatal(err)
	}
	handler := Handler(db)
	n := 0
	call := func(method, path, body, uid, device string, key *ecdsa.PrivateKey) *httptest.ResponseRecorder {
		t.Helper()
		n++
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if key != nil {
			signTestRequest(t, req, uid, device, key, fmt.Sprintf("room-nonce-%016d", n))
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	create := call(http.MethodPost, "/v1/admin/rooms", `{"name":"Секретная"}`, adminID, "admin-device", adminKey)
	if create.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", create.Code, create.Body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("missing room ID")
	}
	roomPath := "/v1/rooms/" + created.ID
	memberPath := "/v1/admin/rooms/" + created.ID + "/members/member"
	if got := call(http.MethodGet, "/v1/admin/rooms", "", adminID, "admin-device", adminKey); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), created.ID) {
		t.Fatalf("created room in admin list=%d %s", got.Code, got.Body)
	}
	if got := call(http.MethodGet, "/v1/rooms", "", adminID, "admin-device", adminKey); strings.Contains(got.Body.String(), created.ID) {
		t.Fatalf("room unexpectedly accessible before rights=%s", got.Body)
	}
	selfPath := "/v1/admin/rooms/" + created.ID + "/members/" + adminID
	if got := call(http.MethodPut, selfPath, `{"read":true,"send_text":true,"add_attachment":true,"save_attachment":true}`, adminID, "admin-device", adminKey); got.Code != http.StatusOK {
		t.Fatalf("admin opens own room=%d %s", got.Code, got.Body)
	}
	if got := call(http.MethodGet, roomPath, "", adminID, "admin-device", adminKey); got.Code != http.StatusOK {
		t.Fatalf("admin room detail=%d %s", got.Code, got.Body)
	}
	if got := call(http.MethodPost, "/v1/admin/rooms", `{"name":"Forbidden"}`, "member", "member-device", memberKey).Code; got != http.StatusForbidden {
		t.Fatalf("member create=%d", got)
	}
	if got := call(http.MethodGet, roomPath, "", "outsider", "outsider-device", outsiderKey).Code; got != http.StatusNotFound {
		t.Fatalf("outsider detail=%d", got)
	}
	if got := call(http.MethodGet, "/v1/rooms", "", "outsider", "outsider-device", outsiderKey); got.Code != http.StatusOK || strings.Contains(got.Body.String(), "Секретная") {
		t.Fatalf("outsider list=%d %s", got.Code, got.Body)
	}
	grant := call(http.MethodPut, memberPath, `{"read":true,"send_text":false,"add_attachment":true,"save_attachment":false}`, adminID, "admin-device", adminKey)
	if grant.Code != http.StatusOK {
		t.Fatalf("grant=%d %s", grant.Code, grant.Body)
	}
	if !strings.Contains(grant.Body.String(), `"add_attachment":true`) {
		t.Fatalf("grant rights=%s", grant.Body)
	}
	for _, path := range []string{"/v1/admin/users", "/v1/admin/rooms", "/v1/admin/rooms/" + created.ID + "/members"} {
		if got := call(http.MethodGet, path, "", "member", "member-device", memberKey).Code; got != http.StatusForbidden {
			t.Fatalf("non-admin list %s=%d", path, got)
		}
		if got := call(http.MethodGet, path, "", adminID, "admin-device", adminKey); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"next_cursor":null`) {
			t.Fatalf("admin list %s=%d %s", path, got.Code, got.Body)
		}
	}
	if got := call(http.MethodGet, "/v1/admin/users", "", adminID, "admin-device", adminKey); !strings.Contains(got.Body.String(), `"name":"member"`) {
		t.Fatalf("users list=%s", got.Body)
	}
	if got := call(http.MethodGet, "/v1/admin/rooms", "", adminID, "admin-device", adminKey); !strings.Contains(got.Body.String(), created.ID) {
		t.Fatalf("rooms list=%s", got.Body)
	}
	if got := call(http.MethodGet, "/v1/admin/rooms/"+created.ID+"/members", "", adminID, "admin-device", adminKey); !strings.Contains(got.Body.String(), `"add_attachment":true`) {
		t.Fatalf("member list rights=%s", got.Body)
	}
	if got := call(http.MethodGet, "/v1/admin/users?cursor=bad", "", adminID, "admin-device", adminKey).Code; got != http.StatusBadRequest {
		t.Fatalf("invalid cursor=%d", got)
	}
	if got := call(http.MethodGet, roomPath, "", "member", "member-device", memberKey); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Секретная") {
		t.Fatalf("member detail=%d %s", got.Code, got.Body)
	}
	if got := call(http.MethodGet, "/v1/rooms", "", "member", "member-device", memberKey); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), created.ID) {
		t.Fatalf("member list=%d %s", got.Code, got.Body)
	}
	commanderPath := "/v1/admin/rooms/" + created.ID + "/members/commander"
	if got := call(http.MethodGet, roomPath, "", "commander", "commander-device", commanderKey).Code; got != http.StatusNotFound {
		t.Fatalf("unassigned captain detail=%d", got)
	}
	if got := call(http.MethodPut, commanderPath,
		`{"read":true,"send_text":false,"add_attachment":false,"save_attachment":false}`,
		adminID, "admin-device", adminKey); got.Code != http.StatusOK {
		t.Fatalf("add captain=%d %s", got.Code, got.Body)
	}
	if got := call(http.MethodGet, roomPath, "", "commander", "commander-device", commanderKey); got.Code != http.StatusOK {
		t.Fatalf("commander detail=%d %s", got.Code, got.Body)
	}
	presencePath := roomPath + "/presence"
	eventsPath := roomPath + "/events"
	if got := call(http.MethodPost, presencePath, `{"kind":"commander_entered"}`, "member", "member-device", memberKey).Code; got != http.StatusForbidden {
		t.Fatalf("member presence=%d", got)
	}
	if got := call(http.MethodGet, eventsPath, "", "outsider", "outsider-device", outsiderKey).Code; got != http.StatusNotFound {
		t.Fatalf("outsider events=%d", got)
	}
	for i := 0; i < 2; i++ {
		if got := call(http.MethodPost, presencePath, `{"kind":"commander_entered"}`, "commander", "commander-device", commanderKey); got.Code != http.StatusCreated || !strings.Contains(got.Body.String(), `"commander_id":"commander"`) {
			t.Fatalf("commander presence=%d %s", got.Code, got.Body)
		}
	}
	eventPage := call(http.MethodGet, eventsPath+"?limit=1", "", "member", "member-device", memberKey)
	var visibleEvents struct {
		Items      []roomEvent `json:"items"`
		NextCursor *string     `json:"next_cursor"`
	}
	if err := json.Unmarshal(eventPage.Body.Bytes(), &visibleEvents); err != nil {
		t.Fatal(err)
	}
	if eventPage.Code != http.StatusOK || len(visibleEvents.Items) != 1 || visibleEvents.Items[0].Kind != "commander_entered" || visibleEvents.NextCursor == nil {
		t.Fatalf("event page=%d %s", eventPage.Code, eventPage.Body)
	}
	eventPage = call(http.MethodGet, eventsPath+"?limit=1&cursor="+*visibleEvents.NextCursor, "", "member", "member-device", memberKey)
	if err := json.Unmarshal(eventPage.Body.Bytes(), &visibleEvents); err != nil {
		t.Fatal(err)
	}
	if eventPage.Code != http.StatusOK || len(visibleEvents.Items) != 1 || visibleEvents.NextCursor != nil {
		t.Fatalf("next event page=%d %s", eventPage.Code, eventPage.Body)
	}
	latestEvents := call(http.MethodGet, eventsPath+"?latest=1&limit=1", "", "member", "member-device", memberKey)
	var recentEvents struct {
		Items      []roomEvent `json:"items"`
		NextCursor *string     `json:"next_cursor"`
		Cursor     string      `json:"cursor"`
	}
	if err := json.Unmarshal(latestEvents.Body.Bytes(), &recentEvents); err != nil {
		t.Fatal(err)
	}
	if latestEvents.Code != http.StatusOK || len(recentEvents.Items) != 1 || recentEvents.NextCursor == nil || recentEvents.Cursor == "" {
		t.Fatalf("latest events=%d %s", latestEvents.Code, latestEvents.Body)
	}
	olderEvents := call(http.MethodGet, eventsPath+"?cursor="+*recentEvents.NextCursor, "", "member", "member-device", memberKey)
	if err := json.Unmarshal(olderEvents.Body.Bytes(), &visibleEvents); err != nil {
		t.Fatal(err)
	}
	if olderEvents.Code != http.StatusOK || len(visibleEvents.Items) != 1 || visibleEvents.Items[0].ID == recentEvents.Items[0].ID {
		t.Fatalf("older events=%d %s", olderEvents.Code, olderEvents.Body)
	}
	lateEventID := "44444444-4444-4444-8444-444444444444"
	if _, err := db.Exec("INSERT INTO room_events(id,room_id,kind,actor_id,created_at) VALUES(?,?,'commander_entered',?,?)",
		lateEventID, created.ID, "commander", time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	eventUpdates := call(http.MethodGet, eventsPath+"?since_rowid="+recentEvents.Cursor, "", "member", "member-device", memberKey)
	if err := json.Unmarshal(eventUpdates.Body.Bytes(), &recentEvents); err != nil {
		t.Fatal(err)
	}
	if eventUpdates.Code != http.StatusOK || !strings.Contains(eventUpdates.Body.String(), lateEventID) {
		t.Fatalf("event updates=%d %s", eventUpdates.Code, eventUpdates.Body)
	}
	messagePath := roomPath + "/messages"
	messageID := "11111111-1111-4111-8111-111111111111"
	postBody := `{"id":"` + messageID + `","text":"Привет"}`
	if got := call(http.MethodPost, messagePath, postBody, "member", "member-device", memberKey).Code; got != http.StatusForbidden {
		t.Fatalf("send without right=%d", got)
	}
	grant = call(http.MethodPut, memberPath, `{"read":true,"send_text":true,"add_attachment":false,"save_attachment":false}`, adminID, "admin-device", adminKey)
	if grant.Code != http.StatusOK {
		t.Fatalf("update right=%d %s", grant.Code, grant.Body)
	}
	var canSend int
	if err := db.QueryRow("SELECT can_send_text FROM room_rights WHERE room_id=? AND user_id='member'", created.ID).Scan(&canSend); err != nil || canSend != 1 {
		t.Fatalf("stored right=%d err=%v", canSend, err)
	}
	posted := call(http.MethodPost, messagePath, postBody, "member", "member-device", memberKey)
	if posted.Code != http.StatusCreated || !strings.Contains(posted.Body.String(), `"state":"saved"`) {
		t.Fatalf("post=%d %s", posted.Code, posted.Body)
	}
	if got := call(http.MethodPost, messagePath, postBody, "member", "member-device", memberKey).Code; got != http.StatusCreated {
		t.Fatalf("repeat post=%d", got)
	}
	if got := call(http.MethodPost, messagePath, `{"id":"`+messageID+`","text":"Другое"}`, "member", "member-device", memberKey).Code; got != http.StatusConflict {
		t.Fatalf("changed repeat=%d", got)
	}
	var messageCount int
	if err := db.QueryRow("SELECT count(*) FROM messages WHERE id=?", messageID).Scan(&messageCount); err != nil || messageCount != 1 {
		t.Fatalf("message count=%d err=%v", messageCount, err)
	}
	if got := call(http.MethodGet, messagePath, "", "outsider", "outsider-device", outsiderKey).Code; got != http.StatusNotFound {
		t.Fatalf("outsider history=%d", got)
	}
	if got := call(http.MethodGet, messagePath, "", "member", "member-device", memberKey); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Привет") {
		t.Fatalf("history=%d %s", got.Code, got.Body)
	}
	secondID := "22222222-2222-4222-8222-222222222222"
	if got := call(http.MethodPost, messagePath, `{"id":"`+secondID+`","text":"Второе"}`, "member", "member-device", memberKey).Code; got != http.StatusCreated {
		t.Fatalf("second post=%d", got)
	}
	page1 := call(http.MethodGet, messagePath+"?limit=1", "", "member", "member-device", memberKey)
	var page struct {
		Items      []message `json:"items"`
		NextCursor *string   `json:"next_cursor"`
	}
	if err := json.Unmarshal(page1.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page1.Code != http.StatusOK || len(page.Items) != 1 || page.Items[0].ID != messageID || page.NextCursor == nil {
		t.Fatalf("page1=%d %s", page1.Code, page1.Body)
	}
	page2 := call(http.MethodGet, messagePath+"?limit=1&cursor="+*page.NextCursor, "", "member", "member-device", memberKey)
	if err := json.Unmarshal(page2.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page2.Code != http.StatusOK || len(page.Items) != 1 || page.Items[0].ID != secondID || page.NextCursor != nil {
		t.Fatalf("page2=%d %s", page2.Code, page2.Body)
	}
	latest := call(http.MethodGet, messagePath+"?latest=1&limit=1", "", "member", "member-device", memberKey)
	var recent struct {
		Items      []message `json:"items"`
		NextCursor *string   `json:"next_cursor"`
		Cursor     string    `json:"cursor"`
	}
	if err := json.Unmarshal(latest.Body.Bytes(), &recent); err != nil {
		t.Fatal(err)
	}
	if latest.Code != http.StatusOK || len(recent.Items) != 1 || recent.Items[0].ID != secondID || recent.NextCursor == nil || recent.Cursor == "" {
		t.Fatalf("latest=%d %s", latest.Code, latest.Body)
	}
	older := call(http.MethodGet, messagePath+"?cursor="+*recent.NextCursor, "", "member", "member-device", memberKey)
	if err := json.Unmarshal(older.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if older.Code != http.StatusOK || len(page.Items) != 1 || page.Items[0].ID != messageID || page.NextCursor != nil {
		t.Fatalf("older=%d %s", older.Code, older.Body)
	}
	lateID := "33333333-3333-4333-8333-333333333334"
	if _, err := db.Exec("INSERT INTO messages(id,room_id,sender_id,text,sent_at,expires_at) VALUES(?,?,?,?,?,?)",
		lateID, created.ID, "member", "late peer event", time.Now().Add(-time.Hour).Unix(), time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	updates := call(http.MethodGet, messagePath+"?since_rowid="+recent.Cursor, "", "member", "member-device", memberKey)
	if err := json.Unmarshal(updates.Body.Bytes(), &recent); err != nil {
		t.Fatal(err)
	}
	if updates.Code != http.StatusOK || len(recent.Items) != 1 || recent.Items[0].ID != lateID {
		t.Fatalf("updates=%d %s", updates.Code, updates.Body)
	}
	lateCursor := recent.Cursor
	if _, err := db.Exec("DELETE FROM messages WHERE id=?", lateID); err != nil {
		t.Fatal(err)
	}
	reusedID := "33333333-3333-4333-8333-333333333335"
	if _, err := db.Exec("INSERT INTO messages(id,room_id,sender_id,text,sent_at,expires_at) VALUES(?,?,?,?,?,?)",
		reusedID, created.ID, "member", "new after cleanup", time.Now().Unix(), time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	reused := call(http.MethodGet, messagePath+"?since_rowid="+lateCursor, "", "member", "member-device", memberKey)
	if err := json.Unmarshal(reused.Body.Bytes(), &recent); err != nil {
		t.Fatal(err)
	}
	if reused.Code != http.StatusOK || len(recent.Items) == 0 || recent.Items[len(recent.Items)-1].ID != reusedID {
		t.Fatalf("reused rowid=%d %s", reused.Code, reused.Body)
	}
	if got := call(http.MethodGet, messagePath, "", "commander", "commander-device", commanderKey); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Привет") {
		t.Fatalf("commander history=%d %s", got.Code, got.Body)
	}
	if got := call(http.MethodPut, messagePath+"/"+messageID, `{"text":"Изменено"}`, "member", "member-device", memberKey).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("edit=%d", got)
	}
	adminMemberPath := "/v1/admin/rooms/" + created.ID + "/members/" + adminID
	if got := call(http.MethodPut, adminMemberPath, `{"read":true,"send_text":false,"add_attachment":false,"save_attachment":false}`, adminID, "admin-device", adminKey).Code; got != http.StatusOK {
		t.Fatalf("admin read right=%d", got)
	}
	receiptPath := messagePath + "/" + messageID + "/receipts"
	if got := call(http.MethodPost, receiptPath, `{"state":"delivered"}`, "member", "member-device", memberKey).Code; got != http.StatusForbidden {
		t.Fatalf("self receipt=%d", got)
	}
	if got := call(http.MethodPost, receiptPath, `{"state":"delivered"}`, adminID, "admin-device", adminKey); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"state":"delivered"`) {
		t.Fatalf("delivered=%d %s", got.Code, got.Body)
	}
	if got := call(http.MethodPost, receiptPath, `{"state":"read"}`, adminID, "admin-device", adminKey); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"state":"read"`) {
		t.Fatalf("read=%d %s", got.Code, got.Body)
	}
	if got := call(http.MethodPost, receiptPath, `{"state":"delivered"}`, adminID, "admin-device", adminKey); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"state":"read"`) {
		t.Fatalf("downgrade=%d %s", got.Code, got.Body)
	}
	if got := call(http.MethodGet, messagePath, "", "member", "member-device", memberKey); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"receipts":[{"user_id":"`+adminID+`","state":"read"`) {
		t.Fatalf("receipt history=%d %s", got.Code, got.Body)
	}
	brief := call(http.MethodGet, messagePath+"?latest=1&receipts_only=1", "", "member", "member-device", memberKey)
	if brief.Code != http.StatusOK || !strings.Contains(brief.Body.String(), `"state":"read"`) || strings.Contains(brief.Body.String(), `"text":`) {
		t.Fatalf("receipt summary=%d %s", brief.Code, brief.Body)
	}
	settingsPath := "/v1/admin/settings"
	if got := call(http.MethodGet, settingsPath, "", "member", "member-device", memberKey).Code; got != http.StatusForbidden {
		t.Fatalf("member settings=%d", got)
	}
	updatedSettings := call(http.MethodPut, settingsPath, `{"attachment_max_bytes":104857600,"retention_seconds":1}`, adminID, "admin-device", adminKey)
	if updatedSettings.Code != http.StatusOK || !strings.Contains(updatedSettings.Body.String(), `"retention_seconds":1`) {
		t.Fatalf("settings=%d %s", updatedSettings.Code, updatedSettings.Body)
	}
	expiringID := "33333333-3333-4333-8333-333333333333"
	if got := call(http.MethodPost, messagePath, `{"id":"`+expiringID+`","text":"Короткий срок"}`, "member", "member-device", memberKey).Code; got != http.StatusCreated {
		t.Fatalf("expiring post=%d", got)
	}
	if _, err := db.Exec("UPDATE messages SET sent_at=?, expires_at=? WHERE id=?", time.Now().Add(-3*time.Second).Unix(), time.Now().Add(-2*time.Second).Unix(), expiringID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO receipts(message_id,user_id,state,at) VALUES(?,?,'delivered',?)", expiringID, "member", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if got := call(http.MethodGet, messagePath, "", "member", "member-device", memberKey); strings.Contains(got.Body.String(), expiringID) {
		t.Fatalf("expired in history=%s", got.Body)
	}
	if err := PruneExpired(db, time.Now()); err != nil {
		t.Fatal(err)
	}
	var expiredCount int
	if err := db.QueryRow("SELECT count(*) FROM messages WHERE id=?", expiringID).Scan(&expiredCount); err != nil || expiredCount != 0 {
		t.Fatalf("expired count=%d err=%v", expiredCount, err)
	}
	if got := call(http.MethodDelete, memberPath, "", adminID, "admin-device", adminKey).Code; got != http.StatusNoContent {
		t.Fatalf("revoke=%d", got)
	}
	if got := call(http.MethodGet, roomPath, "", "member", "member-device", memberKey).Code; got != http.StatusNotFound {
		t.Fatalf("revoked detail=%d", got)
	}
	if got := call(http.MethodGet, messagePath, "", "member", "member-device", memberKey).Code; got != http.StatusNotFound {
		t.Fatalf("revoked history=%d", got)
	}
	if got := call(http.MethodGet, eventsPath, "", "member", "member-device", memberKey).Code; got != http.StatusNotFound {
		t.Fatalf("revoked events=%d", got)
	}
	if got := call(http.MethodPost, messagePath, postBody, "member", "member-device", memberKey).Code; got != http.StatusForbidden {
		t.Fatalf("revoked send=%d", got)
	}
	if got := call(http.MethodGet, "/v1/rooms", "", "member", "member-device", memberKey); got.Code != http.StatusOK || strings.Contains(got.Body.String(), created.ID) {
		t.Fatalf("revoked list=%d %s", got.Code, got.Body)
	}
	if got := call(http.MethodDelete, commanderPath, "", adminID, "admin-device", adminKey); got.Code != http.StatusNoContent {
		t.Fatalf("remove captain=%d", got.Code)
	}
	if got := call(http.MethodGet, roomPath, "", "commander", "commander-device", commanderKey).Code; got != http.StatusNotFound {
		t.Fatalf("removed captain detail=%d", got)
	}
	if got := call(http.MethodGet, messagePath, "", "commander", "commander-device", commanderKey).Code; got != http.StatusNotFound {
		t.Fatalf("removed captain history=%d", got)
	}
	if got := call(http.MethodGet, roomPath, "", "member", "member-device", nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("anonymous detail=%d", got)
	}
	if got := call(http.MethodPost, "/v1/admin/rooms", `{"name":"Вторая"}`, adminID, "admin-device", adminKey).Code; got != http.StatusCreated {
		t.Fatalf("second room=%d", got)
	}
	renamePath := "/v1/admin/rooms/" + created.ID
	if got := call(http.MethodPut, renamePath, `{"name":"Новое имя"}`, "member", "member-device", memberKey).Code; got != http.StatusForbidden {
		t.Fatalf("member rename=%d", got)
	}
	if got := call(http.MethodPut, renamePath, `{"name":"  "}`, adminID, "admin-device", adminKey).Code; got != http.StatusBadRequest {
		t.Fatalf("empty rename=%d", got)
	}
	if got := call(http.MethodPut, renamePath, `{"name":"вторая"}`, adminID, "admin-device", adminKey).Code; got != http.StatusConflict {
		t.Fatalf("duplicate rename=%d", got)
	}
	if got := call(http.MethodPut, renamePath, `{"name":"  Новое имя  "}`, adminID, "admin-device", adminKey); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"name":"Новое имя"`) {
		t.Fatalf("rename=%d %s", got.Code, got.Body)
	}
	if got := call(http.MethodGet, roomPath, "", adminID, "admin-device", adminKey); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"name":"Новое имя"`) {
		t.Fatalf("renamed room detail=%d %s", got.Code, got.Body)
	}
	first := call(http.MethodGet, "/v1/admin/rooms?limit=1", "", adminID, "admin-device", adminKey)
	var roomsPage struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &roomsPage); err != nil || first.Code != http.StatusOK || len(roomsPage.Items) != 1 || roomsPage.NextCursor == nil {
		t.Fatalf("first admin room page=%d %s err=%v", first.Code, first.Body, err)
	}
	firstID := roomsPage.Items[0].ID
	second := call(http.MethodGet, "/v1/admin/rooms?limit=1&cursor="+*roomsPage.NextCursor, "", adminID, "admin-device", adminKey)
	if err := json.Unmarshal(second.Body.Bytes(), &roomsPage); err != nil || second.Code != http.StatusOK || len(roomsPage.Items) != 1 || roomsPage.Items[0].ID == firstID || roomsPage.NextCursor != nil {
		t.Fatalf("second admin room page=%d %s err=%v", second.Code, second.Body, err)
	}
}
