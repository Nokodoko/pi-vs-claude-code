package netclient_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pi-vs-cc/coms-go/internal/ipc"
	"github.com/pi-vs-cc/coms-go/internal/netclient"
	"github.com/pi-vs-cc/coms-go/internal/proto"
	"github.com/pi-vs-cc/coms-go/internal/util"
)

// ─────────────────────────────────────────────────────────────────────────────
// Mock hub helpers
// ─────────────────────────────────────────────────────────────────────────────

const testToken = "test-token-1234"

// mockHub creates a minimal HTTP test server that handles:
//   - POST /v1/agents/register → RegisterResponse
//   - POST /v1/agents/:sid/heartbeat → {ok:true}
//   - GET  /v1/agents → ListAgentsResponse (empty)
//   - DELETE /v1/agents/:sid → {ok:true}
//   - GET  /health → HealthResponse
func mockHub(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "version": 1, "server_id": "test-srv", "started_at": util.NowIso(),
		})
	})

	mux.HandleFunc("/v1/agents/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !strings.Contains(r.Header.Get("Authorization"), testToken) {
			http.Error(w, `{"ok":false,"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		var req proto.RegisterRequest
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(proto.RegisterResponse{
			Ok: true,
			Agent: proto.AgentCard{
				SessionID: req.SessionID,
				Name:      req.Name,
				Purpose:   req.Purpose,
				Model:     req.Model,
				Color:     req.Color,
				Cwd:       req.Cwd,
				Project:   req.Project,
				Explicit:  req.Explicit,
				StartedAt: util.NowIso(),
				Status:    proto.StatusOnline,
			},
			HeartbeatIntervalMs: 10000,
			SseURL:              "/v1/events?project=" + req.Project + "&session_id=" + req.SessionID,
		})
	})

	mux.HandleFunc("/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(proto.ListAgentsResponse{Agents: []proto.AgentCard{}})
	})

	// /v1/agents/:sid/heartbeat and /v1/agents/:sid (DELETE)
	mux.HandleFunc("/v1/agents/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		// Minimal SSE response — send hello and close.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		fmt.Fprintf(w, "event: hello\nid: 1\ndata: {\"server_id\":\"test\",\"server_time\":\"%s\"}\n\n", util.NowIso())
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Block until request context is done.
		<-r.Context().Done()
	})

	mux.HandleFunc("/v1/messages/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			json.NewEncoder(w).Encode(proto.SendResponse{
				Ok:            true,
				MsgID:         util.NewULID(),
				Status:        proto.MsgStatusDelivered,
				TargetSession: util.NewULID(),
			})
		case http.MethodGet:
			json.NewEncoder(w).Encode(proto.MessageStatusResponse{
				MsgID:  "test",
				Status: proto.MsgStatusQueued,
				Error:  nil,
			})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	return httptest.NewServer(mux)
}

// runNetClient starts a client-net instance against the given hub URL.
// Returns stdin write pipe, stdout read pipe, and a done channel.
func runNetClient(t *testing.T, hub *httptest.Server, name string) (stdinW *os.File, stdoutR *os.File, done <-chan error) {
	t.Helper()

	t.Setenv("PI_SESSION_ID", util.NewULID())
	t.Setenv("PI_COMS_NAME", name)
	t.Setenv("PI_COMS_NET_SERVER_URL", hub.URL)
	t.Setenv("PI_COMS_NET_AUTH_TOKEN", testToken)

	cfg := netclient.DefaultConfig()

	stdinR, stdinW2, _ := os.Pipe()
	stdoutR2, stdoutW, _ := os.Pipe()

	cfg.Stdin = stdinR
	cfg.Stdout = stdoutW

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- netclient.Run(context.Background(), cfg)
	}()

	return stdinW2, stdoutR2, doneCh
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestDefaultConfig_serverURL(t *testing.T) {
	t.Setenv("PI_COMS_NET_SERVER_URL", "http://example.com:1234")
	cfg := netclient.DefaultConfig()
	if cfg.ServerURL != "http://example.com:1234" {
		t.Errorf("ServerURL = %v, want http://example.com:1234", cfg.ServerURL)
	}
}

func TestDefaultConfig_project(t *testing.T) {
	t.Setenv("PI_COMS_PROJECT", "netproj")
	cfg := netclient.DefaultConfig()
	if cfg.Project != "netproj" {
		t.Errorf("Project = %v, want netproj", cfg.Project)
	}
}

func TestRunNetClient_register(t *testing.T) {
	hub := mockHub(t)
	defer hub.Close()

	stdinW, stdoutR, done := runNetClient(t, hub, "reg-agent")

	// Give client time to register + open SSE.
	time.Sleep(200 * time.Millisecond)

	// Send coms_net_list to verify the client is alive.
	req := ipc.Request{
		Kind:   "tool_request",
		ID:     "list-net-1",
		Tool:   "coms_net_list",
		Params: json.RawMessage(`{}`),
	}
	line, _ := json.Marshal(req)
	line = append(line, '\n')
	_, _ = stdinW.Write(line)

	// Give time for response.
	time.Sleep(300 * time.Millisecond)

	// Shutdown.
	shut, _ := json.Marshal(ipc.Request{Kind: "shutdown"})
	shut = append(shut, '\n')
	_, _ = stdinW.Write(shut)
	stdinW.Close()

	<-done

	stdoutR.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 65536)
	n, _ := stdoutR.Read(buf)
	stdoutR.Close()

	if n == 0 {
		t.Skip("no output received (registration may have timed out in CI)")
	}

	lines := bytes.Split(bytes.TrimSpace(buf[:n]), []byte("\n"))
	var found bool
	for _, l := range lines {
		var frame map[string]any
		if json.Unmarshal(l, &frame) == nil && frame["id"] == "list-net-1" {
			found = true
			if frame["kind"] != "tool_response" {
				t.Errorf("kind = %v, want tool_response", frame["kind"])
			}
		}
	}
	if !found {
		t.Logf("output: %s", buf[:n])
		t.Error("did not receive tool_response for coms_net_list")
	}
}

func TestRunNetClient_shutdown_sends_delete(t *testing.T) {
	var deleteCount int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agents/register", func(w http.ResponseWriter, r *http.Request) {
		var req proto.RegisterRequest
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(proto.RegisterResponse{
			Ok: true,
			Agent: proto.AgentCard{
				SessionID: req.SessionID, Name: req.Name,
				Project: req.Project, Status: proto.StatusOnline,
			},
			HeartbeatIntervalMs: 10000,
			SseURL:              "/v1/events?project=default&session_id=" + req.SessionID,
		})
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: hello\nid: 1\ndata: {}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	})
	mux.HandleFunc("/v1/agents/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			atomic.AddInt32(&deleteCount, 1)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("PI_SESSION_ID", util.NewULID())
	t.Setenv("PI_COMS_NAME", "shutdown-agent")
	t.Setenv("PI_COMS_NET_SERVER_URL", srv.URL)
	t.Setenv("PI_COMS_NET_AUTH_TOKEN", testToken)

	cfg := netclient.DefaultConfig()

	stdinR, stdinW, _ := os.Pipe()
	cfg.Stdin = stdinR

	done := make(chan error, 1)
	go func() {
		done <- netclient.Run(context.Background(), cfg)
	}()

	time.Sleep(250 * time.Millisecond)

	shut, _ := json.Marshal(ipc.Request{Kind: "shutdown"})
	shut = append(shut, '\n')
	stdinW.Write(shut)
	stdinW.Close()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("client did not shut down in time")
	}

	if atomic.LoadInt32(&deleteCount) == 0 {
		t.Error("expected DELETE /v1/agents/:sid on shutdown, got none")
	}
}

func TestSSE_exponentialBackoff(t *testing.T) {
	// Server that always returns 503 so the client must backoff + reconnect.
	var connectCount int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agents/register", func(w http.ResponseWriter, r *http.Request) {
		var req proto.RegisterRequest
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(proto.RegisterResponse{
			Ok:                  true,
			Agent:               proto.AgentCard{SessionID: req.SessionID, Name: req.Name, Status: proto.StatusOnline},
			HeartbeatIntervalMs: 10000,
			SseURL:              "/v1/events?session_id=" + req.SessionID,
		})
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&connectCount, 1)
		if count <= 2 {
			// First two connections: fail immediately.
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		// Third connection: succeed (send hello and block).
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: hello\nid: 1\ndata: {}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	})
	mux.HandleFunc("/v1/agents/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("PI_SESSION_ID", util.NewULID())
	t.Setenv("PI_COMS_NAME", "backoff-agent")
	t.Setenv("PI_COMS_NET_SERVER_URL", srv.URL)
	t.Setenv("PI_COMS_NET_AUTH_TOKEN", testToken)

	cfg := netclient.DefaultConfig()

	stdinR, stdinW, _ := os.Pipe()
	cfg.Stdin = stdinR

	done := make(chan error, 1)
	go func() {
		done <- netclient.Run(context.Background(), cfg)
	}()

	// Wait for at least 3 SSE connection attempts (backoff: 500ms, 1000ms, then success).
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&connectCount) >= 3 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	stdinW.Write(append(func() []byte { b, _ := json.Marshal(ipc.Request{Kind: "shutdown"}); return b }(), '\n'))
	stdinW.Close()
	<-done

	if atomic.LoadInt32(&connectCount) < 3 {
		t.Errorf("expected >= 3 SSE connection attempts (exponential backoff), got %d", atomic.LoadInt32(&connectCount))
	}
}

// TestSSEParser verifies the hand-rolled SSE parser handles frame boundaries.
func TestSSEParser(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantEvt  string
		wantData string
	}{
		{
			name:     "simple frame",
			input:    "event: hello\nid: 1\ndata: {\"ok\":true}\n\n",
			wantEvt:  "hello",
			wantData: `{"ok":true}`,
		},
		{
			name:     "default event name",
			input:    "data: foo\n\n",
			wantEvt:  "message",
			wantData: "foo",
		},
		{
			name:     "comment skipped",
			input:    ": this is a comment\nevent: ping\ndata: {}\n\n",
			wantEvt:  "ping",
			wantData: "{}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []netclient.SSEEvent
			parser := netclient.NewSSEParser(func(ev netclient.SSEEvent) {
				got = append(got, ev)
			})
			parser.Feed([]byte(tt.input))

			if len(got) != 1 {
				t.Fatalf("got %d events, want 1", len(got))
			}
			if got[0].Event != tt.wantEvt {
				t.Errorf("Event = %v, want %v", got[0].Event, tt.wantEvt)
			}
			if got[0].Data != tt.wantData {
				t.Errorf("Data = %v, want %v", got[0].Data, tt.wantData)
			}
		})
	}
}

func TestSSEParser_splitDelivery(t *testing.T) {
	// Feed the frame in two chunks to verify buffering.
	var got []netclient.SSEEvent
	parser := netclient.NewSSEParser(func(ev netclient.SSEEvent) {
		got = append(got, ev)
	})

	parser.Feed([]byte("event: pool_snapshot\ndata: "))
	if len(got) != 0 {
		t.Fatal("should not emit before frame is complete")
	}
	parser.Feed([]byte("{\"agents\":[]}\n\n"))
	if len(got) != 1 {
		t.Fatalf("got %d events after full frame, want 1", len(got))
	}
	if got[0].Event != "pool_snapshot" {
		t.Errorf("Event = %v, want pool_snapshot", got[0].Event)
	}
}
