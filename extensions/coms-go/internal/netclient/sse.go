// Package netclient implements the client-net subcommand: the Go port of
// extensions/coms-net.ts. It registers with the HTTP/SSE hub, maintains an
// SSE read loop with exponential-backoff reconnect, and dispatches IPC tool
// calls from the shim.
package netclient

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// SSE parser (hand-rolled, no deps)
// Mirrors makeSseParser() in coms-net.ts lines 502-538.
// ─────────────────────────────────────────────────────────────────────────────

// SSEEvent is a parsed Server-Sent Event frame.
type SSEEvent struct {
	Event string
	Data  string
	ID    string
}

// SSEParser accumulates bytes and emits complete SSE frames.
type SSEParser struct {
	buf      bytes.Buffer
	onEvent  func(SSEEvent)
}

// NewSSEParser returns a parser that calls onEvent for each complete SSE frame.
func NewSSEParser(onEvent func(SSEEvent)) *SSEParser {
	return &SSEParser{onEvent: onEvent}
}

// Feed appends chunk to the internal buffer and emits any complete frames.
// A frame is terminated by a blank line ("\n\n").
func (p *SSEParser) Feed(chunk []byte) {
	p.buf.Write(chunk)
	for {
		idx := bytes.Index(p.buf.Bytes(), []byte("\n\n"))
		if idx < 0 {
			break
		}
		frame := string(p.buf.Bytes()[:idx])
		p.buf.Next(idx + 2)
		p.dispatch(frame)
	}
}

func (p *SSEParser) dispatch(frame string) {
	var ev SSEEvent
	ev.Event = "message" // SSE default
	var dataLines []string

	for _, line := range strings.Split(frame, "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // comment
		}
		if strings.HasPrefix(line, "event:") {
			ev.Event = strings.TrimPrefix(line, "event:")
			ev.Event = strings.TrimLeft(ev.Event, " ")
		} else if strings.HasPrefix(line, "data:") {
			v := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(v, " ") {
				v = v[1:]
			}
			dataLines = append(dataLines, v)
		} else if strings.HasPrefix(line, "id:") {
			ev.ID = strings.TrimPrefix(line, "id:")
			ev.ID = strings.TrimLeft(ev.ID, " ")
		}
	}

	if len(dataLines) == 0 {
		return
	}
	ev.Data = strings.Join(dataLines, "\n")
	p.onEvent(ev)
}

// ─────────────────────────────────────────────────────────────────────────────
// SSE read loop
// ─────────────────────────────────────────────────────────────────────────────

// readSSEStream reads SSE frames from body until EOF or ctx cancellation.
// It returns the error that caused the stream to end.
func readSSEStream(body io.ReadCloser, onEvent func(SSEEvent)) error {
	parser := NewSSEParser(onEvent)
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64*1024), 64*1024)

	// We scan line by line but feed pairs to the parser.
	// Simpler: just feed raw bytes.
	buf := make([]byte, 4096)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			parser.Feed(buf[:n])
		}
		if err != nil {
			_ = sc // suppress unused warning
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// SSE event payload helpers
// ─────────────────────────────────────────────────────────────────────────────

// parseEventData parses the JSON data field of an SSE event. Returns nil if
// data is not valid JSON or is empty.
func parseEventData(ev SSEEvent) map[string]any {
	if ev.Data == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(ev.Data), &m); err != nil {
		return nil
	}
	return m
}

// strField extracts a string field from a map, returning "" if missing/wrong type.
func strField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// intField extracts an int field (JSON numbers decode as float64).
func intField(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// boolField extracts a bool field.
func boolField(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

// rawJSON re-encodes a value as JSON (for storing in json.RawMessage fields).
func rawJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// safeError strips the auth token from any user-visible error string.
// Mirrors safeError() in coms-net.ts lines 494-499.
func safeError(err error, token string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if token == "" {
		return msg
	}
	return strings.ReplaceAll(msg, token, "<redacted>")
}

// safeErrorStr is the same but for plain strings.
func safeErrorStr(msg, token string) string {
	if token == "" {
		return msg
	}
	return strings.ReplaceAll(msg, token, "<redacted>")
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP error type
// ─────────────────────────────────────────────────────────────────────────────

// HTTPError wraps an HTTP error response.
type HTTPError struct {
	Status int
	Body   string
	Err    error
}

func (e *HTTPError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("HTTP %d: %v", e.Status, e.Err)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body)
}
