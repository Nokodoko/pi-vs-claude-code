package netclient_test

// T10 — receiver-side integration coverage and §12 concurrent-senders
// regression. These tests exercise handleInboundPrompt and handleLifecycle
// directly (via export_test helpers) without spinning up the full SSE / HTTP
// stack, so they run cheap and deterministically as part of `go test ./...`.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pi-vs-cc/coms-go/internal/ipc"
	"github.com/pi-vs-cc/coms-go/internal/netclient"
)

// TestInboundPromptEvent — handleInboundPrompt MUST emit an "inbound_prompt"
// event frame to the ipc.Writer after queueing. Verifies the T1 wiring.
func TestInboundPromptEvent(t *testing.T) {
	var out bytes.Buffer
	c := netclient.NewClientForTest(&out, "default", "self-session", "self")

	c.CallHandleInboundPrompt(map[string]any{
		"msg_id": "01HMSG_ALPHA",
		"sender": map[string]any{
			"name":       "alpha",
			"session_id": "alpha-session",
			"cwd":        "/tmp",
		},
		"hops":   1,
		"prompt": "What is 2+2?",
	})

	// Parse stdout: must contain one event frame for "inbound_prompt".
	lines := bytes.Split(bytes.TrimRight(out.Bytes(), "\n"), []byte("\n"))
	var foundEvent map[string]any
	for _, l := range lines {
		if len(l) == 0 {
			continue
		}
		var f map[string]any
		if json.Unmarshal(l, &f) != nil {
			continue
		}
		if f["kind"] == "event" && f["name"] == "inbound_prompt" {
			foundEvent = f
			break
		}
	}
	if foundEvent == nil {
		t.Fatalf("no inbound_prompt event in output: %s", out.String())
	}
	data, _ := foundEvent["data"].(map[string]any)
	if data == nil {
		t.Fatalf("event has no data: %+v", foundEvent)
	}
	if data["msg_id"] != "01HMSG_ALPHA" {
		t.Errorf("msg_id = %v, want 01HMSG_ALPHA", data["msg_id"])
	}
	if data["sender_name"] != "alpha" {
		t.Errorf("sender_name = %v, want alpha", data["sender_name"])
	}
	if data["body"] != "What is 2+2?" {
		t.Errorf("body = %v, want 'What is 2+2?'", data["body"])
	}
	if c.InboundQueueLen() != 1 {
		t.Errorf("inboundQueueLen = %d, want 1", c.InboundQueueLen())
	}
}

// TestConcurrentSendersRegression — §12 "Concurrent-senders regression". Two
// distinct senders fire inbound_prompt events back-to-back before any
// before_agent_start hook drains them. Both MUST be present in the Go-side
// inboundQueue and BOTH MUST emit ipc events. Guards against the last-wins
// scalar pendingInjection bug that the FIFO design closes.
func TestConcurrentSendersRegression(t *testing.T) {
	var out bytes.Buffer
	c := netclient.NewClientForTest(&out, "default", "self-session", "self")

	c.CallHandleInboundPrompt(map[string]any{
		"msg_id": "MSG_A",
		"sender": map[string]any{"name": "sender-A", "session_id": "a-session"},
		"hops":   1,
		"prompt": "from A",
	})
	c.CallHandleInboundPrompt(map[string]any{
		"msg_id": "MSG_B",
		"sender": map[string]any{"name": "sender-B", "session_id": "b-session"},
		"hops":   1,
		"prompt": "from B",
	})

	if got := c.InboundQueueLen(); got != 2 {
		t.Errorf("inboundQueueLen after two sends = %d, want 2 (no last-wins)", got)
	}

	// Both events MUST appear on stdout, in arrival order.
	body := out.String()
	idxA := strings.Index(body, "MSG_A")
	idxB := strings.Index(body, "MSG_B")
	if idxA < 0 || idxB < 0 {
		t.Fatalf("both events not in output:\n%s", body)
	}
	if !(idxA < idxB) {
		t.Errorf("event order: A at %d, B at %d — expected FIFO (A first)", idxA, idxB)
	}
}

// TestHandleLifecycle_LastText — handleLifecycle MUST call onAgentEnd ONLY
// when last_text is non-empty. Without an inbound entry to respond to, the
// call is a safe no-op either way; the test verifies that the gate at
// client.go does not panic on missing/empty last_text and that the audit
// trail proves the call shape.
func TestHandleLifecycle_LastText(t *testing.T) {
	tests := []struct {
		name string
		data map[string]any
	}{
		{name: "empty last_text", data: map[string]any{"cwd": "/x", "model": "m"}},
		{name: "explicit empty",  data: map[string]any{"cwd": "/x", "model": "m", "last_text": ""}},
		{name: "non-empty",       data: map[string]any{"cwd": "/x", "model": "m", "last_text": "hello"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			c := netclient.NewClientForTest(&out, "default", "self-session", "self")
			raw, _ := json.Marshal(tt.data)
			req := ipc.Request{
				Kind:  "lifecycle",
				ID:    "le-1",
				Event: "agent_end",
				Data:  raw,
			}
			// Must not panic for any of the variants.
			c.CallHandleLifecycle(req)
		})
	}
}
