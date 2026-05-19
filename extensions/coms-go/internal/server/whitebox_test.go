// White-box tests for unexported server internals (package server, not server_test).
package server

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// ─── generateToken ────────────────────────────────────────────────────────────

func TestGenerateToken(t *testing.T) {
	tok1, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken: %v", err)
	}
	tok2, _ := generateToken()
	if len(tok1) != 64 {
		t.Errorf("token length = %d, want 64 (32 bytes hex)", len(tok1))
	}
	if tok1 == tok2 {
		t.Error("two generated tokens should not be equal")
	}
}

// ─── writeServerSecret ────────────────────────────────────────────────────────

func TestWriteServerSecret(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/server.secret.json"
	tok := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	if err := writeServerSecret(path, tok); err != nil {
		t.Fatalf("writeServerSecret: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["token"] != tok {
		t.Errorf("token = %q, want %q", m["token"], tok)
	}
}

// ─── SSE frame helpers ────────────────────────────────────────────────────────

func TestSseFrameWithID(t *testing.T) {
	frame := sseFrameWithID("hello", map[string]string{"msg": "hi"}, 7)
	if !strings.Contains(frame, "event: hello") {
		t.Errorf("frame missing event line: %q", frame)
	}
	if !strings.Contains(frame, "id: 7") {
		t.Errorf("frame missing id line: %q", frame)
	}
	if !strings.Contains(frame, "data:") {
		t.Errorf("frame missing data line: %q", frame)
	}
	if !strings.HasSuffix(frame, "\n\n") {
		t.Errorf("frame missing double newline terminator: %q", frame)
	}
}

func TestSsePingFrame(t *testing.T) {
	frame := ssePingFrame("2026-05-19T00:00:00Z")
	if !strings.HasPrefix(frame, ": ping") {
		t.Errorf("ping frame = %q, want prefix ': ping'", frame)
	}
	if !strings.HasSuffix(frame, "\n\n") {
		t.Errorf("ping frame missing terminator: %q", frame)
	}
	if !strings.Contains(frame, "2026-05-19T00:00:00Z") {
		t.Errorf("ping frame missing timestamp: %q", frame)
	}
}

// ─── Log helper coverage ──────────────────────────────────────────────────────

func TestLogHelpers_noColor(t *testing.T) {
	initColors(false)
	// These write to stdout; just verify no panics.
	logRegister("agent", "proj", "session-id-123456", false)
	logRegister("agent", "proj", "session-id-123456", true)
	logUnregister("agent", "shutdown")
	logSseOpen("agent", 1)
	logSseOpen("agent", 5)
	logSseClose("agent", "connection_closed")
	logMessageSend("sender", "target", "msg-id-123456", "hello world", 0, true)
	logMessageSend("sender", "target", "msg-id-123456", strings.Repeat("x", 100), 1, false)
	logResponse("responder", "sender", "msg-id-123456", false, "", 42)
	logResponse("responder", "sender", "msg-id-123456", true, "timeout", 0)
	logStale("agent", 35)
	logOffline("agent")
	logExpired("msg-id-123456")
	logRejected("hop_limit", "detail")
	logHeartbeat("agent", 50, 3)
}

func TestLogHelpers_quiet(t *testing.T) {
	logQuiet = true
	defer func() { logQuiet = false }()
	logLine("✓", "", "test", "detail")
	logRegister("agent", "proj", "sid", false)
}

func TestHeartbeatLog_disabled(t *testing.T) {
	logHeartbeatEnabled = false
	logHeartbeat("agent", 10, 2) // should be a no-op
}

func TestHeartbeatLog_enabled(t *testing.T) {
	logHeartbeatEnabled = true
	defer func() { logHeartbeatEnabled = false }()
	initColors(false)
	logHeartbeat("agent", 10, 2)
}

func TestBootBanner_noSecret(t *testing.T) {
	initColors(false)
	// Just verify no panics.
	BootBanner("http://127.0.0.1:0", "default", "/tmp/server.json", "", 12345)
}

func TestBootBanner_withSecret(t *testing.T) {
	initColors(false)
	BootBanner("http://127.0.0.1:0", "default", "/tmp/server.json", "/tmp/secret.json", 99)
}

func TestBootBanner_quiet(t *testing.T) {
	logQuiet = true
	defer func() { logQuiet = false }()
	initColors(false)
	BootBanner("http://127.0.0.1:0", "default", "/tmp/server.json", "", 1)
}

// ─── isLoopback ───────────────────────────────────────────────────────────────

func TestIsLoopback(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"localhost", true},
		{"0.0.0.0", false},
		{"192.168.1.1", false},
	}
	for _, c := range cases {
		got := isLoopback(c.host)
		if got != c.want {
			t.Errorf("isLoopback(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

// ─── tail6 ───────────────────────────────────────────────────────────────────

func TestTail6(t *testing.T) {
	if got := tail6("abcdefgh"); got != "cdefgh" {
		t.Errorf("tail6 long = %q, want cdefgh", got)
	}
	if got := tail6("ab"); got != "ab" {
		t.Errorf("tail6 short = %q, want ab", got)
	}
	if got := tail6(""); got != "" {
		t.Errorf("tail6 empty = %q, want empty", got)
	}
}

// ─── dim ──────────────────────────────────────────────────────────────────────

func TestDim(t *testing.T) {
	initColors(false) // no color codes
	got := dim("hello")
	if got != "hello" {
		t.Errorf("dim (no color) = %q, want 'hello'", got)
	}
}
