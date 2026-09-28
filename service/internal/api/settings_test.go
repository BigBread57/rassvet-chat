package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"rassvet-chat/service/migrations"
)

func TestSettingsRejectOverflowAndOversizedPeerLimit(t *testing.T) {
	db, err := migrations.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tooLong := maxRetentionSeconds + 1
	request := httptest.NewRequest(http.MethodPut, "/v1/admin/settings", nil)
	response := httptest.NewRecorder()
	adminSettings(response, request, db, caller{role: "admin", body: []byte(fmt.Sprintf(`{"attachment_max_bytes":1,"retention_seconds":%d}`, tooLong))})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("admin accepted overflowing retention: %d", response.Code)
	}

	for _, payload := range []string{
		fmt.Sprintf(`{"attachment_max_bytes":1,"retention_seconds":%d}`, tooLong),
		fmt.Sprintf(`{"attachment_max_bytes":%d,"retention_seconds":1}`, absoluteMaxAttachmentBytes+1),
	} {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := applyPeerPayload(tx, "settings", []byte(payload), ""); err != errInvalidPeerEvent {
			t.Fatalf("peer settings %s: %v", payload, err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
}
