// Package proto contains all wire types for coms-go: Unix-socket envelopes,
// AgentCard, ComsMessage, and HTTP request/response shapes. JSON tags are
// byte-identical to the TypeScript field names in coms.ts / coms-net.ts /
// coms-net-server.ts so that TS and Go peers interoperate without translation.
package proto

import "encoding/json"

// Version and Commit are set at build time.
const Version = "1.0.0"

// Commit is overridden at build time via -ldflags.
var Commit = "dev"

// ─────────────────────────────────────────────────────────────────────────────
// Unix-socket envelope types (coms.ts lines 44-86)
// ─────────────────────────────────────────────────────────────────────────────

// Envelope is the base wire type for all Unix-socket frames.
type Envelope struct {
	Type           string `json:"type"`            // "prompt" | "response" | "ping"
	MsgID          string `json:"msg_id"`
	SenderSession  string `json:"sender_session"`
	SenderEndpoint string `json:"sender_endpoint"`
	Hops           int    `json:"hops"`
	Timestamp      string `json:"timestamp"`
}

// PromptEnvelope carries a prompt from one local agent to another.
type PromptEnvelope struct {
	Envelope
	Prompt         string          `json:"prompt"`
	SenderName     string          `json:"sender_name"`
	SenderCwd      string          `json:"sender_cwd"`
	ConversationID *string         `json:"conversation_id,omitempty"`
	ResponseSchema json.RawMessage `json:"response_schema,omitempty"`
}

// ResponseEnvelope carries a response back to the sender.
type ResponseEnvelope struct {
	Envelope
	Response json.RawMessage `json:"response"`
	Error    *string         `json:"error,omitempty"`
}

// PingEnvelope is a keepalive probe; the peer replies with a Pong.
type PingEnvelope struct {
	Envelope
}

// AckMessage is the immediate ack/nack reply to a prompt or response envelope.
type AckMessage struct {
	Type  string `json:"type"`            // "ack" | "nack"
	MsgID string `json:"msg_id"`
	Error string `json:"error,omitempty"` // populated only on nack
}

// Pong is the reply to a PingEnvelope. It carries the local agent's card.
type Pong struct {
	Type      string        `json:"type"`       // "pong"
	MsgID     string        `json:"msg_id"`
	AgentCard AgentCardLocal `json:"agent_card"`
}

// ─────────────────────────────────────────────────────────────────────────────
// AgentCard — two variants, one per mode (spec §4)
// ─────────────────────────────────────────────────────────────────────────────

// AgentCardLocal is the card shape used in local-mode (coms.ts lines 73-81).
type AgentCardLocal struct {
	Name           string `json:"name"`
	Purpose        string `json:"purpose"`
	Model          string `json:"model"`
	Color          string `json:"color"`
	ContextUsedPct int    `json:"context_used_pct"`
	QueueDepth     int    `json:"queue_depth"`
}

// AgentStatus describes the heartbeat liveness of a network-mode agent.
type AgentStatus string

const (
	StatusOnline  AgentStatus = "online"
	StatusStale   AgentStatus = "stale"
	StatusOffline AgentStatus = "offline"
)

// AgentCard is the network-mode card (coms-net.ts lines 60-74 / server lines 139-153).
type AgentCard struct {
	SessionID      string      `json:"session_id"`
	Name           string      `json:"name"`
	Purpose        string      `json:"purpose"`
	Model          string      `json:"model"`
	Provider       string      `json:"provider,omitempty"`
	Color          string      `json:"color"`
	Cwd            string      `json:"cwd"`
	Project        string      `json:"project"`
	Explicit       bool        `json:"explicit"`
	StartedAt      string      `json:"started_at"`
	ContextUsedPct int         `json:"context_used_pct"`
	QueueDepth     int         `json:"queue_depth"`
	Status         AgentStatus `json:"status"`
}

// ─────────────────────────────────────────────────────────────────────────────
// RegistryEntry — local-mode on-disk format (coms.ts lines 88-105)
// ─────────────────────────────────────────────────────────────────────────────

// RegistryEntry is written to ~/.pi/coms/projects/<project>/agents/<name>.json.
type RegistryEntry struct {
	SessionID      string `json:"session_id"`
	Name           string `json:"name"`
	Purpose        string `json:"purpose"`
	Model          string `json:"model"`
	Color          string `json:"color"`
	Pid            int    `json:"pid"`
	Endpoint       string `json:"endpoint"`
	Cwd            string `json:"cwd"`
	StartedAt      string `json:"started_at"`
	Explicit       bool   `json:"explicit"`
	Version        int    `json:"version"`
	ContextUsedPct *int   `json:"context_used_pct,omitempty"`
	QueueDepth     *int   `json:"queue_depth,omitempty"`
	HeartbeatAt    string `json:"heartbeat_at,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Server-side RegistryEntry — network mode (coms-net-server.ts lines 155-158)
// Extends AgentCard with housekeeping timestamps used only internally.
// ─────────────────────────────────────────────────────────────────────────────

// NetRegistryEntry is AgentCard extended with server-side housekeeping timestamps.
type NetRegistryEntry struct {
	AgentCard
	LastSeenAt   string `json:"last_seen_at"`
	RegisteredAt string `json:"registered_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// ComsMessage — server-side in-flight message (coms-net-server.ts lines 160-176)
// ─────────────────────────────────────────────────────────────────────────────

// MessageStatus describes the lifecycle state of an in-flight message.
type MessageStatus string

const (
	MsgStatusQueued    MessageStatus = "queued"
	MsgStatusDelivered MessageStatus = "delivered"
	MsgStatusComplete  MessageStatus = "complete"
	MsgStatusError     MessageStatus = "error"
	MsgStatusTimeout   MessageStatus = "timeout"
)

// ComsMessage is the server-side representation of a queued/delivered/completed message.
type ComsMessage struct {
	MsgID          string          `json:"msg_id"`
	Project        string          `json:"project"`
	SenderSession  string          `json:"sender_session"`
	TargetSession  string          `json:"target_session"`
	Prompt         string          `json:"prompt"`
	ConversationID *string         `json:"conversation_id"`
	ResponseSchema json.RawMessage `json:"response_schema"`
	Hops           int             `json:"hops"`
	Status         MessageStatus   `json:"status"`
	Response       json.RawMessage `json:"response,omitempty"`
	Error          *string         `json:"error,omitempty"`
	CreatedAt      string          `json:"created_at"`
	DeliveredAt    string          `json:"delivered_at,omitempty"`
	CompletedAt    string          `json:"completed_at,omitempty"`
	ExpiresAt      string          `json:"expires_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP request / response shapes (coms-net-server.ts lines 178-231)
// ─────────────────────────────────────────────────────────────────────────────

// RegisterRequest is the body of POST /v1/agents/register.
type RegisterRequest struct {
	Project   string `json:"project"`
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	Purpose   string `json:"purpose"`
	Model     string `json:"model"`
	Provider  string `json:"provider,omitempty"`
	Color     string `json:"color"`
	Cwd       string `json:"cwd"`
	Explicit  bool   `json:"explicit"`
}

// RegisterResponse is the success body of POST /v1/agents/register.
type RegisterResponse struct {
	Ok                bool      `json:"ok"`
	Agent             AgentCard `json:"agent"`
	HeartbeatIntervalMs int     `json:"heartbeat_interval_ms"`
	SseURL            string    `json:"sse_url"`
}

// HeartbeatRequest is the body of POST /v1/agents/:sid/heartbeat.
type HeartbeatRequest struct {
	Project        string      `json:"project"`
	ContextUsedPct int         `json:"context_used_pct"`
	QueueDepth     int         `json:"queue_depth"`
	Model          string      `json:"model,omitempty"`
	Status         AgentStatus `json:"status,omitempty"`
}

// SendRequest is the body of POST /v1/messages.
type SendRequest struct {
	Project        string          `json:"project"`
	SenderSession  string          `json:"sender_session"`
	Target         string          `json:"target"`
	TargetSession  *string         `json:"target_session"`
	Prompt         string          `json:"prompt"`
	ConversationID *string         `json:"conversation_id"`
	ResponseSchema json.RawMessage `json:"response_schema"`
	Hops           int             `json:"hops"`
}

// SendResponse is the success body of POST /v1/messages.
type SendResponse struct {
	Ok            bool          `json:"ok"`
	MsgID         string        `json:"msg_id"`
	Status        MessageStatus `json:"status"`
	TargetSession string        `json:"target_session"`
}

// ResponseSubmitRequest is the body of POST /v1/messages/:id/response.
type ResponseSubmitRequest struct {
	Project          string          `json:"project"`
	ResponderSession string          `json:"responder_session"`
	Response         json.RawMessage `json:"response"`
	Error            *string         `json:"error"`
}

// ErrorResponse is the universal error envelope (spec §6, coms-net-server.ts safeError).
type ErrorResponse struct {
	Ok      bool   `json:"ok"`
	Error   string `json:"error"`
	Details any    `json:"details,omitempty"`
}

// HealthResponse is the body of GET /health.
type HealthResponse struct {
	Ok        bool   `json:"ok"`
	Version   int    `json:"version"`
	ServerID  string `json:"server_id"`
	StartedAt string `json:"started_at"`
}

// MessageStatusResponse is the body of GET /v1/messages/:id and GET /v1/messages/:id/await.
type MessageStatusResponse struct {
	MsgID    string          `json:"msg_id"`
	Status   MessageStatus   `json:"status"`
	Response json.RawMessage `json:"response,omitempty"`
	Error    *string         `json:"error"`
}

// ListAgentsResponse is the body of GET /v1/agents.
type ListAgentsResponse struct {
	Agents []AgentCard `json:"agents"`
}

// OkResponse is a generic {ok: true} success body.
type OkResponse struct {
	Ok bool `json:"ok"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Server on-disk files
// ─────────────────────────────────────────────────────────────────────────────

// ServerJson is written to ~/.pi/coms-net/projects/<project>/server.json at boot.
type ServerJson struct {
	Version   int    `json:"version"`
	Project   string `json:"project"`
	Pid       int    `json:"pid"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	LocalURL  string `json:"local_url"`
	PublicURL string `json:"public_url"`
	StartedAt string `json:"started_at"`
	ServerID  string `json:"server_id"`
}

// ServerSecretJson is written to ~/.pi/coms-net/projects/<project>/server.secret.json (mode 0600).
type ServerSecretJson struct {
	Token string `json:"token"`
}

// ─────────────────────────────────────────────────────────────────────────────
// IPC — JSON-line stdio protocol between shim.ts and coms-go subcommands
// ─────────────────────────────────────────────────────────────────────────────

// IPCFrame is a single line in the shim ↔ coms-go JSON-line protocol.
type IPCFrame struct {
	Kind string `json:"kind"` // "tool_request" | "tool_response" | "event" | "shutdown"
	// tool_request fields
	ID     string          `json:"id,omitempty"`
	Tool   string          `json:"tool,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	// tool_response fields
	Ok      *bool           `json:"ok,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
	Details json.RawMessage `json:"details,omitempty"`
	// event fields
	Name string          `json:"name,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
	// error field (on nack tool_response)
	Error string `json:"error,omitempty"`
}
