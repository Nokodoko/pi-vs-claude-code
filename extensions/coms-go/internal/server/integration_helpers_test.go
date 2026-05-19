//go:build integration

package server_test

// integration_helpers.go — shared helpers for integration and pi-to-pi tests.
// Only compiled when -tags=integration is set.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pi-vs-cc/coms-go/internal/proto"
	"github.com/pi-vs-cc/coms-go/internal/server"
)

// ─── Test server setup ───────────────────────────────────────────────────────

const integrationToken = "integration-test-token-deadbeef1234"

// newIntegrationServer returns an httptest.Server with the standard integration
// config and a known bearer token. Caller must call ts.Close().
func newIntegrationServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	cfg := &server.Config{
		Host:           "127.0.0.1",
		Port:           0,
		Project:        "default",
		Token:          integrationToken,
		MaxHops:        5,
		MessageTTLMS:   1_800_000,
		MaxInbox:       100,
		HeartbeatMS:    10_000,
		StaleAfterMS:   30_000,
		OfflineAfterMS: 60_000,
	}
	ts := httptest.NewServer(server.NewServeMux(cfg))
	return ts, integrationToken
}

// ─── HTTP helpers ────────────────────────────────────────────────────────────

// doIntegrationJSON sends method+path+body and returns status + raw body.
func doIntegrationJSON(t *testing.T, ts *httptest.Server, method, path string, body any, token string) (int, []byte) {
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
		req.Header.Set("Authorization", "Bearer "+token)
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

// integrationRegister registers a test agent and returns the RegisterResponse.
func integrationRegister(t *testing.T, ts *httptest.Server, token, project, sessionID, name string) proto.RegisterResponse {
	t.Helper()
	code, body := doIntegrationJSON(t, ts, http.MethodPost, "/v1/agents/register", proto.RegisterRequest{
		Project:   project,
		SessionID: sessionID,
		Name:      name,
		Purpose:   "integration test agent",
		Model:     "test-model",
		Color:     "#aabbcc",
		Cwd:       "/tmp",
		Explicit:  false,
	}, token)
	if code != 200 {
		t.Fatalf("register %s: got %d, body: %s", name, code, body)
	}
	var resp proto.RegisterResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	return resp
}

// ─── JSON canonicalization ───────────────────────────────────────────────────

// ulidRe matches 26-char Crockford Base32 ULIDs.
var ulidRe = regexp.MustCompile(`"[0-9A-HJKMNP-TV-Z]{26}"`)

// isoRe matches RFC3339 / ISO8601 timestamps.
var isoRe = regexp.MustCompile(`"[0-9]{4}-[0-9]{2}-[0-9]{2}T[^"]*"`)

// sseURLRe matches the /v1/events?… SSE URL path.
var sseURLRe = regexp.MustCompile(`"/v1/events[^"]*"`)

// canonicalize replaces dynamic fields with deterministic placeholders.
// Must be applied to both fixture and live response before diffing.
func canonicalize(b []byte) []byte {
	b = ulidRe.ReplaceAll(b, []byte(`"<ulid>"`))
	b = isoRe.ReplaceAll(b, []byte(`"<iso>"`))
	b = sseURLRe.ReplaceAll(b, []byte(`"<sse_url>"`))
	return b
}

// marshalNoEscape encodes v to JSON without HTML-escaping angle brackets.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Encoder appends a newline; strip it.
	b := buf.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}

// placeholderValues are the recognized placeholder strings in fixture files.
// They are matched AFTER normalization (no HTML escaping).
var placeholderValues = []string{
	`"<ulid>"`, `"<iso>"`, `"<bearer>"`, `"<sse_url>"`, `"<response>"`, `"<string>"`,
}

// isPlaceholder returns true if a JSON-encoded leaf value is a known placeholder.
func isPlaceholder(jsonVal string) bool {
	for _, p := range placeholderValues {
		if jsonVal == p {
			return true
		}
	}
	return false
}

// assertJSONShape verifies that got contains at least the same keys/types as
// the fixture (after canonicalization). Rules:
//   - Fixture keys must be present in response.
//   - If the fixture leaf value is a placeholder, the response value must be
//     non-null and non-empty (shape check, any value ok).
//   - If the fixture leaf value is a nested object, recursively check keys.
//   - If the fixture leaf value is a non-placeholder scalar, the response must
//     match after canonicalization.
func assertJSONShape(t *testing.T, fixture, got []byte) {
	t.Helper()
	var fixVal any
	var gotVal any
	if err := json.Unmarshal(fixture, &fixVal); err != nil {
		t.Fatalf("fixture JSON parse: %v", err)
	}
	if err := json.Unmarshal(got, &gotVal); err != nil {
		t.Fatalf("response JSON parse: %v", err)
	}
	checkShape(t, "", fixVal, gotVal)
}

// checkShape recursively verifies that gotVal satisfies the shape contract of fixVal.
func checkShape(t *testing.T, path string, fixVal, gotVal any) {
	t.Helper()

	// If fixture value is a string, check for placeholder or compare directly.
	if fixStr, ok := fixVal.(string); ok {
		if isPlaceholder(`"` + fixStr + `"`) {
			// Any non-null, non-empty value satisfies a placeholder.
			if gotVal == nil {
				t.Errorf("%s: placeholder %q but response is null", path, fixStr)
			}
			if gotStr, ok := gotVal.(string); ok && gotStr == "" {
				t.Errorf("%s: placeholder %q but response is empty string", path, fixStr)
			}
			return
		}
		// Non-placeholder string: compare directly.
		gotStr, ok := gotVal.(string)
		if !ok {
			t.Errorf("%s: type mismatch: fixture is string %q, response is %T", path, fixStr, gotVal)
			return
		}
		// Canonicalize both before comparing.
		fixNorm := string(canonicalize([]byte(`"` + fixStr + `"`)))
		gotNorm := string(canonicalize([]byte(`"` + gotStr + `"`)))
		if fixNorm != gotNorm {
			t.Errorf("%s: string mismatch: fixture=%q, response=%q", path, fixStr, gotStr)
		}
		return
	}

	// Nested object: recurse.
	if fixMap, ok := fixVal.(map[string]any); ok {
		gotMap, ok := gotVal.(map[string]any)
		if !ok {
			t.Errorf("%s: type mismatch: fixture is object, response is %T", path, gotVal)
			return
		}
		for k, fv := range fixMap {
			gv, ok := gotMap[k]
			if !ok {
				t.Errorf("%s.%s: key missing in response", path, k)
				continue
			}
			keyPath := k
			if path != "" {
				keyPath = path + "." + k
			}
			checkShape(t, keyPath, fv, gv)
		}
		return
	}

	// Array: check length and element shapes.
	if fixArr, ok := fixVal.([]any); ok {
		gotArr, ok := gotVal.([]any)
		if !ok {
			t.Errorf("%s: type mismatch: fixture is array, response is %T", path, gotVal)
			return
		}
		if len(fixArr) > 0 && len(gotArr) == 0 {
			t.Errorf("%s: fixture has %d elements but response is empty", path, len(fixArr))
			return
		}
		// Check first element shape only (array is representative).
		if len(fixArr) > 0 && len(gotArr) > 0 {
			checkShape(t, path+"[0]", fixArr[0], gotArr[0])
		}
		return
	}

	// Scalar (number, bool, null): exact match.
	fixNorm, _ := marshalNoEscape(fixVal)
	gotNorm, _ := marshalNoEscape(gotVal)
	if string(fixNorm) != string(gotNorm) {
		t.Errorf("%s: value mismatch: fixture=%s, response=%s", path, fixNorm, gotNorm)
	}
}

// ─── SSE reader ──────────────────────────────────────────────────────────────

// SSEEvent holds a parsed SSE frame (event name + data).
type integrationSSEEvent struct {
	Event string
	Data  string
}

// openSSEStream opens an SSE stream to the given URL with the given token.
// Returns a channel of parsed events and a cancel func. Call cancel to close
// the stream; the channel will be closed by the reader goroutine after cancel.
func openSSEStream(t *testing.T, url, token string) (<-chan integrationSSEEvent, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		cancel()
		t.Fatalf("sse request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")

	// SSE connections are long-lived; no client-level timeout.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		cancel()
		t.Fatalf("sse connect: %v", err)
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		cancel()
		t.Fatalf("sse status: %d", resp.StatusCode)
	}

	ch := make(chan integrationSSEEvent, 32)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		sc := bufio.NewScanner(resp.Body)
		var ev integrationSSEEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.Event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.Data = strings.TrimPrefix(line, "data: ")
			case line == "" && ev.Event != "":
				ch <- ev
				ev = integrationSSEEvent{}
			}
		}
	}()

	return ch, cancel
}

// waitForSSEEvent reads from ch until it finds an event with the given name,
// or until timeout. Returns the matching event's data or "" on timeout.
func waitForSSEEvent(t *testing.T, ch <-chan integrationSSEEvent, name string, timeout time.Duration) (string, bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return "", false
			}
			if ev.Event == name {
				return ev.Data, true
			}
		case <-deadline:
			return "", false
		}
	}
}

// ─── Response shapes (partial) used in assertions ────────────────────────────

type sendResponseShape struct {
	Ok            bool   `json:"ok"`
	MsgID         string `json:"msg_id"`
	Status        string `json:"status"`
	TargetSession string `json:"target_session"`
}

type msgStatusShape struct {
	MsgID    string          `json:"msg_id"`
	Status   string          `json:"status"`
	Response json.RawMessage `json:"response,omitempty"`
	Error    *string         `json:"error"`
}

type errorShape struct {
	Ok    bool   `json:"ok"`
	Error string `json:"error"`
}
