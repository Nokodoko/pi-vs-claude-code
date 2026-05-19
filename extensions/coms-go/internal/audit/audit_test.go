package audit_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pi-vs-cc/coms-go/internal/audit"
)

func TestAppendCreatesFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "coms-log")
	l := audit.New(p)
	if err := l.Append(audit.Entry{"event": "boot", "ts": "2026-05-19T00:00:00.000Z"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("log file not created: %v", err)
	}
}

func TestAppendJSONL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "coms-log")
	l := audit.New(p)

	entries := []audit.Entry{
		{"event": "boot", "session_id": "01A", "ts": "2026-05-19T00:00:00.000Z"},
		{"event": "outbound_prompt", "msg_id": "01B", "hops": 0, "ts": "2026-05-19T00:00:01.000Z"},
		{"event": "shutdown", "session_id": "01A", "ts": "2026-05-19T00:01:00.000Z"},
	}
	for _, e := range entries {
		if err := l.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var lines []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Errorf("invalid JSON line: %q", sc.Text())
			continue
		}
		lines = append(lines, m)
	}

	if len(lines) != len(entries) {
		t.Fatalf("line count = %d, want %d", len(lines), len(entries))
	}
	if lines[0]["event"] != "boot" {
		t.Errorf("first line event = %v, want boot", lines[0]["event"])
	}
	if lines[1]["event"] != "outbound_prompt" {
		t.Errorf("second line event = %v, want outbound_prompt", lines[1]["event"])
	}
}

func TestConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "coms-log")
	l := audit.New(p)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_ = l.Append(audit.Entry{"event": "tick", "i": i})
		}(i)
	}
	wg.Wait()

	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	count := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Errorf("invalid JSON line: %q", sc.Text())
		}
		count++
	}
	if count != n {
		t.Errorf("concurrent append: got %d lines, want %d", count, n)
	}
}

func TestNopLogger(t *testing.T) {
	l := audit.New("")
	if err := l.Append(audit.Entry{"event": "boot"}); err != nil {
		t.Errorf("nop logger should not return error, got: %v", err)
	}
}

func TestNoPayloadLeakage(t *testing.T) {
	// This test documents the convention: the entry must NOT contain prompt/response.
	// It checks that a compliant caller (with only event metadata) produces valid output.
	dir := t.TempDir()
	p := filepath.Join(dir, "coms-log")
	l := audit.New(p)
	e := audit.Entry{
		"event":    "inbound_prompt",
		"msg_id":   "01MSG00",
		"sender":   "01SESS0",
		"hops":     1,
		"ts":       "2026-05-19T00:00:00.000Z",
		// NOTE: "prompt" key is intentionally absent — this is correct usage.
	}
	if err := l.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	data, _ := os.ReadFile(p)
	var m map[string]any
	if err := json.Unmarshal(data[:len(data)-1], &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["prompt"]; ok {
		t.Error("FAIL: prompt body appeared in audit log — security violation")
	}
}
