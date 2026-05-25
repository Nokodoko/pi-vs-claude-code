package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/pi-vs-cc/coms-go/internal/proto"
	"github.com/pi-vs-cc/coms-go/internal/util"
)

// Default tunables — match the TS server's module-scope constants.
const (
	defaultHost           = "127.0.0.1"
	defaultPort           = 0
	defaultProject        = "default"
	defaultMaxHops        = 5
	defaultMessageTTLMS   = 1_800_000
	defaultMaxInbox       = 100
	defaultHeartbeatMS    = 10_000
	defaultStaleAfterMS   = 30_000
	defaultOfflineAfterMS = 60_000

	staleScanIntervalMS = 5_000
	ttlScanIntervalMS   = 10_000
	ssKeepaliveMS       = 15_000
)

// Package-level log control vars, read from env in Run().
var (
	logQuiet            bool
	logHeartbeatEnabled bool
)

// NewServeMux creates a ready-to-use http.ServeMux wired with all routes.
// Used by tests to create an httptest.Server without binding a port.
func NewServeMux(cfg *Config) *http.ServeMux {
	st := newServerState()
	mux := http.NewServeMux()
	registerRoutes(mux, st, cfg)
	return mux
}

// Config holds all server tunables parsed from flags and env.
type Config struct {
	Host           string
	Port           int
	Project        string
	PublicURL      string
	Token          string
	TokenOwned     bool // true if we generated the token and must unlink the secret file
	SecretPath     string
	MaxHops        int
	MessageTTLMS   int
	MaxInbox       int
	HeartbeatMS    int
	StaleAfterMS   int
	OfflineAfterMS int
	NoColor        bool
}

// ParseConfig reads flags from args (after the "serve" subcommand) and env vars,
// matching the TS server's env-var precedence: flag > env > default.
func ParseConfig(args []string) (*Config, error) {
	cfg := &Config{
		Host:           util.EnvOr("PI_COMS_NET_HOST", defaultHost),
		Port:           util.EnvInt("PI_COMS_NET_PORT", defaultPort),
		Project:        util.EnvOr("PI_COMS_NET_PROJECT", defaultProject),
		PublicURL:      os.Getenv("PI_COMS_NET_PUBLIC_URL"),
		Token:          os.Getenv("PI_COMS_NET_AUTH_TOKEN"),
		MaxHops:        util.EnvInt("PI_COMS_NET_MAX_HOPS", defaultMaxHops),
		MessageTTLMS:   util.EnvInt("PI_COMS_NET_MESSAGE_TTL_MS", defaultMessageTTLMS),
		MaxInbox:       util.EnvInt("PI_COMS_NET_MAX_INBOX", defaultMaxInbox),
		HeartbeatMS:    util.EnvInt("PI_COMS_NET_HEARTBEAT_MS", defaultHeartbeatMS),
		StaleAfterMS:   util.EnvInt("PI_COMS_NET_STALE_AFTER_MS", defaultStaleAfterMS),
		OfflineAfterMS: util.EnvInt("PI_COMS_NET_OFFLINE_AFTER_MS", defaultOfflineAfterMS),
	}

	// Parse flags.
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--host":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("--host requires a value")
			}
			cfg.Host = args[i]
		case "--port":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("--port requires a value")
			}
			n, err := strconv.Atoi(args[i])
			if err != nil {
				return nil, fmt.Errorf("--port: %w", err)
			}
			cfg.Port = n
		case "--project":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("--project requires a value")
			}
			cfg.Project = args[i]
		case "--public-url":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("--public-url requires a value")
			}
			cfg.PublicURL = args[i]
		case "--secret-path":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("--secret-path requires a value")
			}
			cfg.SecretPath = args[i]
		case "--heartbeat-ms":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("--heartbeat-ms requires a value")
			}
			n, err := strconv.Atoi(args[i])
			if err != nil {
				return nil, fmt.Errorf("--heartbeat-ms: %w", err)
			}
			cfg.HeartbeatMS = n
		case "--no-color":
			cfg.NoColor = true
		case "--help", "-h":
			printServeHelp()
			os.Exit(0)
		default:
			return nil, fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	return cfg, nil
}

func printServeHelp() {
	fmt.Fprintln(os.Stderr, "Usage: coms-go serve [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags:")
	fmt.Fprintln(os.Stderr, "  --host <addr>         Bind address (default: 127.0.0.1; env: PI_COMS_NET_HOST)")
	fmt.Fprintln(os.Stderr, "  --port <n>            Bind port (default: 0/OS-assigned; env: PI_COMS_NET_PORT)")
	fmt.Fprintln(os.Stderr, "  --project <name>      Default project name (default: default; env: PI_COMS_NET_PROJECT)")
	fmt.Fprintln(os.Stderr, "  --public-url <url>    Public base URL (env: PI_COMS_NET_PUBLIC_URL)")
	fmt.Fprintln(os.Stderr, "  --secret-path <path>  Path for server.secret.json")
	fmt.Fprintln(os.Stderr, "  --heartbeat-ms <n>    Heartbeat interval hint (env: PI_COMS_NET_HEARTBEAT_MS)")
	fmt.Fprintln(os.Stderr, "  --no-color            Disable ANSI color output")
}

// Run is the entrypoint for the serve subcommand.
func Run(args []string) error {
	cfg, err := ParseConfig(args)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}

	// Init log globals.
	logQuiet = os.Getenv("PI_COMS_NET_LOG_QUIET") == "1"
	logHeartbeatEnabled = os.Getenv("PI_COMS_NET_LOG_HEARTBEAT") == "1"
	noColor := cfg.NoColor || os.Getenv("NO_COLOR") != ""
	tty := isTerminal(os.Stdout) && !noColor
	initColors(tty)

	// Token policy (matches TS server verbatim).
	if cfg.Token == "" {
		if !isLoopback(cfg.Host) {
			fmt.Fprintf(os.Stderr,
				"coms-net: refusing to bind %s without an explicit PI_COMS_NET_AUTH_TOKEN.\n",
				cfg.Host)
			os.Exit(1)
		}
		tok, err := generateToken()
		if err != nil {
			return fmt.Errorf("generate token: %w", err)
		}
		cfg.Token = tok
		cfg.TokenOwned = true
	}

	// Set up project dir and file paths.
	regRoot := filepath.Join(os.Getenv("HOME"), ".pi", "coms-net")
	projectDir := filepath.Join(regRoot, "projects", cfg.Project)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", projectDir, err)
	}
	serverJSONPath := filepath.Join(projectDir, "server.json")
	var secretPath string
	if cfg.SecretPath != "" {
		secretPath = cfg.SecretPath
	} else if cfg.TokenOwned {
		secretPath = filepath.Join(projectDir, "server.secret.json")
	}
	cfg.SecretPath = secretPath

	// Write server.secret.json if we own the token.
	if cfg.TokenOwned && secretPath != "" {
		if err := writeServerSecret(secretPath, cfg.Token); err != nil {
			return fmt.Errorf("write server.secret.json: %w", err)
		}
	}

	// Bind listener.
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	claimedPort := ln.Addr().(*net.TCPAddr).Port

	localHost := cfg.Host
	if localHost == "0.0.0.0" || localHost == "::" {
		localHost = "127.0.0.1"
	}
	localURL := fmt.Sprintf("http://%s:%d", localHost, claimedPort)
	publicURL := cfg.PublicURL
	if publicURL == "" {
		publicURL = localURL
	}

	// Write server.json.
	sj := proto.ServerJson{
		Version:   1,
		Project:   cfg.Project,
		Pid:       os.Getpid(),
		Host:      cfg.Host,
		Port:      claimedPort,
		LocalURL:  localURL,
		PublicURL: publicURL,
		StartedAt: "", // filled below
		ServerID:  "", // filled below
	}

	st := newServerState()
	sj.StartedAt = st.startedAt
	sj.ServerID = st.serverID

	sjData, _ := json.MarshalIndent(sj, "", "  ")
	if err := util.AtomicWrite(serverJSONPath, sjData, 0); err != nil {
		return fmt.Errorf("write server.json: %w", err)
	}

	// Boot banner.
	BootBanner(localURL, cfg.Project, serverJSONPath, func() string {
		if cfg.TokenOwned {
			return secretPath
		}
		return ""
	}(), os.Getpid())

	// Wire routes.
	mux := http.NewServeMux()
	registerRoutes(mux, st, cfg)

	// WriteTimeout is intentionally 0: SSE streams (/v1/stream) hold connections open
	// indefinitely; per-request deadlines come via context cancellation. ReadHeaderTimeout
	// guards against slowloris; IdleTimeout caps keep-alive connections.
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}

	// Unlink helper (idempotent).
	var unlinked bool
	unlinkStateFiles := func() {
		if unlinked {
			return
		}
		unlinked = true
		os.Remove(serverJSONPath)
		if cfg.TokenOwned && secretPath != "" {
			os.Remove(secretPath)
		}
	}

	// Context for graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handler.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		unlinkStateFiles()
		fmt.Printf("coms-net: %s received, shutting down\n", sig)
		// Notify all streams.
		for projectName, p := range st.allProjects() {
			p.mu.Lock()
			for sid, entry := range p.agents {
				broadcast(p, "agent_left", map[string]any{
					"project":    projectName,
					"session_id": sid,
					"name":       entry.Name,
					"reason":     "shutdown",
				}, sid)
			}
			for _, w := range p.streams {
				close(w.done)
			}
			p.streams = make(map[string]*SseWriter)
			p.mu.Unlock()
		}
		cancel()
		srv.Shutdown(context.Background()) //nolint:errcheck
	}()

	// Start background tickers.
	startTickers(ctx, st, cfg)

	// Serve.
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		unlinkStateFiles()
		return fmt.Errorf("serve: %w", err)
	}
	unlinkStateFiles()
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func isLoopback(host string) bool {
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

// isTerminal reports whether f is a terminal (TTY).
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
