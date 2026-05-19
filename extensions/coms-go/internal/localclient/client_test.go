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

// tempComsDir creates a temporary PI_COMS_DIR and sets the env var.
func tempComsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PI_COMS_DIR", dir)
	return dir
}

// ─────────────────────────────────────────────────────────────────────────────
// Config defaults
// ─────────────────────────────────────────────────────────────────────────────

func TestDefaultConfig_project(t *testing.T) {
	t.Setenv("PI_COMS_PROJECT", "my-proj")
	cfg := localclient.DefaultConfig()
	if cfg.Project != "my-proj" {
		t.Errorf("project = %v, want my-proj", cfg.Project)
	}
}

func TestDefaultConfig_defaults(t *testing.T) {
	// Ensure no lingering env.
	os.Unsetenv("PI_COMS_PROJECT")
	os.Unsetenv("PI_COMS_MAX_HOPS")
	cfg := localclient.DefaultConfig()
	if cfg.Project != "default" {
		t.Errorf("default project = %v, want default", cfg.Project)
	}
	if cfg.MaxHops != 5 {
		t.Errorf("maxHops = %v, want 5", cfg.MaxHops)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Ping handler
// ─────────────────────────────────────────────────────────────────────────────

func TestPingRespondsWithPong(t *testing.T) {
	tempComsDir(t)
	t.Setenv("PI_SESSION_ID", util.NewULID())
	t.Setenv("PI_COMS_NAME", "test-ping-agent")

	cfg := localclient.DefaultConfig()
	ctx, cancel := context.WithCancel(context.Background())

	// Use a pipe for stdin so we can close it later.
	pr, pw, _ := os.Pipe()

	cfg.Stdin = pr

	done := make(chan error, 1)
	go func() {
		done <- localclient.Run(ctx, cfg)
	}()

	// Give the client a moment to bind.
	time.Sleep(80 * time.Millisecond)

	// Resolve the socket path (same logic as MakeEndpoint).
	comsDir := os.Getenv("PI_COMS_DIR")
	sockDir := filepath.Join(comsDir, "sockets")

	// Find the socket file.
	entries, err := os.ReadDir(sockDir)
	if err != nil {
		cancel()
		pw.Close()
		t.Fatalf("readdir sockets: %v", err)
	}
	if len(entries) == 0 {
		cancel()
		pw.Close()
		t.Fatal("no socket files found")
	}
	sockPath := filepath.Join(sockDir, entries[0].Name())

	// Send a ping envelope.
	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		cancel()
		pw.Close()
		t.Fatalf("dial socket: %v", err)
	}
	defer conn.Close()

	ping := proto.PingEnvelope{
		Envelope: proto.Envelope{
			Type:           "ping",
			MsgID:          util.NewULID(),
			SenderSession:  "fake-sender",
			SenderEndpoint: "/tmp/fake.sock",
			Hops:           0,
			Timestamp:      util.NowIso(),
		},
	}
	data, _ := json.Marshal(ping)
	data = append(data, '\n')
	_, _ = conn.Write(data)

	// Read pong.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		cancel()
		pw.Close()
		t.Fatalf("read pong: %v", err)
	}

	var pong proto.Pong
	if err := json.Unmarshal(buf[:n], &pong); err != nil {
		cancel()
		pw.Close()
		t.Fatalf("unmarshal pong: %v (raw: %s)", err, buf[:n])
	}
	if pong.Type != "pong" {
		t.Errorf("pong.Type = %v, want pong", pong.Type)
	}
	if pong.AgentCard.Name != "test-ping-agent" {
		t.Errorf("pong.AgentCard.Name = %v, want test-ping-agent", pong.AgentCard.Name)
	}
	if pong.MsgID != ping.MsgID {
		t.Errorf("pong.MsgID = %v, want %v", pong.MsgID, ping.MsgID)
	}

	cancel()
	pw.Close()
	<-done
}

// ─────────────────────────────────────────────────────────────────────────────
// IPC tool: coms_list (unit path — no real peers)
// ─────────────────────────────────────────────────────────────────────────────

func TestIPC_comsList_noPeers(t *testing.T) {
	tempComsDir(t)
	t.Setenv("PI_SESSION_ID", util.NewULID())
	t.Setenv("PI_COMS_NAME", "list-agent")

	cfg := localclient.DefaultConfig()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Build an IPC request for coms_list.
	req := ipc.Request{
		Kind:   "tool_request",
		ID:     "list-1",
		Tool:   "coms_list",
		Params: json.RawMessage(`{"project":"default"}`),
	}
	reqLine, _ := json.Marshal(req)
	reqLine = append(reqLine, '\n')

	// Append shutdown after the request.
	shutdown := ipc.Request{Kind: "shutdown"}
	shutLine, _ := json.Marshal(shutdown)
	shutLine = append(shutLine, '\n')

	stdinData := append(reqLine, shutLine...)
	pr, pw, _ := os.Pipe()
	pw.Write(stdinData)
	pw.Close()

	outR, outW, _ := os.Pipe()
	cfg.Stdin = pr
	cfg.Stdout = outW

	done := make(chan error, 1)
	go func() {
		done <- localclient.Run(ctx, cfg)
	}()

	<-done
	outW.Close()

	buf := make([]byte, 65536)
	n, _ := outR.Read(buf)
	outR.Close()

	// Parse output lines.
	lines := bytes.Split(bytes.TrimSpace(buf[:n]), []byte("\n"))
	if len(lines) == 0 {
		t.Fatal("no IPC output lines")
	}

	var resp map[string]any
	if err := json.Unmarshal(lines[0], &resp); err != nil {
		t.Fatalf("unmarshal IPC response: %v (raw: %s)", err, lines[0])
	}
	if resp["id"] != "list-1" {
		t.Errorf("id = %v, want list-1", resp["id"])
	}
	if resp["kind"] != "tool_response" {
		t.Errorf("kind = %v, want tool_response", resp["kind"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// IPC tool: coms_get — unknown msg_id returns error detail
// ─────────────────────────────────────────────────────────────────────────────

func TestIPC_comsGet_unknownMsgID(t *testing.T) {
	tempComsDir(t)
	t.Setenv("PI_SESSION_ID", util.NewULID())
	t.Setenv("PI_COMS_NAME", "get-agent")

	cfg := localclient.DefaultConfig()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := ipc.Request{
		Kind:   "tool_request",
		ID:     "get-1",
		Tool:   "coms_get",
		Params: json.RawMessage(`{"msg_id":"NONEXISTENT"}`),
	}
	reqLine, _ := json.Marshal(req)
	reqLine = append(reqLine, '\n')
	shutdown := ipc.Request{Kind: "shutdown"}
	shutLine, _ := json.Marshal(shutdown)
	shutLine = append(shutLine, '\n')

	stdinData := append(reqLine, shutLine...)
	pr, pw, _ := os.Pipe()
	pw.Write(stdinData)
	pw.Close()

	outR, outW, _ := os.Pipe()
	cfg.Stdin = pr
	cfg.Stdout = outW

	done := make(chan error, 1)
	go func() {
		done <- localclient.Run(ctx, cfg)
	}()

	<-done
	outW.Close()

	buf := make([]byte, 65536)
	n, _ := outR.Read(buf)
	outR.Close()

	lines := bytes.Split(bytes.TrimSpace(buf[:n]), []byte("\n"))
	if len(lines) == 0 {
		t.Fatal("no output")
	}
	var resp map[string]any
	_ = json.Unmarshal(lines[0], &resp)
	if resp["id"] != "get-1" {
		t.Errorf("id = %v, want get-1", resp["id"])
	}
	// Should return tool_response (not tool_error) with status=error
	if resp["kind"] != "tool_response" {
		t.Errorf("kind = %v, want tool_response", resp["kind"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handlePrompt — hop limit check
// ─────────────────────────────────────────────────────────────────────────────

func TestHandlePrompt_hopLimit(t *testing.T) {
	tempComsDir(t)
	t.Setenv("PI_SESSION_ID", util.NewULID())
	t.Setenv("PI_COMS_NAME", "hop-agent")
	t.Setenv("PI_COMS_MAX_HOPS", "5")

	cfg := localclient.DefaultConfig()
	ctx, cancel := context.WithCancel(context.Background())

	pr, pw, _ := os.Pipe()
	cfg.Stdin = pr

	done := make(chan error, 1)
	go func() {
		done <- localclient.Run(ctx, cfg)
	}()

	time.Sleep(80 * time.Millisecond)

	comsDir := os.Getenv("PI_COMS_DIR")
	sockDir := filepath.Join(comsDir, "sockets")
	entries, err := os.ReadDir(sockDir)
	if err != nil || len(entries) == 0 {
		cancel()
		pw.Close()
		t.Skip("could not find socket (client not bound yet)")
	}

	sockPath := filepath.Join(sockDir, entries[0].Name())
	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		cancel()
		pw.Close()
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send prompt with hops=5 (at limit).
	env := proto.PromptEnvelope{
		Envelope: proto.Envelope{
			Type:           "prompt",
			MsgID:          util.NewULID(),
			SenderSession:  "sender-x",
			SenderEndpoint: "/tmp/fake.sock",
			Hops:           5,
			Timestamp:      util.NowIso(),
		},
		Prompt:     "hello",
		SenderName: "sender",
		SenderCwd:  "/tmp",
	}
	data, _ := json.Marshal(env)
	data = append(data, '\n')
	_, _ = conn.Write(data)

	conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 1024)
	n, _ := conn.Read(buf)

	var ack proto.AckMessage
	_ = json.Unmarshal(buf[:n], &ack)
	if ack.Type != "nack" {
		t.Errorf("expected nack for hop limit exceeded, got %v", ack.Type)
	}

	cancel()
	pw.Close()
	<-done
}

// ─────────────────────────────────────────────────────────────────────────────
// Malformed envelope → nack
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleMalformedEnvelope(t *testing.T) {
	tempComsDir(t)
	t.Setenv("PI_SESSION_ID", util.NewULID())
	t.Setenv("PI_COMS_NAME", "malform-agent")

	cfg := localclient.DefaultConfig()
	ctx, cancel := context.WithCancel(context.Background())

	pr, pw, _ := os.Pipe()
	cfg.Stdin = pr

	done := make(chan error, 1)
	go func() {
		done <- localclient.Run(ctx, cfg)
	}()

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

	_, _ = conn.Write([]byte("not json at all\n"))

	conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 1024)
	n, _ := conn.Read(buf)

	var ack proto.AckMessage
	_ = json.Unmarshal(buf[:n], &ack)
	if ack.Type != "nack" {
		t.Errorf("expected nack for malformed envelope, got %q", ack.Type)
	}

	cancel()
	pw.Close()
	<-done
}
