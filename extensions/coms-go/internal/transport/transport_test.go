package transport_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pi-vs-cc/coms-go/internal/transport"
)

// ─────────────────────────────────────────────────────────────────────────────
// WriteEnvelope / ReadEnvelopes round-trip
// ─────────────────────────────────────────────────────────────────────────────

func TestWriteEnvelopeBasic(t *testing.T) {
	var buf bytes.Buffer
	env := map[string]any{"type": "ping", "msg_id": "01TEST"}
	if err := transport.WriteEnvelope(&buf, env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	line := buf.String()
	if !strings.HasSuffix(line, "\n") {
		t.Errorf("WriteEnvelope output missing trailing newline: %q", line)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &m); err != nil {
		t.Errorf("WriteEnvelope output not valid JSON: %v", err)
	}
}

func TestReadEnvelopesBasic(t *testing.T) {
	data := `{"type":"ping","msg_id":"01A"}` + "\n" +
		`{"type":"pong","msg_id":"01B"}` + "\n"
	r := strings.NewReader(data)
	ch := transport.ReadEnvelopes(r)
	var got []map[string]any
	for item := range ch {
		if item.Err != nil {
			t.Fatalf("ReadEnvelopes error: %v", item.Err)
		}
		var m map[string]any
		if err := json.Unmarshal(item.Data, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		got = append(got, m)
	}
	if len(got) != 2 {
		t.Fatalf("ReadEnvelopes count = %d, want 2", len(got))
	}
	if got[0]["type"] != "ping" {
		t.Errorf("first type = %v, want ping", got[0]["type"])
	}
	if got[1]["type"] != "pong" {
		t.Errorf("second type = %v, want pong", got[1]["type"])
	}
}

func TestReadEnvelopesLineCap(t *testing.T) {
	// A line that exactly hits the cap should be delivered.
	line := strings.Repeat("x", transport.LineCap-1) + "\n"
	r := strings.NewReader(line)
	ch := transport.ReadEnvelopes(r)
	item := <-ch
	// The scanner will deliver it as a token (even though it's not JSON).
	// The key check: no error for a line ≤ LineCap.
	if item.Err != nil {
		t.Errorf("line at cap should not error: %v", item.Err)
	}
}

func TestReadEnvelopesEOF(t *testing.T) {
	r := strings.NewReader("")
	ch := transport.ReadEnvelopes(r)
	for item := range ch {
		if item.Err != nil && item.Err != io.EOF {
			t.Errorf("unexpected error on empty reader: %v", item.Err)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// MakeEndpoint
// ─────────────────────────────────────────────────────────────────────────────

func TestMakeEndpoint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_COMS_DIR", dir)
	sid := "01HXNJ0E5Q4M7Z2C1V8YR6F3KT"
	got := transport.MakeEndpoint(sid)
	if !strings.HasSuffix(got, sid+".sock") {
		t.Errorf("MakeEndpoint = %q, want suffix %s.sock", got, sid)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// BindEndpoint + ProbeStaleSocket + SendEnvelope round-trip
// ─────────────────────────────────────────────────────────────────────────────

func TestBindAndProbe(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")

	l, err := transport.BindEndpoint(sock)
	if err != nil {
		t.Fatalf("BindEndpoint: %v", err)
	}
	defer l.Close()
	defer os.Remove(sock)

	// Should report in_use while the listener is open.
	verdict := transport.ProbeStaleSocket(sock)
	if verdict != "in_use" {
		t.Errorf("ProbeStaleSocket with live listener = %q, want in_use", verdict)
	}

	l.Close()
	os.Remove(sock)

	// After close+remove, should report stale.
	verdict2 := transport.ProbeStaleSocket(sock)
	if verdict2 != "stale" {
		t.Errorf("ProbeStaleSocket after close = %q, want stale", verdict2)
	}
}

func TestBindEndpointStaleCleanup(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "stale.sock")

	// Create a stale socket file (no server behind it).
	if err := os.WriteFile(sock, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}
	// BindEndpoint should remove the stale file and bind successfully.
	l, err := transport.BindEndpoint(sock)
	if err != nil {
		t.Fatalf("BindEndpoint with stale file: %v", err)
	}
	l.Close()
}

func TestSendEnvelopeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "rt.sock")

	l, err := transport.BindEndpoint(sock)
	if err != nil {
		t.Fatalf("BindEndpoint: %v", err)
	}
	defer l.Close()

	// Echo server: accept one connection, read one line, write one line back.
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Read the incoming envelope and echo back an ack.
		ack := map[string]string{"type": "ack", "msg_id": "01ECHO"}
		_ = transport.WriteEnvelope(conn, ack)
	}()

	envelope := map[string]string{"type": "ping", "msg_id": "01SEND"}
	resp, err := transport.SendEnvelope(sock, envelope)
	if err != nil {
		t.Fatalf("SendEnvelope: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(resp, &m); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if m["type"] != "ack" {
		t.Errorf("response type = %v, want ack", m["type"])
	}
	<-done
}

func TestSendEnvelopeToClosedSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "closed.sock")
	// No server — should return an error.
	_, err := transport.SendEnvelope(sock, map[string]string{"type": "ping"})
	if err == nil {
		t.Error("SendEnvelope to closed socket should return error")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Direct net.Listener usage (for integration-style tests)
// ─────────────────────────────────────────────────────────────────────────────

func TestWriteReadMultipleFrames(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	type msg struct {
		N int    `json:"n"`
		S string `json:"s"`
	}

	go func() {
		for i := 0; i < 5; i++ {
			_ = transport.WriteEnvelope(pw, msg{N: i, S: "hello"})
		}
		pw.Close()
	}()

	ch := transport.ReadEnvelopes(pr)
	count := 0
	for item := range ch {
		if item.Err != nil {
			t.Errorf("ReadEnvelopes error: %v", item.Err)
			continue
		}
		var m msg
		if err := json.Unmarshal(item.Data, &m); err != nil {
			t.Errorf("unmarshal: %v", err)
			continue
		}
		if m.S != "hello" {
			t.Errorf("unexpected msg: %+v", m)
		}
		count++
	}
	if count != 5 {
		t.Errorf("received %d frames, want 5", count)
	}
}

// Ensure BindEndpoint returns error when socket is already in use.
func TestBindEndpointInUse(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "inuse.sock")

	l1, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer l1.Close()

	_, err = transport.BindEndpoint(sock)
	if err == nil {
		t.Error("BindEndpoint should fail when socket is in use")
	}
}
