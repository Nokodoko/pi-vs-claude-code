// Package netclient implements the client-net subcommand (Go port of
// extensions/coms-net.ts). It connects to the HTTP/SSE hub, registers this
// agent, maintains an SSE read loop with exponential-backoff reconnect, sends
// heartbeats, and dispatches IPC tool calls from the shim.
package netclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pi-vs-cc/coms-go/internal/audit"
	"github.com/pi-vs-cc/coms-go/internal/ipc"
	"github.com/pi-vs-cc/coms-go/internal/proto"
	"github.com/pi-vs-cc/coms-go/internal/util"
)

// ─────────────────────────────────────────────────────────────────────────────
// Config
// ─────────────────────────────────────────────────────────────────────────────

// Config holds configuration passed from the CLI / env.
type Config struct {
	// Identity
	Name     string
	Purpose  string
	Project  string
	Color    string
	Explicit bool

	// Server
	ServerURL string // PI_COMS_NET_SERVER_URL
	AuthToken string // PI_COMS_NET_AUTH_TOKEN

	// Tuning
	MaxHops      int // PI_COMS_NET_MAX_HOPS; default 5
	HeartbeatMs  int // PI_COMS_NET_HEARTBEAT_MS; default 10_000
	MessageTTLMs int // PI_COMS_NET_MESSAGE_TTL_MS; default 1_800_000

	// Session supplied by shim
	SessionID string
	Cwd       string
	Model     string

	// IO (defaults to os.Stdin / os.Stdout; overridable for testing)
	Stdin  io.Reader
	Stdout io.Writer
}

// DefaultConfig returns a Config with env-var overrides applied.
func DefaultConfig() Config {
	return Config{
		Name:         os.Getenv("PI_COMS_NAME"),
		Purpose:      os.Getenv("PI_COMS_PURPOSE"),
		Project:      util.EnvOr("PI_COMS_PROJECT", "default"),
		Color:        os.Getenv("PI_COMS_COLOR"),
		Explicit:     os.Getenv("PI_COMS_EXPLICIT") == "1",
		ServerURL:    os.Getenv("PI_COMS_NET_SERVER_URL"),
		AuthToken:    os.Getenv("PI_COMS_NET_AUTH_TOKEN"),
		MaxHops:      util.EnvInt("PI_COMS_NET_MAX_HOPS", 5),
		HeartbeatMs:  util.EnvInt("PI_COMS_NET_HEARTBEAT_MS", 10_000),
		MessageTTLMs: util.EnvInt("PI_COMS_NET_MESSAGE_TTL_MS", 1_800_000),
		SessionID:    os.Getenv("PI_SESSION_ID"),
		Cwd:          util.EnvOr("PI_CWD", util.MustGetwd()),
		Model:        util.EnvOr("PI_MODEL", "unknown"),
		Stdin:        os.Stdin,
		Stdout:       os.Stdout,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal state types
// ─────────────────────────────────────────────────────────────────────────────

type identity struct {
	sessionID string
	name      string
	purpose   string
	color     string
	project   string
	explicit  bool
	cwd       string
	model     string
	startedAt string
}

type netPendingResult struct {
	response json.RawMessage
	errMsg   string
}

type netPendingReply struct {
	mu            sync.Mutex
	result        *netPendingResult
	ready         chan struct{}
	targetName    string
	targetSession string
	createdAt     string
}

type netInboundCtx struct {
	msgID          string
	hops           int
	senderSession  string
	senderName     string
	senderCwd      string
	responseSchema json.RawMessage
	fulfilled      bool
}

// ─────────────────────────────────────────────────────────────────────────────
// Client
// ─────────────────────────────────────────────────────────────────────────────

// Client is the long-lived client-net process state.
type Client struct {
	cfg   Config
	audit *audit.Logger
	hc    *http.Client

	mu             sync.RWMutex
	identity       *identity
	serverURL      string
	authToken      string
	sseURLPath     string
	peerCards      map[string]proto.AgentCard // session_id → card
	pendingReplies map[string]*netPendingReply
	inboundQueue   map[string]*netInboundCtx
	currentInbound *netInboundCtx
	shuttingDown   bool

	sseCancel         context.CancelFunc
	reconnectAttempts int
	toolWg            sync.WaitGroup // tracks in-flight dispatchTool goroutines

	// ipcWriter is the unsolicited-event channel back to shim.ts. Set once in
	// run() before the IPC select loop starts; read from any goroutine that
	// needs to push an "event" frame (e.g. handleInboundPrompt). The underlying
	// ipc.Writer is mutex-protected, so no extra locking is required here.
	ipcWriter *ipc.Writer
}

const (
	reconnectBaseMs  = 500
	reconnectMaxMs   = 10_000
	httpTimeoutMs    = 10_000
	shutdownDeleteMs = 2_000
)

// newClient creates a Client from cfg.
func newClient(cfg Config) *Client {
	auditPath := ""
	if home, err := os.UserHomeDir(); err == nil {
		auditPath = filepath.Join(home, ".pi", "coms-net-log")
	}

	return &Client{
		cfg:            cfg,
		audit:          audit.New(auditPath),
		hc:             &http.Client{Timeout: httpTimeoutMs * time.Millisecond},
		peerCards:      make(map[string]proto.AgentCard),
		pendingReplies: make(map[string]*netPendingReply),
		inboundQueue:   make(map[string]*netInboundCtx),
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Run — main entrypoint
// ─────────────────────────────────────────────────────────────────────────────

// Run initialises the client and starts the IPC loop.
func Run(ctx context.Context, cfg Config) error {
	c := newClient(cfg)
	return c.run(ctx)
}

func (c *Client) run(ctx context.Context) error {
	// 1. Resolve identity.
	if err := c.initIdentity(); err != nil {
		return fmt.Errorf("client-net: identity init: %w", err)
	}

	// 2. Resolve server URL and auth token.
	if err := c.resolveServerConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "coms-net: %v\n", err)
		// Non-fatal: continue running in degraded mode (tools will error).
	}

	// 3. Register and open SSE (if server available).
	if c.serverURL != "" && c.authToken != "" {
		if err := c.registerAndOpenSSE(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "coms-net: boot warning: %v\n", safeErrorStr(err.Error(), c.authToken))
		}
	}

	// 4. Heartbeat ticker.
	heartbeatMs := c.cfg.HeartbeatMs
	if heartbeatMs <= 0 {
		heartbeatMs = 10_000
	}
	hbTicker := time.NewTicker(time.Duration(heartbeatMs) * time.Millisecond)
	defer hbTicker.Stop()

	// 5. IPC loop.
	stdin := c.cfg.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := c.cfg.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	w := ipc.NewWriter(stdout)
	c.ipcWriter = w
	requests := ipc.ReadRequests(stdin)

	for {
		select {
		case <-ctx.Done():
			c.toolWg.Wait()
			c.shutdown()
			return nil

		case req, ok := <-requests:
			if !ok {
				c.toolWg.Wait()
				c.shutdown()
				return nil
			}
			switch req.Kind {
			case "tool_request":
				c.toolWg.Add(1)
				go func() {
					defer c.toolWg.Done()
					c.dispatchTool(req, w)
				}()
			case "lifecycle":
				c.handleLifecycle(req)
			case "command":
				// /coms-net command — refresh.
				go c.refreshPeerCards()
			case "shutdown":
				c.toolWg.Wait()
				c.shutdown()
				return nil
			}

		case <-hbTicker.C:
			go c.sendHeartbeat(ctx)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Identity init
// ─────────────────────────────────────────────────────────────────────────────

func (c *Client) initIdentity() error {
	cfg := c.cfg
	fm := util.ReadFrontmatterFromArgv(os.Args)

	project := cfg.Project
	if project == "" {
		project = "default"
	}

	sessionID := cfg.SessionID
	if sessionID == "" {
		sessionID = util.NewULID()
	}

	// Name: CLI > frontmatter > auto.
	defaultName := fmt.Sprintf("agent-%s", sessionID[len(sessionID)-6:])
	desiredName := cfg.Name
	if desiredName == "" {
		desiredName = fm.Name
	}
	if desiredName == "" {
		desiredName = defaultName
	}

	purpose := cfg.Purpose
	if purpose == "" {
		purpose = fm.Description
	}

	color := util.FallbackColor(sessionID)
	if fm.Color != "" && util.IsValidHex(fm.Color) {
		color = fm.Color
	}
	if cfg.Color != "" && util.IsValidHex(cfg.Color) {
		color = cfg.Color
	}

	cwd := cfg.Cwd
	if cwd == "" {
		cwd = util.MustGetwd()
	}
	model := cfg.Model
	if model == "" {
		model = "unknown"
	}

	c.mu.Lock()
	c.identity = &identity{
		sessionID: sessionID,
		name:      desiredName,
		purpose:   purpose,
		color:     color,
		project:   project,
		explicit:  cfg.Explicit,
		cwd:       cwd,
		model:     model,
		startedAt: util.NowIso(),
	}
	c.mu.Unlock()
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Server config resolution
// ─────────────────────────────────────────────────────────────────────────────

func (c *Client) resolveServerConfig() error {
	cfg := c.cfg

	// Server URL: CLI flag > env > server.json.
	serverURL := cfg.ServerURL
	if serverURL == "" {
		serverURL = os.Getenv("PI_COMS_NET_SERVER_URL")
	}
	if serverURL == "" {
		c.mu.RLock()
		project := ""
		if c.identity != nil {
			project = c.identity.project
		}
		c.mu.RUnlock()
		if sj := readServerJSON(project); sj != nil {
			serverURL = strings.TrimRight(sj.LocalURL, "/")
		}
	}
	if serverURL != "" {
		serverURL = strings.TrimRight(serverURL, "/")
	}

	// Auth token: CLI flag > env > server.secret.json.
	authToken := cfg.AuthToken
	if authToken == "" {
		authToken = os.Getenv("PI_COMS_NET_AUTH_TOKEN")
	}
	if authToken == "" {
		c.mu.RLock()
		project := ""
		if c.identity != nil {
			project = c.identity.project
		}
		c.mu.RUnlock()
		if sec := readServerSecret(project); sec != nil {
			authToken = sec.Token
		}
	}

	c.mu.Lock()
	c.serverURL = serverURL
	c.authToken = authToken
	c.mu.Unlock()

	if serverURL == "" {
		return fmt.Errorf("no server URL for project; set PI_COMS_NET_SERVER_URL or run coms-go serve")
	}
	if authToken == "" {
		return fmt.Errorf("no auth token; set PI_COMS_NET_AUTH_TOKEN or ensure server.secret.json exists (mode 0600)")
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Registration + SSE open
// ─────────────────────────────────────────────────────────────────────────────

func (c *Client) registerAndOpenSSE(ctx context.Context) error {
	sseURLPath, err := c.registerAgent(ctx)
	if err != nil {
		return err
	}

	sseCtx, sseCancel := context.WithCancel(ctx)

	c.mu.Lock()
	c.sseURLPath = sseURLPath
	c.sseCancel = sseCancel
	c.mu.Unlock()

	go c.sseLoop(sseCtx)
	return nil
}

// registerAgent POSTs /v1/agents/register and returns the SSE URL path.
// Mirrors registerAgent() in coms-net.ts lines 828-868.
func (c *Client) registerAgent(ctx context.Context) (string, error) {
	c.mu.RLock()
	id := c.identity
	serverURL := c.serverURL
	authToken := c.authToken
	c.mu.RUnlock()

	if id == nil {
		return "", fmt.Errorf("not initialised")
	}

	req := proto.RegisterRequest{
		Project:   id.project,
		SessionID: id.sessionID,
		Name:      id.name,
		Purpose:   id.purpose,
		Model:     id.model,
		Color:     id.color,
		Cwd:       id.cwd,
		Explicit:  id.explicit,
	}

	respData, err := c.httpPost(ctx, serverURL+"/v1/agents/register", authToken, req)
	if err != nil {
		return "", fmt.Errorf("register: %w", err)
	}

	var regResp proto.RegisterResponse
	if err := json.Unmarshal(respData, &regResp); err != nil {
		return "", fmt.Errorf("register: malformed response: %w", err)
	}

	// Server may suffix name on collision.
	if regResp.Agent.Name != id.name {
		_ = c.audit.Append(map[string]any{
			"event":    "name_collision",
			"desired":  id.name,
			"assigned": regResp.Agent.Name,
			"project":  id.project,
			"ts":       util.NowIso(),
		})
		c.mu.Lock()
		c.identity.name = regResp.Agent.Name
		c.mu.Unlock()
	}

	_ = c.audit.Append(map[string]any{
		"event":      "register",
		"session_id": id.sessionID,
		"name":       regResp.Agent.Name,
		"project":    id.project,
		"ts":         util.NowIso(),
	})

	return regResp.SseURL, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// SSE loop with exponential-backoff reconnect
// ─────────────────────────────────────────────────────────────────────────────

// sseLoop opens the SSE stream and reconnects with exponential backoff.
// Mirrors openSse() + scheduleReconnect() in coms-net.ts.
func (c *Client) sseLoop(ctx context.Context) {
	for {
		c.mu.RLock()
		shutting := c.shuttingDown
		serverURL := c.serverURL
		authToken := c.authToken
		sseURLPath := c.sseURLPath
		c.mu.RUnlock()

		if shutting || sseURLPath == "" {
			return
		}

		err := c.connectSSE(ctx, serverURL+sseURLPath, authToken)

		c.mu.RLock()
		shutting = c.shuttingDown
		attempts := c.reconnectAttempts
		c.mu.RUnlock()

		if shutting {
			return
		}
		if ctx.Err() != nil {
			return
		}

		_ = c.audit.Append(map[string]any{
			"event":  "sse_disconnect",
			"reason": safeError(err, authToken),
			"ts":     util.NowIso(),
		})

		// Exponential backoff: base 500ms, cap 10s.
		backoffMs := reconnectBaseMs * (1 << attempts) // 500, 1000, 2000, 4000, 8000, 10000...
		if backoffMs > reconnectMaxMs {
			backoffMs = reconnectMaxMs
		}

		c.mu.Lock()
		c.reconnectAttempts++
		c.mu.Unlock()

		_ = c.audit.Append(map[string]any{
			"event":      "sse_reconnect_scheduled",
			"attempt":    attempts + 1,
			"backoff_ms": backoffMs,
			"ts":         util.NowIso(),
		})

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(backoffMs) * time.Millisecond):
		}

		// Re-register before reopening SSE.
		sseURLPath, err2 := c.registerAgent(ctx)
		if err2 != nil {
			fmt.Fprintf(os.Stderr, "coms-net: reconnect register failed: %v\n", safeErrorStr(err2.Error(), authToken))
			continue
		}
		c.mu.Lock()
		c.sseURLPath = sseURLPath
		c.mu.Unlock()

		_ = c.audit.Append(map[string]any{
			"event":   "sse_reconnect",
			"attempt": attempts + 1,
			"ts":      util.NowIso(),
		})
	}
}

// connectSSE opens one SSE connection and reads events until the stream ends.
func (c *Client) connectSSE(ctx context.Context, url, authToken string) error {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+authToken)
	httpReq.Header.Set("Accept", "text/event-stream")

	// No timeout for SSE connections — they are long-lived.
	sseClient := &http.Client{}
	resp, err := sseClient.Do(httpReq)
	if err != nil {
		_ = c.audit.Append(map[string]any{
			"event":  "sse_connect_failed",
			"reason": safeErrorStr(err.Error(), authToken),
			"ts":     util.NowIso(),
		})
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = c.audit.Append(map[string]any{
			"event":  "sse_connect_http_error",
			"status": resp.StatusCode,
			"ts":     util.NowIso(),
		})
		return &HTTPError{Status: resp.StatusCode, Body: string(body)}
	}

	// Connection established — reset backoff counter.
	c.mu.Lock()
	c.reconnectAttempts = 0
	c.mu.Unlock()

	_ = c.audit.Append(map[string]any{
		"event": "sse_open",
		"ts":    util.NowIso(),
	})

	return readSSEStream(resp.Body, c.handleSSEEvent)
}

// ─────────────────────────────────────────────────────────────────────────────
// SSE event dispatch
// ─────────────────────────────────────────────────────────────────────────────

// handleSSEEvent processes one parsed SSE frame.
// Mirrors handleSseEvent() in coms-net.ts lines 571-651.
func (c *Client) handleSSEEvent(ev SSEEvent) {
	data := parseEventData(ev)
	if data == nil {
		return
	}

	switch ev.Event {
	case "hello":
		_ = c.audit.Append(map[string]any{
			"event":       "sse_hello",
			"server_id":   strField(data, "server_id"),
			"server_time": strField(data, "server_time"),
			"ts":          util.NowIso(),
		})

	case "pool_snapshot":
		agents, _ := data["agents"].([]any)
		c.mu.Lock()
		c.peerCards = make(map[string]proto.AgentCard)
		selfID := ""
		if c.identity != nil {
			selfID = c.identity.sessionID
		}
		for _, a := range agents {
			am, ok := a.(map[string]any)
			if !ok {
				continue
			}
			sid := strField(am, "session_id")
			if sid == "" || sid == selfID {
				continue
			}
			c.peerCards[sid] = mapToAgentCard(am)
		}
		c.mu.Unlock()

	case "agent_joined":
		am, ok := data["agent"].(map[string]any)
		if !ok {
			return
		}
		sid := strField(am, "session_id")
		if sid == "" {
			return
		}
		c.mu.Lock()
		selfID := ""
		if c.identity != nil {
			selfID = c.identity.sessionID
		}
		if sid != selfID {
			c.peerCards[sid] = mapToAgentCard(am)
		}
		c.mu.Unlock()

	case "agent_updated":
		am, ok := data["agent"].(map[string]any)
		if !ok {
			return
		}
		sid := strField(am, "session_id")
		if sid == "" {
			return
		}
		c.mu.Lock()
		if prev, exists := c.peerCards[sid]; exists {
			merged := mergePatch(prev, am)
			c.peerCards[sid] = merged
		}
		c.mu.Unlock()

	case "agent_stale":
		sid := strField(data, "session_id")
		if sid == "" {
			return
		}
		c.mu.Lock()
		if card, exists := c.peerCards[sid]; exists {
			card.Status = proto.StatusStale
			c.peerCards[sid] = card
		}
		c.mu.Unlock()

	case "agent_left":
		sid := strField(data, "session_id")
		if sid == "" {
			return
		}
		c.mu.Lock()
		delete(c.peerCards, sid)
		c.mu.Unlock()

	case "prompt":
		c.handleInboundPrompt(data)

	case "response":
		c.handleInboundResponse(data)

	case "message_status":
		// Informational — no-op beyond audit (debug level).

	case "server_ping":
		// No-op.

	case "error":
		_ = c.audit.Append(map[string]any{
			"event":   "sse_error",
			"code":    strField(data, "code"),
			"message": strField(data, "message"),
			"ts":      util.NowIso(),
		})
	}
}

// handleInboundPrompt processes a `prompt` SSE event (peer sent us a message).
// Mirrors handleInboundPrompt() in coms-net.ts lines 653-710.
func (c *Client) handleInboundPrompt(data map[string]any) {
	msgID := strField(data, "msg_id")
	if msgID == "" {
		return
	}
	sender, _ := data["sender"].(map[string]any)
	senderName := "unknown"
	senderCwd := "?"
	senderSession := "?"
	if sender != nil {
		senderName = strField(sender, "name")
		senderCwd = strField(sender, "cwd")
		senderSession = strField(sender, "session_id")
	}
	hops := intField(data, "hops")
	var responseSchema json.RawMessage
	if rs, ok := data["response_schema"]; ok && rs != nil {
		responseSchema = rawJSON(rs)
	}

	inbound := &netInboundCtx{
		msgID:          msgID,
		hops:           hops,
		senderSession:  senderSession,
		senderName:     senderName,
		senderCwd:      senderCwd,
		responseSchema: responseSchema,
		fulfilled:      false,
	}

	c.mu.Lock()
	c.inboundQueue[msgID] = inbound
	c.currentInbound = inbound
	c.mu.Unlock()

	_ = c.audit.Append(map[string]any{
		"event":  "prompt_in",
		"msg_id": msgID,
		"sender": senderSession,
		"hops":   hops,
		"ts":     util.NowIso(),
	})

	// T1: notify shim.ts of the inbound prompt so its before_agent_start hook
	// can drain the queue and inject a directive into the receiver model. The
	// event is push-only — shim correlates by msg_id, not by request id. The
	// underlying ipc.Writer is mutex-protected so this is safe from sseLoop.
	if c.ipcWriter != nil {
		body := strField(data, "prompt")
		_ = c.ipcWriter.Event("inbound_prompt", map[string]any{
			"msg_id":         msgID,
			"sender_name":    senderName,
			"sender_session": senderSession,
			"body":           body,
			"hops":           hops,
		})
	}
}

// handleInboundResponse resolves a pending reply when the SSE `response` event arrives.
// Mirrors handleInboundResponse() in coms-net.ts lines 712-732.
func (c *Client) handleInboundResponse(data map[string]any) {
	msgID := strField(data, "msg_id")
	if msgID == "" {
		return
	}

	var respRaw json.RawMessage
	if v, ok := data["response"]; ok {
		respRaw = rawJSON(v)
	}
	errVal := strField(data, "error")

	c.mu.RLock()
	pr, ok := c.pendingReplies[msgID]
	c.mu.RUnlock()

	if ok {
		pr.mu.Lock()
		if pr.result == nil {
			pr.result = &netPendingResult{
				response: respRaw,
				errMsg:   errVal,
			}
			close(pr.ready)
		}
		pr.mu.Unlock()

		_ = c.audit.Append(map[string]any{
			"event":  "response_in",
			"msg_id": msgID,
			"error":  errVal,
			"ts":     util.NowIso(),
		})
	} else {
		_ = c.audit.Append(map[string]any{
			"event":  "orphan_response",
			"msg_id": msgID,
			"ts":     util.NowIso(),
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Heartbeat
// ─────────────────────────────────────────────────────────────────────────────

// sendHeartbeat POSTs /v1/agents/:sid/heartbeat.
// Mirrors the heartbeat setInterval in coms-net.ts lines 985-1001.
func (c *Client) sendHeartbeat(ctx context.Context) {
	c.mu.RLock()
	id := c.identity
	serverURL := c.serverURL
	authToken := c.authToken
	shutting := c.shuttingDown
	queueDepth := len(c.inboundQueue)
	c.mu.RUnlock()

	if id == nil || serverURL == "" || authToken == "" || shutting {
		return
	}

	req := proto.HeartbeatRequest{
		Project:        id.project,
		ContextUsedPct: 0, // no pi ctx in Go binary
		QueueDepth:     queueDepth,
		Model:          id.model,
		Status:         proto.StatusOnline,
	}

	hbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/v1/agents/%s/heartbeat", serverURL, urlEscape(id.sessionID))
	_, err := c.httpPost(hbCtx, url, authToken, req)
	if err != nil {
		_ = c.audit.Append(map[string]any{
			"event":  "heartbeat_failed",
			"reason": safeErrorStr(err.Error(), authToken),
			"ts":     util.NowIso(),
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Lifecycle
// ─────────────────────────────────────────────────────────────────────────────

func (c *Client) handleLifecycle(req ipc.Request) {
	if req.Event != "agent_end" {
		return
	}

	var data struct {
		LastText string `json:"last_text"`
	}
	if len(req.Data) > 0 {
		_ = json.Unmarshal(req.Data, &data)
	}
	if data.LastText != "" {
		c.onAgentEnd(data.LastText)
	}
}

// onAgentEnd submits the last assistant text as a response to any pending inbound.
// Mirrors agent_end handler in coms-net.ts lines 1464-1521.
func (c *Client) onAgentEnd(lastText string) {
	c.mu.Lock()
	var inbound *netInboundCtx
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
	serverURL := c.serverURL
	authToken := c.authToken
	c.mu.Unlock()

	if id == nil || serverURL == "" || authToken == "" {
		return
	}

	var payload json.RawMessage
	var errStr *string

	if len(inbound.responseSchema) > 0 {
		if json.Valid([]byte(lastText)) {
			payload = json.RawMessage(lastText)
		} else {
			s := "response not valid JSON"
			errStr = &s
		}
	} else {
		b, _ := json.Marshal(lastText)
		payload = b
	}

	req := proto.ResponseSubmitRequest{
		Project:          id.project,
		ResponderSession: id.sessionID,
		Response:         payload,
		Error:            errStr,
	}

	submitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/v1/messages/%s/response", serverURL, urlEscape(inbound.msgID))
	_, err := c.httpPost(submitCtx, url, authToken, req)
	if err != nil {
		_ = c.audit.Append(map[string]any{
			"event":  "response_out_failed",
			"msg_id": inbound.msgID,
			"reason": safeErrorStr(err.Error(), authToken),
			"ts":     util.NowIso(),
		})
	} else {
		_ = c.audit.Append(map[string]any{
			"event":  "response_out",
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

// ─────────────────────────────────────────────────────────────────────────────
// Shutdown
// ─────────────────────────────────────────────────────────────────────────────

func (c *Client) shutdown() {
	c.mu.Lock()
	if c.shuttingDown {
		c.mu.Unlock()
		return
	}
	c.shuttingDown = true
	id := c.identity
	serverURL := c.serverURL
	authToken := c.authToken
	cancel := c.sseCancel
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	if id != nil && serverURL != "" && authToken != "" {
		// DELETE /v1/agents/:sid — best-effort, 2s cap.
		delCtx, delCancel := context.WithTimeout(context.Background(), shutdownDeleteMs*time.Millisecond)
		defer delCancel()
		url := fmt.Sprintf("%s/v1/agents/%s?project=%s", serverURL, urlEscape(id.sessionID), urlEscape(id.project))
		_ = c.httpDelete(delCtx, url, authToken)

		_ = c.audit.Append(map[string]any{
			"event":      "shutdown",
			"session_id": id.sessionID,
			"ts":         util.NowIso(),
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// refreshPeerCards
// ─────────────────────────────────────────────────────────────────────────────

func (c *Client) refreshPeerCards() {
	c.mu.RLock()
	id := c.identity
	serverURL := c.serverURL
	authToken := c.authToken
	c.mu.RUnlock()

	if id == nil || serverURL == "" || authToken == "" {
		return
	}

	project := id.project
	qs := fmt.Sprintf("?project=%s&include_explicit=false", urlEscape(project))
	respData, err := c.httpGet(context.Background(), serverURL+"/v1/agents"+qs, authToken)
	if err != nil {
		return
	}

	var listResp proto.ListAgentsResponse
	if err := json.Unmarshal(respData, &listResp); err != nil {
		return
	}

	c.mu.Lock()
	c.peerCards = make(map[string]proto.AgentCard)
	for _, a := range listResp.Agents {
		if c.identity != nil && a.SessionID == c.identity.sessionID {
			continue
		}
		c.peerCards[a.SessionID] = a
	}
	c.mu.Unlock()
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP helpers
// ─────────────────────────────────────────────────────────────────────────────

func (c *Client) httpPost(ctx context.Context, url, token string, body any) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Status: resp.StatusCode, Body: string(respBody)}
	}
	return respBody, nil
}

func (c *Client) httpGet(ctx context.Context, url, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Status: resp.StatusCode, Body: string(body)}
	}
	return body, nil
}

func (c *Client) httpDelete(ctx context.Context, url, token string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Server discovery helpers
// ─────────────────────────────────────────────────────────────────────────────

type serverJSON struct {
	LocalURL string `json:"local_url"`
}

type serverSecretJSON struct {
	Token string `json:"token"`
}

func comsNetDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pi", "coms-net")
}

func readServerJSON(project string) *serverJSON {
	p := filepath.Join(comsNetDir(), "projects", project, "server.json")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var sj serverJSON
	if json.Unmarshal(data, &sj) != nil || sj.LocalURL == "" {
		return nil
	}
	return &sj
}

func readServerSecret(project string) *serverSecretJSON {
	p := filepath.Join(comsNetDir(), "projects", project, "server.secret.json")
	fi, err := os.Stat(p)
	if err != nil {
		return nil
	}
	// Only trust mode 0600.
	if fi.Mode()&0o777 != 0o600 {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var sec serverSecretJSON
	if json.Unmarshal(data, &sec) != nil || sec.Token == "" {
		return nil
	}
	return &sec
}

// ─────────────────────────────────────────────────────────────────────────────
// AgentCard conversion helpers
// ─────────────────────────────────────────────────────────────────────────────

func mapToAgentCard(m map[string]any) proto.AgentCard {
	status := proto.AgentStatus(strField(m, "status"))
	if status == "" {
		status = proto.StatusOnline
	}
	return proto.AgentCard{
		SessionID:      strField(m, "session_id"),
		Name:           strField(m, "name"),
		Purpose:        strField(m, "purpose"),
		Model:          strField(m, "model"),
		Provider:       strField(m, "provider"),
		Color:          strField(m, "color"),
		Cwd:            strField(m, "cwd"),
		Project:        strField(m, "project"),
		Explicit:       boolField(m, "explicit"),
		StartedAt:      strField(m, "started_at"),
		ContextUsedPct: intField(m, "context_used_pct"),
		QueueDepth:     intField(m, "queue_depth"),
		Status:         status,
	}
}

// mergePatch applies a partial update map onto an existing AgentCard,
// returning the merged card.
func mergePatch(prev proto.AgentCard, patch map[string]any) proto.AgentCard {
	if v := strField(patch, "name"); v != "" {
		prev.Name = v
	}
	if v := strField(patch, "purpose"); v != "" {
		prev.Purpose = v
	}
	if v := strField(patch, "model"); v != "" {
		prev.Model = v
	}
	if v := strField(patch, "color"); v != "" {
		prev.Color = v
	}
	if _, ok := patch["context_used_pct"]; ok {
		prev.ContextUsedPct = intField(patch, "context_used_pct")
	}
	if _, ok := patch["queue_depth"]; ok {
		prev.QueueDepth = intField(patch, "queue_depth")
	}
	if v := strField(patch, "status"); v != "" {
		prev.Status = proto.AgentStatus(v)
	}
	return prev
}

// ─────────────────────────────────────────────────────────────────────────────
// URL encoding helpers
// ─────────────────────────────────────────────────────────────────────────────

func urlEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if isURLSafe(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func isURLSafe(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
		c == '-' || c == '_' || c == '.' || c == '~'
}
