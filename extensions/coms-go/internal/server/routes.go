package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pi-vs-cc/coms-go/internal/proto"
	"github.com/pi-vs-cc/coms-go/internal/util"
)

// registerRoutes wires all routes onto mux using only stdlib net/http.
// Route table matches §10 Routes Comparison Table verbatim.
func registerRoutes(mux *http.ServeMux, st *ServerState, cfg *Config) {
	h := &handlers{st: st, cfg: cfg}

	// GET /health — no auth
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, "method_not_allowed", http.StatusMethodNotAllowed, nil)
			return
		}
		h.handleHealth(w, r)
	})

	// All /v1/* require auth.
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r, cfg.Token) {
			writeUnauthorized(w)
			return
		}
		h.dispatchV1(w, r)
	})

	// Catch-all: any path not matched by /health or /v1/ returns 404.
	// The TS server returns 404 for unknown routes; a 200-empty response would
	// violate the parity contract (T8 required fix).
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, "not_found", http.StatusNotFound, nil)
	})
}

// handlers groups all route handler methods.
type handlers struct {
	st  *ServerState
	cfg *Config
}

// ─── /health ─────────────────────────────────────────────────────────────────

func (h *handlers) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, proto.HealthResponse{
		Ok:        true,
		Version:   1,
		ServerID:  h.st.serverID,
		StartedAt: h.st.startedAt,
	}, http.StatusOK)
}

// ─── Internal dispatcher for /v1/* ───────────────────────────────────────────

func (h *handlers) dispatchV1(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	// POST /v1/agents/register
	if path == "/v1/agents/register" && method == http.MethodPost {
		h.handleRegister(w, r)
		return
	}
	// GET /v1/events
	if path == "/v1/events" && method == http.MethodGet {
		h.handleEvents(w, r)
		return
	}
	// GET /v1/agents
	if path == "/v1/agents" && method == http.MethodGet {
		h.handleListAgents(w, r)
		return
	}
	// POST /v1/messages
	if path == "/v1/messages" && method == http.MethodPost {
		h.handleSendMessage(w, r)
		return
	}

	// /v1/agents/:session_id[/heartbeat]
	if m := compiledAgentRe.FindStringSubmatch(path); m != nil {
		sessionID := urlDecode(m[1])
		tail := m[2]
		switch {
		case tail == "heartbeat" && method == http.MethodPost:
			h.handleHeartbeat(w, r, sessionID)
		case tail == "" && method == http.MethodDelete:
			h.handleDeleteAgent(w, r, sessionID)
		default:
			writeError(w, "method_not_allowed", http.StatusMethodNotAllowed, nil)
		}
		return
	}

	// /v1/messages/:id[/await|/response]
	if m := compiledMsgRe.FindStringSubmatch(path); m != nil {
		msgID := urlDecode(m[1])
		tail := m[2]
		switch {
		case tail == "" && method == http.MethodGet:
			h.handleGetMessage(w, r, msgID)
		case tail == "await" && method == http.MethodGet:
			h.handleAwaitMessage(w, r, msgID)
		case tail == "response" && method == http.MethodPost:
			h.handleSubmitResponse(w, r, msgID)
		default:
			writeError(w, "method_not_allowed", http.StatusMethodNotAllowed, nil)
		}
		return
	}

	writeError(w, "not_found", http.StatusNotFound, nil)
}

// ─── POST /v1/agents/register ────────────────────────────────────────────────

func (h *handlers) handleRegister(w http.ResponseWriter, r *http.Request) {
	var body proto.RegisterRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, "invalid_json", http.StatusBadRequest, nil)
		return
	}
	if body.SessionID == "" || body.Project == "" || body.Name == "" {
		writeError(w, "invalid_request", http.StatusBadRequest, nil)
		return
	}

	p := h.st.getOrCreateProject(body.Project)

	p.mu.Lock()
	defer p.mu.Unlock()

	existing, isReregister := p.agents[body.SessionID]
	var resolvedName string
	if isReregister {
		if body.Name != existing.Name {
			resolvedName = resolveUniqueName(p, body.Name)
		} else {
			resolvedName = existing.Name
		}
	} else {
		resolvedName = resolveUniqueName(p, body.Name)
	}

	now := util.NowIso()
	startedAt := now
	if isReregister {
		startedAt = existing.StartedAt
	}
	registeredAt := now
	if isReregister {
		registeredAt = existing.RegisteredAt
	}
	ctxPct := 0
	queueDepth := 0
	if isReregister {
		ctxPct = existing.ContextUsedPct
		queueDepth = existing.QueueDepth
	}

	entry := &proto.NetRegistryEntry{
		AgentCard: proto.AgentCard{
			SessionID:      body.SessionID,
			Name:           resolvedName,
			Purpose:        body.Purpose,
			Model:          body.Model,
			Provider:       body.Provider,
			Color:          body.Color,
			Cwd:            body.Cwd,
			Project:        body.Project,
			Explicit:       body.Explicit,
			StartedAt:      startedAt,
			ContextUsedPct: ctxPct,
			QueueDepth:     queueDepth,
			Status:         proto.StatusOnline,
		},
		LastSeenAt:   now,
		RegisteredAt: registeredAt,
	}

	// Update name index.
	if isReregister && existing.Name != entry.Name {
		nameIndexRemove(p, existing.Name, body.SessionID)
	}
	p.agents[body.SessionID] = entry
	nameIndexAdd(p, entry.Name, body.SessionID)

	logRegister(entry.Name, body.Project, body.SessionID, isReregister)

	// Broadcast agent_joined to OTHER streams.
	broadcast(p, "agent_joined", map[string]any{
		"project": body.Project,
		"agent":   entryToCard(entry),
	}, body.SessionID)

	sseURL := fmt.Sprintf("/v1/events?project=%s&session_id=%s",
		urlEncode(body.Project), urlEncode(body.SessionID))

	writeJSON(w, proto.RegisterResponse{
		Ok:                  true,
		Agent:               entryToCard(entry),
		HeartbeatIntervalMs: h.cfg.HeartbeatMS,
		SseURL:              sseURL,
	}, http.StatusOK)
}

// ─── GET /v1/events (SSE) ────────────────────────────────────────────────────

func (h *handlers) handleEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectName := q.Get("project")
	if projectName == "" {
		projectName = "default"
	}
	sessionID := q.Get("session_id")
	if sessionID == "" {
		writeError(w, "missing_session_id", http.StatusBadRequest, nil)
		return
	}

	p := h.st.getOrCreateProject(projectName)

	p.mu.Lock()
	entry, ok := p.agents[sessionID]
	if !ok {
		p.mu.Unlock()
		writeError(w, "agent_not_found", http.StatusNotFound, nil)
		return
	}
	agentName := entry.Name

	// Replace possibly-stale stream entry.
	if old, exists := p.streams[sessionID]; exists {
		close(old.done)
	}
	sw := &SseWriter{
		sessionID: sessionID,
		ch:        make(chan string, 64),
		done:      make(chan struct{}),
		lastID:    0,
	}
	p.streams[sessionID] = sw

	// Build hello and pool_snapshot while holding the lock.
	sw.lastID++
	helloFrame := sseFrameWithID("hello", map[string]any{
		"server_time": util.NowIso(),
		"server_id":   h.st.serverID,
	}, sw.lastID)

	var poolAgents []proto.AgentCard
	for _, a := range p.agents {
		if a.SessionID == sessionID || a.Explicit {
			continue
		}
		poolAgents = append(poolAgents, entryToCard(a))
	}
	if poolAgents == nil {
		poolAgents = []proto.AgentCard{}
	}
	sw.lastID++
	snapFrame := sseFrameWithID("pool_snapshot", map[string]any{
		"project": projectName,
		"agents":  poolAgents,
	}, sw.lastID)

	streamCount := len(p.streams)
	p.mu.Unlock()

	logSseOpen(agentName, streamCount)

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, hasFlusher := w.(http.Flusher)

	writeSSE := func(frame string) bool {
		_, err := fmt.Fprint(w, frame)
		if err != nil {
			return false
		}
		if hasFlusher {
			flusher.Flush()
		}
		return true
	}

	// Send hello and pool_snapshot immediately.
	if !writeSSE(helloFrame) || !writeSSE(snapFrame) {
		cleanupStream(p, sessionID, sw, projectName, agentName, "write_error")
		return
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			cleanupStream(p, sessionID, sw, projectName, agentName, "connection_closed")
			return
		case <-sw.done:
			// Closed by server (shutdown, delete, or offline eviction).
			return
		case frame := <-sw.ch:
			if !writeSSE(frame) {
				cleanupStream(p, sessionID, sw, projectName, agentName, "write_error")
				return
			}
		}
	}
}

// cleanupStream removes the stream from the registry and broadcasts agent_left.
func cleanupStream(p *ProjectState, sessionID string, sw *SseWriter, projectName, agentName, reason string) {
	p.mu.Lock()
	cur := p.streams[sessionID]
	if cur == sw {
		delete(p.streams, sessionID)
	}
	broadcast(p, "agent_left", map[string]any{
		"project":    projectName,
		"session_id": sessionID,
		"name":       agentName,
		"reason":     reason,
	}, sessionID)
	p.mu.Unlock()

	logSseClose(agentName, reason)
}

// ─── POST /v1/agents/:sid/heartbeat ──────────────────────────────────────────

func (h *handlers) handleHeartbeat(w http.ResponseWriter, r *http.Request, sessionID string) {
	var body proto.HeartbeatRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, "invalid_json", http.StatusBadRequest, nil)
		return
	}

	projectName := body.Project
	if projectName == "" {
		projectName = "default"
	}
	p := h.st.getProject(projectName)
	if p == nil {
		writeError(w, "agent_not_found", http.StatusNotFound, nil)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.agents[sessionID]
	if !ok {
		writeError(w, "agent_not_found", http.StatusNotFound, nil)
		return
	}

	prevCtx := entry.ContextUsedPct
	prevQueue := entry.QueueDepth
	prevModel := entry.Model
	prevStatus := entry.Status

	entry.ContextUsedPct = body.ContextUsedPct
	entry.QueueDepth = body.QueueDepth
	if body.Model != "" {
		entry.Model = body.Model
	}
	switch body.Status {
	case proto.StatusOnline, proto.StatusStale, proto.StatusOffline:
		entry.Status = body.Status
	default:
		entry.Status = proto.StatusOnline
	}
	entry.LastSeenAt = util.NowIso()

	logHeartbeat(entry.Name, entry.ContextUsedPct, entry.QueueDepth)

	changed := prevCtx != entry.ContextUsedPct ||
		prevQueue != entry.QueueDepth ||
		prevModel != entry.Model ||
		prevStatus != entry.Status

	if changed {
		broadcast(p, "agent_updated", map[string]any{
			"project": projectName,
			"agent": map[string]any{
				"session_id":       entry.SessionID,
				"name":             entry.Name,
				"context_used_pct": entry.ContextUsedPct,
				"queue_depth":      entry.QueueDepth,
				"model":            entry.Model,
				"status":           entry.Status,
			},
		}, sessionID)
	}

	writeJSON(w, proto.OkResponse{Ok: true}, http.StatusOK)
}

// ─── GET /v1/agents ──────────────────────────────────────────────────────────

func (h *handlers) handleListAgents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectName := q.Get("project")
	if projectName == "" {
		projectName = "default"
	}
	includeExplicit := strings.ToLower(q.Get("include_explicit")) == "true"

	p := h.st.getProject(projectName)
	cards := []proto.AgentCard{}
	if p != nil {
		p.mu.RLock()
		for _, e := range p.agents {
			if !includeExplicit && e.Explicit {
				continue
			}
			cards = append(cards, entryToCard(e))
		}
		p.mu.RUnlock()
	}
	writeJSON(w, proto.ListAgentsResponse{Agents: cards}, http.StatusOK)
}

// ─── POST /v1/messages ───────────────────────────────────────────────────────

func (h *handlers) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	var body proto.SendRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, "invalid_json", http.StatusBadRequest, nil)
		return
	}
	if body.SenderSession == "" || body.Prompt == "" {
		writeError(w, "invalid_request", http.StatusBadRequest, nil)
		return
	}

	projectName := body.Project
	if projectName == "" {
		projectName = "default"
	}
	p := h.st.getProject(projectName)
	if p == nil {
		writeError(w, "agent_not_found", http.StatusNotFound, nil)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	sender, ok := p.agents[body.SenderSession]
	if !ok {
		writeError(w, "sender_not_registered", http.StatusNotFound, nil)
		return
	}

	hops := body.Hops
	if hops >= h.cfg.MaxHops {
		logRejected("hop_limit", fmt.Sprintf("%s hops=%d max=%d", sender.Name, hops, h.cfg.MaxHops))
		writeError(w, "hop_limit_exceeded", http.StatusConflict, map[string]any{
			"hops": hops, "max_hops": h.cfg.MaxHops,
		})
		return
	}

	// Resolve target.
	var target *proto.NetRegistryEntry
	if body.TargetSession != nil && *body.TargetSession != "" {
		t, ok := p.agents[*body.TargetSession]
		if !ok {
			logRejected("target_not_found", fmt.Sprintf("%s → %s", sender.Name, tail6(*body.TargetSession)))
			writeError(w, "target_not_found", http.StatusNotFound, nil)
			return
		}
		target = t
	} else {
		desired := strings.TrimSpace(body.Target)
		if desired == "" {
			writeError(w, "missing_target", http.StatusBadRequest, nil)
			return
		}
		// Direct session_id match first.
		if t, ok := p.agents[desired]; ok {
			target = t
		} else {
			bag := p.nameIndex[desired]
			if len(bag) == 0 {
				logRejected("target_not_found", fmt.Sprintf(`%s → "%s"`, sender.Name, desired))
				writeError(w, "target_not_found", http.StatusNotFound, map[string]any{"target": desired})
				return
			}
			if len(bag) > 1 {
				var candidates []string
				for sid := range bag {
					candidates = append(candidates, sid)
				}
				logRejected("ambiguous", fmt.Sprintf(`%s → "%s" matches %d`, sender.Name, desired, len(bag)))
				writeError(w, "ambiguous_target", http.StatusConflict, map[string]any{
					"target": desired, "candidates": candidates,
				})
				return
			}
			for sid := range bag {
				target = p.agents[sid]
			}
			if target == nil {
				writeError(w, "target_not_found", http.StatusNotFound, nil)
				return
			}
		}
	}

	// Inbox cap.
	depth := inboxDepthFor(p, target.SessionID)
	if depth >= h.cfg.MaxInbox {
		logRejected("inbox_full", fmt.Sprintf("%s → %s depth=%d", sender.Name, target.Name, depth))
		writeError(w, "inbox_full", http.StatusTooManyRequests, map[string]any{
			"depth": depth, "max_inbox": h.cfg.MaxInbox,
		})
		return
	}

	now := util.NowIso()
	expiresAt := time.Now().Add(time.Duration(h.cfg.MessageTTLMS) * time.Millisecond).UTC().Format(time.RFC3339Nano)

	msg := &proto.ComsMessage{
		MsgID:          util.NewULID(),
		Project:        projectName,
		SenderSession:  body.SenderSession,
		TargetSession:  target.SessionID,
		Prompt:         body.Prompt,
		ConversationID: body.ConversationID,
		ResponseSchema: body.ResponseSchema,
		Hops:           hops,
		Status:         proto.MsgStatusQueued,
		CreatedAt:      now,
		ExpiresAt:      expiresAt,
	}
	p.messages[msg.MsgID] = msg

	// Notify sender: queued.
	sendToStream(p, body.SenderSession, "message_status", map[string]any{
		"msg_id": msg.MsgID,
		"status": "queued",
	})

	// Emit prompt to target if its stream is open.
	if _, open := p.streams[target.SessionID]; open {
		sendToStream(p, target.SessionID, "prompt", map[string]any{
			"msg_id":  msg.MsgID,
			"project": projectName,
			"sender": map[string]any{
				"session_id": sender.SessionID,
				"name":       sender.Name,
				"cwd":        sender.Cwd,
			},
			"prompt":          msg.Prompt,
			"conversation_id": msg.ConversationID,
			"response_schema": msg.ResponseSchema,
			"hops":            msg.Hops,
		})
		msg.Status = proto.MsgStatusDelivered
		msg.DeliveredAt = now
		// Notify sender: delivered.
		sendToStream(p, body.SenderSession, "message_status", map[string]any{
			"msg_id": msg.MsgID,
			"status": "delivered",
		})
	}

	logMessageSend(sender.Name, target.Name, msg.MsgID, msg.Prompt, hops, msg.Status == proto.MsgStatusDelivered)

	writeJSON(w, proto.SendResponse{
		Ok:            true,
		MsgID:         msg.MsgID,
		Status:        msg.Status,
		TargetSession: target.SessionID,
	}, http.StatusOK)
}

// ─── GET /v1/messages/:id ────────────────────────────────────────────────────

func (h *handlers) handleGetMessage(w http.ResponseWriter, _ *http.Request, msgID string) {
	for _, p := range h.st.allProjects() {
		p.mu.RLock()
		m, ok := p.messages[msgID]
		if ok {
			resp := proto.MessageStatusResponse{
				MsgID:    m.MsgID,
				Status:   m.Status,
				Response: m.Response,
				Error:    m.Error,
			}
			p.mu.RUnlock()
			writeJSON(w, resp, http.StatusOK)
			return
		}
		p.mu.RUnlock()
	}
	writeError(w, "message_not_found", http.StatusNotFound, nil)
}

// ─── GET /v1/messages/:id/await ──────────────────────────────────────────────

func (h *handlers) handleAwaitMessage(w http.ResponseWriter, r *http.Request, msgID string) {
	// Find message.
	var foundP *ProjectState
	var foundMsg *proto.ComsMessage
	for _, p := range h.st.allProjects() {
		p.mu.RLock()
		m, ok := p.messages[msgID]
		if ok {
			foundP = p
			foundMsg = m
			p.mu.RUnlock()
			break
		}
		p.mu.RUnlock()
	}
	if foundP == nil || foundMsg == nil {
		writeError(w, "message_not_found", http.StatusNotFound, nil)
		return
	}

	// Already terminal? Resolve immediately.
	foundP.mu.RLock()
	status := foundMsg.Status
	var immediateResp proto.MessageStatusResponse
	if status == proto.MsgStatusComplete || status == proto.MsgStatusError || status == proto.MsgStatusTimeout {
		immediateResp = proto.MessageStatusResponse{
			MsgID:    foundMsg.MsgID,
			Status:   foundMsg.Status,
			Response: foundMsg.Response,
			Error:    foundMsg.Error,
		}
	}
	foundP.mu.RUnlock()

	if status == proto.MsgStatusComplete || status == proto.MsgStatusError || status == proto.MsgStatusTimeout {
		writeJSON(w, immediateResp, http.StatusOK)
		return
	}

	// Parse timeout.
	const defaultAwaitTimeoutMS = 30_000
	var requestedMS int64
	if ts := r.URL.Query().Get("timeout_ms"); ts != "" {
		fmt.Sscan(ts, &requestedMS)
	}
	timeoutMS := int64(defaultAwaitTimeoutMS)
	if requestedMS > 0 {
		timeoutMS = requestedMS
	}
	if timeoutMS > int64(h.cfg.MessageTTLMS) {
		timeoutMS = int64(h.cfg.MessageTTLMS)
	}

	// Register awaiter. The timer is constructed and assigned to a.timer BEFORE
	// publishing a into foundP.awaiters so that releaseAwaiters (which reads
	// a.timer under foundP.mu write lock) can never observe a nil timer — this
	// eliminates the data race reported by -race on TestPiToPiRoundTrip.
	a := &Awaiter{
		ch: make(chan proto.ComsMessage, 1),
	}

	// Construct the timer first (still pre-publication, so no concurrent reader
	// can see a yet). The callback references a and foundP, which are stable.
	var timer *time.Timer
	timer = time.AfterFunc(time.Duration(timeoutMS)*time.Millisecond, func() {
		foundP.mu.Lock()
		set := foundP.awaiters[msgID]
		delete(set, a)
		if len(set) == 0 {
			delete(foundP.awaiters, msgID)
		}
		foundP.mu.Unlock()
		select {
		case a.ch <- proto.ComsMessage{MsgID: msgID, Status: proto.MsgStatusTimeout}:
		default:
		}
	})
	a.timer = timer // assigned before publication into awaiters map

	foundP.mu.Lock()
	if foundP.awaiters[msgID] == nil {
		foundP.awaiters[msgID] = make(map[*Awaiter]struct{})
	}
	foundP.awaiters[msgID][a] = struct{}{} // a.timer visible to all readers after this
	foundP.mu.Unlock()

	ctx := r.Context()
	var resolved proto.ComsMessage
	select {
	case <-ctx.Done():
		timer.Stop()
		foundP.mu.Lock()
		set := foundP.awaiters[msgID]
		delete(set, a)
		if len(set) == 0 {
			delete(foundP.awaiters, msgID)
		}
		foundP.mu.Unlock()
		return
	case resolved = <-a.ch:
	}

	var resp proto.MessageStatusResponse
	if resolved.Status == proto.MsgStatusTimeout {
		errStr := "timeout"
		resp = proto.MessageStatusResponse{
			MsgID:  msgID,
			Status: proto.MsgStatusTimeout,
			Error:  &errStr,
		}
	} else {
		resp = proto.MessageStatusResponse{
			MsgID:    resolved.MsgID,
			Status:   resolved.Status,
			Response: resolved.Response,
			Error:    resolved.Error,
		}
	}
	writeJSON(w, resp, http.StatusOK)
}

// ─── POST /v1/messages/:id/response ──────────────────────────────────────────

func (h *handlers) handleSubmitResponse(w http.ResponseWriter, r *http.Request, msgID string) {
	var body proto.ResponseSubmitRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, "invalid_json", http.StatusBadRequest, nil)
		return
	}
	if body.ResponderSession == "" {
		writeError(w, "invalid_request", http.StatusBadRequest, nil)
		return
	}

	var foundP *ProjectState
	var foundMsg *proto.ComsMessage
	for _, p := range h.st.allProjects() {
		p.mu.RLock()
		m, ok := p.messages[msgID]
		if ok {
			foundP = p
			foundMsg = m
			p.mu.RUnlock()
			break
		}
		p.mu.RUnlock()
	}
	if foundP == nil || foundMsg == nil {
		writeError(w, "message_not_found", http.StatusNotFound, nil)
		return
	}

	foundP.mu.Lock()
	defer foundP.mu.Unlock()

	if body.ResponderSession != foundMsg.TargetSession {
		writeError(w, "not_target", http.StatusForbidden, nil)
		return
	}
	if foundMsg.Status == proto.MsgStatusComplete ||
		foundMsg.Status == proto.MsgStatusError ||
		foundMsg.Status == proto.MsgStatusTimeout {
		writeError(w, "already_terminal", http.StatusConflict, map[string]any{"status": foundMsg.Status})
		return
	}

	isError := body.Error != nil
	if isError {
		foundMsg.Status = proto.MsgStatusError
		errStr := *body.Error
		foundMsg.Error = &errStr
	} else {
		foundMsg.Status = proto.MsgStatusComplete
	}
	foundMsg.Response = body.Response
	foundMsg.CompletedAt = util.NowIso()

	responder := foundP.agents[body.ResponderSession]
	responderName := "unknown"
	if responder != nil {
		responderName = responder.Name
	}

	errVal := ""
	if foundMsg.Error != nil {
		errVal = *foundMsg.Error
	}
	sendToStream(foundP, foundMsg.SenderSession, "response", map[string]any{
		"msg_id":  foundMsg.MsgID,
		"project": foundMsg.Project,
		"responder": map[string]any{
			"session_id": body.ResponderSession,
			"name":       responderName,
		},
		"response": foundMsg.Response,
		"error":    foundMsg.Error,
		"status":   foundMsg.Status,
	})
	sendToStream(foundP, foundMsg.SenderSession, "message_status", map[string]any{
		"msg_id": foundMsg.MsgID,
		"status": foundMsg.Status,
	})

	releaseAwaiters(foundP, msgID)

	senderName := "(gone)"
	if s := foundP.agents[foundMsg.SenderSession]; s != nil {
		senderName = s.Name
	}
	var responseSize int
	if foundMsg.Response != nil {
		responseSize = len(foundMsg.Response)
	}
	logResponse(responderName, senderName, foundMsg.MsgID, isError, errVal, responseSize)

	writeJSON(w, proto.OkResponse{Ok: true}, http.StatusOK)
}

// ─── DELETE /v1/agents/:sid ──────────────────────────────────────────────────

func (h *handlers) handleDeleteAgent(w http.ResponseWriter, r *http.Request, sessionID string) {
	q := r.URL.Query()
	projectName := q.Get("project")
	if projectName == "" {
		projectName = "default"
	}
	p := h.st.getProject(projectName)
	if p == nil {
		writeError(w, "agent_not_found", http.StatusNotFound, nil)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.agents[sessionID]
	if !ok {
		writeError(w, "agent_not_found", http.StatusNotFound, nil)
		return
	}

	// Close stream first.
	if sw, ok := p.streams[sessionID]; ok {
		close(sw.done)
		delete(p.streams, sessionID)
	}

	delete(p.agents, sessionID)
	nameIndexRemove(p, entry.Name, sessionID)

	logUnregister(entry.Name, "shutdown")

	broadcast(p, "agent_left", map[string]any{
		"project":    projectName,
		"session_id": sessionID,
		"name":       entry.Name,
		"reason":     "shutdown",
	}, sessionID)

	writeJSON(w, proto.OkResponse{Ok: true}, http.StatusOK)
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, body any, status int) {
	data, err := json.Marshal(body)
	if err != nil {
		http.Error(w, `{"ok":false,"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(data) //nolint:errcheck
}

func writeError(w http.ResponseWriter, errStr string, status int, details any) {
	body := proto.ErrorResponse{Ok: false, Error: safeError(errStr)}
	if details != nil {
		body.Details = details
	}
	writeJSON(w, body, status)
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="coms-net"`)
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"ok":false,"error":"unauthorized"}`)) //nolint:errcheck
}

func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func urlEncode(s string) string { return url.QueryEscape(s) }

func urlDecode(s string) string {
	d, err := url.QueryUnescape(s)
	if err != nil {
		return s
	}
	return d
}
