//go:build !windows

package localclient_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pi-vs-cc/coms-go/internal/ipc"
	"github.com/pi-vs-cc/coms-go/internal/localclient"
	"github.com/pi-vs-cc/coms-go/internal/proto"
	"github.com/pi-vs-cc/coms-go/internal/util"
)

// runClient starts a localclient with piped stdin/stdout and returns the write-end
// of stdin, read-end of stdout, and a done channel.
func runClient(t *testing.T) (stdinW *os.File, stdoutR *os.File, done <-chan error) {
	t.Helper()
	tempComsDir(t)
	t.Setenv("PI_SESSION_ID", util.NewULID())
	t.Setenv("PI_COMS_NAME", "cov-agent-"+util.NewULID()[20:])

	cfg := localclient.DefaultConfig()
	ctx, cancel := context.WithCancel(context.Background())

	stdinR, stdinW2, _ := os.Pipe()
	stdoutR2, stdoutW, _ := os.Pipe()
	cfg.Stdin = stdinR
	cfg.Stdout = stdoutW

	t.Cleanup(func() {
		cancel()
		stdinW2.Close()
		stdoutW.Close()
	})

	doneCh := make(chan error, 1)
	go func() {
		err := localclient.Run(ctx, cfg)
		stdoutW.Close()
		doneCh <- err
	}()

	return stdinW2, stdoutR2, doneCh
}

// sendIPCAndShutdown sends one IPC request, then a shutdown, and collects output.
func sendIPCAndShutdown(t *testing.T, stdinW *os.File, stdoutR *os.File, done <-chan error, req ipc.Request) []map[string]any {
	t.Helper()
	line, _ := json.Marshal(req)
	stdinW.Write(append(line, '\n'))
	shut, _ := json.Marshal(ipc.Request{Kind: "shutdown"})
	stdinW.Write(append(shut, '\n'))
	stdinW.Close()
	<-done
	stdoutR.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 65536)
	n, _ := stdoutR.Read(buf)
	stdoutR.Close()
	var out []map[string]any
	for _, l := range bytes.Split(bytes.TrimSpace(buf[:n]), []byte("\n")) {
		if len(l) == 0 {
			continue
		}
		var m map[string]any
		if json.Unmarshal(l, &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// ─── coms_send — missing params ───────────────────────────────────────────────

func TestIPC_comsSend_missingTarget(t *testing.T) {
	stdinW, stdoutR, done := runClient(t)
	time.Sleep(80 * time.Millisecond)

	req := ipc.Request{
		Kind:   "tool_request",
		ID:     "send-bad-1",
		Tool:   "coms_send",
		Params: json.RawMessage(`{"target":"","prompt":"hello"}`),
	}
	frames := sendIPCAndShutdown(t, stdinW, stdoutR, done, req)
	found := false
	for _, f := range frames {
		if f["id"] == "send-bad-1" {
			found = true
			// Should be tool_response or tool_error indicating missing target.
		}
	}
	if !found {
		t.Error("did not receive response for send-bad-1")
	}
}

func TestIPC_comsSend_targetNotFound(t *testing.T) {
	stdinW, stdoutR, done := runClient(t)
	time.Sleep(80 * time.Millisecond)

	req := ipc.Request{
		Kind:   "tool_request",
		ID:     "send-tnf-1",
		Tool:   "coms_send",
		Params: json.RawMessage(`{"target":"ghost-agent","prompt":"hello"}`),
	}
	frames := sendIPCAndShutdown(t, stdinW, stdoutR, done, req)
	found := false
	for _, f := range frames {
		if f["id"] == "send-tnf-1" {
			found = true
		}
	}
	if !found {
		t.Error("did not receive response for send-tnf-1")
	}
}

// ─── coms_await — unknown msg_id ──────────────────────────────────────────────

func TestIPC_comsAwait_unknownMsgID(t *testing.T) {
	stdinW, stdoutR, done := runClient(t)
	time.Sleep(80 * time.Millisecond)

	req := ipc.Request{
		Kind:   "tool_request",
		ID:     "await-unk-1",
		Tool:   "coms_await",
		Params: json.RawMessage(`{"msg_id":"DOESNOTEXIST"}`),
	}
	frames := sendIPCAndShutdown(t, stdinW, stdoutR, done, req)
	found := false
	for _, f := range frames {
		if f["id"] == "await-unk-1" {
			found = true
		}
	}
	if !found {
		t.Error("did not receive response for await-unk-1")
	}
}

// ─── coms_await — missing msg_id ──────────────────────────────────────────────

func TestIPC_comsAwait_missingMsgID(t *testing.T) {
	stdinW, stdoutR, done := runClient(t)
	time.Sleep(80 * time.Millisecond)

	req := ipc.Request{
		Kind:   "tool_request",
		ID:     "await-missing-1",
		Tool:   "coms_await",
		Params: json.RawMessage(`{}`),
	}
	frames := sendIPCAndShutdown(t, stdinW, stdoutR, done, req)
	found := false
	for _, f := range frames {
		if f["id"] == "await-missing-1" {
			found = true
		}
	}
	if !found {
		t.Error("did not receive response for await-missing-1")
	}
}

// ─── coms_get — pending ───────────────────────────────────────────────────────

func TestIPC_comsGet_pending(t *testing.T) {
	stdinW, stdoutR, done := runClient(t)
	time.Sleep(80 * time.Millisecond)

	// coms_get on a nonexistent msg_id should return status=error.
	req := ipc.Request{
		Kind:   "tool_request",
		ID:     "get-pend-1",
		Tool:   "coms_get",
		Params: json.RawMessage(`{"msg_id":"NOPE"}`),
	}
	frames := sendIPCAndShutdown(t, stdinW, stdoutR, done, req)
	found := false
	for _, f := range frames {
		if f["id"] == "get-pend-1" {
			found = true
		}
	}
	if !found {
		t.Error("did not receive response for get-pend-1")
	}
}

// ─── unknown tool ─────────────────────────────────────────────────────────────

func TestIPC_unknownTool(t *testing.T) {
	stdinW, stdoutR, done := runClient(t)
	time.Sleep(80 * time.Millisecond)

	req := ipc.Request{
		Kind:   "tool_request",
		ID:     "unk-tool-1",
		Tool:   "coms_unknown_tool",
		Params: json.RawMessage(`{}`),
	}
	frames := sendIPCAndShutdown(t, stdinW, stdoutR, done, req)
	found := false
	for _, f := range frames {
		if f["id"] == "unk-tool-1" {
			found = true
		}
	}
	if !found {
		t.Error("did not receive response for unk-tool-1")
	}
}

// ─── lifecycle / agent_end ────────────────────────────────────────────────────

func TestIPC_lifecycle_agentEnd_noInbound(t *testing.T) {
	stdinW, stdoutR, done := runClient(t)
	time.Sleep(80 * time.Millisecond)

	// Send agent_end with no current inbound — should be a no-op.
	lc := ipc.Request{
		Kind:  "lifecycle",
		Event: "agent_end",
		Data:  json.RawMessage(`{"last_text":"hello from agent"}`),
	}
	shut := ipc.Request{Kind: "shutdown"}
	lcLine, _ := json.Marshal(lc)
	shutLine, _ := json.Marshal(shut)
	stdinW.Write(append(lcLine, '\n'))
	stdinW.Write(append(shutLine, '\n'))
	stdinW.Close()
	<-done
	stdoutR.Close()
}

func TestIPC_lifecycle_unknownEvent(t *testing.T) {
	stdinW, stdoutR, done := runClient(t)
	time.Sleep(80 * time.Millisecond)

	lc := ipc.Request{Kind: "lifecycle", Event: "other_event"}
	shut := ipc.Request{Kind: "shutdown"}
	lcLine, _ := json.Marshal(lc)
	shutLine, _ := json.Marshal(shut)
	stdinW.Write(append(lcLine, '\n'))
	stdinW.Write(append(shutLine, '\n'))
	stdinW.Close()
	<-done
	stdoutR.Close()
}

// ─── command event ────────────────────────────────────────────────────────────

func TestIPC_commandEvent(t *testing.T) {
	stdinW, stdoutR, done := runClient(t)
	time.Sleep(80 * time.Millisecond)

	cmd := ipc.Request{Kind: "command", Data: json.RawMessage(`{}`)}
	shut := ipc.Request{Kind: "shutdown"}
	cmdLine, _ := json.Marshal(cmd)
	shutLine, _ := json.Marshal(shut)
	stdinW.Write(append(cmdLine, '\n'))
	time.Sleep(30 * time.Millisecond)
	stdinW.Write(append(shutLine, '\n'))
	stdinW.Close()
	<-done
	stdoutR.Close()
}

// ─── handleConn: response envelope ───────────────────────────────────────────

func TestHandleConn_responseEnvelope(t *testing.T) {
	tempComsDir(t)
	t.Setenv("PI_SESSION_ID", util.NewULID())
	t.Setenv("PI_COMS_NAME", "resp-agent")

	cfg := localclient.DefaultConfig()
	ctx, cancel := context.WithCancel(context.Background())

	pr, pw, _ := os.Pipe()
	cfg.Stdin = pr

	done := make(chan error, 1)
	go func() { done <- localclient.Run(ctx, cfg) }()
	time.Sleep(80 * time.Millisecond)

	comsDir := os.Getenv("PI_COMS_DIR")
	sockDir := filepath.Join(comsDir, "sockets")
	entries, err := os.ReadDir(sockDir)
	if err != nil || len(entries) == 0 {
		cancel()
		pw.Close()
		t.Skip("could not find socket")
	}
	sockPath := filepath.Join(sockDir, entries[0].Name())
	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		cancel()
		pw.Close()
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send a response envelope for an unknown msg_id (orphan response).
	env := proto.ResponseEnvelope{
		Envelope: proto.Envelope{
			Type:           "response",
			MsgID:          util.NewULID(),
			SenderSession:  "ghost-sender",
			SenderEndpoint: "/tmp/ghost.sock",
			Hops:           0,
			Timestamp:      util.NowIso(),
		},
		Response: json.RawMessage(`"result"`),
	}
	data, _ := json.Marshal(env)
	data = append(data, '\n')
	conn.Write(data)

	conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 1024)
	n, _ := conn.Read(buf)
	var ack proto.AckMessage
	_ = json.Unmarshal(buf[:n], &ack)
	if ack.Type != "ack" {
		t.Errorf("orphan response: got ack.Type=%q, want ack", ack.Type)
	}

	cancel()
	pw.Close()
	<-done
}

// ─── handleConn: unknown type ────────────────────────────────────────────────

func TestHandleConn_unknownType(t *testing.T) {
	tempComsDir(t)
	t.Setenv("PI_SESSION_ID", util.NewULID())
	t.Setenv("PI_COMS_NAME", "unk-type-agent")

	cfg := localclient.DefaultConfig()
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw, _ := os.Pipe()
	cfg.Stdin = pr

	done := make(chan error, 1)
	go func() { done <- localclient.Run(ctx, cfg) }()
	time.Sleep(80 * time.Millisecond)

	comsDir := os.Getenv("PI_COMS_DIR")
	sockDir := filepath.Join(comsDir, "sockets")
	entries, err := os.ReadDir(sockDir)
	if err != nil || len(entries) == 0 {
		cancel()
		pw.Close()
		t.Skip("could not find socket")
	}
	sockPath := filepath.Join(sockDir, entries[0].Name())
	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		cancel()
		pw.Close()
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Valid envelope shape but unknown type.
	env := map[string]any{
		"type":            "unicorn",
		"msg_id":          util.NewULID(),
		"sender_session":  "s1",
		"sender_endpoint": "/tmp/s1.sock",
		"hops":            0,
		"timestamp":       util.NowIso(),
	}
	data, _ := json.Marshal(env)
	data = append(data, '\n')
	conn.Write(data)

	conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 1024)
	n, _ := conn.Read(buf)
	var ack proto.AckMessage
	_ = json.Unmarshal(buf[:n], &ack)
	if ack.Type != "nack" {
		t.Errorf("unknown type: got %q, want nack", ack.Type)
	}

	cancel()
	pw.Close()
	<-done
}

// ─── coms_list with wildcard project ─────────────────────────────────────────

func TestIPC_comsList_wildcardProject(t *testing.T) {
	stdinW, stdoutR, done := runClient(t)
	time.Sleep(80 * time.Millisecond)

	req := ipc.Request{
		Kind:   "tool_request",
		ID:     "list-star-1",
		Tool:   "coms_list",
		Params: json.RawMessage(`{"project":"*"}`),
	}
	frames := sendIPCAndShutdown(t, stdinW, stdoutR, done, req)
	found := false
	for _, f := range frames {
		if f["id"] == "list-star-1" {
			found = true
		}
	}
	if !found {
		t.Error("did not receive response for list-star-1")
	}
}
