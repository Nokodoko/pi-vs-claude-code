// Package audit provides an append-only JSONL writer for coms-log and
// coms-net-log. Lines contain only event metadata (event name, msg_id, session
// IDs, hop count, timestamps) — prompt/response payloads and bearer tokens
// MUST NEVER appear here.
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Entry is a single audit log line. Fields must remain limited to event
// metadata. Prompt text, response text, and auth tokens are forbidden.
type Entry map[string]any

// Logger appends JSONL lines to a file under ~/.pi/.
type Logger struct {
	path string
	mu   sync.Mutex // serialize concurrent appends within the same process
}

// New creates a Logger that writes to logPath. The file and parent directories
// are created on first Append. Use logPath = "" to create a no-op logger.
func New(logPath string) *Logger {
	return &Logger{path: logPath}
}

// Append writes one JSONL entry. Returns nil on success.
// The caller MUST NOT include prompt bodies, response bodies, or auth tokens
// in entry — this is enforced by convention, not code, because the Go type
// system cannot prevent arbitrary map values.
func (l *Logger) Append(entry Entry) error {
	if l.path == "" {
		return nil
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("audit marshal: %w", err)
	}
	data = append(data, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(l.path), 0755); err != nil {
		return fmt.Errorf("audit mkdir: %w", err)
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("audit open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("audit write: %w", err)
	}
	return nil
}
