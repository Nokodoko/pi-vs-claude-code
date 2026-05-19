// coms-go — native Go replacement for extensions/coms.ts, extensions/coms-net.ts,
// and scripts/coms-net-server.ts. Single static binary, multi-mode dispatch.
package main

import (
	"fmt"
	"os"

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
		// T4: replaces extensions/coms.ts runtime logic
		unimplemented("client-local")
	case "client-net":
		// T4: replaces extensions/coms-net.ts runtime logic
		unimplemented("client-net")
	default:
		fmt.Fprintf(os.Stderr, "coms-go: unknown subcommand %q\n", os.Args[1])
		usage()
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

func unimplemented(cmd string) {
	fmt.Fprintf(os.Stderr, "coms-go: %q is not yet implemented (see task T3/T4)\n", cmd)
	os.Exit(2)
}
