package uuid7

import (
	"crypto/rand"
	"fmt"
	"time"
)

func New(now time.Time) (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[6:]); err != nil {
		return "", err
	}
	millis := uint64(now.UnixMilli())
	for i := 5; i >= 0; i-- {
		id[i] = byte(millis)
		millis >>= 8
	}
	id[6] = (id[6] & 0x0f) | 0x70
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]), nil
}
