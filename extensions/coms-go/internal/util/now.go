package util

import "time"

// NowIso returns the current UTC time as an ISO 8601 string with millisecond
// precision, matching JavaScript's new Date().toISOString() output.
// Example: "2026-05-19T14:32:11.482Z"
func NowIso() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}
