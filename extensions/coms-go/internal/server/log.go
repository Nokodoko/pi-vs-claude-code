// Package server implements the coms-net HTTP/SSE hub (replaces scripts/coms-net-server.ts).
package server

import (
	"fmt"
	"strings"
	"time"
)

// Color codes — emitted only when stdout is a TTY and NO_COLOR is unset.
// Mirrors the TS server's color constants verbatim.
var (
	cDim    string
	cReset  string
	cGreen  string
	cCyan   string
	cYellow string
	cRed    string
	cPink   string
	cBlue   string
)

func initColors(tty bool) {
	if tty {
		cDim = "\x1b[2m"
		cReset = "\x1b[0m"
		cGreen = "\x1b[32m"
		cCyan = "\x1b[36m"
		cYellow = "\x1b[33m"
		cRed = "\x1b[31m"
		cPink = "\x1b[95m"
		cBlue = "\x1b[34m"
	}
}

func logLine(symbol, color, kind, detail string) {
	if logQuiet {
		return
	}
	t := time.Now().UTC().Format("15:04:05.000")
	padded := fmt.Sprintf("%-10s", kind)
	fmt.Printf("%s%s%s  %s%s%s %s%s%s %s\n",
		cDim, t, cReset,
		color, symbol, cReset,
		color, padded, cReset,
		detail)
}

func dim(s string) string {
	return cDim + s + cReset
}

func tail6(id string) string {
	if len(id) > 6 {
		return id[len(id)-6:]
	}
	return id
}

func logRegister(name, project, sid string, isReregister bool) {
	verb := "register"
	symbol := "✓"
	color := cGreen
	if isReregister {
		verb = "re-register"
		symbol = "↻"
	}
	logLine(symbol, color, verb, name+"@"+project+" "+dim("sid=…"+tail6(sid)))
}

func logUnregister(name, reason string) {
	logLine("✗", cRed, "unregister", name+" "+dim("reason="+reason))
}

func logSseOpen(name string, totalStreams int) {
	streams := "streams"
	if totalStreams == 1 {
		streams = "stream"
	}
	logLine("⇄", cCyan, "sse-open", name+" "+dim(fmt.Sprintf("(%d %s)", totalStreams, streams)))
}

func logSseClose(name, reason string) {
	logLine("⇄", cDim, "sse-close", name+" "+dim("reason="+reason))
}

func logMessageSend(sender, target, msgID, prompt string, hops int, delivered bool) {
	runes := []rune(prompt)
	preview := prompt
	if len(runes) > 50 {
		preview = string(runes[:47]) + "…"
	}
	safePreview := strings.ReplaceAll(preview, "\n", " ⏎ ")
	status := dim("delivered")
	if !delivered {
		status = dim("queued")
	}
	logLine("→", cPink, "message",
		fmt.Sprintf(`%s → %s %s "%s" %s %s`,
			sender, target, dim(tail6(msgID)), safePreview, dim(fmt.Sprintf("hops=%d", hops)), status))
}

func logResponse(responder, sender, msgID string, isError bool, errStr string, size int) {
	var status string
	color := cGreen
	if isError {
		status = cRed + "error=" + errStr + cReset
		color = cRed
	} else {
		status = dim(fmt.Sprintf("%dc", size))
	}
	logLine("←", color, "response",
		fmt.Sprintf("%s → %s %s %s", responder, sender, dim(tail6(msgID)), status))
}

func logStale(name string, dtSec int) {
	logLine("⚠", cYellow, "stale", name+" "+dim(fmt.Sprintf("(%ds since last heartbeat)", dtSec)))
}

func logOffline(name string) {
	logLine("⌛", cRed, "offline", name+" "+dim("removed (no heartbeat)"))
}

func logExpired(msgID string) {
	logLine("⏱", cYellow, "expired", dim(tail6(msgID)))
}

func logHeartbeat(name string, pct, depth int) {
	if !logHeartbeatEnabled {
		return
	}
	logLine("♥", cBlue, "heartbeat", name+" "+dim(fmt.Sprintf("ctx=%d%% queue=%d", pct, depth)))
}

func logRejected(reason, detail string) {
	logLine("✗", cYellow, "rejected", reason+" "+dim(detail))
}

// BootBanner emits the startup banner to stdout, matching the TS server format exactly.
// The bearer token is NEVER included — only the path to server.secret.json (if owned).
// initColors must be called before BootBanner.
func BootBanner(localURL, project, serverJSONPath, secretPath string, pid int) {
	fmt.Printf("%scoms-net%s: listening on %s%s%s\n", cCyan, cReset, cCyan, localURL, cReset)
	fmt.Printf("%s          project=%s pid=%d%s\n", cDim, project, pid, cReset)
	fmt.Printf("%s          server.json=%s%s\n", cDim, serverJSONPath, cReset)
	if secretPath != "" {
		fmt.Printf("%s          server.secret.json=%s (chmod 0600)%s\n", cDim, secretPath, cReset)
	} else {
		fmt.Printf("%s          using token from PI_COMS_NET_AUTH_TOKEN%s\n", cDim, cReset)
	}
	if !logQuiet {
		fmt.Printf("%s          ─── events below (Ctrl-C to quit, set PI_COMS_NET_LOG_HEARTBEAT=1 for heartbeat noise) ───%s\n", cDim, cReset)
	}
}
