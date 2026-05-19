package proto_test

import (
	"encoding/json"
	"testing"

	"github.com/pi-vs-cc/coms-go/internal/proto"
)

// TestEnvelopeJSONTags verifies that JSON field names match the wire protocol.
func TestEnvelopeJSONTags(t *testing.T) {
	env := proto.Envelope{
		Type:           "ping",
		MsgID:          "01TEST00000000000000000000",
		SenderSession:  "01SESS000000000000000000000",
		SenderEndpoint: "/tmp/test.sock",
		Hops:           1,
		Timestamp:      "2026-05-19T14:32:11.482Z",
	}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"type", "msg_id", "sender_session", "sender_endpoint", "hops", "timestamp"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON key %q", key)
		}
	}
}

// TestPromptEnvelopeRoundTrip verifies that optional fields use correct omitempty behavior.
func TestPromptEnvelopeRoundTrip(t *testing.T) {
	sid := "01CONV000000000000000000000"
	pe := proto.PromptEnvelope{
		Envelope: proto.Envelope{
			Type:           "prompt",
			MsgID:          "01MSG0000000000000000000000",
			SenderSession:  "01SESS000000000000000000000",
			SenderEndpoint: "/tmp/s.sock",
			Hops:           0,
			Timestamp:      "2026-05-19T00:00:00.000Z",
		},
		Prompt:         "hello",
		SenderName:     "planner",
		SenderCwd:      "/tmp",
		ConversationID: &sid,
	}
	data, err := json.Marshal(pe)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "prompt" {
		t.Errorf("type = %v, want prompt", m["type"])
	}
	if m["sender_name"] != "planner" {
		t.Errorf("sender_name = %v, want planner", m["sender_name"])
	}
	if m["conversation_id"] != sid {
		t.Errorf("conversation_id = %v, want %s", m["conversation_id"], sid)
	}
}

// TestAgentCardJSONTags verifies the network-mode AgentCard field names.
func TestAgentCardJSONTags(t *testing.T) {
	card := proto.AgentCard{
		SessionID:      "01HXNJ0E5Q4M7Z2C1V8YR6F3KT",
		Name:           "planner",
		Purpose:        "Plans the work",
		Model:          "claude-opus-4-7",
		Color:          "#36F9F6",
		Cwd:            "/home/n0ko",
		Project:        "default",
		Explicit:       false,
		StartedAt:      "2026-05-19T14:32:11.482Z",
		ContextUsedPct: 12,
		QueueDepth:     0,
		Status:         proto.StatusOnline,
	}
	data, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{
		"session_id": card.SessionID,
		"name":       card.Name,
		"purpose":    card.Purpose,
		"model":      card.Model,
		"color":      card.Color,
		"cwd":        card.Cwd,
		"project":    card.Project,
		"started_at": card.StartedAt,
		"status":     string(card.Status),
	}
	for k, v := range expected {
		got, ok := m[k]
		if !ok {
			t.Errorf("missing key %q", k)
			continue
		}
		if got != v {
			t.Errorf("key %q = %v, want %v", k, got, v)
		}
	}
	// context_used_pct and queue_depth should be numbers
	if _, ok := m["context_used_pct"]; !ok {
		t.Error("missing context_used_pct")
	}
	// Provider is omitempty — should be absent when empty
	if _, ok := m["provider"]; ok {
		t.Error("provider should be omitted when empty")
	}
}

// TestRegistryEntryOmitempty verifies optional fields are omitted when nil/zero.
func TestRegistryEntryOmitempty(t *testing.T) {
	entry := proto.RegistryEntry{
		SessionID: "01HXNJ0E5Q4M7Z2C1V8YR6F3KT",
		Name:      "coder",
		Purpose:   "Writes code",
		Model:     "claude-opus-4-7",
		Color:     "#72F1B8",
		Pid:       12345,
		Endpoint:  "/tmp/coder.sock",
		Cwd:       "/tmp",
		StartedAt: "2026-05-19T14:32:11.482Z",
		Explicit:  false,
		Version:   1,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	// Optional fields should be absent
	for _, key := range []string{"context_used_pct", "queue_depth", "heartbeat_at"} {
		if _, ok := m[key]; ok {
			t.Errorf("key %q should be omitted when zero/nil", key)
		}
	}
}

// TestComsMessageJSONTags verifies ComsMessage wire shape.
func TestComsMessageJSONTags(t *testing.T) {
	msg := proto.ComsMessage{
		MsgID:         "01MSG0000000000000000000000",
		Project:       "default",
		SenderSession: "01SESS000000000000000000000",
		TargetSession: "01TARG000000000000000000000",
		Prompt:        "do something",
		Hops:          0,
		Status:        proto.MsgStatusQueued,
		CreatedAt:     "2026-05-19T14:32:11.482Z",
		ExpiresAt:     "2026-05-19T15:32:11.482Z",
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"msg_id", "project", "sender_session", "target_session", "prompt", "hops", "status", "created_at", "expires_at"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON key %q", key)
		}
	}
}

// TestErrorResponseShape verifies the universal error envelope shape.
func TestErrorResponseShape(t *testing.T) {
	er := proto.ErrorResponse{
		Ok:      false,
		Error:   "target_not_found",
		Details: map[string]string{"target": "ghost"},
	}
	data, err := json.Marshal(er)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m["ok"] != false {
		t.Errorf("ok = %v, want false", m["ok"])
	}
	if m["error"] != "target_not_found" {
		t.Errorf("error = %v, want target_not_found", m["error"])
	}
}

// TestRegisterResponseShape verifies RegisterResponse matches the spec §6 example.
func TestRegisterResponseShape(t *testing.T) {
	resp := proto.RegisterResponse{
		Ok: true,
		Agent: proto.AgentCard{
			SessionID:      "01HXNJ0E5Q4M7Z2C1V8YR6F3KT",
			Name:           "planner",
			Purpose:        "Plans the work",
			Model:          "claude-opus-4-7",
			Color:          "#36F9F6",
			Cwd:            "/home/n0ko",
			Project:        "default",
			Explicit:       false,
			StartedAt:      "2026-05-19T14:32:11.482Z",
			ContextUsedPct: 0,
			QueueDepth:     0,
			Status:         proto.StatusOnline,
		},
		HeartbeatIntervalMs: 10000,
		SseURL:              "/v1/events?project=default&session_id=01HXNJ0E5Q4M7Z2C1V8YR6F3KT",
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m["ok"] != true {
		t.Errorf("ok = %v, want true", m["ok"])
	}
	if _, ok := m["agent"]; !ok {
		t.Error("missing agent field")
	}
	if m["heartbeat_interval_ms"] != float64(10000) {
		t.Errorf("heartbeat_interval_ms = %v, want 10000", m["heartbeat_interval_ms"])
	}
	if m["sse_url"] != resp.SseURL {
		t.Errorf("sse_url = %v, want %s", m["sse_url"], resp.SseURL)
	}
}
