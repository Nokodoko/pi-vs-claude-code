// coms-go — native Go replacement for extensions/coms.ts, extensions/coms-net.ts,
// and scripts/coms-net-server.ts. Single static binary, multi-mode dispatch.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/pi-vs-cc/coms-go/internal/localclient"
	"github.com/pi-vs-cc/coms-go/internal/netclient"
	"github.com/pi-vs-cc/coms-go/internal/server"
)

const version = "v1.0.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "version":
		fmt.Printf("coms-go %s\n", version)
	case "serve":
		// T3: replaces scripts/coms-net-server.ts
		if err := server.Run(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "coms-go serve: %v\n", err)
			os.Exit(1)
		}
	case "client-local":
		runLocalClient(os.Args[2:])
	case "client-net":
		runNetClient(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "coms-go: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func runLocalClient(args []string) {
	fs := flag.NewFlagSet("client-local", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: coms-go client-local [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Runs the local Unix-socket P2P client (replaces extensions/coms.ts).")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}

	cfg := localclient.DefaultConfig()

	name := fs.String("name", cfg.Name, "agent display name")
	purpose := fs.String("purpose", cfg.Purpose, "agent purpose/role")
	project := fs.String("project", cfg.Project, "project path (used to scope registry)")
	color := fs.String("color", cfg.Color, "agent hex color (e.g. #ff6600)")
	explicit := fs.Bool("explicit", cfg.Explicit, "explicit/trusted mode")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	// CLI flags override env-sourced defaults when explicitly set.
	if *name != "" {
		cfg.Name = *name
	}
	if *purpose != "" {
		cfg.Purpose = *purpose
	}
	if *project != "" {
		cfg.Project = *project
	}
	if *color != "" {
		cfg.Color = *color
	}
	cfg.Explicit = *explicit

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := localclient.Run(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "coms-go client-local: %v\n", err)
		os.Exit(1)
	}
}

func runNetClient(args []string) {
	fs := flag.NewFlagSet("client-net", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: coms-go client-net [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Runs the networked HTTP+SSE client (replaces extensions/coms-net.ts).")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}

	cfg := netclient.DefaultConfig()

	name := fs.String("name", cfg.Name, "agent display name")
	purpose := fs.String("purpose", cfg.Purpose, "agent purpose/role")
	project := fs.String("project", cfg.Project, "project path (used to scope messages)")
	color := fs.String("color", cfg.Color, "agent hex color (e.g. #ff6600)")
	explicit := fs.Bool("explicit", cfg.Explicit, "explicit/trusted mode")
	serverURL := fs.String("server-url", cfg.ServerURL, "coms-net hub URL (e.g. https://hub.example.com)")
	authToken := fs.String("auth-token", cfg.AuthToken, "bearer auth token for hub (default: read from server.secret.json)")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if *name != "" {
		cfg.Name = *name
	}
	if *purpose != "" {
		cfg.Purpose = *purpose
	}
	if *project != "" {
		cfg.Project = *project
	}
	if *color != "" {
		cfg.Color = *color
	}
	cfg.Explicit = *explicit
	if *serverURL != "" {
		cfg.ServerURL = *serverURL
	}
	if *authToken != "" {
		cfg.AuthToken = *authToken
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := netclient.Run(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "coms-go client-net: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: coms-go <subcommand> [args...]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  serve         Run the coms-net hub server (replaces coms-net-server.ts)")
	fmt.Fprintln(os.Stderr, "  client-local  Run the local Unix-socket client (replaces coms.ts)")
	fmt.Fprintln(os.Stderr, "  client-net    Run the networked SSE client (replaces coms-net.ts)")
	fmt.Fprintln(os.Stderr, "  version       Print version and exit")
}
