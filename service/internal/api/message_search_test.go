package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rassvet-chat/service/migrations"
)

func TestSearchMessagesRespectsRoomRightsAndExpiry(t *testing.T) {
	db, err := migrations.Open(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mustID := func() string {
		id, err := newID()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	user := mustID()
	room := mustID()
	now := time.Now().Unix()
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO users(id,name,role,created_at) VALUES(?,'A','user',?)", []any{user, now}},
		{"INSERT INTO rooms(id,name,created_by,created_at) VALUES(?,'R',?,?)", []any{room, user, now}},
		{"INSERT INTO room_rights(room_id,user_id,can_read) VALUES(?,?,1)", []any{room, user}},
	} {
		if _, err := db.Exec(query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(text string, expires int64) {
		id := mustID()
		if _, err := db.Exec("INSERT INTO messages VALUES(?,?,?,?,?,?)", id, room, user, text, expires-3600, expires); err != nil {
			t.Fatal(err)
		}
	}
	insert("Alpha visible", now+3600)
	insert("Alpha expired", now-1)
	insert("Other", now+3600)
	insert("Привет Мир", now+3600)
	for i := 0; i < 50; i++ {
		insert("Alpha visible", now+3600)
	}
	request := func(actor caller, cursor string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		path := "/v1/rooms/" + room + "/messages?q=Alpha"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		messages(w, httptest.NewRequest(http.MethodGet, path, nil), db, actor)
		return w
	}
	allowed := request(caller{userID: user, role: "user"}, "")
	if allowed.Code != http.StatusOK {
		t.Fatalf("search=%d %s", allowed.Code, allowed.Body)
	}
	var page struct {
		Items      []message `json:"items"`
		NextCursor string    `json:"next_cursor"`
	}
	if err := json.Unmarshal(allowed.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 50 || page.NextCursor == "" {
		t.Fatalf("items=%v", page.Items)
	}
	for _, item := range page.Items {
		if !strings.HasPrefix(item.Text, "Alpha visible") {
			t.Fatalf("unexpected item: %s", item.Text)
		}
	}
	second := request(caller{userID: user, role: "user"}, page.NextCursor)
	if second.Code != http.StatusOK {
		t.Fatalf("second page=%d %s", second.Code, second.Body)
	}
	var last struct {
		Items []message `json:"items"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &last); err != nil {
		t.Fatal(err)
	}
	if len(last.Items) != 1 || last.Items[0].Text != "Alpha visible" {
		t.Fatalf("last=%v", last.Items)
	}
	if denied := request(caller{userID: "other", role: "user"}, ""); denied.Code != http.StatusNotFound {
		t.Fatalf("denied=%d %s", denied.Code, denied.Body)
	}
	russian := httptest.NewRecorder()
	messages(russian, httptest.NewRequest(http.MethodGet,
		"/v1/rooms/"+room+"/messages?q="+url.QueryEscape("прИВЕТ"), nil),
		db, caller{userID: user, role: "user"})
	if russian.Code != http.StatusOK {
		t.Fatalf("unicode search=%d %s", russian.Code, russian.Body)
	}
	var found struct {
		Items []message `json:"items"`
	}
	if err := json.Unmarshal(russian.Body.Bytes(), &found); err != nil {
		t.Fatal(err)
	}
	if len(found.Items) != 1 || found.Items[0].Text != "Привет Мир" {
		t.Fatalf("unicode items=%v", found.Items)
	}
}
