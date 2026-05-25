package netclient

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pi-vs-cc/coms-go/internal/ipc"
	"github.com/pi-vs-cc/coms-go/internal/proto"
	"github.com/pi-vs-cc/coms-go/internal/util"
)

// dispatchTool routes an IPC tool_request to the appropriate handler.
func (c *Client) dispatchTool(req ipc.Request, w *ipc.Writer) {
	switch req.Tool {
	case "coms_net_list":
		c.toolNetList(req, w)
	case "coms_net_send":
		c.toolNetSend(req, w)
	case "coms_net_get":
		c.toolNetGet(req, w)
	case "coms_net_await":
		c.toolNetAwait(req, w)
	case "coms_net_ask":
		c.toolNetAsk(req, w)
	default:
		_ = w.RespondError(req.ID, fmt.Sprintf("coms-net: unknown tool %q", req.Tool))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// coms_net_list
// ─────────────────────────────────────────────────────────────────────────────

type netListParams struct {
	Project         string `json:"project"`
	IncludeExplicit bool   `json:"include_explicit"`
}

func (c *Client) toolNetList(req ipc.Request, w *ipc.Writer) {
	var p netListParams
	if len(req.Params) > 0 {
		_ = json.Unmarshal(req.Params, &p)
	}

	c.mu.RLock()
	id := c.identity
	serverURL := c.serverURL
	authToken := c.authToken
	c.mu.RUnlock()

	if id == nil {
		_ = w.RespondError(req.ID, "coms-net not initialised")
		return
	}

	projectFilter := p.Project
	if projectFilter == "" {
		projectFilter = id.project
	}

	if serverURL == "" || authToken == "" {
		_ = w.RespondError(req.ID, "coms-net: no server connection")
		return
	}

	qs := fmt.Sprintf("?project=%s&include_explicit=%v", urlEscape(projectFilter), p.IncludeExplicit)
	respData, err := c.httpGet(context.Background(), serverURL+"/v1/agents"+qs, authToken)
	if err != nil {
		_ = w.RespondError(req.ID, fmt.Sprintf("coms-net: list failed: %v", safeErrorStr(err.Error(), authToken)))
		return
	}

	var listResp proto.ListAgentsResponse
	if err := json.Unmarshal(respData, &listResp); err != nil {
		_ = w.RespondError(req.ID, "coms-net: malformed list response")
		return
	}

	peers := make([]proto.AgentCard, 0)
	for _, a := range listResp.Agents {
		if id != nil && a.SessionID == id.sessionID {
			continue
		}
		peers = append(peers, a)
	}

	lines := "No peer agents found."
	if len(peers) > 0 {
		out := make([]string, 0, len(peers))
		for _, a := range peers {
			dot := "✗"
			switch a.Status {
			case proto.StatusOnline:
				dot = "●"
			case proto.StatusStale:
				dot = "~"
			}
			pctStr := "?%"
			pctStr = fmt.Sprintf("%d%%", a.ContextUsedPct)
			purposeStr := ""
			if a.Purpose != "" {
				purposeStr = " — " + a.Purpose
			}
			out = append(out, fmt.Sprintf("%s %s (%s) %s%s", dot, a.Name, abbreviateModel(a.Model), pctStr, purposeStr))
		}
		lines = joinStrings(out, "\n")
	}

	text := fmt.Sprintf("%d peer(s):\n%s", len(peers), lines)
	details := map[string]any{"agents": peers, "project": projectFilter}
	_ = w.Respond(req.ID, []ipc.ContentItem{{Type: "text", Text: text}}, details)
}

// ─────────────────────────────────────────────────────────────────────────────
// coms_net_send
// ─────────────────────────────────────────────────────────────────────────────

type netSendParams struct {
	Target         string          `json:"target"`
	Prompt         string          `json:"prompt"`
	ConversationID *string         `json:"conversation_id"`
	ResponseSchema json.RawMessage `json:"response_schema"`
}

func (c *Client) toolNetSend(req ipc.Request, w *ipc.Writer) {
	var p netSendParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.Target == "" || p.Prompt == "" {
		_ = w.RespondError(req.ID, "coms_net_send: target and prompt are required")
		return
	}

	c.mu.RLock()
	id := c.identity
	serverURL := c.serverURL
	authToken := c.authToken
	currentInbound := c.currentInbound
	c.mu.RUnlock()

	if id == nil {
		_ = w.RespondError(req.ID, "coms-net not initialised")
		return
	}
	if serverURL == "" || authToken == "" {
		_ = w.RespondError(req.ID, "coms-net: no server connection")
		return
	}

	hops := 0
	if currentInbound != nil {
		hops = currentInbound.hops + 1
	}
	if hops >= c.cfg.MaxHops {
		_ = w.RespondError(req.ID, fmt.Sprintf("coms-net: hop limit reached (%d >= %d)", hops, c.cfg.MaxHops))
		return
	}

	sendReq := proto.SendRequest{
		Project:        id.project,
		SenderSession:  id.sessionID,
		Target:         p.Target,
		TargetSession:  nil,
		Prompt:         p.Prompt,
		ConversationID: p.ConversationID,
		ResponseSchema: p.ResponseSchema,
		Hops:           hops,
	}

	respData, err := c.httpPost(context.Background(), serverURL+"/v1/messages", authToken, sendReq)
	if err != nil {
		errMsg := fmt.Sprintf("coms-net: send failed: %v", safeErrorStr(err.Error(), authToken))
		if he, ok := err.(*HTTPError); ok {
			// Extract the error code from the body if possible.
			var errBody map[string]any
			if json.Unmarshal([]byte(he.Body), &errBody) == nil {
				if code, ok := errBody["error"].(string); ok {
					errMsg = fmt.Sprintf("coms-net: send failed (%d): %s", he.Status, code)
				}
			}
		}
		_ = w.RespondError(req.ID, errMsg)
		return
	}

	var sendResp proto.SendResponse
	if err := json.Unmarshal(respData, &sendResp); err != nil {
		_ = w.RespondError(req.ID, "coms-net: malformed send response")
		return
	}

	msgID := sendResp.MsgID
	targetSession := sendResp.TargetSession

	// Park pending reply.
	pr := &netPendingReply{
		ready:         make(chan struct{}),
		targetName:    p.Target,
		targetSession: targetSession,
		createdAt:     util.NowIso(),
	}
	c.mu.Lock()
	c.pendingReplies[msgID] = pr
	c.mu.Unlock()

	_ = c.audit.Append(map[string]any{
		"event":          "prompt_out",
		"msg_id":         msgID,
		"target":         p.Target,
		"target_session": targetSession,
		"hops":           hops,
		"ts":             util.NowIso(),
	})

	text := fmt.Sprintf("coms_net_send → %s\nmsg_id %s\nhops %d", p.Target, msgID, hops)
	details := map[string]any{
		"msg_id":         msgID,
		"target":         p.Target,
		"target_session": targetSession,
		"hops":           hops,
	}
	_ = w.Respond(req.ID, []ipc.ContentItem{{Type: "text", Text: text}}, details)
}

// ─────────────────────────────────────────────────────────────────────────────
// coms_net_get
// ─────────────────────────────────────────────────────────────────────────────

type netGetParams struct {
	MsgID string `json:"msg_id"`
}

func (c *Client) toolNetGet(req ipc.Request, w *ipc.Writer) {
	var p netGetParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.MsgID == "" {
		_ = w.RespondError(req.ID, "coms_net_get: msg_id is required")
		return
	}

	c.mu.RLock()
	pr, hasPending := c.pendingReplies[p.MsgID]
	serverURL := c.serverURL
	authToken := c.authToken
	c.mu.RUnlock()

	// Local SSE fast path.
	if hasPending {
		pr.mu.Lock()
		result := pr.result
		pr.mu.Unlock()
		if result != nil {
			deliverNetGetResult(req.ID, p.MsgID, result, w)
			return
		}
	}

	// Fall back to server GET.
	if serverURL == "" || authToken == "" {
		_ = w.Respond(req.ID,
			[]ipc.ContentItem{{Type: "text", Text: "coms_net_get: pending"}},
			map[string]any{"status": "pending"})
		return
	}

	respData, err := c.httpGet(context.Background(), fmt.Sprintf("%s/v1/messages/%s", serverURL, urlEscape(p.MsgID)), authToken)
	if err != nil {
		if he, ok := err.(*HTTPError); ok && he.Status == 404 {
			_ = w.Respond(req.ID,
				[]ipc.ContentItem{{Type: "text", Text: fmt.Sprintf("coms_net_get: unknown msg_id %s", p.MsgID)}},
				map[string]any{"status": "error", "error": "unknown msg_id"})
			return
		}
		_ = w.Respond(req.ID,
			[]ipc.ContentItem{{Type: "text", Text: fmt.Sprintf("coms_net_get: error — %v", safeErrorStr(err.Error(), authToken))}},
			map[string]any{"status": "error", "error": safeErrorStr(err.Error(), authToken)})
		return
	}

	var msgStatus proto.MessageStatusResponse
	if err := json.Unmarshal(respData, &msgStatus); err != nil {
		_ = w.RespondError(req.ID, "coms_net_get: malformed response")
		return
	}

	status := string(msgStatus.Status)
	switch msgStatus.Status {
	case proto.MsgStatusComplete, proto.MsgStatusError, proto.MsgStatusTimeout:
		errVal := ""
		if msgStatus.Error != nil {
			errVal = *msgStatus.Error
		}
		text := ""
		if errVal != "" {
			text = fmt.Sprintf("coms_net_get: %s — %s", status, errVal)
		} else {
			text = fmt.Sprintf("coms_net_get: %s\n%s", status, jsonToStr(msgStatus.Response))
		}
		_ = w.Respond(req.ID,
			[]ipc.ContentItem{{Type: "text", Text: text}},
			map[string]any{"status": status, "response": msgStatus.Response, "error": msgStatus.Error})
	default:
		_ = w.Respond(req.ID,
			[]ipc.ContentItem{{Type: "text", Text: fmt.Sprintf("coms_net_get: %s", status)}},
			map[string]any{"status": status})
	}
}

func deliverNetGetResult(id, msgID string, result *netPendingResult, w *ipc.Writer) {
	if result.errMsg != "" {
		_ = w.Respond(id,
			[]ipc.ContentItem{{Type: "text", Text: fmt.Sprintf("coms_net_get: error — %s", result.errMsg)}},
			map[string]any{"status": "complete", "response": nil, "error": result.errMsg})
		return
	}
	text := fmt.Sprintf("coms_net_get: complete\n%s", jsonToStr(result.response))
	_ = w.Respond(id,
		[]ipc.ContentItem{{Type: "text", Text: text}},
		map[string]any{"status": "complete", "response": result.response, "error": nil})
}

// ─────────────────────────────────────────────────────────────────────────────
// coms_net_await
// ─────────────────────────────────────────────────────────────────────────────

type netAwaitParams struct {
	MsgID     string `json:"msg_id"`
	TimeoutMs *int   `json:"timeout_ms"`
}

func (c *Client) toolNetAwait(req ipc.Request, w *ipc.Writer) {
	var p netAwaitParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.MsgID == "" {
		_ = w.RespondError(req.ID, "coms_net_await: msg_id is required")
		return
	}

	timeoutMs := c.cfg.MessageTTLMs
	if p.TimeoutMs != nil && *p.TimeoutMs > 0 {
		timeoutMs = *p.TimeoutMs
	}
	if timeoutMs <= 0 {
		timeoutMs = 1_800_000
	}

	c.mu.RLock()
	pr, hasPending := c.pendingReplies[p.MsgID]
	serverURL := c.serverURL
	authToken := c.authToken
	c.mu.RUnlock()

	// Local SSE fast path — already resolved.
	if hasPending {
		pr.mu.Lock()
		if pr.result != nil {
			result := pr.result
			pr.mu.Unlock()
			deliverNetAwaitResult(req.ID, result, w)
			return
		}
		pr.mu.Unlock()
	}

	// Race: local SSE channel vs server long-poll vs local timeout.
	timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer timer.Stop()

	// Server long-poll.
	serverTimeoutMs := timeoutMs
	if serverTimeoutMs > c.cfg.MessageTTLMs && c.cfg.MessageTTLMs > 0 {
		serverTimeoutMs = c.cfg.MessageTTLMs
	}

	type serverResult struct {
		data json.RawMessage
		err  error
	}
	serverCh := make(chan serverResult, 1)

	if serverURL != "" && authToken != "" {
		go func() {
			pollCtx, cancel := context.WithTimeout(context.Background(), time.Duration(serverTimeoutMs+5_000)*time.Millisecond)
			defer cancel()
			url := fmt.Sprintf("%s/v1/messages/%s/await?timeout_ms=%d", serverURL, urlEscape(p.MsgID), serverTimeoutMs)
			data, err := c.httpGet(pollCtx, url, authToken)
			serverCh <- serverResult{data: data, err: err}
		}()
	}
	// No-server case: serverCh is a buffered channel that is never sent to.
	// The select below will simply never take the serverCh arm, which is correct.

	var sseReady <-chan struct{}
	if hasPending {
		sseReady = pr.ready
	} else {
		sseReady = make(chan struct{}) // never fires
	}

	select {
	case <-sseReady:
		pr.mu.Lock()
		result := pr.result
		pr.mu.Unlock()
		if result != nil {
			deliverNetAwaitResult(req.ID, result, w)
		}
		return

	case sr := <-serverCh:
		if sr.err != nil {
			if he, ok := sr.err.(*HTTPError); ok && he.Status == 404 {
				_ = w.Respond(req.ID,
					[]ipc.ContentItem{{Type: "text", Text: "coms_net_await: error — unknown msg_id"}},
					map[string]any{"error": "unknown msg_id"})
				return
			}
			_ = w.Respond(req.ID,
				[]ipc.ContentItem{{Type: "text", Text: fmt.Sprintf("coms_net_await: error — %v", safeErrorStr(sr.err.Error(), authToken))}},
				map[string]any{"error": safeErrorStr(sr.err.Error(), authToken)})
			return
		}
		var msgStatus proto.MessageStatusResponse
		if json.Unmarshal(sr.data, &msgStatus) != nil {
			_ = w.RespondError(req.ID, "coms_net_await: malformed server response")
			return
		}
		result := &netPendingResult{}
		if msgStatus.Error != nil {
			result.errMsg = *msgStatus.Error
		} else if msgStatus.Status == proto.MsgStatusTimeout {
			result.errMsg = "timeout"
		} else {
			result.response = msgStatus.Response
		}
		deliverNetAwaitResult(req.ID, result, w)
		return

	case <-timer.C:
		_ = w.Respond(req.ID,
			[]ipc.ContentItem{{Type: "text", Text: "coms_net_await: error — timeout"}},
			map[string]any{"error": "timeout"})
	}
}

func deliverNetAwaitResult(id string, result *netPendingResult, w *ipc.Writer) {
	if result.errMsg != "" {
		_ = w.Respond(id,
			[]ipc.ContentItem{{Type: "text", Text: fmt.Sprintf("coms_net_await: error — %s", result.errMsg)}},
			map[string]any{"error": result.errMsg})
		return
	}
	respText := jsonToStr(result.response)
	_ = w.Respond(id,
		[]ipc.ContentItem{{Type: "text", Text: respText}},
		map[string]any{"response": result.response})
}

// ─────────────────────────────────────────────────────────────────────────────
// coms_net_ask — atomic send+await over HTTP/SSE (T5 unicast, T6 broadcast)
// ─────────────────────────────────────────────────────────────────────────────
//
// Sender side of the auto-await pattern. Model sees one tool call; internally:
//   1. Resolve target (or enumerate peers for broadcast — T6).
//   2. POST /v1/messages (one per peer for broadcast).
//   3. Register pendingReply entries.
//   4. Block until reply arrives via SSE response event, or timeout fires.
//
// Receiver side is automated by the inbound_prompt event + before_agent_start
// hook (T1-T3). Default timeout is 30 s (interactive latency), not the 30 min
// of coms_net_await.

const netAskDefaultTimeoutMs = 30_000

type netAskParams struct {
	Target         *string         `json:"target"`
	Prompt         string          `json:"prompt"`
	TimeoutMs      *int            `json:"timeout_ms"`
	ConversationID *string         `json:"conversation_id"`
	ResponseSchema json.RawMessage `json:"response_schema"`
}

func (c *Client) toolNetAsk(req ipc.Request, w *ipc.Writer) {
	var p netAskParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.Prompt == "" {
		_ = w.RespondError(req.ID, "coms_net_ask: prompt is required")
		return
	}

	timeoutMs := netAskDefaultTimeoutMs
	if p.TimeoutMs != nil && *p.TimeoutMs > 0 {
		timeoutMs = *p.TimeoutMs
	}

	target := ""
	if p.Target != nil {
		target = *p.Target
	}

	// Broadcast path (T6) — no target specified.
	if target == "" {
		c.netAskBroadcast(req, w, p, timeoutMs)
		return
	}

	// Unicast path (T5).
	c.netAskUnicast(req, w, p, target, timeoutMs)
}

func (c *Client) netAskUnicast(req ipc.Request, w *ipc.Writer, p netAskParams, target string, timeoutMs int) {
	c.mu.RLock()
	id := c.identity
	serverURL := c.serverURL
	authToken := c.authToken
	currentInbound := c.currentInbound
	c.mu.RUnlock()

	if id == nil {
		_ = w.RespondError(req.ID, "coms-net not initialised")
		return
	}
	if serverURL == "" || authToken == "" {
		_ = w.RespondError(req.ID, "coms-net: no server connection")
		return
	}

	hops := 0
	if currentInbound != nil {
		hops = currentInbound.hops + 1
	}
	if hops >= c.cfg.MaxHops {
		_ = w.RespondError(req.ID, fmt.Sprintf("coms_net_ask: hop limit reached (%d >= %d)", hops, c.cfg.MaxHops))
		return
	}

	sendReq := proto.SendRequest{
		Project:        id.project,
		SenderSession:  id.sessionID,
		Target:         target,
		TargetSession:  nil,
		Prompt:         p.Prompt,
		ConversationID: p.ConversationID,
		ResponseSchema: p.ResponseSchema,
		Hops:           hops,
	}

	respData, err := c.httpPost(context.Background(), serverURL+"/v1/messages", authToken, sendReq)
	if err != nil {
		errMsg := fmt.Sprintf("coms_net_ask: send failed: %v", safeErrorStr(err.Error(), authToken))
		if he, ok := err.(*HTTPError); ok {
			var errBody map[string]any
			if json.Unmarshal([]byte(he.Body), &errBody) == nil {
				if code, ok := errBody["error"].(string); ok {
					errMsg = fmt.Sprintf("coms_net_ask: send failed (%d): %s", he.Status, code)
				}
			}
		}
		_ = w.RespondError(req.ID, errMsg)
		return
	}

	var sendResp proto.SendResponse
	if err := json.Unmarshal(respData, &sendResp); err != nil {
		_ = w.RespondError(req.ID, "coms_net_ask: malformed send response")
		return
	}

	msgID := sendResp.MsgID
	targetSession := sendResp.TargetSession

	pr := &netPendingReply{
		ready:         make(chan struct{}),
		targetName:    target,
		targetSession: targetSession,
		createdAt:     util.NowIso(),
	}
	c.mu.Lock()
	c.pendingReplies[msgID] = pr
	c.mu.Unlock()

	// T7: audit the ask_send event before blocking on the reply. Mirrors the
	// existing prompt_out event but distinguishes the atomic ask flow from a
	// plain coms_net_send. Log parsers that don't recognise ask_send fall
	// through to their default branch (additive change, see spec §9).
	_ = c.audit.Append(map[string]any{
		"event":          "ask_send",
		"msg_id":         msgID,
		"target":         target,
		"target_session": targetSession,
		"hops":           hops,
		"broadcast":      false,
		"ts":             util.NowIso(),
	})

	// Block until reply arrives or timeout fires.
	timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer timer.Stop()

	select {
	case <-pr.ready:
		pr.mu.Lock()
		result := pr.result
		pr.mu.Unlock()
		if result == nil {
			_ = w.RespondError(req.ID, fmt.Sprintf("coms_net_ask: internal error — pending reply closed with no result for %s", target))
			return
		}
		if result.errMsg != "" {
			_ = w.RespondError(req.ID, fmt.Sprintf("coms_net_ask: %s", result.errMsg))
			return
		}
		respText := jsonToStr(result.response)
		details := map[string]any{
			"msg_id":         msgID,
			"target":         target,
			"target_session": targetSession,
			"hops":           hops,
			"response":       result.response,
		}
		_ = w.Respond(req.ID, []ipc.ContentItem{{Type: "text", Text: respText}}, details)
	case <-timer.C:
		_ = w.RespondError(req.ID, fmt.Sprintf("coms_net_ask: timeout waiting for reply from %s", target))
	}
}

// netAskBroadcast delegates to the implementation in ask.go (T6).
func (c *Client) netAskBroadcast(req ipc.Request, w *ipc.Writer, p netAskParams, timeoutMs int) {
	c.netAskBroadcastImpl(req, w, p, timeoutMs)
}

// ─────────────────────────────────────────────────────────────────────────────
// Misc helpers
// ─────────────────────────────────────────────────────────────────────────────

func jsonToStr(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		b, _ := json.MarshalIndent(v, "", "  ")
		return string(b)
	}
	return string(raw)
}

func abbreviateModel(model string) string {
	m := model
	if len(m) > 7 && m[:7] == "claude-" {
		m = m[7:]
	}
	if len(m) > 14 {
		m = m[:14]
	}
	return m
}

func joinStrings(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
