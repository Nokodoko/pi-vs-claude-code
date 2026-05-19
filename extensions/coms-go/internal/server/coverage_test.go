package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pi-vs-cc/coms-go/internal/proto"
	"github.com/pi-vs-cc/coms-go/internal/server"
)

// ─── T8 required: unknown route → 404 (not 200 empty) ────────────────────────

func TestUnknownRootRoute(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	// GET / — catch-all should now return 404 with not_found envelope.
	code, body := doJSON(t, ts, http.MethodGet, "/", nil, "")
	if code != 404 {
		t.Errorf("GET / got %d, want 404; body: %s", code, body)
	}
	var resp proto.ErrorResponse
	json.Unmarshal(body, &resp)
	if resp.Error != "not_found" {
		t.Errorf("error=%q, want not_found", resp.Error)
	}
}

func TestUnknownNonV1Route(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	code, body := doJSON(t, ts, http.MethodGet, "/foobar", nil, "")
	if code != 404 {
		t.Errorf("GET /foobar got %d, want 404; body: %s", code, body)
	}
	var resp proto.ErrorResponse
	json.Unmarshal(body, &resp)
	if resp.Error != "not_found" {
		t.Errorf("error=%q, want not_found", resp.Error)
	}
}

// ─── ParseConfig ──────────────────────────────────────────────────────────────

func TestParseConfig_defaults(t *testing.T) {
	cfg, err := server.ParseConfig([]string{})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Host != "127.0.0.1" {
		t.Errorf("Host = %q, want 127.0.0.1", cfg.Host)
	}
	if cfg.Port != 0 {
		t.Errorf("Port = %d, want 0", cfg.Port)
	}
	if cfg.MaxHops != 5 {
		t.Errorf("MaxHops = %d, want 5", cfg.MaxHops)
	}
}

func TestParseConfig_flags(t *testing.T) {
	cfg, err := server.ParseConfig([]string{"--host", "0.0.0.0", "--port", "9999", "--project", "myproj", "--no-color"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("Host = %q", cfg.Host)
	}
	if cfg.Port != 9999 {
		t.Errorf("Port = %d", cfg.Port)
	}
	if cfg.Project != "myproj" {
		t.Errorf("Project = %q", cfg.Project)
	}
	if !cfg.NoColor {
		t.Error("NoColor should be true")
	}
}

func TestParseConfig_unknownFlag(t *testing.T) {
	_, err := server.ParseConfig([]string{"--unknown-flag"})
	if err == nil {
		t.Error("unknown flag should return error")
	}
}

func TestParseConfig_portBadValue(t *testing.T) {
	_, err := server.ParseConfig([]string{"--port", "notanumber"})
	if err == nil {
		t.Error("bad port value should return error")
	}
}

func TestParseConfig_heartbeatMs(t *testing.T) {
	cfg, err := server.ParseConfig([]string{"--heartbeat-ms", "5000"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.HeartbeatMS != 5000 {
		t.Errorf("HeartbeatMS = %d, want 5000", cfg.HeartbeatMS)
	}
}

func TestParseConfig_heartbeatMs_badValue(t *testing.T) {
	_, err := server.ParseConfig([]string{"--heartbeat-ms", "notanumber"})
	if err == nil {
		t.Error("bad heartbeat-ms should return error")
	}
}

func TestParseConfig_secretPath(t *testing.T) {
	cfg, err := server.ParseConfig([]string{"--secret-path", "/tmp/secret.json"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.SecretPath != "/tmp/secret.json" {
		t.Errorf("SecretPath = %q", cfg.SecretPath)
	}
}

func TestParseConfig_publicURL(t *testing.T) {
	cfg, err := server.ParseConfig([]string{"--public-url", "https://example.com"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.PublicURL != "https://example.com" {
		t.Errorf("PublicURL = %q", cfg.PublicURL)
	}
}

// ─── Additional route coverage ────────────────────────────────────────────────

func TestSendMissingTarget(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-mt-1", "sender")

	code, body := doJSON(t, ts, http.MethodPost, "/v1/messages", proto.SendRequest{
		Project:       "default",
		SenderSession: "sid-mt-1",
		Target:        "   ", // blank after trim
		Prompt:        "hello",
	}, token)
	if code != 400 {
		t.Errorf("missing target: got %d body: %s", code, body)
	}
	var resp proto.ErrorResponse
	json.Unmarshal(body, &resp)
	if resp.Error != "missing_target" {
		t.Errorf("error=%q, want missing_target", resp.Error)
	}
}

func TestSendInboxFull(t *testing.T) {
	token2 := "test-token-inboxfull1234"
	cfg2 := &server.Config{
		Host:           "127.0.0.1",
		Port:           0,
		Project:        "default",
		Token:          token2,
		MaxHops:        5,
		MessageTTLMS:   1_800_000,
		MaxInbox:       1, // inbox cap = 1
		HeartbeatMS:    10_000,
		StaleAfterMS:   30_000,
		OfflineAfterMS: 60_000,
	}
	ts2 := httptest.NewServer(server.NewServeMux(cfg2))
	defer ts2.Close()

	registerAgent(t, ts2, token2, "default", "sid-ib-1", "sender")
	registerAgent(t, ts2, token2, "default", "sid-ib-2", "receiver")

	// First message — should succeed.
	doJSON(t, ts2, http.MethodPost, "/v1/messages", proto.SendRequest{
		Project:       "default",
		SenderSession: "sid-ib-1",
		Target:        "receiver",
		Prompt:        "first",
	}, token2)

	// Second message — should fail with inbox_full.
	code, body := doJSON(t, ts2, http.MethodPost, "/v1/messages", proto.SendRequest{
		Project:       "default",
		SenderSession: "sid-ib-1",
		Target:        "receiver",
		Prompt:        "second",
	}, token2)
	if code != 429 {
		t.Errorf("inbox full: got %d body: %s", code, body)
	}
	var resp proto.ErrorResponse
	json.Unmarshal(body, &resp)
	if resp.Error != "inbox_full" {
		t.Errorf("error=%q, want inbox_full", resp.Error)
	}
}

func TestAwaitMessageNotFound(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	code, body := doJSON(t, ts, http.MethodGet, "/v1/messages/GHOST/await", nil, token)
	if code != 404 {
		t.Errorf("await unknown msg: got %d body: %s", code, body)
	}
}

func TestSubmitResponseNotFound(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	code, body := doJSON(t, ts, http.MethodPost, "/v1/messages/GHOST/response",
		proto.ResponseSubmitRequest{
			Project:          "default",
			ResponderSession: "sid-x",
		}, token)
	if code != 404 {
		t.Errorf("submit unknown msg: got %d body: %s", code, body)
	}
}

func TestDeleteAgentUnknownProject(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	code, _ := doJSON(t, ts, http.MethodDelete, "/v1/agents/ghost?project=unknown", nil, token)
	if code != 404 {
		t.Errorf("delete unknown project: got %d, want 404", code)
	}
}

func TestHeartbeatUnknownProject(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	code, _ := doJSON(t, ts, http.MethodPost, "/v1/agents/ghost/heartbeat",
		proto.HeartbeatRequest{Project: "ghostproject"},
		token)
	if code != 404 {
		t.Errorf("heartbeat unknown project: got %d, want 404", code)
	}
}

func TestListAgentsIncludeExplicit(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	code, body := doJSON(t, ts, http.MethodPost, "/v1/agents/register", proto.RegisterRequest{
		Project:   "default",
		SessionID: "sid-exp-1",
		Name:      "explicit-agent",
		Purpose:   "explicit",
		Model:     "test",
		Color:     "#fff",
		Explicit:  true,
	}, token)
	if code != 200 {
		t.Fatalf("register explicit: %d %s", code, body)
	}

	// Without include_explicit — should be hidden.
	_, body = doJSON(t, ts, http.MethodGet, "/v1/agents?project=default", nil, token)
	var resp proto.ListAgentsResponse
	json.Unmarshal(body, &resp)
	for _, a := range resp.Agents {
		if a.SessionID == "sid-exp-1" {
			t.Error("explicit agent visible without include_explicit=true")
		}
	}

	// With include_explicit — should appear.
	_, body = doJSON(t, ts, http.MethodGet, "/v1/agents?project=default&include_explicit=true", nil, token)
	var resp2 proto.ListAgentsResponse
	json.Unmarshal(body, &resp2)
	found := false
	for _, a := range resp2.Agents {
		if a.SessionID == "sid-exp-1" {
			found = true
		}
	}
	if !found {
		t.Error("explicit agent not visible with include_explicit=true")
	}
}

func TestSubmitResponseError(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-err-1", "asker")
	registerAgent(t, ts, token, "default", "sid-err-2", "answerer")

	_, body := doJSON(t, ts, http.MethodPost, "/v1/messages", proto.SendRequest{
		Project:       "default",
		SenderSession: "sid-err-1",
		Target:        "answerer",
		Prompt:        "fail for me",
	}, token)
	var sendResp proto.SendResponse
	json.Unmarshal(body, &sendResp)

	errStr := "something went wrong"
	code, body := doJSON(t, ts, http.MethodPost,
		"/v1/messages/"+sendResp.MsgID+"/response",
		proto.ResponseSubmitRequest{
			Project:          "default",
			ResponderSession: "sid-err-2",
			Error:            &errStr,
		}, token)
	if code != 200 {
		t.Fatalf("submit error response: %d %s", code, body)
	}

	_, body = doJSON(t, ts, http.MethodGet, "/v1/messages/"+sendResp.MsgID, nil, token)
	var msgResp proto.MessageStatusResponse
	json.Unmarshal(body, &msgResp)
	if msgResp.Status != proto.MsgStatusError {
		t.Errorf("status=%q, want error", msgResp.Status)
	}
}

func TestRegisterReregister(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	a1 := registerAgent(t, ts, token, "default", "sid-rereg-1", "worker")
	a2 := registerAgent(t, ts, token, "default", "sid-rereg-1", "worker")
	if a2.StartedAt != a1.StartedAt {
		t.Errorf("re-register: started_at changed from %q to %q", a1.StartedAt, a2.StartedAt)
	}
}

func TestSendWithTargetSession(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-ts-1", "sender")
	registerAgent(t, ts, token, "default", "sid-ts-2", "receiver")

	targetSID := "sid-ts-2"
	code, body := doJSON(t, ts, http.MethodPost, "/v1/messages", proto.SendRequest{
		Project:       "default",
		SenderSession: "sid-ts-1",
		TargetSession: &targetSID,
		Prompt:        "direct session route",
	}, token)
	if code != 200 {
		t.Errorf("target_session route: got %d body: %s", code, body)
	}
}

func TestSendWithUnknownTargetSession(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-uts-1", "sender")

	ghost := "ghost-session"
	code, body := doJSON(t, ts, http.MethodPost, "/v1/messages", proto.SendRequest{
		Project:       "default",
		SenderSession: "sid-uts-1",
		TargetSession: &ghost,
		Prompt:        "nowhere",
	}, token)
	if code != 404 {
		t.Errorf("unknown target_session: got %d body: %s", code, body)
	}
}

func TestSendSenderNotRegistered(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-snr-2", "receiver")

	code, body := doJSON(t, ts, http.MethodPost, "/v1/messages", proto.SendRequest{
		Project:       "default",
		SenderSession: "ghost-sender",
		Target:        "receiver",
		Prompt:        "hello",
	}, token)
	if code != 404 {
		t.Errorf("sender not registered: got %d body: %s", code, body)
	}
}

func TestV1MethodNotAllowed_agentHeartbeat(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-mna-1", "agent")

	code, _ := doJSON(t, ts, http.MethodPut, "/v1/agents/sid-mna-1/heartbeat", nil, token)
	if code != 405 {
		t.Errorf("wrong method on heartbeat: got %d, want 405", code)
	}
}

func TestRegisterMissingSessionID(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	code, body := doJSON(t, ts, http.MethodPost, "/v1/agents/register", map[string]any{
		"project": "default",
		"name":    "agent",
	}, token)
	if code != 400 {
		t.Errorf("missing session_id: got %d body: %s", code, body)
	}
}

func TestSendNoProject(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	// No agents registered in "other-project" — project lookup returns nil → agent_not_found.
	code, body := doJSON(t, ts, http.MethodPost, "/v1/messages", proto.SendRequest{
		Project:       "other-project",
		SenderSession: "ghost",
		Target:        "nobody",
		Prompt:        "hello",
	}, token)
	if code != 404 {
		t.Errorf("no project: got %d body: %s", code, body)
	}
}

func TestHeartbeatInvalidJSON(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-hjson-1", "agent")

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/agents/sid-hjson-1/heartbeat",
		strings.NewReader("{bad json"))
	req.Header.Set("Authorization", authHeader(token))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("heartbeat bad json: got %d, want 400", resp.StatusCode)
	}
}

func TestSubmitResponseInvalidJSON(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/messages/FAKE/response",
		strings.NewReader("{bad json"))
	req.Header.Set("Authorization", authHeader(token))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("submit bad json: got %d, want 400", resp.StatusCode)
	}
}

func TestSubmitResponseMissingResponder(t *testing.T) {
	ts, token := newTestServer(t)
	defer ts.Close()

	registerAgent(t, ts, token, "default", "sid-mrp-1", "asker")
	registerAgent(t, ts, token, "default", "sid-mrp-2", "answerer")

	_, body := doJSON(t, ts, http.MethodPost, "/v1/messages", proto.SendRequest{
		Project:       "default",
		SenderSession: "sid-mrp-1",
		Target:        "answerer",
		Prompt:        "hello",
	}, token)
	var sendResp proto.SendResponse
	json.Unmarshal(body, &sendResp)

	code, body := doJSON(t, ts, http.MethodPost, "/v1/messages/"+sendResp.MsgID+"/response",
		proto.ResponseSubmitRequest{
			Project: "default",
			// missing responder_session
		}, token)
	if code != 400 {
		t.Errorf("missing responder: got %d body: %s", code, body)
	}
}
