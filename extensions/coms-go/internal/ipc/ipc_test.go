package ipc_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pi-vs-cc/coms-go/internal/ipc"
)

// ─────────────────────────────────────────────────────────────────────────────
// Writer tests
// ─────────────────────────────────────────────────────────────────────────────

func TestRespond_basic(t *testing.T) {
	var buf bytes.Buffer
	w := ipc.NewWriter(&buf)

	content := []ipc.ContentItem{{Type: "text", Text: "hello"}}
	if err := w.Respond("req-1", content, nil); err != nil {
		t.Fatalf("Respond: %v", err)
	}

	var frame map[string]any
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &frame); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := frame["kind"]; got != "tool_response" {
		t.Errorf("kind = %v, want tool_response", got)
	}
	if got := frame["id"]; got != "req-1" {
		t.Errorf("id = %v, want req-1", got)
	}
	if got, ok := frame["ok"].(bool); !ok || !got {
		t.Errorf("ok = %v, want true", frame["ok"])
	}
}

func TestRespondError_basic(t *testing.T) {
	var buf bytes.Buffer
	w := ipc.NewWriter(&buf)

	if err := w.RespondError("req-2", "something went wrong"); err != nil {
		t.Fatalf("RespondError: %v", err)
	}

	var frame map[string]any
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &frame); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := frame["kind"]; got != "tool_error" {
		t.Errorf("kind = %v, want tool_error", got)
	}
	if got := frame["id"]; got != "req-2" {
		t.Errorf("id = %v, want req-2", got)
	}
	if got := frame["message"]; got != "something went wrong" {
		t.Errorf("message = %v, want 'something went wrong'", got)
	}
}

func TestRespondWithDetails(t *testing.T) {
	var buf bytes.Buffer
	w := ipc.NewWriter(&buf)

	details := map[string]any{"agents": []string{"alice", "bob"}}
	content := []ipc.ContentItem{{Type: "text", Text: "2 peer(s)"}}
	if err := w.Respond("req-3", content, details); err != nil {
		t.Fatalf("Respond: %v", err)
	}

	var frame map[string]any
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &frame); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if frame["details"] == nil {
		t.Error("details field missing")
	}
}

func TestEvent(t *testing.T) {
	var buf bytes.Buffer
	w := ipc.NewWriter(&buf)

	if err := w.Event("agent_joined", map[string]string{"name": "bob"}); err != nil {
		t.Fatalf("Event: %v", err)
	}

	var frame map[string]any
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &frame); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := frame["kind"]; got != "event" {
		t.Errorf("kind = %v, want event", got)
	}
	if got := frame["name"]; got != "agent_joined" {
		t.Errorf("name = %v, want agent_joined", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Reader tests
// ─────────────────────────────────────────────────────────────────────────────

func TestReadRequests_toolRequest(t *testing.T) {
	input := `{"kind":"tool_request","id":"abc","tool":"coms_list","params":{"project":"default"}}` + "\n"
	ch := ipc.ReadRequests(strings.NewReader(input))

	req, ok := <-ch
	if !ok {
		t.Fatal("channel closed immediately")
	}
	if req.Kind != "tool_request" {
		t.Errorf("kind = %v, want tool_request", req.Kind)
	}
	if req.ID != "abc" {
		t.Errorf("id = %v, want abc", req.ID)
	}
	if req.Tool != "coms_list" {
		t.Errorf("tool = %v, want coms_list", req.Tool)
	}
}

func TestReadRequests_shutdown(t *testing.T) {
	input := `{"kind":"shutdown"}` + "\n"
	ch := ipc.ReadRequests(strings.NewReader(input))

	req := <-ch
	if req.Kind != "shutdown" {
		t.Errorf("kind = %v, want shutdown", req.Kind)
	}
}

func TestReadRequests_malformedLineSkipped(t *testing.T) {
	// First line malformed, second line valid — reader should skip the bad one.
	input := "not json at all\n" + `{"kind":"shutdown"}` + "\n"
	ch := ipc.ReadRequests(strings.NewReader(input))

	req := <-ch
	if req.Kind != "shutdown" {
		t.Errorf("kind = %v, want shutdown", req.Kind)
	}
}

func TestReadRequests_emptyLinesSkipped(t *testing.T) {
	input := "\n\n" + `{"kind":"shutdown"}` + "\n"
	ch := ipc.ReadRequests(strings.NewReader(input))

	req := <-ch
	if req.Kind != "shutdown" {
		t.Errorf("kind = %v, want shutdown", req.Kind)
	}
}

func TestReadRequests_idEcho(t *testing.T) {
	// Simulate a tool_request + shutdown sequence and verify IDs are preserved.
	input := `{"kind":"tool_request","id":"xyz-9","tool":"coms_get","params":{"msg_id":"01AABB"}}` + "\n" +
		`{"kind":"shutdown"}` + "\n"
	ch := ipc.ReadRequests(strings.NewReader(input))

	first := <-ch
	if first.ID != "xyz-9" {
		t.Errorf("first.ID = %v, want xyz-9", first.ID)
	}

	second := <-ch
	if second.Kind != "shutdown" {
		t.Errorf("second.Kind = %v, want shutdown", second.Kind)
	}
}

func TestReadRequests_lifecycle(t *testing.T) {
	input := `{"kind":"lifecycle","event":"agent_end","data":{"cwd":"/tmp","model":"claude"}}` + "\n"
	ch := ipc.ReadRequests(strings.NewReader(input))

	req := <-ch
	if req.Kind != "lifecycle" {
		t.Errorf("kind = %v, want lifecycle", req.Kind)
	}
	if req.Event != "agent_end" {
		t.Errorf("event = %v, want agent_end", req.Event)
	}
}

func TestReadRequests_eofClosesChannel(t *testing.T) {
	input := `{"kind":"shutdown"}` + "\n"
	ch := ipc.ReadRequests(strings.NewReader(input))

	<-ch // consume shutdown
	_, open := <-ch
	if open {
		t.Error("channel should be closed after EOF")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Round-trip test: Reader → dispatch → Writer
// ─────────────────────────────────────────────────────────────────────────────

func TestRoundTrip(t *testing.T) {
	input := `{"kind":"tool_request","id":"rt-1","tool":"coms_list","params":{}}` + "\n" +
		`{"kind":"shutdown"}` + "\n"
	var out bytes.Buffer
	w := ipc.NewWriter(&out)
	ch := ipc.ReadRequests(strings.NewReader(input))

	for req := range ch {
		if req.Kind == "tool_request" {
			_ = w.Respond(req.ID, []ipc.ContentItem{{Type: "text", Text: "ok"}}, nil)
		}
	}

	// Verify the response line has the right ID.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 output line, got %d: %v", len(lines), lines)
	}
	var frame map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &frame); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if frame["id"] != "rt-1" {
		t.Errorf("id = %v, want rt-1", frame["id"])
	}
}
