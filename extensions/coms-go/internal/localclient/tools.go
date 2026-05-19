//go:build !windows

package localclient

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/pi-vs-cc/coms-go/internal/ipc"
	"github.com/pi-vs-cc/coms-go/internal/proto"
	"github.com/pi-vs-cc/coms-go/internal/registry"
	"github.com/pi-vs-cc/coms-go/internal/transport"
	"github.com/pi-vs-cc/coms-go/internal/util"
)

// ─────────────────────────────────────────────────────────────────────────────
// IPC tool handler dispatch
// ─────────────────────────────────────────────────────────────────────────────

// dispatchTool routes an IPC tool_request to the appropriate handler, writes
// a tool_response or tool_error back via w, and returns.
func (c *Client) dispatchTool(req ipc.Request, w *ipc.Writer) {
	switch req.Tool {
	case "coms_list":
		c.toolList(req, w)
	case "coms_send":
		c.toolSend(req, w)
	case "coms_get":
		c.toolGet(req, w)
	case "coms_await":
		c.toolAwait(req, w)
	default:
		_ = w.RespondError(req.ID, fmt.Sprintf("coms: unknown tool %q", req.Tool))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// coms_list
// ─────────────────────────────────────────────────────────────────────────────

type listParams struct {
	Project        string `json:"project"`
	IncludeExplicit bool  `json:"include_explicit"`
}

func (c *Client) toolList(req ipc.Request, w *ipc.Writer) {
	var p listParams
	if len(req.Params) > 0 {
		_ = json.Unmarshal(req.Params, &p)
	}
	if p.Project == "" {
		c.mu.RLock()
		if c.identity != nil {
			p.Project = c.identity.project
		}
		c.mu.RUnlock()
	}
	if p.Project == "" {
		p.Project = "default"
	}

	projects := []string{p.Project}
	if p.Project == "*" {
		projects = listProjects(c.comsDir)
	}

	type agentRow struct {
		Name           string `json:"name"`
		SessionID      string `json:"session_id"`
		Purpose        string `json:"purpose"`
		Model          string `json:"model"`
		Cwd            string `json:"cwd"`
		Project        string `json:"project"`
		Alive          bool   `json:"alive"`
		ContextUsedPct *int   `json:"context_used_pct"`
		Color          string `json:"color"`
	}

	c.mu.RLock()
	selfSession := ""
	if c.identity != nil {
		selfSession = c.identity.sessionID
	}
	c.mu.RUnlock()

	var agents []agentRow
	for _, proj := range projects {
		live, _ := registry.Prune(proj)
		for _, e := range live {
			if e.SessionID == selfSession {
				continue
			}
			if e.Explicit && !p.IncludeExplicit {
				continue
			}
			// Ping for live context usage.
			pingEnv := buildPingEnvelope(c)
			rawResp, err := transport.SendEnvelope(e.Endpoint, pingEnv)
			alive := false
			var ctxPct *int
			if err == nil {
				var pong proto.Pong
				if json.Unmarshal(rawResp, &pong) == nil && pong.Type == "pong" {
					alive = true
					v := pong.AgentCard.ContextUsedPct
					ctxPct = &v
				}
			}
			agents = append(agents, agentRow{
				Name:           e.Name,
				SessionID:      e.SessionID,
				Purpose:        e.Purpose,
				Model:          e.Model,
				Cwd:            e.Cwd,
				Project:        proj,
				Alive:          alive,
				ContextUsedPct: ctxPct,
				Color:          e.Color,
			})
		}
	}

	lines := "No peer agents found."
	if len(agents) > 0 {
		out := make([]string, 0, len(agents))
		for _, a := range agents {
			dot := "✗"
			if a.Alive {
				dot = "●"
			}
			pctStr := " ?%"
			if a.ContextUsedPct != nil {
				pctStr = fmt.Sprintf(" %d%%", *a.ContextUsedPct)
			}
			purposeStr := ""
			if a.Purpose != "" {
				purposeStr = " — " + a.Purpose
			}
			out = append(out, fmt.Sprintf("%s %s (%s)%s%s", dot, a.Name, a.Model, pctStr, purposeStr))
		}
		lines = joinLines(out)
	}

	text := fmt.Sprintf("%d peer(s):\n%s", len(agents), lines)
	details := map[string]any{"agents": agents, "project": p.Project}
	_ = w.Respond(req.ID, []ipc.ContentItem{{Type: "text", Text: text}}, details)
}

// ─────────────────────────────────────────────────────────────────────────────
// coms_send
// ─────────────────────────────────────────────────────────────────────────────

type sendParams struct {
	Target         string          `json:"target"`
	Prompt         string          `json:"prompt"`
	ConversationID *string         `json:"conversation_id"`
	ResponseSchema json.RawMessage `json:"response_schema"`
}

func (c *Client) toolSend(req ipc.Request, w *ipc.Writer) {
	var p sendParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.Target == "" || p.Prompt == "" {
		_ = w.RespondError(req.ID, "coms_send: target and prompt are required")
		return
	}

	c.mu.RLock()
	id := c.identity
	currentInbound := c.currentInbound
	c.mu.RUnlock()

	if id == nil {
		_ = w.RespondError(req.ID, "coms: not initialised")
		return
	}

	target := c.resolveTarget(p.Target)
	if target == nil {
		_ = w.RespondError(req.ID, fmt.Sprintf("coms: no live agent matching %q", p.Target))
		return
	}

	hops := 0
	if currentInbound != nil {
		hops = currentInbound.hops + 1
	}
	if hops >= c.maxHops {
		_ = w.RespondError(req.ID, fmt.Sprintf("coms: hop limit reached (%d >= %d)", hops, c.maxHops))
		return
	}

	msgID := util.NewULID()
	env := proto.PromptEnvelope{
		Envelope: proto.Envelope{
			Type:           "prompt",
			MsgID:          msgID,
			SenderSession:  id.sessionID,
			SenderEndpoint: id.endpoint,
			Hops:           hops,
			Timestamp:      util.NowIso(),
		},
		Prompt:         p.Prompt,
		SenderName:     id.name,
		SenderCwd:      id.cwd,
		ConversationID: p.ConversationID,
		ResponseSchema: p.ResponseSchema,
	}

	if _, err := transport.SendEnvelope(target.Endpoint, env); err != nil {
		_ = w.RespondError(req.ID, fmt.Sprintf("coms: send failed: %v", err))
		return
	}

	// Register pending reply.
	pr := &pendingReply{
		ready:      make(chan struct{}),
		targetName: target.Name,
		createdAt:  util.NowIso(),
	}
	c.mu.Lock()
	c.pendingReplies[msgID] = pr
	c.mu.Unlock()

	// Start timeout timer.
	go func() {
		select {
		case <-time.After(time.Duration(c.timeoutMs) * time.Millisecond):
			pr.mu.Lock()
			if pr.result == nil {
				s := "timeout"
				pr.result = &pendingResult{errMsg: s}
				close(pr.ready)
			}
			pr.mu.Unlock()
		case <-pr.ready:
		}
	}()

	_ = c.audit.Append(map[string]any{
		"event":  "outbound_prompt",
		"msg_id": msgID,
		"target": target.Name,
		"hops":   hops,
		"ts":     util.NowIso(),
	})

	text := fmt.Sprintf("coms_send → %s\nmsg_id %s\nhops %d", target.Name, msgID, hops)
	details := map[string]any{
		"msg_id":         msgID,
		"target":         target.Name,
		"target_session": target.SessionID,
		"hops":           hops,
	}
	_ = w.Respond(req.ID, []ipc.ContentItem{{Type: "text", Text: text}}, details)
}

// ─────────────────────────────────────────────────────────────────────────────
// coms_get
// ─────────────────────────────────────────────────────────────────────────────

type getParams struct {
	MsgID string `json:"msg_id"`
}

func (c *Client) toolGet(req ipc.Request, w *ipc.Writer) {
	var p getParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.MsgID == "" {
		_ = w.RespondError(req.ID, "coms_get: msg_id is required")
		return
	}

	c.mu.RLock()
	pr, ok := c.pendingReplies[p.MsgID]
	c.mu.RUnlock()

	if !ok {
		_ = w.Respond(req.ID,
			[]ipc.ContentItem{{Type: "text", Text: fmt.Sprintf("coms_get: unknown msg_id %s", p.MsgID)}},
			map[string]any{"status": "error", "error": "unknown msg_id"})
		return
	}

	pr.mu.Lock()
	result := pr.result
	pr.mu.Unlock()

	if result == nil {
		_ = w.Respond(req.ID,
			[]ipc.ContentItem{{Type: "text", Text: "coms_get: pending"}},
			map[string]any{"status": "pending"})
		return
	}

	if result.errMsg != "" {
		_ = w.Respond(req.ID,
			[]ipc.ContentItem{{Type: "text", Text: fmt.Sprintf("coms_get: error — %s", result.errMsg)}},
			map[string]any{"status": "complete", "response": nil, "error": result.errMsg})
		return
	}

	respText := jsonToString(result.response)
	_ = w.Respond(req.ID,
		[]ipc.ContentItem{{Type: "text", Text: fmt.Sprintf("coms_get: complete\n%s", respText)}},
		map[string]any{"status": "complete", "response": result.response, "error": nil})
}

// ─────────────────────────────────────────────────────────────────────────────
// coms_await
// ─────────────────────────────────────────────────────────────────────────────

type awaitParams struct {
	MsgID     string `json:"msg_id"`
	TimeoutMs *int   `json:"timeout_ms"`
}

func (c *Client) toolAwait(req ipc.Request, w *ipc.Writer) {
	var p awaitParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.MsgID == "" {
		_ = w.RespondError(req.ID, "coms_await: msg_id is required")
		return
	}

	c.mu.RLock()
	pr, ok := c.pendingReplies[p.MsgID]
	c.mu.RUnlock()

	if !ok {
		_ = w.Respond(req.ID,
			[]ipc.ContentItem{{Type: "text", Text: fmt.Sprintf("coms_await: unknown msg_id %s", p.MsgID)}},
			map[string]any{"error": "unknown msg_id"})
		return
	}

	// Check if already resolved.
	pr.mu.Lock()
	if pr.result != nil {
		result := pr.result
		pr.mu.Unlock()
		deliverAwaitResult(req.ID, result, w)
		return
	}
	pr.mu.Unlock()

	// Determine timeout.
	timeoutMs := c.timeoutMs
	if p.TimeoutMs != nil && *p.TimeoutMs > 0 {
		timeoutMs = *p.TimeoutMs
	}

	// Block until ready or timeout.
	timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer timer.Stop()

	select {
	case <-pr.ready:
		pr.mu.Lock()
		result := pr.result
		pr.mu.Unlock()
		deliverAwaitResult(req.ID, result, w)
	case <-timer.C:
		_ = w.Respond(req.ID,
			[]ipc.ContentItem{{Type: "text", Text: "coms_await: error — timeout"}},
			map[string]any{"error": "timeout"})
	}
}

func deliverAwaitResult(id string, result *pendingResult, w *ipc.Writer) {
	if result.errMsg != "" {
		_ = w.Respond(id,
			[]ipc.ContentItem{{Type: "text", Text: fmt.Sprintf("coms_await: error — %s", result.errMsg)}},
			map[string]any{"error": result.errMsg})
		return
	}
	respText := jsonToString(result.response)
	_ = w.Respond(id,
		[]ipc.ContentItem{{Type: "text", Text: respText}},
		map[string]any{"response": result.response})
}

// ─────────────────────────────────────────────────────────────────────────────
// Target resolution helpers
// ─────────────────────────────────────────────────────────────────────────────

// resolveTarget finds a live registry entry matching name (by name first, then
// session_id), searching the local project first, then all projects.
// Mirrors resolveTarget() in coms.ts lines 1163-1182.
func (c *Client) resolveTarget(name string) *proto.RegistryEntry {
	c.mu.RLock()
	project := ""
	selfSession := ""
	if c.identity != nil {
		project = c.identity.project
		selfSession = c.identity.sessionID
	}
	c.mu.RUnlock()

	if project != "" {
		live, _ := registry.Prune(project)
		for _, e := range live {
			if e.SessionID == selfSession {
				continue
			}
			if e.Name == name {
				return &e
			}
		}
	}

	for _, proj := range listProjects(c.comsDir) {
		live, _ := registry.Prune(proj)
		for _, e := range live {
			if e.SessionID == selfSession {
				continue
			}
			if e.SessionID == name || e.Name == name {
				return &e
			}
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Misc helpers
// ─────────────────────────────────────────────────────────────────────────────

func buildPingEnvelope(c *Client) proto.PingEnvelope {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var sessID, endpoint string
	if c.identity != nil {
		sessID = c.identity.sessionID
		endpoint = c.identity.endpoint
	}
	return proto.PingEnvelope{
		Envelope: proto.Envelope{
			Type:           "ping",
			MsgID:          util.NewULID(),
			SenderSession:  sessID,
			SenderEndpoint: endpoint,
			Hops:           0,
			Timestamp:      util.NowIso(),
		},
	}
}

func jsonToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// If it's a JSON string, unquote it.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	// Otherwise pretty-print the JSON.
	var v any
	if json.Unmarshal(raw, &v) == nil {
		b, _ := json.MarshalIndent(v, "", "  ")
		return string(b)
	}
	return string(raw)
}

func joinLines(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += "\n"
		}
		result += s
	}
	return result
}
