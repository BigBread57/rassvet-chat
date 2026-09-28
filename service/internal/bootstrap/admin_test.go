package bootstrap

import (
	"crypto/sha256"
	"path/filepath"
	"testing"
	"time"

	"rassvet-chat/service/migrations"
)

func TestAdminIssuedOnce(t *testing.T) {
	db, err := migrations.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	id, code, err := Admin(db, "Начальник", now)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" || code == "" {
		t.Fatal("missing credentials")
	}
	hash := sha256.Sum256([]byte(code))
	var storedHash []byte
	if err := db.QueryRow("SELECT code_hash FROM activation_codes WHERE user_id = ?", id).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if string(storedHash) != string(hash[:]) {
		t.Fatal("activation code hash mismatch")
	}
	if _, _, err := Admin(db, "Другой", now); err == nil {
		t.Fatal("second initial admin accepted")
	}
	reissuedID, reissuedCode, err := ReissueAdminCode(db, now.Add(time.Minute))
	if err != nil || reissuedID != id || reissuedCode == code {
		t.Fatalf("reissue failed: id=%q err=%v", reissuedID, err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM activation_codes WHERE code_hash=?", hash[:]).Scan(&count); err != nil || count != 0 {
		t.Fatalf("old code retained: count=%d err=%v", count, err)
	}
	reissuedHash := sha256.Sum256([]byte(reissuedCode))
	if err := db.QueryRow("SELECT count(*) FROM activation_codes WHERE code_hash=?", reissuedHash[:]).Scan(&count); err != nil || count != 1 {
		t.Fatalf("new code missing: count=%d err=%v", count, err)
	}
	if _, err := db.Exec("INSERT INTO device_bindings VALUES(?,?,?,?)", id, "first-phone", []byte("key"), now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReissueAdminCode(db, now.Add(2*time.Minute)); err == nil {
		t.Fatal("bound administrator was reissued a code")
	}
}
