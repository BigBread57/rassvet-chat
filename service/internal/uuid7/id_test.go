package uuid7

import (
	"strings"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_123)
	id, err := New(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 36 || id[14] != '7' || !strings.ContainsRune("89ab", rune(id[19])) || id[:13] != "018bcfe5-687b" {
		t.Fatalf("invalid UUIDv7: %s", id)
	}
}
