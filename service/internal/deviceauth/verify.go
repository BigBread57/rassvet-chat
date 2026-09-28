package deviceauth

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

var ErrUnauthorized = errors.New("unauthorized")

// VerifyHeaders проверяет подпись и время. Вызывающий код обязан найти ключ
// действующей привязки, атомарно погасить nonce и сверить хеш принятого тела.
func VerifyHeaders(r *http.Request, publicKeyDER []byte, now time.Time) error {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Device ") {
		return ErrUnauthorized
	}
	timestamp := r.Header.Get("X-Time")
	issuedAt, err := time.Parse(time.RFC3339, timestamp)
	if err != nil || issuedAt.Before(now.Add(-5*time.Minute)) || issuedAt.After(now.Add(5*time.Minute)) {
		return ErrUnauthorized
	}
	nonce := r.Header.Get("X-Nonce")
	if len(nonce) < 16 || len(nonce) > 128 {
		return ErrUnauthorized
	}
	bodyHash := r.Header.Get("X-Body-SHA256")
	decodedHash, err := hex.DecodeString(bodyHash)
	if err != nil || len(decodedHash) != sha256.Size || bodyHash != strings.ToLower(bodyHash) {
		return ErrUnauthorized
	}
	signature, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Signature"))
	if err != nil {
		return ErrUnauthorized
	}
	parsedKey, err := x509.ParsePKIXPublicKey(publicKeyDER)
	if err != nil {
		return ErrUnauthorized
	}
	key, ok := parsedKey.(*ecdsa.PublicKey)
	if !ok || key.Curve.Params().Name != "P-256" {
		return ErrUnauthorized
	}
	message := strings.Join([]string{
		r.Method,
		r.URL.RequestURI(),
		r.Header.Get("Authorization"),
		timestamp,
		nonce,
		bodyHash,
	}, "\n")
	digest := sha256.Sum256([]byte(message))
	if !ecdsa.VerifyASN1(key, digest[:], signature) {
		return ErrUnauthorized
	}
	return nil
}
