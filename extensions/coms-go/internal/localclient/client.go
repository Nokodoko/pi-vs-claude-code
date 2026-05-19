//go:build !windows

// Package localclient implements the client-local subcommand (Go port of
// extensions/coms.ts). It maintains a Unix-socket listener for peer-to-peer
// messages and speaks JSON-line IPC with the shim over stdin/stdout.
package localclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pi-vs-cc/coms-go/internal/audit"
	"github.com/pi-vs-cc/coms-go/internal/ipc"
	"github.com/pi-vs-cc/coms-go/internal/proto"
	"github.com/pi-vs-cc/coms-go/internal/registry"
	"github.com/pi-vs-cc/coms-go/internal/transport"
	"github.com/pi-vs-cc/coms-go/internal/util"
)

// ─────────────────────────────────────────────────────────────────────────────
// Config
// ─────────────────────────────────────────────────────────────────────────────

// Config holds configuration passed from the CLI / env (set by main.go).
type Config struct {
	// Identity
	Name     string // --name flag or PI_COMS_NAME
	Purpose  string // --purpose flag or PI_COMS_PURPOSE
	Project  string // --project flag or PI_COMS_PROJECT; default "default"
	Color    string // --color flag or PI_COMS_COLOR
	Explicit bool   // --explicit flag or PI_COMS_EXPLICIT=1

	// Tuning (env-var names match coms.ts constants)
	MaxHops        int // PI_COMS_MAX_HOPS; default 5
	TimeoutMs      int // PI_COMS_TIMEOUT_MS; default 1_800_000 (30 min)
	PingIntervalMs int // PI_COMS_PING_INTERVAL_MS; default 10_000

	// Identity supplied by shim at session start
	SessionID string // PI_SESSION_ID
	Cwd       string // PI_CWD
	Model     string // PI_MODEL

	// IO (defaults to os.Stdin / os.Stdout; overridable for testing)
	Stdin  io.Reader
	Stdout io.Writer
}

// DefaultConfig returns a Config with env-var overrides applied.
func DefaultConfig() Config {
	cfg := Config{
		Name:           os.Getenv("PI_COMS_NAME"),
		Purpose:        os.Getenv("PI_COMS_PURPOSE"),
		Project:        util.EnvOr("PI_COMS_PROJECT", "default"),
		Color:          os.Getenv("PI_COMS_COLOR"),
		Explicit:       os.Getenv("PI_COMS_EXPLICIT") == "1",
		MaxHops:        util.EnvInt("PI_COMS_MAX_HOPS", 5),
		TimeoutMs:      util.EnvInt("PI_COMS_TIMEOUT_MS", 1_800_000),
		PingIntervalMs: util.EnvInt("PI_COMS_PING_INTERVAL_MS", 10_000),
		SessionID:      os.Getenv("PI_SESSION_ID"),
		Cwd:            util.EnvOr("PI_CWD", util.MustGetwd()),
		Model:          util.EnvOr("PI_MODEL", "unknown"),
		Stdin:          os.Stdin,
		Stdout:         os.Stdout,
	}
	return cfg
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal state types
// ─────────────────────────────────────────────────────────────────────────────

type identity struct {
	sessionID    string
	name         string
	purpose      string
	color        string
	project      string
	explicit     bool
	cwd          string
	model        string
	endpoint     string
	registryFile string
}

type inboundCtx struct {
	msgID          string
	hops           int
	senderEndpoint string
	senderSession  string
	responseSchema json.RawMessage
	fulfilled      bool
}

type pendingResult struct {
	response json.RawMessage
	errMsg   string
}

type pendingReply struct {
	mu         sync.Mutex
	result     *pendingResult
	ready      chan struct{}
	targetName string
	createdAt  string
}

// ─────────────────────────────────────────────────────────────────────────────
// Client
// ─────────────────────────────────────────────────────────────────────────────

// Client is the long-lived client-local process state.
type Client struct {
	cfg     Config
	comsDir string
	audit   *audit.Logger

	mu             sync.RWMutex
	identity       *identity
	inboundQueue   map[string]*inboundCtx
	pendingReplies map[string]*pendingReply
	currentInbound *inboundCtx

	maxHops   int
	timeoutMs int

	listener net.Listener
	toolWg   sync.WaitGroup // tracks in-flight dispatchTool goroutines
}

// newClient creates a Client from cfg.
func newClient(cfg Config) *Client {
	comsDir := os.Getenv("PI_COMS_DIR")
	if comsDir == "" {
		home, _ := os.UserHomeDir()
		comsDir = filepath.Join(home, ".pi", "coms")
	}

	auditPath := ""
	if home, err := os.UserHomeDir(); err == nil {
		auditPath = filepath.Join(home, ".pi", "coms-log")
	}

	maxHops := cfg.MaxHops
	if maxHops <= 0 {
		maxHops = 5
	}
	timeoutMs := cfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 1_800_000
	}

	return &Client{
		cfg:            cfg,
		comsDir:        comsDir,
		audit:          audit.New(auditPath),
		inboundQueue:   make(map[string]*inboundCtx),
		pendingReplies: make(map[string]*pendingReply),
		maxHops:        maxHops,
		timeoutMs:      timeoutMs,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Run — the main entrypoint called from main.go
// ─────────────────────────────────────────────────────────────────────────────

// Run initialises the client, registers with the local registry, starts the
// Unix-socket listener, background tickers, and the IPC dispatcher loop.
// It returns when ctx is cancelled or stdin reaches EOF.
func Run(ctx context.Context, cfg Config) error {
	c := newClient(cfg)
	return c.run(ctx)
}

func (c *Client) run(ctx context.Context) error {
	// 1. Resolve identity.
	if err := c.initIdentity(); err != nil {
		return fmt.Errorf("client-local: identity init: %w", err)
	}

	// 2. Bind Unix socket.
	c.mu.RLock()
	endpoint := c.identity.endpoint
	c.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(endpoint), 0700); err != nil {
		return fmt.Errorf("client-local: mkdir socket dir: %w", err)
	}

	ln, err := transport.BindEndpoint(endpoint)
	if err != nil {
		return fmt.Errorf("client-local: bind: %w", err)
	}
	c.listener = ln

	// Accept loop.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					fmt.Fprintf(os.Stderr, "client-local: accept: %v\n", err)
					return
				}
			}
			go c.handleConn(conn)
		}
	}()

	// 3. Audit boot.
	c.mu.RLock()
	id := c.identity
	c.mu.RUnlock()
	_ = c.audit.Append(map[string]any{
		"event":      "boot",
		"session_id": id.sessionID,
		"name":       id.name,
		"project":    id.project,
		"ts":         util.NowIso(),
	})

	fmt.Fprintf(os.Stderr, "coms client-local: %s@%s (session %s)\n", id.name, id.project, id.sessionID)

	// 4. Start tickers.
	pingInterval := time.Duration(c.cfg.PingIntervalMs) * time.Millisecond
	if pingInterval <= 0 {
		pingInterval = 10 * time.Second
	}
	pingTicker := time.NewTicker(pingInterval)
	keepaliveTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()
	defer keepaliveTicker.Stop()

	// 5. IPC dispatcher.
	stdin := c.cfg.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := c.cfg.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	w := ipc.NewWriter(stdout)
	requests := ipc.ReadRequests(stdin)

	for {
		select {
		case <-ctx.Done():
			c.toolWg.Wait()
			c.shutdown()
			return nil

		case req, ok := <-requests:
			if !ok {
				// stdin EOF — shim closed the pipe.
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
				// /coms slash command — best-effort refresh.
				go c.refreshPool()
			case "shutdown":
				c.toolWg.Wait()
				c.shutdown()
				return nil
			}

		case <-pingTicker.C:
			go c.refreshPool()

		case <-keepaliveTicker.C:
			go c.keepalive()
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Identity init
// ─────────────────────────────────────────────────────────────────────────────

func (c *Client) initIdentity() error {
	cfg := c.cfg
	project := cfg.Project
	if project == "" {
		project = "default"
	}

	// Frontmatter from argv.
	fm := util.ReadFrontmatterFromArgv(os.Args)

	// Session ID.
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

	resolvedName, err := registry.ResolveUniqueName(project, desiredName)
	if err != nil {
		resolvedName = desiredName
	}
	if resolvedName != desiredName {
		_ = c.audit.Append(map[string]any{
			"event":    "name_collision",
			"desired":  desiredName,
			"assigned": resolvedName,
			"project":  project,
			"ts":       util.NowIso(),
		})
	}

	// Purpose.
	purpose := cfg.Purpose
	if purpose == "" {
		purpose = fm.Description
	}

	// Color: CLI > frontmatter > deterministic fallback.
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

	endpoint := transport.MakeEndpoint(sessionID)

	// Ensure dirs exist.
	sockDir := filepath.Dir(endpoint)
	if err := os.MkdirAll(sockDir, 0700); err != nil {
		return fmt.Errorf("mkdir sockets: %w", err)
	}
	agentsDir := filepath.Join(c.comsDir, "projects", project, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		return fmt.Errorf("mkdir agents: %w", err)
	}

	pid := os.Getpid()
	entry := proto.RegistryEntry{
		SessionID: sessionID,
		Name:      resolvedName,
		Purpose:   purpose,
		Model:     model,
		Color:     color,
		Pid:       pid,
		Endpoint:  endpoint,
		Cwd:       cwd,
		StartedAt: util.NowIso(),
		Explicit:  cfg.Explicit,
		Version:   1,
	}

	registryFile, err := registry.Write(entry, project)
	if err != nil {
		return fmt.Errorf("registry write: %w", err)
	}

	c.mu.Lock()
	c.identity = &identity{
		sessionID:    sessionID,
		name:         resolvedName,
		purpose:      purpose,
		color:        color,
		project:      project,
		explicit:     cfg.Explicit,
		cwd:          cwd,
		model:        model,
		endpoint:     endpoint,
		registryFile: registryFile,
	}
	c.mu.Unlock()
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Tickers
// ─────────────────────────────────────────────────────────────────────────────

// refreshPool pings all live peers to update the in-memory card cache.
// Mirrors refreshPool() in coms.ts lines 1096-1150.
func (c *Client) refreshPool() {
	c.mu.RLock()
	if c.identity == nil {
		c.mu.RUnlock()
		return
	}
	project := c.identity.project
	selfSession := c.identity.sessionID
	c.mu.RUnlock()

	live, _ := registry.Prune(project)
	for _, e := range live {
		if e.SessionID == selfSession {
			continue
		}
		pingEnv := buildPingEnvelope(c)
		_, _ = transport.SendEnvelope(e.Endpoint, pingEnv) // best-effort
	}
}

// keepalive rewrites the registry entry atomically so mtime stays fresh.
// Mirrors the keepaliveTimer callback in coms.ts lines 887-925.
func (c *Client) keepalive() {
	c.mu.RLock()
	id := c.identity
	queueDepth := len(c.inboundQueue)
	c.mu.RUnlock()

	if id == nil {
		return
	}

	now := util.NowIso()
	hbAt := now
	entry := proto.RegistryEntry{
		SessionID:   id.sessionID,
		Name:        id.name,
		Purpose:     id.purpose,
		Model:       id.model,
		Color:       id.color,
		Pid:         os.Getpid(),
		Endpoint:    id.endpoint,
		Cwd:         id.cwd,
		StartedAt:   now,
		Explicit:    id.explicit,
		Version:     1,
		QueueDepth:  func() *int { v := queueDepth; return &v }(),
		HeartbeatAt: hbAt,
	}
	_, _ = registry.Write(entry, id.project)
}

// ─────────────────────────────────────────────────────────────────────────────
// Lifecycle (agent_end forwarded from shim)
// ─────────────────────────────────────────────────────────────────────────────

func (c *Client) handleLifecycle(req ipc.Request) {
	if req.Event != "agent_end" {
		return
	}
	// The shim sends the last assistant text as data.last_text (extension of
	// the lifecycle contract) or we derive from data.model — the shim doesn't
	// actually send last_text; the Go client doesn't have access to session
	// history. We handle agent_end as a best-effort: if there's a pending
	// inbound, we don't have the text here (Go has no pi ctx). The shim is
	// responsible for delivering the text via a separate tool call if needed.
	// For now, agent_end without text data is a no-op from the Go side.
	// The actual response submission happens via handleResponse when the shim
	// explicitly calls a response tool (the architecture leaves this to the
	// shim or to a future T4+ enhancement).
	//
	// Note: coms.ts agent_end works because it has direct access to
	// ctx.sessionManager.getBranch(). The Go binary cannot replicate this
	// because it has no pi API access. The shim would need to pass the last
	// assistant text as part of the lifecycle event data. For now we attempt
	// to parse it from the data field.
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

// ─────────────────────────────────────────────────────────────────────────────
// Shutdown
// ─────────────────────────────────────────────────────────────────────────────

func (c *Client) shutdown() {
	if c.listener != nil {
		_ = c.listener.Close()
		c.listener = nil
	}

	c.mu.RLock()
	id := c.identity
	c.mu.RUnlock()

	if id != nil {
		// Unlink socket file.
		_ = os.Remove(id.endpoint)
		// Remove registry entry.
		registry.Remove(id.project, id.name)
		_ = c.audit.Append(map[string]any{
			"event":      "shutdown",
			"session_id": id.sessionID,
			"ts":         util.NowIso(),
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func listProjects(comsDir string) []string {
	root := filepath.Join(comsDir, "projects")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}
