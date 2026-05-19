// Package ipc implements the JSON-line stdin/stdout protocol between shim.ts
// and coms-go subcommands (client-local, client-net).
//
// Wire format (one JSON object per line, newline terminated):
//
// Inbound (shim → coms-go, on stdin):
//
//	{ "kind": "tool_request",  "id": "<hex>", "tool": "<name>", "params": {...} }
//	{ "kind": "command",       "id": "<hex>", "name": "<cmd>",  "args":  "..." }
//	{ "kind": "lifecycle",     "event": "<ev>",                 "data":  {...} }
//	{ "kind": "shutdown" }
//
// Outbound (coms-go → shim, on stdout):
//
//	{ "kind": "tool_response", "id": "<hex>", "ok": true,  "content": [...], "details": {...} }
//	{ "kind": "tool_error",    "id": "<hex>", "message": "..." }
//	{ "kind": "event",         "name": "<ev>",              "data":  {...} }
//
// Stdout is EXCLUSIVELY for IPC frames. All human-readable logs go to stderr.
package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// ─────────────────────────────────────────────────────────────────────────────
// Inbound frame types (shim → coms-go)
// ─────────────────────────────────────────────────────────────────────────────

// Request is an inbound IPC frame parsed from stdin.
type Request struct {
	Kind   string          `json:"kind"`             // "tool_request" | "command" | "lifecycle" | "shutdown"
	ID     string          `json:"id,omitempty"`     // present on tool_request, command
	Tool   string          `json:"tool,omitempty"`   // present on tool_request
	Params json.RawMessage `json:"params,omitempty"` // present on tool_request
	Name   string          `json:"name,omitempty"`   // present on command
	Args   string          `json:"args,omitempty"`   // present on command
	Event  string          `json:"event,omitempty"`  // present on lifecycle
	Data   json.RawMessage `json:"data,omitempty"`   // present on lifecycle
}

// ─────────────────────────────────────────────────────────────────────────────
// Outbound frame types (coms-go → shim)
// ─────────────────────────────────────────────────────────────────────────────

// ContentItem matches pi's tool result content shape.
type ContentItem struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

// toolResponse is the success outbound frame for a tool call.
type toolResponse struct {
	Kind    string          `json:"kind"` // "tool_response"
	ID      string          `json:"id"`
	Ok      bool            `json:"ok"`
	Content []ContentItem   `json:"content"`
	Details json.RawMessage `json:"details,omitempty"`
}

// toolError is the error outbound frame for a tool call.
type toolError struct {
	Kind    string `json:"kind"` // "tool_error"
	ID      string `json:"id"`
	Message string `json:"message"`
}

// eventFrame is an unsolicited event pushed from coms-go to the shim.
type eventFrame struct {
	Kind string          `json:"kind"` // "event"
	Name string          `json:"name"`
	Data json.RawMessage `json:"data,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Writer — thread-safe outbound frame emitter
// ─────────────────────────────────────────────────────────────────────────────

// Writer serializes outbound IPC frames to stdout (or any io.Writer).
// All writes are serialized under a mutex so goroutines can safely call
// Respond / RespondError / Event concurrently.
type Writer struct {
	mu  sync.Mutex
	enc *json.Encoder
}

// NewWriter returns a Writer that writes to w.
// Use NewStdoutWriter() for the normal production case.
func NewWriter(w io.Writer) *Writer {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &Writer{enc: enc}
}

// NewStdoutWriter returns a Writer bound to os.Stdout.
func NewStdoutWriter() *Writer {
	return NewWriter(os.Stdout)
}

// Respond emits a tool_response frame. details may be nil.
func (w *Writer) Respond(id string, content []ContentItem, details any) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	var rawDetails json.RawMessage
	if details != nil {
		b, err := json.Marshal(details)
		if err != nil {
			return fmt.Errorf("ipc Respond marshal details: %w", err)
		}
		rawDetails = b
	}
	return w.enc.Encode(toolResponse{
		Kind:    "tool_response",
		ID:      id,
		Ok:      true,
		Content: content,
		Details: rawDetails,
	})
}

// RespondError emits a tool_error frame.
func (w *Writer) RespondError(id, message string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enc.Encode(toolError{
		Kind:    "tool_error",
		ID:      id,
		Message: message,
	})
}

// Event emits an unsolicited event frame toward the shim.
func (w *Writer) Event(name string, data any) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	var rawData json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("ipc Event marshal data: %w", err)
		}
		rawData = b
	}
	return w.enc.Encode(eventFrame{
		Kind: "event",
		Name: name,
		Data: rawData,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Reader — parse inbound frames from stdin
// ─────────────────────────────────────────────────────────────────────────────

// ReadRequests returns a channel that receives parsed inbound frames until
// r reaches EOF or an unrecoverable error. Malformed JSON lines are discarded
// (a zero-value Request is NOT sent); the loop continues after malformed input.
// The channel is closed when reading is done.
func ReadRequests(r io.Reader) <-chan Request {
	ch := make(chan Request, 16)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(r)
		// stdin lines can be large (large params); give generous buffer.
		const maxBuf = 4 * 1024 * 1024 // 4 MB
		sc.Buffer(make([]byte, 64*1024), maxBuf)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			var req Request
			if err := json.Unmarshal(line, &req); err != nil {
				// Malformed — log to stderr (not stdout — stdout is IPC-only).
				fmt.Fprintf(os.Stderr, "ipc: malformed frame: %v\n", err)
				continue
			}
			if req.Kind == "" {
				fmt.Fprintf(os.Stderr, "ipc: frame missing kind field\n")
				continue
			}
			ch <- req
		}
		// Scanner error is silently absorbed; EOF is the normal case.
	}()
	return ch
}
