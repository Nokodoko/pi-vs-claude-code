package netclient_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/pi-vs-cc/coms-go/internal/ipc"
	. "github.com/pi-vs-cc/coms-go/internal/netclient"
)

// ─── SSE helper functions (internal, tested via white-box) ────────────────────

// These are in the netclient package (sse.go) and are unexported.
// We test them through the exported SSEParser interface and round-trip behavior.
// The pure helpers intField, boolField, rawJSON, safeError, safeErrorStr are
// tested directly in the whitebox file (netclient_whitebox_test.go).

// ─── SSEParser edge cases ────────────────────────────────────────────────────

func TestSSEParser_multipleFrames(t *testing.T) {
	var evts []SSEEvent
	p := NewSSEParser(func(ev SSEEvent) { evts = append(evts, ev) })

	input := "event: a\ndata: 1\n\nevent: b\ndata: 2\n\nevent: c\ndata: 3\n\n"
	p.Feed([]byte(input))

	if len(evts) != 3 {
		t.Fatalf("got %d events, want 3", len(evts))
	}
	if evts[0].Event != "a" || evts[1].Event != "b" || evts[2].Event != "c" {
		t.Errorf("events = %v", evts)
	}
}

func TestSSEParser_emptyFrameSkipped(t *testing.T) {
	var evts []SSEEvent
	p := NewSSEParser(func(ev SSEEvent) { evts = append(evts, ev) })

	// Frame with no data lines — should be skipped.
	p.Feed([]byte(": comment only\n\n"))
	if len(evts) != 0 {
		t.Errorf("empty frame: got %d events, want 0", len(evts))
	}
}

func TestSSEParser_multiLineData(t *testing.T) {
	var evts []SSEEvent
	p := NewSSEParser(func(ev SSEEvent) { evts = append(evts, ev) })

	p.Feed([]byte("event: multi\ndata: line1\ndata: line2\n\n"))
	if len(evts) != 1 {
		t.Fatalf("got %d events, want 1", len(evts))
	}
	if evts[0].Data != "line1\nline2" {
		t.Errorf("data = %q, want 'line1\\nline2'", evts[0].Data)
	}
}

func TestSSEParser_idField(t *testing.T) {
	var evts []SSEEvent
	p := NewSSEParser(func(ev SSEEvent) { evts = append(evts, ev) })

	p.Feed([]byte("event: ping\nid: 42\ndata: {}\n\n"))
	if len(evts) != 1 {
		t.Fatalf("got %d events, want 1", len(evts))
	}
	if evts[0].ID != "42" {
		t.Errorf("ID = %q, want '42'", evts[0].ID)
	}
}

// ─── DefaultConfig additional env vars ───────────────────────────────────────

func TestDefaultConfig_maxHops(t *testing.T) {
	t.Setenv("PI_COMS_NET_MAX_HOPS", "10")
	cfg := DefaultConfig()
	if cfg.MaxHops != 10 {
		t.Errorf("MaxHops = %d, want 10", cfg.MaxHops)
	}
}

func TestDefaultConfig_heartbeatMs(t *testing.T) {
	t.Setenv("PI_COMS_NET_HEARTBEAT_MS", "5000")
	cfg := DefaultConfig()
	if cfg.HeartbeatMs != 5000 {
		t.Errorf("HeartbeatMs = %d, want 5000", cfg.HeartbeatMs)
	}
}

func TestDefaultConfig_explicitFlag(t *testing.T) {
	t.Setenv("PI_COMS_EXPLICIT", "1")
	cfg := DefaultConfig()
	if !cfg.Explicit {
		t.Error("Explicit should be true when PI_COMS_EXPLICIT=1")
	}
}

func TestDefaultConfig_messageTTLMs(t *testing.T) {
	t.Setenv("PI_COMS_NET_MESSAGE_TTL_MS", "60000")
	cfg := DefaultConfig()
	if cfg.MessageTTLMs != 60000 {
		t.Errorf("MessageTTLMs = %d, want 60000", cfg.MessageTTLMs)
	}
}

// ─── SSE tool dispatch: unknown tool ─────────────────────────────────────────

func TestNetClient_unknownTool(t *testing.T) {
	hub := mockHub(t)
	defer hub.Close()

	stdinW, stdoutR, done := runNetClient(t, hub, "tooltest-agent")
	time.Sleep(200 * time.Millisecond)

	req := ipc.Request{
		Kind:   "tool_request",
		ID:     "unknown-tool-1",
		Tool:   "coms_net_unknown",
		Params: json.RawMessage(`{}`),
	}
	line, _ := json.Marshal(req)
	stdinW.Write(append(line, '\n'))
	time.Sleep(100 * time.Millisecond)

	shut, _ := json.Marshal(ipc.Request{Kind: "shutdown"})
	stdinW.Write(append(shut, '\n'))
	stdinW.Close()
	<-done

	stdoutR.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 65536)
	n, _ := stdoutR.Read(buf)
	stdoutR.Close()

	lines := bytes.Split(bytes.TrimSpace(buf[:n]), []byte("\n"))
	found := false
	for _, l := range lines {
		var m map[string]any
		if json.Unmarshal(l, &m) == nil && m["id"] == "unknown-tool-1" {
			found = true
		}
	}
	if !found {
		t.Logf("output: %s", buf[:n])
		t.Error("did not receive response for unknown-tool-1")
	}
}

// ─── coms_net_send — missing params ──────────────────────────────────────────

func TestNetClient_send_missingParams(t *testing.T) {
	hub := mockHub(t)
	defer hub.Close()

	stdinW, stdoutR, done := runNetClient(t, hub, "send-param-agent")
	time.Sleep(200 * time.Millisecond)

	req := ipc.Request{
		Kind:   "tool_request",
		ID:     "send-missing-1",
		Tool:   "coms_net_send",
		Params: json.RawMessage(`{"target":"","prompt":""}`),
	}
	line, _ := json.Marshal(req)
	stdinW.Write(append(line, '\n'))
	time.Sleep(100 * time.Millisecond)

	shut, _ := json.Marshal(ipc.Request{Kind: "shutdown"})
	stdinW.Write(append(shut, '\n'))
	stdinW.Close()
	<-done

	stdoutR.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 65536)
	n, _ := stdoutR.Read(buf)
	stdoutR.Close()

	lines := bytes.Split(bytes.TrimSpace(buf[:n]), []byte("\n"))
	found := false
	for _, l := range lines {
		var m map[string]any
		if json.Unmarshal(l, &m) == nil && m["id"] == "send-missing-1" {
			found = true
		}
	}
	if !found {
		t.Logf("output: %s", buf[:n])
		t.Error("did not receive response for send-missing-1")
	}
}

// ─── coms_net_get — unknown msg_id ───────────────────────────────────────────

func TestNetClient_get_unknownMsgID(t *testing.T) {
	hub := mockHub(t)
	defer hub.Close()

	stdinW, stdoutR, done := runNetClient(t, hub, "get-unk-agent")
	time.Sleep(200 * time.Millisecond)

	req := ipc.Request{
		Kind:   "tool_request",
		ID:     "get-unk-1",
		Tool:   "coms_net_get",
		Params: json.RawMessage(`{"msg_id":"DOESNOTEXIST"}`),
	}
	line, _ := json.Marshal(req)
	stdinW.Write(append(line, '\n'))
	time.Sleep(100 * time.Millisecond)

	shut, _ := json.Marshal(ipc.Request{Kind: "shutdown"})
	stdinW.Write(append(shut, '\n'))
	stdinW.Close()
	<-done

	stdoutR.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 65536)
	n, _ := stdoutR.Read(buf)
	stdoutR.Close()

	lines := bytes.Split(bytes.TrimSpace(buf[:n]), []byte("\n"))
	found := false
	for _, l := range lines {
		var m map[string]any
		if json.Unmarshal(l, &m) == nil && m["id"] == "get-unk-1" {
			found = true
		}
	}
	if !found {
		t.Logf("output: %s", buf[:n])
		t.Error("did not receive response for get-unk-1")
	}
}

// ─── coms_net_await — unknown msg_id ─────────────────────────────────────────

func TestNetClient_await_unknownMsgID(t *testing.T) {
	hub := mockHub(t)
	defer hub.Close()

	stdinW, stdoutR, done := runNetClient(t, hub, "await-unk-agent")
	time.Sleep(200 * time.Millisecond)

	timeoutMs := 100
	req := ipc.Request{
		Kind:   "tool_request",
		ID:     "await-unk-1",
		Tool:   "coms_net_await",
		Params: json.RawMessage(`{"msg_id":"DOESNOTEXIST","timeout_ms":100}`),
	}
	_ = timeoutMs
	line, _ := json.Marshal(req)
	stdinW.Write(append(line, '\n'))
	time.Sleep(300 * time.Millisecond) // wait for await to resolve (timeout or 404)

	shut, _ := json.Marshal(ipc.Request{Kind: "shutdown"})
	stdinW.Write(append(shut, '\n'))
	stdinW.Close()
	<-done

	stdoutR.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 65536)
	n, _ := stdoutR.Read(buf)
	stdoutR.Close()

	lines := bytes.Split(bytes.TrimSpace(buf[:n]), []byte("\n"))
	found := false
	for _, l := range lines {
		var m map[string]any
		if json.Unmarshal(l, &m) == nil && m["id"] == "await-unk-1" {
			found = true
		}
	}
	if !found {
		t.Logf("output: %s", buf[:n])
		t.Error("did not receive response for await-unk-1")
	}
}

// ─── lifecycle: agent_end ─────────────────────────────────────────────────────

func TestNetClient_lifecycle_agentEnd(t *testing.T) {
	hub := mockHub(t)
	defer hub.Close()

	stdinW, _, done := runNetClient(t, hub, "lc-agent")
	time.Sleep(200 * time.Millisecond)

	lc := ipc.Request{Kind: "lifecycle", Event: "agent_end", Data: json.RawMessage(`{"last_text":"answer"}`)}
	shut := ipc.Request{Kind: "shutdown"}
	lcLine, _ := json.Marshal(lc)
	shutLine, _ := json.Marshal(shut)
	stdinW.Write(append(lcLine, '\n'))
	time.Sleep(30 * time.Millisecond)
	stdinW.Write(append(shutLine, '\n'))
	stdinW.Close()
	<-done
}

// ─── command event ────────────────────────────────────────────────────────────

func TestNetClient_commandEvent(t *testing.T) {
	hub := mockHub(t)
	defer hub.Close()

	stdinW, _, done := runNetClient(t, hub, "cmd-agent")
	time.Sleep(200 * time.Millisecond)

	cmd := ipc.Request{Kind: "command"}
	shut := ipc.Request{Kind: "shutdown"}
	cmdLine, _ := json.Marshal(cmd)
	shutLine, _ := json.Marshal(shut)
	stdinW.Write(append(cmdLine, '\n'))
	time.Sleep(30 * time.Millisecond)
	stdinW.Write(append(shutLine, '\n'))
	stdinW.Close()
	<-done
}
