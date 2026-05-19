// Package util provides shared helpers deduped from coms.ts, coms-net.ts,
// and coms-net-server.ts.
package util

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

// crockford is the Crockford base-32 alphabet used by the TS ulid() function.
// Source: coms.ts line 128, coms-net.ts line 175, coms-net-server.ts line 263.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewULID returns a 26-character Crockford-base32 ULID. Format is byte-identical
// to the TypeScript ulid() function: 10 chars timestamp + 16 chars randomness.
// The timestamp encodes milliseconds-since-epoch in big-endian base-32.
func NewULID() string {
	ms := time.Now().UnixMilli()

	// Encode 48-bit (10 base-32 chars) timestamp, MSB first.
	var timeChars [10]byte
	t := ms
	for i := 9; i >= 0; i-- {
		timeChars[i] = crockford[t%32]
		t /= 32
	}

	// Read 10 random bytes → 80 bits → 16 base-32 chars.
	var randBuf [10]byte
	if _, err := rand.Read(randBuf[:]); err != nil {
		// Fallback: use time-derived bytes. Should never happen.
		binary.BigEndian.PutUint64(randBuf[:8], uint64(ms^0xDEADBEEFCAFE))
	}

	var randChars [16]byte
	var bits, value int
	idx := 0
	for _, b := range randBuf {
		value = (value << 8) | int(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			randChars[idx] = crockford[(value>>bits)&31]
			idx++
		}
	}

	out := make([]byte, 26)
	copy(out[:10], timeChars[:])
	copy(out[10:], randChars[:16])
	return string(out)
}
