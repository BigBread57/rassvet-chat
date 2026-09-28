package deviceauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestVerifyHeaders(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	req, err := http.NewRequest("POST", "https://node-a/v1/rooms/r/messages?x=1", strings.NewReader(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Device user-a:device-a")
	req.Header.Set("X-Time", now.Format(time.RFC3339))
	req.Header.Set("X-Nonce", "1234567890abcdef")
	bodyHash := sha256.Sum256([]byte(`{"text":"hi"}`))
	req.Header.Set("X-Body-SHA256", hex.EncodeToString(bodyHash[:]))
	message := strings.Join([]string{req.Method, req.URL.RequestURI(), req.Header.Get("Authorization"), req.Header.Get("X-Time"), req.Header.Get("X-Nonce"), req.Header.Get("X-Body-SHA256")}, "\n")
	digest := sha256.Sum256([]byte(message))
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(signature))
	if err := VerifyHeaders(req, publicDER, now); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	req.URL.RawQuery = "x=2"
	if err := VerifyHeaders(req, publicDER, now); err == nil {
		t.Fatal("changed request accepted")
	}
	req.URL.RawQuery = "x=1"
	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherDER, err := x509.MarshalPKIXPublicKey(&otherKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyHeaders(req, otherDER, now); err == nil {
		t.Fatal("unknown key accepted")
	}
	if err := VerifyHeaders(req, publicDER, now.Add(6*time.Minute)); err == nil {
		t.Fatal("expired request accepted")
	}
}
