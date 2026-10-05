package surriti

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// newUUID returns an RFC 4122 version-4 UUID using crypto/rand.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failures indicate a broken runtime. Panicking here is
		// preferable to silently emitting colliding persistent identifiers.
		panic(fmt.Sprintf("surriti: crypto/rand failed while generating uuid: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
