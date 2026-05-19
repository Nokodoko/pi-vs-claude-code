package server

import (
	"crypto/subtle"
	"net/http"
	"regexp"
	"strings"
)

// bearerRe matches a Bearer token in an Authorization header.
var bearerRe = regexp.MustCompile(`(?i)^Bearer\s+\S+`)

// authed returns true if req carries the correct bearer token.
// Uses constant-time comparison to prevent timing attacks, matching the TS
// tokensEqual / crypto.timingSafeEqual implementation verbatim.
func authed(req *http.Request, token string) bool {
	if token == "" {
		return false
	}
	h := req.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return false
	}
	got := h[len("Bearer "):]
	// Length guard: ConstantTimeCompare returns 0 for unequal-length inputs,
	// but we make it explicit to mirror the TS guard.
	if len(got) != len(token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// safeError strips any Bearer token from a user-visible error string.
// Matches the TS safeError() semantics — tokens must never appear in responses.
func safeError(msg string) string {
	return bearerRe.ReplaceAllStringFunc(msg, func(s string) string {
		parts := strings.Fields(s)
		if len(parts) >= 2 {
			return parts[0] + " [REDACTED]"
		}
		return "[REDACTED]"
	})
}
