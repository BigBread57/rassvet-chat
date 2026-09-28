package migrations

import (
	"database/sql"
	_ "embed"
	"fmt"
	"net/url"
	"os"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed 001_initial.sql
var initialSchema string

//go:embed 002_request_nonces.sql
var nonceSchema string

//go:embed 003_peer_journal.sql
var peerJournalSchema string

//go:embed 004_user_deletions.sql
var userDeletionsSchema string

//go:embed 005_uuid7_triggers.sql
var uuid7TriggersSchema string

//go:embed 006_names_and_roles.sql
var namesAndRolesSchema string

//go:embed 007_room_rename.sql
var roomRenameSchema string

// Open создаёт базу с закрытыми правами и применяет повторяемую миграцию.
func Open(path string) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("empty database path")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		if err := file.Close(); err != nil {
			return nil, err
		}
	} else if !os.IsExist(err) {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: path}).String() + "?_foreign_keys=on&_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	// ponytail: одно соединение сериализует записи; расширять только после замера нагрузки.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(initialSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(nonceSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(peerJournalSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(userDeletionsSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(uuid7TriggersSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(namesAndRolesSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(roomRenameSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
