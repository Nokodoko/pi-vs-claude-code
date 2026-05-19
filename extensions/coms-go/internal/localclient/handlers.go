//go:build !windows

// Package localclient implements the client-local subcommand: the Go port of
// extensions/coms.ts. It accepts JSON-line IPC requests from the shim on stdin,
// dispatches them via Unix-socket transport, and returns results on stdout.
package localclient

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/pi-vs-cc/coms-go/internal/proto"
	"github.com/pi-vs-cc/coms-go/internal/transport"
	"github.com/pi-vs-cc/coms-go/internal/util"
)

// ─────────────────────────────────────────────────────────────────────────────
// Connection-level handlers (called once per accepted Unix-socket connection)
// ─────────────────────────────────────────────────────────────────────────────

// handleConn reads one envelope from the socket, dispatches to the appropriate
// handler, and writes back the ack/nack/pong. One envelope per connection
// (matches the TS "write line, read line, close" pattern).
func (c *Client) handleConn(conn net.Conn) {
	defer conn.Close()

	// Read exactly one JSON line (up to LineCap) from the connection.
	raw, err := transport.ReadOneLine(conn)
	if err != nil {
		writeNack(conn, "", "malformed envelope")
		return
	}

	// Dispatch based on envelope type.
	var base proto.Envelope
	if err := json.Unmarshal(raw, &base); err != nil || !isValidEnvelope(base) {
		var partial struct {
			MsgID string `json:"msg_id"`
		}
		_ = json.Unmarshal(raw, &partial)
		writeNack(conn, partial.MsgID, "malformed envelope")
		return
	}

	switch base.Type {
	case "prompt":
		var env proto.PromptEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			writeNack(conn, base.MsgID, "malformed prompt envelope")
			return
		}
		c.handlePrompt(conn, env)
	case "response":
		var env proto.ResponseEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			writeNack(conn, base.MsgID, "malformed response envelope")
			return
		}
		c.handleResponse(conn, env)
	case "ping":
		var env proto.PingEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			writeNack(conn, base.MsgID, "malformed ping envelope")
			return
		}
		c.handlePing(conn, env)
	default:
		writeNack(conn, base.MsgID, "unknown type")
	}
}

// handlePrompt processes an inbound prompt envelope.
// Mirrors handlePrompt() in coms.ts lines 604-661.
func (c *Client) handlePrompt(conn net.Conn, env proto.PromptEnvelope) {
	if env.Hops >= c.maxHops {
		writeNack(conn, env.MsgID, "hops exceeded")
		return
	}

	c.mu.Lock()
	c.inboundQueue[env.MsgID] = &inboundCtx{
		msgID:          env.MsgID,
		hops:           env.Hops,
		senderEndpoint: env.SenderEndpoint,
		senderSession:  env.SenderSession,
		responseSchema: env.ResponseSchema,
		fulfilled:      false,
	}
	c.currentInbound = c.inboundQueue[env.MsgID]
	c.mu.Unlock()

	// Audit log — event only, no prompt body.
	_ = c.audit.Append(map[string]any{
		"event":  "inbound_prompt",
		"msg_id": env.MsgID,
		"sender": env.SenderSession,
		"hops":   env.Hops,
		"ts":     util.NowIso(),
	})

	writeAck(conn, env.MsgID)
}

// handleResponse processes an inbound response envelope, resolving the pending
// reply promise that coms_await / coms_get are waiting on.
// Mirrors handleResponse() in coms.ts lines 663-685.
func (c *Client) handleResponse(conn net.Conn, env proto.ResponseEnvelope) {
	c.mu.Lock()
	pr, ok := c.pendingReplies[env.MsgID]
	c.mu.Unlock()

	if ok {
		pr.mu.Lock()
		if pr.result == nil {
			pr.result = &pendingResult{
				response: env.Response,
				errMsg:   nilStr(env.Error),
			}
			close(pr.ready)
		}
		pr.mu.Unlock()
	} else {
		_ = c.audit.Append(map[string]any{
			"event":  "orphan_response",
			"msg_id": env.MsgID,
			"ts":     util.NowIso(),
		})
	}

	writeAck(conn, env.MsgID)
}

// handlePing responds to a ping with the agent's current card.
// Mirrors handlePing() in coms.ts lines 687-706.
func (c *Client) handlePing(conn net.Conn, env proto.PingEnvelope) {
	c.mu.RLock()
	id := c.identity
	queueDepth := len(c.inboundQueue)
	c.mu.RUnlock()

	name := "unknown"
	purpose := ""
	model := "unknown"
	color := "#36F9F6"
	if id != nil {
		name = id.name
		purpose = id.purpose
		model = id.model
		color = id.color
	}

	card := proto.AgentCardLocal{
		Name:           name,
		Purpose:        purpose,
		Model:          model,
		Color:          color,
		ContextUsedPct: 0, // not available in Go binary (no pi ctx)
		QueueDepth:     queueDepth,
	}
	pong := proto.Pong{
		Type:      "pong",
		MsgID:     env.MsgID,
		AgentCard: card,
	}

	data, err := json.Marshal(pong)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = conn.Write(data)
}

// ─────────────────────────────────────────────────────────────────────────────
// Ack / nack helpers
// ─────────────────────────────────────────────────────────────────────────────

func writeAck(conn net.Conn, msgID string) {
	ack := proto.AckMessage{Type: "ack", MsgID: msgID}
	data, err := json.Marshal(ack)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = conn.Write(data)
}

func writeNack(conn net.Conn, msgID, errMsg string) {
	nack := proto.AckMessage{Type: "nack", MsgID: msgID, Error: errMsg}
	data, err := json.Marshal(nack)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = conn.Write(data)
}

// ─────────────────────────────────────────────────────────────────────────────
// Envelope validation helpers
// ─────────────────────────────────────────────────────────────────────────────

func isValidEnvelope(e proto.Envelope) bool {
	return e.Type != "" && e.MsgID != "" && e.SenderSession != "" && e.SenderEndpoint != ""
}

// nilStr dereferences a *string safely.
func nilStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ─────────────────────────────────────────────────────────────────────────────
// Agent-end handler (called by the lifecycle dispatcher when agent_end fires)
// ─────────────────────────────────────────────────────────────────────────────

// onAgentEnd captures the last assistant turn text (passed from shim via the
// lifecycle event) and dispatches a response envelope back to the waiting peer.
// Mirrors the agent_end handler in coms.ts lines 1477-1549.
func (c *Client) onAgentEnd(lastText string) {
	c.mu.Lock()
	var inbound *inboundCtx
	// Find the most-recent unfulfilled inbound, scanning in insertion order.
	for _, ic := range c.inboundQueue {
		if !ic.fulfilled {
			inbound = ic
		}
	}
	if inbound == nil {
		c.mu.Unlock()
		return
	}
	id := c.identity
	c.mu.Unlock()

	if id == nil {
		return
	}

	var payload json.RawMessage
	var errStr *string

	if len(inbound.responseSchema) > 0 {
		// Try to parse the assistant text as JSON.
		if json.Valid([]byte(lastText)) {
			payload = json.RawMessage(lastText)
		} else {
			s := "response not valid JSON"
			errStr = &s
		}
	} else {
		// Plain string response — marshal it as a JSON string.
		b, err := json.Marshal(lastText)
		if err == nil {
			payload = b
		}
	}

	env := proto.ResponseEnvelope{
		Envelope: proto.Envelope{
			Type:           "response",
			MsgID:          inbound.msgID,
			SenderSession:  id.sessionID,
			SenderEndpoint: id.endpoint,
			Hops:           0,
			Timestamp:      util.NowIso(),
		},
		Response: payload,
		Error:    errStr,
	}

	rawResp, err := transport.SendEnvelope(inbound.senderEndpoint, env)
	if err != nil {
		_ = c.audit.Append(map[string]any{
			"event":  "outbound_response_failed",
			"msg_id": inbound.msgID,
			"reason": fmt.Sprintf("%v", err),
			"ts":     util.NowIso(),
		})
	} else {
		// Log the ack type (ack vs nack) without the body.
		var ack proto.AckMessage
		_ = json.Unmarshal(rawResp, &ack)
		_ = c.audit.Append(map[string]any{
			"event":  "outbound_response",
			"msg_id": inbound.msgID,
			"ts":     util.NowIso(),
		})
	}

	c.mu.Lock()
	inbound.fulfilled = true
	delete(c.inboundQueue, inbound.msgID)
	if c.currentInbound != nil && c.currentInbound.msgID == inbound.msgID {
		c.currentInbound = nil
	}
	c.mu.Unlock()
}
