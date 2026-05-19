package server_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pi-vs-cc/coms-go/internal/proto"
	"github.com/pi-vs-cc/coms-go/internal/server"
)

// newTestServer returns an httptest.Server wired with a fresh server state and
// a known bearer token. Caller must call ts.Close().
func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	token := "test-token-1234567890abcdef"
	cfg := &server.Config{
		Host:           "127.0.0.1",
		Port:           0,
		Project:        "default",
		Token:          token,
		MaxHops:        5,
		MessageTTLMS:   1_800_000,
		MaxInbox:       100,
		HeartbeatMS:    10_000,
		StaleAfterMS:   30_000,
		OfflineAfterMS: 60_000,
	}
	ts := httptest.NewServer(server.NewServeMux(cfg))
	return ts, token
}

// authHeader returns the Bearer auth header value.
func authHeader(token string) string {
	return "Bearer " + token
}

// doJSON sends method+path+body and returns the decoded response body.
func doJSON(t *testing.T, ts *httptest.Server, method, path string, body any, token string) (int, []byte) {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, ts.URL+path, reqBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", authHeader(token))
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

// registerAgent registers a test agent and returns its session_id and resolved name.
func registerAgent(t *testing.T, ts *httptest.Server, token, project, sessionID, name string) proto.AgentCard {
	t.Helper()
	code, body := doJSON(t, ts, http.MethodPost, "/v1/agents/register", proto.RegisterRequest{
		Project:   project,
		SessionID: sessionID,
		Name:      name,
		Purpose:   "test agent",
		Model:     "test-model",
		Color:     "#ff0000",
		Cwd:       "/tmp",
		Explicit:  false,
	}, token)
	if code != 200 {
		t.Fatalf("register: got %d, body: %s", code, body)
	}
	var resp proto.RegisterResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("register not ok: %s", body)
	}
	return resp.Agent
}

// ─── /health ─────────────────────────────────────────────────────────────────

func TestHealthNoAuth(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	code, body := doJSON(t, ts, http.MethodGet, "/health", nil, "")
	if code != 200 {
		t.Fatalf("health: got %d, want 200; body: %s", code, body)
	}
	var resp proto.HealthResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Ok {
		t.Errorf("health: ok=false")
	}
	if resp.Version != 1 {
		t.Errorf("health: version=%d, want 1", resp.Version)
	}
	if resp.ServerID == "" {
		t.Errorf("health: server_id empty")
	}
	if resp.StartedAt == "" {
		t.Errorf("health: started_at empty")
	}
}

func TestHealthWrongMethod(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	code, _ := doJSON(t, ts, http.MethodPost, "/health", nil, "")
	if code != 405 {
		t.Errorf("health POST: got %d, want 405", code)
	}
}

// ─── Auth ─────────────────────────────────────────────────────────────────────

func TestAuthMissingBearer(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	code, body := doJSON(t, ts, http.MethodPost, "/v1/agents/register", proto.RegisterRequest{
		Project:   "default",
		SessionID: "sid-1",
		Name:      "agent",
	}, "")
	if code != 401 {
		t.Errorf("missing auth: got %d, want 401; body: %s", code, body)
	}
	var resp proto.ErrorResponse
	json.Unmarshal(body, &resp)
	if resp.Error != "unauthorized" {
		t.Errorf("error=%q, want unauthorized", resp.Error)
	}
}

func TestAuthWrongToken(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()
	_ = token

	code, _ := doJSON(t, ts, http.MethodGet, "/v1/agents?project=default", nil, "wrong-token")
	if code != 401 {
		t.Errorf("wrong token: got %d, want 401", code)
	}
}

// ─── Register ─────────────────────────────────────────────────────────────────

func TestRegisterHappyPath(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	agent := registerAgent(t, ts, token, "default", "sid-reg-1", "planner")
	if agent.Name != "planner" {
		t.Errorf("name=%q, want planner", agent.Name)
	}
	if agent.Status != proto.StatusOnline {
		t.Errorf("status=%q, want online", agent.Status)
	}
	if agent.SessionID != "sid-reg-1" {
		t.Errorf("session_id=%q, want sid-reg-1", agent.SessionID)
	}
}

func TestRegisterNameDedup(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	a1 := registerAgent(t, ts, token, "default", "sid-dd-1", "coder")
	a2 := registerAgent(t, ts, token, "default", "sid-dd-2", "coder")
	if a1.Name != "coder" {
		t.Errorf("a1.Name=%q, want coder", a1.Name)
	}
	if a2.Name != "coder2" {
		t.Errorf("a2.Name=%q, want coder2", a2.Name)
	}
}

func TestRegisterInvalidJSON(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/agents/register", strings.NewReader("{bad json"))
	req.Header.Set("Authorization", authHeader(token))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("bad json: got %d, want 400", resp.StatusCode)
	}
}

func TestRegisterMissingFields(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	code, body := doJSON(t, ts, http.MethodPost, "/v1/agents/register", map[string]any{
		"project": "default",
		// missing session_id and name
	}, token)
	if code != 400 {
		t.Errorf("missing fields: got %d, want 400; body: %s", code, body)
	}
}

// ─── List Agents ──────────────────────────────────────────────────────────────

func TestListAgentsEmpty(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	code, body := doJSON(t, ts, http.MethodGet, "/v1/agents?project=default", nil, token)
	if code != 200 {
		t.Fatalf("list agents: %d %s", code, body)
	}
	var resp proto.ListAgentsResponse
	json.Unmarshal(body, &resp)
	if len(resp.Agents) != 0 {
		t.Errorf("agents=%d, want 0", len(resp.Agents))
	}
}

func TestListAgentsAfterRegister(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-list-1", "alpha")
	registerAgent(t, ts, token, "default", "sid-list-2", "beta")

	code, body := doJSON(t, ts, http.MethodGet, "/v1/agents?project=default", nil, token)
	if code != 200 {
		t.Fatalf("list: %d %s", code, body)
	}
	var resp proto.ListAgentsResponse
	json.Unmarshal(body, &resp)
	if len(resp.Agents) != 2 {
		t.Errorf("agents=%d, want 2", len(resp.Agents))
	}
}

// ─── Heartbeat ────────────────────────────────────────────────────────────────

func TestHeartbeatHappyPath(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-hb-1", "worker")

	code, body := doJSON(t, ts, http.MethodPost, "/v1/agents/sid-hb-1/heartbeat",
		proto.HeartbeatRequest{Project: "default", ContextUsedPct: 25, QueueDepth: 3},
		token)
	if code != 200 {
		t.Fatalf("heartbeat: %d %s", code, body)
	}
	var resp proto.OkResponse
	json.Unmarshal(body, &resp)
	if !resp.Ok {
		t.Errorf("heartbeat: ok=false")
	}
}

func TestHeartbeatUnknownAgent(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	code, _ := doJSON(t, ts, http.MethodPost, "/v1/agents/ghost-sid/heartbeat",
		proto.HeartbeatRequest{Project: "default"},
		token)
	if code != 404 {
		t.Errorf("heartbeat unknown: got %d, want 404", code)
	}
}

// ─── Send + Get Message ───────────────────────────────────────────────────────

func TestSendAndGetMessage(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-send-1", "sender")
	registerAgent(t, ts, token, "default", "sid-send-2", "receiver")

	// Send message by target name.
	code, body := doJSON(t, ts, http.MethodPost, "/v1/messages", proto.SendRequest{
		Project:       "default",
		SenderSession: "sid-send-1",
		Target:        "receiver",
		Prompt:        "Hello receiver",
		Hops:          0,
	}, token)
	if code != 200 {
		t.Fatalf("send: %d %s", code, body)
	}
	var sendResp proto.SendResponse
	json.Unmarshal(body, &sendResp)
	if !sendResp.Ok {
		t.Errorf("send: ok=false")
	}
	if sendResp.MsgID == "" {
		t.Errorf("send: msg_id empty")
	}
	if sendResp.TargetSession != "sid-send-2" {
		t.Errorf("send: target_session=%q, want sid-send-2", sendResp.TargetSession)
	}

	// Get the message.
	code, body = doJSON(t, ts, http.MethodGet, "/v1/messages/"+sendResp.MsgID, nil, token)
	if code != 200 {
		t.Fatalf("get message: %d %s", code, body)
	}
	var msgResp proto.MessageStatusResponse
	json.Unmarshal(body, &msgResp)
	if msgResp.MsgID != sendResp.MsgID {
		t.Errorf("msg_id=%q, want %q", msgResp.MsgID, sendResp.MsgID)
	}
}

func TestSendHopLimitExceeded(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-hop-1", "sender")
	registerAgent(t, ts, token, "default", "sid-hop-2", "receiver")

	code, body := doJSON(t, ts, http.MethodPost, "/v1/messages", proto.SendRequest{
		Project:       "default",
		SenderSession: "sid-hop-1",
		Target:        "receiver",
		Prompt:        "too many hops",
		Hops:          5, // >= MaxHops(5)
	}, token)
	if code != 409 {
		t.Errorf("hop limit: got %d, want 409; body: %s", code, body)
	}
	var errResp proto.ErrorResponse
	json.Unmarshal(body, &errResp)
	if errResp.Error != "hop_limit_exceeded" {
		t.Errorf("error=%q, want hop_limit_exceeded", errResp.Error)
	}
}

func TestSendTargetNotFound(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-tnf-1", "sender")

	code, body := doJSON(t, ts, http.MethodPost, "/v1/messages", proto.SendRequest{
		Project:       "default",
		SenderSession: "sid-tnf-1",
		Target:        "ghost-agent",
		Prompt:        "hello ghost",
	}, token)
	if code != 404 {
		t.Errorf("target not found: got %d, want 404; body: %s", code, body)
	}
}

// ─── Submit Response ──────────────────────────────────────────────────────────

func TestSubmitResponse(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-resp-1", "asker")
	registerAgent(t, ts, token, "default", "sid-resp-2", "answerer")

	// Send.
	_, body := doJSON(t, ts, http.MethodPost, "/v1/messages", proto.SendRequest{
		Project:       "default",
		SenderSession: "sid-resp-1",
		Target:        "answerer",
		Prompt:        "What is 2+2?",
	}, token)
	var sendResp proto.SendResponse
	json.Unmarshal(body, &sendResp)

	// Submit response.
	answerJSON, _ := json.Marshal("4")
	code, body := doJSON(t, ts, http.MethodPost,
		"/v1/messages/"+sendResp.MsgID+"/response",
		proto.ResponseSubmitRequest{
			Project:          "default",
			ResponderSession: "sid-resp-2",
			Response:         json.RawMessage(answerJSON),
		}, token)
	if code != 200 {
		t.Fatalf("submit response: %d %s", code, body)
	}

	// Get final status.
	code, body = doJSON(t, ts, http.MethodGet, "/v1/messages/"+sendResp.MsgID, nil, token)
	if code != 200 {
		t.Fatalf("get after response: %d %s", code, body)
	}
	var msgResp proto.MessageStatusResponse
	json.Unmarshal(body, &msgResp)
	if msgResp.Status != proto.MsgStatusComplete {
		t.Errorf("status=%q, want complete", msgResp.Status)
	}
}

func TestSubmitResponseWrongResponder(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-wr-1", "asker")
	registerAgent(t, ts, token, "default", "sid-wr-2", "answerer")
	registerAgent(t, ts, token, "default", "sid-wr-3", "interloper")

	_, body := doJSON(t, ts, http.MethodPost, "/v1/messages", proto.SendRequest{
		Project:       "default",
		SenderSession: "sid-wr-1",
		Target:        "answerer",
		Prompt:        "Answer me",
	}, token)
	var sendResp proto.SendResponse
	json.Unmarshal(body, &sendResp)

	code, body := doJSON(t, ts, http.MethodPost,
		"/v1/messages/"+sendResp.MsgID+"/response",
		proto.ResponseSubmitRequest{
			Project:          "default",
			ResponderSession: "sid-wr-3", // wrong responder
			Response:         json.RawMessage(`"haha"`),
		}, token)
	if code != 403 {
		t.Errorf("wrong responder: got %d, want 403; body: %s", code, body)
	}
}

func TestSubmitResponseAlreadyTerminal(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-at-1", "asker")
	registerAgent(t, ts, token, "default", "sid-at-2", "answerer")

	_, body := doJSON(t, ts, http.MethodPost, "/v1/messages", proto.SendRequest{
		Project:       "default",
		SenderSession: "sid-at-1",
		Target:        "answerer",
		Prompt:        "once",
	}, token)
	var sendResp proto.SendResponse
	json.Unmarshal(body, &sendResp)

	submitResp := proto.ResponseSubmitRequest{
		Project:          "default",
		ResponderSession: "sid-at-2",
		Response:         json.RawMessage(`"done"`),
	}
	doJSON(t, ts, http.MethodPost, "/v1/messages/"+sendResp.MsgID+"/response", submitResp, token)
	// Second submit should 409.
	code, _ := doJSON(t, ts, http.MethodPost, "/v1/messages/"+sendResp.MsgID+"/response", submitResp, token)
	if code != 409 {
		t.Errorf("already_terminal: got %d, want 409", code)
	}
}

// ─── Delete Agent ─────────────────────────────────────────────────────────────

func TestDeleteAgent(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-del-1", "mortal")

	code, body := doJSON(t, ts, http.MethodDelete, "/v1/agents/sid-del-1?project=default", nil, token)
	if code != 200 {
		t.Fatalf("delete agent: %d %s", code, body)
	}

	// Should be gone from list.
	_, body = doJSON(t, ts, http.MethodGet, "/v1/agents?project=default", nil, token)
	var listResp proto.ListAgentsResponse
	json.Unmarshal(body, &listResp)
	for _, a := range listResp.Agents {
		if a.SessionID == "sid-del-1" {
			t.Errorf("deleted agent still in list")
		}
	}
}

// ─── SSE events ───────────────────────────────────────────────────────────────

func TestSSEHelloAndPoolSnapshot(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-sse-1", "watcher")

	req, _ := http.NewRequest(http.MethodGet,
		ts.URL+"/v1/events?project=default&session_id=sid-sse-1", nil)
	req.Header.Set("Authorization", authHeader(token))

	// Use a client with no timeout; close manually.
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("sse request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("sse: got %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content-type=%q, want text/event-stream", ct)
	}

	// Read until we get both hello and pool_snapshot or timeout.
	events := make(chan string, 10)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: ") {
				events <- strings.TrimPrefix(line, "event: ")
			}
		}
		close(events)
	}()

	seen := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				goto done
			}
			seen[ev] = true
			if seen["hello"] && seen["pool_snapshot"] {
				goto done
			}
		case <-deadline:
			t.Fatalf("timeout waiting for hello+pool_snapshot; got: %v", seen)
		}
	}
done:
	if !seen["hello"] {
		t.Errorf("never received 'hello' event")
	}
	if !seen["pool_snapshot"] {
		t.Errorf("never received 'pool_snapshot' event")
	}
}

func TestSSEMissingSessionID(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	code, _ := doJSON(t, ts, http.MethodGet, "/v1/events?project=default", nil, token)
	if code != 400 {
		t.Errorf("missing session_id: got %d, want 400", code)
	}
}

func TestSSEAgentNotFound(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	code, _ := doJSON(t, ts, http.MethodGet, "/v1/events?project=default&session_id=ghost", nil, token)
	if code != 404 {
		t.Errorf("agent not found: got %d, want 404", code)
	}
}

// ─── Await (long-poll) ────────────────────────────────────────────────────────

func TestAwaitMessageTimeout(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-aw-1", "asker")
	registerAgent(t, ts, token, "default", "sid-aw-2", "answerer")

	_, body := doJSON(t, ts, http.MethodPost, "/v1/messages", proto.SendRequest{
		Project:       "default",
		SenderSession: "sid-aw-1",
		Target:        "answerer",
		Prompt:        "await me",
	}, token)
	var sendResp proto.SendResponse
	json.Unmarshal(body, &sendResp)

	// Await with a very short timeout.
	code, body := doJSON(t, ts, http.MethodGet,
		fmt.Sprintf("/v1/messages/%s/await?timeout_ms=100", sendResp.MsgID), nil, token)
	if code != 200 {
		t.Fatalf("await: %d %s", code, body)
	}
	var msgResp proto.MessageStatusResponse
	json.Unmarshal(body, &msgResp)
	if msgResp.Status != proto.MsgStatusTimeout {
		t.Errorf("status=%q, want timeout", msgResp.Status)
	}
}

func TestAwaitMessageImmediateIfTerminal(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-ai-1", "asker")
	registerAgent(t, ts, token, "default", "sid-ai-2", "answerer")

	_, body := doJSON(t, ts, http.MethodPost, "/v1/messages", proto.SendRequest{
		Project:       "default",
		SenderSession: "sid-ai-1",
		Target:        "answerer",
		Prompt:        "quick one",
	}, token)
	var sendResp proto.SendResponse
	json.Unmarshal(body, &sendResp)

	// Complete it.
	doJSON(t, ts, http.MethodPost, "/v1/messages/"+sendResp.MsgID+"/response",
		proto.ResponseSubmitRequest{
			Project:          "default",
			ResponderSession: "sid-ai-2",
			Response:         json.RawMessage(`"done"`),
		}, token)

	// Await should resolve immediately.
	code, body := doJSON(t, ts, http.MethodGet,
		"/v1/messages/"+sendResp.MsgID+"/await", nil, token)
	if code != 200 {
		t.Fatalf("await terminal: %d %s", code, body)
	}
	var msgResp proto.MessageStatusResponse
	json.Unmarshal(body, &msgResp)
	if msgResp.Status != proto.MsgStatusComplete {
		t.Errorf("status=%q, want complete", msgResp.Status)
	}
}

// ─── Error envelopes ──────────────────────────────────────────────────────────

func TestGetMessageNotFound(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	code, body := doJSON(t, ts, http.MethodGet, "/v1/messages/NOTEXIST", nil, token)
	if code != 404 {
		t.Errorf("get missing msg: got %d, want 404; body: %s", code, body)
	}
	var errResp proto.ErrorResponse
	json.Unmarshal(body, &errResp)
	if errResp.Error != "message_not_found" {
		t.Errorf("error=%q, want message_not_found", errResp.Error)
	}
}

func TestUnknownRoute(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	code, _ := doJSON(t, ts, http.MethodGet, "/v1/nonexistent", nil, token)
	if code != 404 {
		t.Errorf("unknown route: got %d, want 404", code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-ma-1", "agent")

	// POST to a DELETE-only route.
	code, _ := doJSON(t, ts, http.MethodPost, "/v1/agents/sid-ma-1?project=default", nil, token)
	if code != 405 {
		t.Errorf("method not allowed: got %d, want 405", code)
	}
}
