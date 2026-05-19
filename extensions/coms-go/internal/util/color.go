package util

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"regexp"
)

// FallbackPalette is the ordered color list used when a session has no explicit color.
// Source: coms.ts line 36-39, coms-net.ts line 50-53.
var FallbackPalette = []string{
	"#72F1B8", "#36F9F6", "#FF7EDB", "#FEDE5D",
	"#C792EA", "#FF8B39", "#4D9DE0", "#FFAA8B",
}

var hexRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// IsValidHex returns true if hex is a valid 6-digit CSS color (#rrggbb).
func IsValidHex(hex string) bool {
	return hexRe.MatchString(hex)
}

// FallbackColor derives a deterministic palette color from sessionId using sha256.
// Algorithm mirrors coms.ts:fallbackColor (line 164-167):
//
//	h = sha256(sessionId).hex[:8]  →  BigInt("0x"+h) % len(palette)
func FallbackColor(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	// Take the first 4 bytes of the hex digest (= first 8 hex chars = first 4 raw bytes).
	// TS does: Number(BigInt("0x" + h)) where h = hex[:8] = first 4 bytes interpreted as uint32.
	n := binary.BigEndian.Uint32(sum[:4])
	return FallbackPalette[int(n)%len(FallbackPalette)]
}

// HexFg wraps s in an ANSI 24-bit foreground color escape using the given #rrggbb color.
// Output format: \x1b[38;2;R;G;Bm<s>\x1b[39m — byte-identical to TS hexFg().
func HexFg(hex, s string) string {
	if len(hex) != 7 || hex[0] != '#' {
		return s
	}
	r := hexVal(hex[1], hex[2])
	g := hexVal(hex[3], hex[4])
	b := hexVal(hex[5], hex[6])
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[39m", r, g, b, s)
}

// hexVal converts two ASCII hex digits to a byte value.
func hexVal(hi, lo byte) int {
	return int(fromHexDigit(hi)<<4 | fromHexDigit(lo))
}

func fromHexDigit(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}
