package migrations

import (
	"path/filepath"
	"testing"
)

func TestOpenTwice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.db")
	for i := 0; i < 2; i++ {
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var version, settingsCount int
		if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT count(*) FROM settings").Scan(&settingsCount); err != nil {
			t.Fatal(err)
		}
		if version != 7 || settingsCount != 1 {
			t.Fatalf("version=%d settings=%d", version, settingsCount)
		}
		if _, err := db.Exec("INSERT INTO device_bindings(user_id, device_id, public_key_p256, activated_at) VALUES ('missing', 'device', X'01', 1)"); err == nil {
			t.Fatal("foreign key constraint not enforced")
		}
		if i == 0 {
			if _, err := db.Exec("UPDATE peer_runtime SET node_id='test' WHERE id=1"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("INSERT INTO users(id,name,role,created_at) VALUES('018bcfe5-687b-7000-8000-000000000000','A','user',1)"); err != nil {
				t.Fatal(err)
			}
			var eventID string
			if err := db.QueryRow("SELECT id FROM peer_events LIMIT 1").Scan(&eventID); err != nil {
				t.Fatal(err)
			}
			if len(eventID) != 36 || eventID[14] != '7' {
				t.Fatalf("peer event ID is not UUIDv7: %s", eventID)
			}
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
