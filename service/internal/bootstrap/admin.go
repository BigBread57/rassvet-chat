package bootstrap

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"errors"
	"time"

	"rassvet-chat/service/internal/uuid7"
)

// Admin создаёт первого администратора и короткоживущий код для его телефона.
// Код показывается один раз; в базе остаётся только хеш.
func Admin(db *sql.DB, name string, now time.Time) (userID, code string, err error) {
	if name == "" || len([]rune(name)) > 200 {
		return "", "", errors.New("invalid name")
	}
	userID, err = uuid7.New(now)
	if err != nil {
		return "", "", err
	}
	tx, err := db.Begin()
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow("SELECT count(*) FROM users WHERE role = 'admin'").Scan(&count); err != nil {
		return "", "", err
	}
	if count != 0 {
		return "", "", errors.New("administrator already exists")
	}
	if _, err := tx.Exec("INSERT INTO users(id, name, role, created_at) VALUES (?, ?, 'admin', ?)", userID, name, now.Unix()); err != nil {
		return "", "", err
	}
	code, err = issueCode(tx, userID, now)
	if err != nil {
		return "", "", err
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	return userID, code, nil
}

// ReissueAdminCode заменяет истёкший код первого администратора до привязки телефона.
func ReissueAdminCode(db *sql.DB, now time.Time) (userID, code string, err error) {
	tx, err := db.Begin()
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow("SELECT count(*) FROM users WHERE role='admin'").Scan(&count); err != nil {
		return "", "", err
	}
	if count != 1 {
		return "", "", errors.New("expected one initial administrator")
	}
	if err := tx.QueryRow("SELECT id FROM users WHERE role='admin'").Scan(&userID); err != nil {
		return "", "", err
	}
	if err := tx.QueryRow("SELECT count(*) FROM device_bindings WHERE user_id=?", userID).Scan(&count); err != nil {
		return "", "", err
	}
	if count != 0 {
		return "", "", errors.New("administrator already activated")
	}
	if _, err := tx.Exec("DELETE FROM activation_codes WHERE user_id=? AND used_at IS NULL", userID); err != nil {
		return "", "", err
	}
	code, err = issueCode(tx, userID, now)
	if err != nil {
		return "", "", err
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	return userID, code, nil
}

func issueCode(tx *sql.Tx, userID string, now time.Time) (string, error) {
	var codeBytes [20]byte
	if _, err := rand.Read(codeBytes[:]); err != nil {
		return "", err
	}
	code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(codeBytes[:])
	hash := sha256.Sum256([]byte(code))
	_, err := tx.Exec("INSERT INTO activation_codes(code_hash, user_id, expires_at) VALUES (?, ?, ?)", hash[:], userID, now.Add(10*time.Minute).Unix())
	return code, err
}
