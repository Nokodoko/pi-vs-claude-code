# coms_go_port

Migration spec: port `extensions/coms.ts` (Unix-socket P2P) and `extensions/coms-net.ts` (HTTP/SSE client) — plus the supporting `scripts/coms-net-server.ts` hub — from TypeScript/Bun to native Go under `extensions/coms-go/`, preserving the wire protocol byte-for-byte while replacing the Bun runtime with a single static `coms-go` binary built from Go stdlib `net/http` + goroutines.

Replaces [`extensions/coms.ts`](../../extensions/coms.ts) + [`extensions/coms-net.ts`](../../extensions/coms-net.ts) + [`scripts/coms-net-server.ts`](../../scripts/coms-net-server.ts) in the pi-vs-claude-code playground with a purpose-built Go implementation, in service of the global `~/.claude/CLAUDE.md` rule: *"Default language is Go. All scripts, hooks, automation, and new code must be Go. Do not use Python or Bash unless explicitly requested."*

> **MIGRATION CONTRACT**
>
> Behavioral parity IS the acceptance bar.
> - DO NOT add features, endpoints, or wire fields the TS version lacks
> - DO NOT change route paths, JSON shapes, status codes, header names, SSE event names, or error strings
> - DO NOT alter ULID format, fallback color palette, hop limits, timeout defaults, or env-var names
> - The TS server in `scripts/coms-net-server.ts` is the byte-level reference for `coms-go serve`
> - The TS extensions in `extensions/coms.ts` and `extensions/coms-net.ts` are the byte-level reference for `coms-go client-local` and `coms-go client-net`
> - When in doubt, diff the JSON output of `coms-go` against the TS server using the golden fixtures captured in T5
> - After each task, verify `go vet ./...` and `go test ./...` exit 0; if either fails, fix before continuing

---

## Outputs

The following are the gating, testable acceptance objectives for this migration. Every item is a shell-checkable predicate (mirrored in Section 19 as a checkbox list).

- The Go module rooted at `extensions/coms-go/` compiles cleanly. Run from the repository root: `(cd extensions/coms-go && go build ./...)` — zero warnings, zero errors. (The module is nested; top-level invocations like `go build ./extensions/coms-go/...` will not work without a `go.work` file, which is intentionally NOT added — every verify command in this spec cd's into the module dir first.)
- `(cd extensions/coms-go && go vet ./...)` returns no findings.
- `(cd extensions/coms-go && go test ./...)` passes; coverage includes (a) happy-path request/response for every HTTP route enumerated in the Routes Comparison Table, (b) the full pi-to-pi handshake round-trip (register → SSE open → send → receive → respond), and (c) at least one auth/permission failure path per route family (missing bearer, wrong bearer, missing project, wrong responder).
- HTTP endpoint surface in `coms-go serve` matches the TS server 1:1 — verified by the Routes Comparison Table inside the spec AND by integration tests that hit each endpoint and diff JSON response shape against golden fixtures captured from `bun scripts/coms-net-server.ts`.
- Pi-to-pi communication round-trip succeeds between two `coms-go` instances (real two-host run OR loopback simulation in CI tagged `//go:build integration`).
- The pi extension shim (`extensions/coms-go/shim.ts`) registers correctly with the pi extension loader; both `coms_*` (local) and `coms_net_*` (networked) tools appear in pi's registered-tool list when launched with `pi -e extensions/coms-go/shim.ts`.
- No `bun`, `node`, `package.json`, or `*.ts` source remains under `extensions/coms-go/` (the TS shim `shim.ts` is the lone allowed exception; it is documented and minimal).
- `extensions/coms.ts`, `extensions/coms-net.ts`, and `scripts/coms-net-server.ts` are deleted only at cutover (task T11) and not before; until then they coexist with `coms-go` for dual-run validation.
- Top-level `README.md` and `justfile` are updated: Bun prereq for `coms`/`coms-net` is replaced with a Go toolchain prereq (`go ≥ 1.23`); `just local-coms`, `just coms`, `just coms-net-server` invoke the Go binary.
- A migration note in this spec (Section 9) explicitly states what callers (other pi instances, the claude-code window) see during cutover — ideally nothing visible beyond a one-time binary swap.

# Outcome

- `coms-go` binary exists, is statically linked, passes `go vet` and `go test`.
- All HTTP routes, SSE events, JSON payloads, and Unix-socket envelopes match the TS reference verbatim.
- Two-host pi-to-pi handshake round-trip succeeds end-to-end.
- Pi extension loader registers the four local + four networked tools.
- TS sources for coms/coms-net are removed; README/justfile point to Go.
- No regression in caller behavior; the swap is invisible to external pi instances and the claude-code window.

---

## 1. Why

The TS implementations of `coms` and `coms-net` work — they ship today and proved out the bidirectional, flat-topology agent communication pattern documented in the project README. But they carry baggage `coms-go` doesn't need:

- **Bun dependency on every pi host.** Pi already pulls in Bun (≥ 1.3.2 per `README.md`) as runtime, but the coms-net hub is a *separate* long-running Bun process (`bun scripts/coms-net-server.ts`) competing with the pi UI for the same runtime. On Raspberry Pi targets this is a 50+ MB resident process for what amounts to a JSON router with timer ticks. A single 8-12 MB statically-linked Go binary eliminates that.
- **Multi-process language footprint on pi hosts.** Pi hosts already run heterogeneous binaries (the pi CLI itself, optional Ollama, optional Datadog Agent, ssh daemons). Adding Bun specifically for coms-net widens the supply-chain attack surface and the supervised-process count. Folding the hub and the local-coms transport into one Go binary that ships as a single artifact reduces both.
- **Two TS files duplicate ~300 LOC of helper code.** `ulid()`, `hexFg()`, `parseFrontmatter()`, `findSystemPromptPath()`, `readFrontmatterFromArgv()`, the `CROCKFORD` table, and the `FALLBACK_PALETTE` are pasted verbatim between `coms.ts` (lines 131-210) and `coms-net.ts` (lines 176-282). A Go module gets these into one `internal/util/` package once.
- **Goroutines fit the workload better than Node's event loop for pi-to-pi.** The TS server holds per-project state in `Map`s and a hand-rolled SSE writer registry; pi-to-pi inherently *is* "N concurrent streams, M concurrent message awaits." Go's `net/http` + goroutines + channels express this in roughly half the LOC, with no async/await sprinkling.
- **Global Anthropic Claude Code policy.** `~/.claude/CLAUDE.md` mandates Go for all new code, scripts, hooks, and automation across the user's environment. Pi extensions on pi-vs-claude-code are downstream of this rule; the coms/coms-net pair is the largest remaining TS surface in the project.

This spec covers exactly the existing `coms` + `coms-net` surface — four local tools, four networked tools, one hub server, identical wire protocol. It does not add, remove, or improve features.

---

## 2. Design Principles

1. **Behavioral parity is the only acceptance bar.** Every route, every field, every status code, every SSE event name, every error string, every default timeout, every env-var name must match the TS reference. New features are out of scope. Improvements are out of scope. The diff against the TS server's JSON fixtures must be empty (whitespace permitted).
2. **Go stdlib only.** Use `net/http`, `net`, `encoding/json`, `crypto/rand`, `crypto/sha256`, `crypto/subtle`, `os`, `os/signal`, `path/filepath`, `time`, `context`, `sync`. No third-party HTTP routers, no `gin`/`echo`/`fiber`, no SSE libraries. `bitfield/script` is permitted only for shell-style pipes in any helper test scripts, not in the binary's hot path.
3. **Single static binary, multi-mode.** One binary, `coms-go`, with subcommands: `serve` (replaces `scripts/coms-net-server.ts`), `client-local` (replaces `extensions/coms.ts` runtime logic), `client-net` (replaces `extensions/coms-net.ts` runtime logic), plus a one-shot `version` subcommand for the install-verification check.
4. **Zero CGO.** Cross-compile for `linux/arm64`, `linux/amd64`, `darwin/arm64`, `darwin/amd64` from any host. The `CGO_ENABLED=0` env var is set in every build invocation.
5. **Observability via stdout log lines matching the TS server's format.** The TS server writes its event lines and boot banner with `console.log` (stdout); the Go server MUST do the same for byte-level parity. Helpers `logRegister`/`logUnregister`/`logSseOpen`/`logSseClose`/`logMessageSend`/`logResponse`/`logStale`/`logOffline`/`logExpired`/`logHeartbeat`/`logRejected` produce one line per event with `HH:MM:SS.sss <symbol> <kind:10> <detail>` format. The Go server emits the same format with the same symbols on stdout. Operators monitoring `journalctl -fu coms-go` (which captures stdout by default for `Type=simple` units) should see no visible difference vs `bun scripts/coms-net-server.ts`. ANSI 24-bit colors are emitted when stdout is a TTY; plain ASCII otherwise. Fatal/panic conditions (bind failures, secret-file read errors) go to stderr. `PI_COMS_NET_LOG_QUIET=1` and `PI_COMS_NET_LOG_HEARTBEAT=1` are honored.
6. **Pi extension contract preserved via a thin TS shim.** Pi loads extensions through its JS runtime; a Go binary cannot register `pi.registerTool`, `pi.on("session_start", ...)`, or `pi.appendEntry()`. The single bridge file `extensions/coms-go/shim.ts` (≤ 200 LOC) registers the eight tools, the slash commands `/coms` and `/coms-net`, and the four pi lifecycle events (`session_start`, `agent_end`, `session_shutdown`, plus identity flags via `pi.registerFlag`). The shim's only job is to marshal tool params to JSON, spawn or talk to the Go binary, marshal results back, and forward pi UI invalidation signals. All transport, state, registry, server, audit logic lives in Go.
7. **Wire envelopes byte-identical.** The Unix-socket envelopes (`prompt`/`response`/`ping`/`pong`/`ack`/`nack`) for `coms-go client-local` use the same JSON field names, ULID format, hop accounting, and 64 KB line cap as `coms.ts`. The HTTP request/response bodies for `coms-go client-net` use the same field names, types, and validation rules as `coms-net.ts` and the TS server. Existing TS instances must be able to talk to a Go peer and vice versa during cutover.
8. **Atomic writes everywhere persistence touches disk.** Registry entries (`~/.pi/coms/projects/<project>/agents/<name>.json`), server.json, and server.secret.json all use the temp-file-then-rename pattern, matching the TS atomic-write semantics.
9. **No prompt bodies in audit logs, ever.** `coms-log` and `coms-net-log` append-only files record event names, msg_id, sender session, hop count, and timestamps. Prompt and response payloads MUST NOT be written. This matches the TS audit semantics and the project README's "Audit log" safety rail.
10. **Bearer tokens never logged, never echoed in errors.** The TS `safeError()` helper strips the bearer from any user-visible error string; the Go equivalent does the same. The boot banner prints the *path* to `server.secret.json` and never the token contents. `crypto/subtle.ConstantTimeCompare` replaces `crypto.timingSafeEqual`.

---

## 3. On-Disk Format

```
pi-vs-claude-code/
├── extensions/
│   ├── coms-go/                    # NEW — entire Go module + thin TS shim
│   │   ├── go.mod                  # module github.com/pi-vs-cc/coms-go ; go 1.23
│   │   ├── go.sum                  # generated; expected empty (stdlib only)
│   │   ├── README.md               # build/run, env vars, dev workflow
│   │   ├── manifest.json           # pi extension manifest (name, version, entry shim.ts)
│   │   ├── shim.ts                 # ≤ 200 LOC TS bridge that pi loads
│   │   ├── cmd/
│   │   │   └── coms-go/
│   │   │       └── main.go         # CLI entrypoint; dispatches to serve/client-local/client-net/version
│   │   ├── internal/
│   │   │   ├── proto/              # Shared types: AgentCard, ComsMessage, Envelope, *Request/Response
│   │   │   │   ├── proto.go
│   │   │   │   └── proto_test.go
│   │   │   ├── util/               # ulid, hex color, frontmatter, fallback palette, abbreviateModel, nowIso
│   │   │   │   ├── ulid.go
│   │   │   │   ├── color.go
│   │   │   │   ├── frontmatter.go
│   │   │   │   └── util_test.go
│   │   │   ├── audit/              # appendEntry equivalent — JSONL append with file lock
│   │   │   │   ├── audit.go
│   │   │   │   └── audit_test.go
│   │   │   ├── registry/           # Local registry I/O (coms.ts lines 240-370)
│   │   │   │   ├── registry.go
│   │   │   │   └── registry_test.go
│   │   │   ├── transport/          # Unix-socket + named-pipe bind/send/probe (coms.ts lines 372-490)
│   │   │   │   ├── transport_unix.go      # build tag: !windows
│   │   │   │   ├── transport_windows.go   # build tag: windows
│   │   │   │   └── transport_test.go
│   │   │   ├── localclient/        # client-local subcommand: replaces coms.ts runtime
│   │   │   │   ├── client.go              # session_start equivalent, ping/keepalive timers
│   │   │   │   ├── handlers.go            # handlePrompt/handleResponse/handlePing
│   │   │   │   ├── tools.go               # IPC handlers for coms_list/send/get/await
│   │   │   │   └── client_test.go
│   │   │   ├── netclient/          # client-net subcommand: replaces coms-net.ts runtime
│   │   │   │   ├── client.go              # register, heartbeat, SSE open + reconnect
│   │   │   │   ├── sse.go                 # hand-rolled SSE parser; mirrors makeSseParser
│   │   │   │   ├── tools.go               # IPC handlers for coms_net_list/send/get/await
│   │   │   │   └── client_test.go
│   │   │   ├── server/             # serve subcommand: replaces scripts/coms-net-server.ts
│   │   │   │   ├── server.go              # router + boot + signal handling
│   │   │   │   ├── handlers.go            # per-route handlers
│   │   │   │   ├── state.go               # ProjectState, ServerState, broadcast helpers
│   │   │   │   ├── loops.go               # staleScanTick, ttlScanTick, keepaliveTick
│   │   │   │   ├── log.go                 # event-line logger (color-aware)
│   │   │   │   └── server_test.go
│   │   │   └── ipc/                # JSON-line stdio protocol between shim.ts and coms-go subcommands
│   │   │       ├── ipc.go
│   │   │       └── ipc_test.go
│   │   └── testdata/
│   │       ├── golden/             # JSON fixtures captured from bun scripts/coms-net-server.ts
│   │       │   ├── health.json
│   │       │   ├── register_resp.json
│   │       │   ├── list_agents_resp.json
│   │       │   ├── send_resp.json
│   │       │   ├── get_message_resp.json
│   │       │   ├── await_complete.json
│   │       │   ├── await_timeout.json
│   │       │   └── sse_pool_snapshot.txt
│   │       └── frontmatter/
│   │           └── sample-agent.md
│   ├── coms.ts                     # DELETED at cutover (T11)
│   ├── coms-net.ts                 # DELETED at cutover (T11)
│   └── ...                         # all other extensions untouched
├── scripts/
│   └── coms-net-server.ts          # DELETED at cutover (T11)
├── justfile                        # MODIFIED — coms recipes invoke coms-go
├── README.md                       # MODIFIED — Bun prereq replaced with Go for coms/coms-net pair
└── SPEC/coms_go_port/
    ├── coms_go_port.md             # THIS SPEC
    └── REVIEWS/
        └── coms_go_port_REVIEW.md  # produced by spec-reviewer in the next phase
```

### `extensions/coms-go/manifest.json`

Pi extension manifest — minimal, points pi's loader at the TS shim:

```json
{
  "name": "coms-go",
  "version": "1.0.0",
  "description": "Native Go implementation of coms + coms-net pi extensions. Tools: coms_{list,send,get,await} + coms_net_{list,send,get,await}.",
  "entry": "shim.ts",
  "binaries": {
    "linux/amd64": "bin/coms-go-linux-amd64",
    "linux/arm64": "bin/coms-go-linux-arm64",
    "darwin/amd64": "bin/coms-go-darwin-amd64",
    "darwin/arm64": "bin/coms-go-darwin-arm64"
  }
}
```

### `extensions/coms-go/shim.ts` (sketch — full ≤ 200 LOC implementation in T6)

The shim is the one TS file that survives. It registers tools and lifecycle hooks with pi, then forwards everything to the Go binary via stdin/stdout JSON-line IPC. No business logic.

```typescript
import type { ExtensionAPI } from "@mariozechner/pi-coding-agent";
import { Type } from "@sinclair/typebox";
import { spawn, type ChildProcess } from "node:child_process";
import * as path from "node:path";

export default function (pi: ExtensionAPI) {
  // 1. Register the same identity flags as coms.ts + coms-net.ts (--name, --purpose, --project,
  //    --color, --explicit, --server-url, --auth-token).
  // 2. On session_start, spawn `coms-go client-local` and `coms-go client-net` as long-lived
  //    children, piping JSON lines over stdin/stdout for tool calls and lifecycle events.
  // 3. Register the eight tools (coms_list/send/get/await + coms_net_list/send/get/await) as
  //    thin wrappers that send a JSON-line IPC request to the appropriate child and return
  //    the JSON reply unchanged.
  // 4. Register /coms and /coms-net slash commands as IPC passthroughs.
  // 5. On session_shutdown / SIGINT / SIGTERM, send a "shutdown" IPC line and wait briefly.
  //
  // FULL IMPLEMENTATION IS PART OF T6. This is the structural sketch only.
}
```

### `~/.pi/coms/projects/<project>/agents/<name>.json`

Registry entry written by `coms-go client-local` (Unix-socket peer discovery). Format is byte-identical to what `coms.ts` writes today:

```json
{
  "session_id": "01HXNJ0E5Q4M7Z2C1V8YR6F3KT",
  "name": "planner",
  "purpose": "Plans the work",
  "model": "claude-opus-4-7",
  "color": "#36F9F6",
  "pid": 481923,
  "endpoint": "/home/n0ko/.pi/coms/sockets/01HXNJ0E5Q4M7Z2C1V8YR6F3KT.sock",
  "cwd": "/home/n0ko/Programs/ai/pi-vs-claude-code",
  "started_at": "2026-05-19T14:32:11.482Z",
  "explicit": false,
  "version": 1,
  "context_used_pct": 12,
  "queue_depth": 0,
  "heartbeat_at": "2026-05-19T14:32:41.531Z"
}
```

### `~/.pi/coms-net/projects/<project>/server.json`

Written by `coms-go serve` at boot, deleted at shutdown. Byte-identical to the TS server's output:

```json
{
  "version": 1,
  "project": "default",
  "pid": 482104,
  "host": "127.0.0.1",
  "port": 43219,
  "local_url": "http://127.0.0.1:43219",
  "public_url": "http://127.0.0.1:43219",
  "started_at": "2026-05-19T14:32:09.103Z",
  "server_id": "01HXNJ0D6Y3J8KAQ9XBVMP4ZTC"
}
```

### `~/.pi/coms-net/projects/<project>/server.secret.json` (mode 0600)

Written only when `coms-go serve` generates a fresh token (loopback bind without `PI_COMS_NET_AUTH_TOKEN` in env). Bun/Go produce identical content:

```json
{
  "token": "8c7e3a91f02b46d5a1e8f6c9d2b4e7f1a3c5d7e9b1f3a5c7d9e1b3f5a7c9e1d"
}
```

### `~/.pi/coms-log` and `~/.pi/coms-net-log`

JSONL append-only audit files. Same key shape as the TS `pi.appendEntry()` writes. Example lines:

```jsonl
{"event":"boot","session_id":"01HXNJ0E5Q4M7Z2C1V8YR6F3KT","name":"planner","project":"default","ts":"2026-05-19T14:32:11.482Z"}
{"event":"outbound_prompt","msg_id":"01HXNJ0F2N8K3LM4P5Q6R7S8TV","target":"coder","hops":0,"ts":"2026-05-19T14:32:18.917Z"}
{"event":"inbound_prompt","msg_id":"01HXNJ0G5M1H7P3N9Q2R4S6T8W","sender":"01HXNJ0C9P3M4L8Q5R6S7T2V1W","hops":1,"ts":"2026-05-19T14:32:21.043Z"}
{"event":"shutdown","session_id":"01HXNJ0E5Q4M7Z2C1V8YR6F3KT","ts":"2026-05-19T14:33:45.221Z"}
```

---

## 4. Data Model

### Envelope (Unix-socket / named-pipe transport — used by `client-local`)

TypeScript reference (`coms.ts` lines 44-73):

```typescript
interface Envelope {
  type: "prompt" | "response" | "ping";
  msg_id: string;
  sender_session: string;
  sender_endpoint: string;
  hops: number;
  timestamp: string;
}

interface PromptEnvelope extends Envelope {
  type: "prompt";
  prompt: string;
  sender_name: string;
  sender_cwd: string;
  conversation_id?: string | null;
  response_schema?: object | null;
}

interface ResponseEnvelope extends Envelope {
  type: "response";
  response: any;
  error?: string | null;
}

interface PingEnvelope extends Envelope {
  type: "ping";
}
```

Go target (`internal/proto/proto.go`) — JSON tags preserve wire field names verbatim:

```go
type Envelope struct {
    Type            string `json:"type"`              // "prompt" | "response" | "ping"
    MsgID           string `json:"msg_id"`
    SenderSession   string `json:"sender_session"`
    SenderEndpoint  string `json:"sender_endpoint"`
    Hops            int    `json:"hops"`
    Timestamp       string `json:"timestamp"`
}

type PromptEnvelope struct {
    Envelope
    Prompt          string          `json:"prompt"`
    SenderName      string          `json:"sender_name"`
    SenderCwd       string          `json:"sender_cwd"`
    ConversationID  *string         `json:"conversation_id,omitempty"`
    ResponseSchema  json.RawMessage `json:"response_schema,omitempty"`
}

type ResponseEnvelope struct {
    Envelope
    Response        json.RawMessage `json:"response"`
    Error           *string         `json:"error,omitempty"`
}

type PingEnvelope struct {
    Envelope
}

type AckMessage struct {
    Type  string `json:"type"`            // "ack" | "nack"
    MsgID string `json:"msg_id"`
    Error string `json:"error,omitempty"` // populated only on nack
}

type Pong struct {
    Type      string    `json:"type"`     // "pong"
    MsgID     string    `json:"msg_id"`
    AgentCard AgentCard `json:"agent_card"`
}
```

### AgentCard (shared by client-local AND client-net)

TypeScript reference (`coms.ts` lines 75-82 and `coms-net.ts` lines 61-75 — note: `coms-net` adds `session_id`, `cwd`, `project`, `explicit`, `started_at`, `provider?`, `status`):

```typescript
// Local-mode (coms.ts)
interface AgentCardLocal {
  name: string;
  purpose: string;
  model: string;
  color: string;
  context_used_pct: number;
  queue_depth: number;
}

// Network-mode (coms-net.ts) — superset
interface AgentCardNet {
  session_id: string;
  name: string;
  purpose: string;
  model: string;
  provider?: string;
  color: string;
  cwd: string;
  project: string;
  explicit: boolean;
  started_at: string;     // ISO 8601
  context_used_pct: number;
  queue_depth: number;
  status: "online" | "stale" | "offline";
}
```

Go target — TWO types, one per mode, with identical JSON wire shape (no merging):

```go
type AgentCardLocal struct {
    Name           string `json:"name"`
    Purpose        string `json:"purpose"`
    Model          string `json:"model"`
    Color          string `json:"color"`
    ContextUsedPct int    `json:"context_used_pct"`
    QueueDepth     int    `json:"queue_depth"`
}

type AgentStatus string
const (
    StatusOnline  AgentStatus = "online"
    StatusStale   AgentStatus = "stale"
    StatusOffline AgentStatus = "offline"
)

type AgentCard struct {
    SessionID       string      `json:"session_id"`
    Name            string      `json:"name"`
    Purpose         string      `json:"purpose"`
    Model           string      `json:"model"`
    Provider        string      `json:"provider,omitempty"`
    Color           string      `json:"color"`
    Cwd             string      `json:"cwd"`
    Project         string      `json:"project"`
    Explicit        bool        `json:"explicit"`
    StartedAt       string      `json:"started_at"`
    ContextUsedPct  int         `json:"context_used_pct"`
    QueueDepth      int         `json:"queue_depth"`
    Status          AgentStatus `json:"status"`
}
```

### RegistryEntry (local mode, `~/.pi/coms/projects/<project>/agents/<name>.json`)

TypeScript reference (`coms.ts` lines 89-106):

```typescript
interface RegistryEntry {
  session_id: string;
  name: string;
  purpose: string;
  model: string;
  color: string;
  pid: number;
  endpoint: string;
  cwd: string;
  started_at: string;
  explicit: boolean;
  version: number;
  context_used_pct?: number;   // populated by heartbeat
  queue_depth?: number;
  heartbeat_at?: string;
}
```

Go target:

```go
type RegistryEntry struct {
    SessionID      string `json:"session_id"`
    Name           string `json:"name"`
    Purpose        string `json:"purpose"`
    Model          string `json:"model"`
    Color          string `json:"color"`
    Pid            int    `json:"pid"`
    Endpoint       string `json:"endpoint"`
    Cwd            string `json:"cwd"`
    StartedAt      string `json:"started_at"`
    Explicit       bool   `json:"explicit"`
    Version        int    `json:"version"`
    ContextUsedPct *int   `json:"context_used_pct,omitempty"`
    QueueDepth     *int   `json:"queue_depth,omitempty"`
    HeartbeatAt    string `json:"heartbeat_at,omitempty"`
}
```

### ComsMessage (server-side state, `coms-net-server.ts` lines 161-177)

```typescript
interface ComsMessage {
  msg_id: string;
  project: string;
  sender_session: string;
  target_session: string;
  prompt: string;
  conversation_id: string | null;
  response_schema: object | null;
  hops: number;
  status: "queued" | "delivered" | "complete" | "error" | "timeout";
  response?: any;
  error?: string | null;
  created_at: string;
  delivered_at?: string;
  completed_at?: string;
  expires_at: string;
}
```

Go target:

```go
type MessageStatus string
const (
    StatusQueued    MessageStatus = "queued"
    StatusDelivered MessageStatus = "delivered"
    StatusComplete  MessageStatus = "complete"
    StatusError     MessageStatus = "error"
    StatusTimeout   MessageStatus = "timeout"
)

type ComsMessage struct {
    MsgID          string          `json:"msg_id"`
    Project        string          `json:"project"`
    SenderSession  string          `json:"sender_session"`
    TargetSession  string          `json:"target_session"`
    Prompt         string          `json:"prompt"`
    ConversationID *string         `json:"conversation_id"`
    ResponseSchema json.RawMessage `json:"response_schema"`
    Hops           int             `json:"hops"`
    Status         MessageStatus   `json:"status"`
    Response       json.RawMessage `json:"response,omitempty"`
    Error          *string         `json:"error,omitempty"`
    CreatedAt      string          `json:"created_at"`
    DeliveredAt    string          `json:"delivered_at,omitempty"`
    CompletedAt    string          `json:"completed_at,omitempty"`
    ExpiresAt      string          `json:"expires_at"`
}
```

### HTTP Request/Response shapes (server endpoints)

Mirror `coms-net-server.ts` lines 179-231 verbatim. Each exists in Go as a struct with matching JSON tags. Full list (see Routes Comparison Table for endpoint-to-struct mapping): `RegisterRequest`, `RegisterResponse`, `HeartbeatRequest`, `SendRequest`, `SendResponse`, `ResponseSubmitRequest`, `ErrorResponse`, `HealthResponse`, `MessageStatusResponse`.

### ID Generation

- **Format**: 26-char Crockford-base32 ULID (timestamp prefix + 80-bit randomness)
- **Source alphabet**: `"0123456789ABCDEFGHJKMNPQRSTVWXYZ"` — IDENTICAL to TS reference (`coms.ts` line 129, `coms-net.ts` line 176, `coms-net-server.ts` line 264).
- **Collision handling**: 80 bits of entropy → effectively zero collision risk for the message and session counts these systems handle; no dedup logic.
- **Rationale**: Matches the TS implementation's wire output, lexicographically sortable by time, URL-safe, no ambiguous characters.

### Lifecycle: Inbound message (server perspective)

```
queued ──> delivered ──> complete
   │           │             │
   │           │             └──> (terminal; releaseAwaiters fires)
   │           └────────────────> error  (terminal; sender notified)
   └─────────────────> timeout (terminal; TTL scan; sender notified)
```

### Lifecycle: SSE connection (client perspective)

```
                  ┌──────────────────────────────────────┐
                  │                                      ▼
session_start ─> register ─> open_sse ─> read_loop ─> disconnect
                              ▲                            │
                              │                            ▼
                              └─── exponential_backoff ─ reconnect_scheduled
                                   (500 ms → 10 s cap)
```

### Status enum (network-mode AgentCard)

| Value | Meaning |
|-------|---------|
| `online` | Heartbeat received within `STALE_AFTER_MS` (30 s default) |
| `stale` | Heartbeat last seen between 30 s and 60 s ago; emit `agent_stale` |
| `offline` | Heartbeat last seen > `OFFLINE_AFTER_MS` (60 s default); remove from registry, broadcast `agent_left` |

### Message status enum

| Value | Meaning |
|-------|---------|
| `queued` | Server accepted; target's SSE stream not yet open |
| `delivered` | Server pushed `prompt` event on target's SSE stream |
| `complete` | Target submitted response; `releaseAwaiters` fired |
| `error` | Target submitted response with `error` field set |
| `timeout` | TTL exceeded before completion; `expires_at` enforced by `ttlScanTick` |

---

## 5. CLI

Binary name: `coms-go` (matches the directory name; "coms-go" reads naturally in shell history vs the ambiguous `coms`).

### Subcommands

```
coms-go serve                            Start the HTTP/SSE hub (replaces scripts/coms-net-server.ts).
  --host <addr>            default 127.0.0.1   PI_COMS_NET_HOST env override.
  --port <n>               default 0 (OS-claimed)  PI_COMS_NET_PORT env override.
  --project <name>         default "default"   PI_COMS_NET_PROJECT env override.
  --public-url <url>                            PI_COMS_NET_PUBLIC_URL env override.
                                                Token policy:
                                                  * PI_COMS_NET_AUTH_TOKEN set -> use it
                                                  * Loopback bind without env token -> generate + write server.secret.json (0600)
                                                  * Non-loopback bind without env token -> exit 1
coms-go client-local                     Long-lived client for the Unix-socket coms protocol.
                                         Reads identity from --name/--purpose/--project/--color/--explicit
                                         flags AND from --system-prompt frontmatter (same precedence as coms.ts).
                                         Speaks JSON-line IPC over stdin/stdout to the pi shim.
  --name <s>                                    CLI > frontmatter > "agent-<sid6>"
  --purpose <s>
  --project <name>         default "default"
  --color <#rrggbb>                             CLI > frontmatter > sha256(session_id) palette
  --explicit                                    bool; hides from auto-discovery

coms-go client-net                       Long-lived client for the HTTP/SSE coms-net protocol.
                                         Same identity flags as client-local, plus:
  --server-url <url>                            PI_COMS_NET_SERVER_URL env or server.json fallback
  --auth-token <tok>                            PI_COMS_NET_AUTH_TOKEN env or server.secret.json fallback (mode 0600 required)

coms-go version                          Print "coms-go vX.Y.Z (commit <short>)" and exit 0.
                                         Used by the install-verification check.
```

### Behavior notes

- `serve` exits 0 on SIGINT/SIGTERM after unlinking `server.json` and (if owned) `server.secret.json`.
- `client-local` exits 0 on EOF on stdin (shim closed the pipe) or SIGINT/SIGTERM; cleans up the registry entry and socket file.
- `client-net` exits 0 on EOF on stdin or SIGINT/SIGTERM; sends `DELETE /v1/agents/<sid>` to the hub on the way out with `SHUTDOWN_DELETE_TIMEOUT_MS=2000` cap, matching `coms-net.ts`.
- No `--json` flag: all stdout output is structured by design.
  - For `serve`: stdout carries the boot banner AND the human-readable event log (matching the TS server's `console.log`); stderr carries only fatal/panic conditions (bind failures, unrecoverable I/O errors). This is the byte-level parity contract — supervisors capturing TS server stdout must capture Go server stdout identically.
  - For `client-local` / `client-net`: stdout is reserved for JSON-line IPC framing to the shim; the human-readable event log goes to stderr. (Routing logs to stdout in client mode would corrupt the IPC stream — this is the only place where the Go binary diverges from "logs to stdout" and it diverges out of necessity, not preference.)
- `NO_COLOR` env is honored for all log lines regardless of stream.

---

## 6. JSON Output Format

`coms-go serve` mirrors the TS server's response shapes byte-for-byte. Every response shape is captured as a golden fixture under `extensions/coms-go/testdata/golden/` in T5.

### Health (no auth)

```json
{ "ok": true, "version": 1, "server_id": "01HXNJ0D6Y3J8KAQ9XBVMP4ZTC", "started_at": "2026-05-19T14:32:09.103Z" }
```

### Register success

```json
{
  "ok": true,
  "agent": {
    "session_id": "01HXNJ0E5Q4M7Z2C1V8YR6F3KT",
    "name": "planner",
    "purpose": "Plans the work",
    "model": "claude-opus-4-7",
    "color": "#36F9F6",
    "cwd": "/home/n0ko/Programs/ai/pi-vs-claude-code",
    "project": "default",
    "explicit": false,
    "started_at": "2026-05-19T14:32:11.482Z",
    "context_used_pct": 0,
    "queue_depth": 0,
    "status": "online"
  },
  "heartbeat_interval_ms": 10000,
  "sse_url": "/v1/events?project=default&session_id=01HXNJ0E5Q4M7Z2C1V8YR6F3KT"
}
```

### List agents

```json
{
  "agents": [
    { "session_id": "...", "name": "coder", "status": "online", "context_used_pct": 14, "queue_depth": 0, "...": "..." }
  ]
}
```

### Send message success

```json
{ "ok": true, "msg_id": "01HXNJ0F2N8K3LM4P5Q6R7S8TV", "status": "delivered", "target_session": "01HXNJ0G5M1H7P3N9Q2R4S6T8W" }
```

### Get message

```json
{ "msg_id": "01HXNJ0F2N8K3LM4P5Q6R7S8TV", "status": "complete", "response": "...", "error": null }
```

### Await message (long-poll)

```json
{ "msg_id": "01HXNJ0F2N8K3LM4P5Q6R7S8TV", "status": "complete", "response": "...", "error": null }
```

### Error envelope (universal)

```json
{ "ok": false, "error": "target_not_found", "details": { "target": "ghost-agent" } }
```

Error strings — verbatim list from `coms-net-server.ts`: `invalid_json`, `invalid_request`, `invalid_url`, `unauthorized`, `not_found`, `agent_not_found`, `sender_not_registered`, `missing_session_id`, `missing_target`, `target_not_found`, `ambiguous_target`, `hop_limit_exceeded`, `inbox_full`, `message_not_found`, `not_target`, `already_terminal`, `method_not_allowed`. Go MUST emit these literal strings.

### SSE frames

Frame format (matches `sseFrame()` in `coms-net-server.ts` line 357):

```
event: <name>
id: <numeric monotonic>
data: <json>
\n
```

Event names — verbatim: `hello`, `pool_snapshot`, `agent_joined`, `agent_updated`, `agent_stale`, `agent_left`, `prompt`, `response`, `message_status`, `server_ping`, `error`.

### IPC stdout protocol (shim ↔ coms-go client-{local,net})

Newline-delimited JSON, one frame per line:

```json
{ "kind": "tool_request", "id": "req-1", "tool": "coms_list", "params": { "project": "default" } }
{ "kind": "tool_response", "id": "req-1", "ok": true, "content": [{"type":"text","text":"2 peer(s): ..."}], "details": { "agents": [...] } }
{ "kind": "event", "name": "agent_joined", "data": { "session_id": "...", "name": "coder" } }
{ "kind": "shutdown" }
```

---

## 7. Concurrency Model

### Server (`coms-go serve`)

Pattern: one accept goroutine (`http.Server`) + per-request goroutines (Go's default `net/http` handler-per-request model) + three background tickers managed by `time.NewTicker` in dedicated goroutines.

```
+------------------------+
|  http.Server.Serve()   |  one goroutine; spawns handler goroutine per request
+-----------+------------+
            |
            v
+-----------+------------+      +------------------------+
|  router (handlers.go)  |----->|  ProjectState (state.go)|  shared, guarded by sync.RWMutex
+-----------+------------+      +-----------+------------+
            |                               ^
            v                               |
+-----------+------------+                  |
|  SSE writer (stream)   |<-----------------+  broadcast() iterates streams under RLock
+------------------------+

Background goroutines (loops.go):
  staleScanTick      every 5  s   -> mark stale/offline, broadcast agent_left/agent_stale
  ttlScanTick        every 10 s   -> expire messages, releaseAwaiters
  keepaliveTick      every 15 s   -> SSE comment ping ": ping <iso>\n\n"
```

Shared state — `ServerState.projects` is a `map[string]*ProjectState`. Each `ProjectState` carries its own `sync.RWMutex`. All reads use RLock; all writes use Lock. `awaiters` and `streams` maps are guarded by the same mutex as `agents` and `messages` for that project (minimizes lock granularity churn vs separate locks).

Long-poll await (`GET /v1/messages/:id/await`) uses a `context.Context` with deadline + a `chan ComsMessage` placed in `awaiters[msg_id]`. When `handleSubmitResponse` lands the response, `releaseAwaiters` closes/sends on those channels. Goroutine exits on either the channel signal or the deadline.

### Client (`coms-go client-local`)

```
+------------------------+      +------------------------+
|  stdin reader          |----->|  IPC dispatcher        |
+------------------------+      +-----------+------------+
                                            |
                                            v
+------------------------+      +-----------+------------+
|  net.Listen("unix")    |----->|  conn handler goroutine|  one per accepted connection
+------------------------+      +------------------------+

Background goroutines:
  pingTicker         every 10 s (PI_COMS_PING_INTERVAL_MS) -> refreshPool, send Pong to peers
  keepaliveTicker    every 30 s -> rewrite registry entry atomically (self-heal)
```

### Client (`coms-go client-net`)

```
+------------------------+      +------------------------+
|  stdin reader          |----->|  IPC dispatcher        |
+------------------------+      +-----------+------------+
                                            |
                                            v
+------------------------+      +-----------+------------+
|  HTTP POST/GET/DELETE  |<-----+  http.Client (timeout) |
+------------------------+      +-----------+------------+
            |
            v
+-----------+------------+
|  SSE read loop         |  one goroutine; backoff reconnect on EOF/error
+------------------------+

Background goroutines:
  heartbeatTicker    every 10 s -> POST /v1/agents/<sid>/heartbeat
```

### Atomic file writes

Pattern (matches TS `atomicWriteSync`):

1. Write content to `<final>.tmp` (with mode 0600 if secret token)
2. `os.Chmod(tmp, 0600)` if applicable
3. `os.Rename(tmp, final)` — POSIX-guaranteed atomic on same filesystem

Implemented as `internal/util.AtomicWrite(path string, data []byte, mode os.FileMode) error`.

### Audit log appends

Append-only writes to `~/.pi/coms-log` and `~/.pi/coms-net-log` use `O_APPEND|O_CREATE|O_WRONLY` with `flock(LOCK_EX)` via `syscall.Flock` to serialize writes across processes (multiple pi instances on the same host append to the same audit file).

### Conflict Resolution

- **Local registry name collisions**: deterministic suffix (`planner` → `planner2` → `planner3` ...) matching `resolveUniqueName()` in `coms.ts` line 331.
- **Server name collisions**: server suffixes on register; client picks up the new name from the response and records `name_collision` in the audit log (mirrors `coms-net.ts` lines 845-857).
- **Dead PID cleanup**: `client-local` calls `kill(pid, 0)` (ESRCH → remove entry; EPERM → treat as live) on every list operation; mirrors `pruneDeadEntries()` in `coms.ts` line 312.

---

## 8. Migration

This section is the cutover contract. It is the section the merge-manager agent must read in full before performing T11.

### Current vs target — module-by-module comparison

| Component | Current (TS/Bun) | Target (Go) |
|-----------|------------------|-------------|
| Local-mode transport | `extensions/coms.ts` Node `net.createServer` + Unix sockets / Windows named pipes | `coms-go client-local`, `net.Listen("unix", ...)` (or `winio.ListenPipe` on win — out of scope for pi targets but stubbed for completeness) |
| Local-mode tools | `pi.registerTool("coms_list" / "coms_send" / "coms_get" / "coms_await")` inline in coms.ts | Registered by `extensions/coms-go/shim.ts`; forwarded over JSON-line IPC to `coms-go client-local` |
| Local-mode registry | `~/.pi/coms/projects/<project>/agents/<name>.json`, atomic write via `coms.ts:writeRegistryAtomic` | Same path, same content; `internal/registry.WriteAtomic` |
| Network-mode transport | `extensions/coms-net.ts` `fetch()` + hand-rolled SSE parser | `coms-go client-net`, `net/http.Client` + hand-rolled `internal/netclient/sse.go` |
| Network-mode tools | `pi.registerTool("coms_net_list" / ...)` inline in coms-net.ts | Registered by `extensions/coms-go/shim.ts`; forwarded over JSON-line IPC to `coms-go client-net` |
| Network-mode hub server | `scripts/coms-net-server.ts` (Bun `Bun.serve`) | `coms-go serve`, `net/http.Server` |
| Hub server state | `Map<project, ProjectState>` with sub-maps for agents/messages/streams/awaiters | `map[string]*ProjectState` with `sync.RWMutex`; sub-maps identical |
| Hub cleanup loops | `setInterval` for stale/TTL/keepalive | `time.NewTicker` in dedicated goroutines, stopped via `context.Cancel` |
| SSE keepalive | `: ping <iso>\n\n` every 15 s | Identical; same frame contents and interval |
| Token policy | `crypto.randomBytes(32).toString("hex")` on loopback bind without env token | `crypto/rand.Read([]byte, 32)` + `hex.EncodeToString` — identical output format |
| Token comparison | `crypto.timingSafeEqual` | `crypto/subtle.ConstantTimeCompare` |
| Logging | Inline color helpers in coms-net-server.ts | `internal/server/log.go` — identical symbols + format |
| Frontmatter parsing | `parseFrontmatter()` in both TS files (lines 170-192 / 217-238) | `internal/util/frontmatter.go` (single implementation; both client modes import it) |
| Identity flags | `pi.registerFlag` calls in each TS extension | Registered in `shim.ts`; passed through to Go via JSON IPC at session_start |
| Justfile recipes | `bun scripts/coms-net-server.ts` and `pi -e extensions/coms*.ts` | `coms-go serve` and `pi -e extensions/coms-go/shim.ts` |

### Cutover Plan (executed in T11 by `merge-manager`)

The cutover is staged across THREE phases (NOT three commits — one final commit) to ensure callers see no visible downtime:

**Phase A — Dual-run (T1 through T9 produce this state)**
- `coms-go` binary built and installed to `extensions/coms-go/bin/`.
- `extensions/coms.ts`, `extensions/coms-net.ts`, `scripts/coms-net-server.ts` remain on disk and remain functional.
- A new manifest at `extensions/coms-go/manifest.json` is recognized by the pi loader but NOT yet auto-loaded (no justfile change yet).
- Tests T5 (integration parity tests) run BOTH the TS hub and the Go hub on separate ports against the same client suite, diffing outputs. Result: identical (modulo JSON whitespace).

**Phase B — Loader flip (the actual T11 commit)**
- `justfile` is rewritten: `local-coms`, `coms`, `coms-net-server`, `coms-net-server-lan`, `coms1`, `coms2`, `coms3`, `coms4` recipes invoke `pi -e extensions/coms-go/shim.ts` (clients) and `coms-go serve` (server) respectively.
- `extensions/coms.ts`, `extensions/coms-net.ts`, `scripts/coms-net-server.ts` are deleted.
- `README.md` Prerequisites table replaces "Bun ≥ 1.3.2" specifically as it relates to the coms/coms-net pair with a "Go ≥ 1.23" row; the rest of the README's Bun dependency (for other extensions still in TS) stays.
- `README.md` Extensions table row for `coms` is updated to point at `extensions/coms-go/shim.ts`. The `coms-net` row is folded into the same entry (both modes provided by one extension now).
- Single git commit with message: `feat(coms-go): migrate coms + coms-net to Go (cutover T11)`. NO bundled changes.

**Phase C — Post-cutover (out of scope of this spec, but documented for the merge-manager)**
- After 7 days of running the Go binary in production on the user's pi hosts, a follow-up task may evaluate any drift and tighten anything that's stayed defensive. Not part of this spec.

### What callers see during cutover

- **External pi instances** talking to this host's coms-net hub: a brief disconnect (≤ 5 s) when the hub is restarted; SSE clients reconnect via their existing backoff logic (handled by both TS and Go clients). No protocol incompatibility because the Go server emits identical wire bytes.
- **Other pi agents on the same host using local-mode coms**: their sockets disappear when the old client exits and reappear when the new client boots; the `staleSocket` probe path (`probeStaleSocket` in `coms.ts` line 373; ported to `internal/transport`) handles cleanup of orphan socket files.
- **The claude-code window**: nothing visible. Claude Code does not directly consume coms; it only delegates to pi via subagents, which use their own pi runtime.

### Rollback

The cutover is a single git commit. To roll back:

```bash
cd /home/n0ko/Programs/ai/pi-vs-claude-code
git revert <cutover-commit-sha>
```

This restores `extensions/coms.ts`, `extensions/coms-net.ts`, `scripts/coms-net-server.ts`, the original `justfile`, and the original `README.md` in one atomic operation. The Go binary stays in `extensions/coms-go/bin/` but is no longer referenced — it can be re-deleted in a follow-up.

---

## 9. Integration

### Pi Extension Loader Integration

The pi extension loader treats `extensions/coms-go/shim.ts` exactly like any other `.ts` extension. The shim does NOT advertise the Go binary path or version to pi — pi sees it as a normal TS extension that happens to spawn helper subprocesses. From pi's POV: `pi.registerTool`, `pi.registerCommand`, `pi.registerFlag`, `pi.on("session_start", ...)`, `pi.appendEntry`, `pi.sendMessage` all behave identically to today.

### How the shim talks to the Go binary

```
+-------------------------+
|   pi runtime (Bun/jiti) |
+-----------+-------------+
            |
            | loads extensions/coms-go/shim.ts
            v
+-----------+-------------+
|   shim.ts (~200 LOC)    |
|                         |
|   pi.registerTool(...)  |        spawn coms-go client-{local,net}
|   pi.on("session_start")|------>  +----------------------+
|   pi.on("agent_end")    |         |  child process       |
|   pi.on("session_shutdown")|<-----+  stdin: JSON-line    |
+-------------------------+  IPC    |  stdout: JSON-line   |
                                    |  stderr: log lines   |
                                    +-----+----------------+
                                          |
                          +---------------+---------------+
                          v                               v
              +-----------+----------+          +---------+----------+
              | client-local: Unix    |          | client-net: HTTP   |
              | socket peer transport |          | + SSE to coms-go   |
              +-----------+-----------+          | serve hub          |
                          |                       +--------+---------+
                          v                                v
              +-----------+-----------+          +---------+---------+
              |  peer pi instances    |          |  coms-go serve    |
              |  (same machine)       |          |  (any host)       |
              +-----------------------+          +-------------------+
```

### Pi tool registration mapping

| TS tool (now) | Go-backed tool (after) | IPC route |
|---------------|------------------------|-----------|
| `coms_list` | `coms_list` | shim → `client-local` stdin: `{kind:"tool_request",tool:"coms_list",params:{...}}` |
| `coms_send` | `coms_send` | shim → `client-local` |
| `coms_get` | `coms_get` | shim → `client-local` |
| `coms_await` | `coms_await` | shim → `client-local` |
| `coms_net_list` | `coms_net_list` | shim → `client-net` |
| `coms_net_send` | `coms_net_send` | shim → `client-net` |
| `coms_net_get` | `coms_net_get` | shim → `client-net` |
| `coms_net_await` | `coms_net_await` | shim → `client-net` |
| `/coms` slash | `/coms` slash | shim → `client-local` |
| `/coms-net` slash | `/coms-net` slash | shim → `client-net` |

### Other pi instances calling this one

Wire-compatible. A TS-based pi instance running on host A can talk to a Go-based pi instance running on host B (or vice versa) because both speak the same protocol byte-for-byte. This is the property that enables Phase A dual-run validation in the cutover plan above.

### Justfile invocations (after cutover, T11)

```make
# Same-machine peer-to-peer (Unix sockets)
local-coms *args:
    pi -e extensions/coms-go/shim.ts -e extensions/minimal.ts -e extensions/theme-cycler.ts {{args}}

# Networked client (auto-discovers server.json)
coms *args:
    pi -e extensions/coms-go/shim.ts -e extensions/minimal.ts -e extensions/theme-cycler.ts {{args}}

# Start a local coms-net hub
coms-net-server:
    coms-go serve

# Start a LAN-visible hub (requires PI_COMS_NET_AUTH_TOKEN)
coms-net-server-lan:
    PI_COMS_NET_HOST=0.0.0.0 coms-go serve
```

### Hooks Integration

None required. The Go binary is invoked by pi's extension loader (through the shim) and by justfile recipes. No Claude Code hook registration is needed.

---

## 10. What It Does NOT Do

Explicit anti-scope. The merge-manager and reviewing agents MUST reject any change that violates the below:

- **Does NOT add features.** No new routes, no new tools, no new slash commands, no new env vars. The Go binary's surface area is a strict subset of (== equal to) the TS surface area.
- **Does NOT change the wire protocol.** Field names, field types, status codes, header names, SSE event names, error strings, ULID format, color palette, hop limits, default timeouts, env-var names, file paths — all frozen at TS-reference values.
- **Does NOT rewrite other pi extensions.** `agent-team`, `agent-chain`, `damage-control`, `subagent-widget`, etc. all stay TS. This spec ONLY covers `coms` + `coms-net`.
- **Does NOT introduce a third-party HTTP router.** No `gin`, no `chi`, no `gorilla/mux`. Pure stdlib `net/http`.
- **Does NOT introduce a third-party SSE library.** SSE writer + parser are hand-rolled in `internal/server/state.go` and `internal/netclient/sse.go` respectively, matching the TS hand-rolled approach.
- **Does NOT introduce CGO.** All builds set `CGO_ENABLED=0`; the resulting binaries are statically linked.
- **Does NOT add persistent storage.** No SQLite, no Redis, no Bolt, no embedded KV. State lives in `map[*]*` exactly as it does in the TS server. Restarting the hub clears all in-flight messages (matches TS behavior; the `expires_at` TTL is the only persistence concept).
- **Does NOT improve security beyond TS parity.** No mTLS, no Argon2 token hashing, no rate limits beyond `MAX_INBOX`. These are out of scope; the security-review agent (T9) confirms parity, not improvement.
- **Does NOT touch the Bun dependency for other extensions.** Only the coms/coms-net pair's Bun usage is removed. Other extensions still use Bun for their TS runtime.
- **Does NOT support Windows on pi hosts.** Pi targets are Linux ARM64 (Raspberry Pi) + Linux/macOS AMD64 dev hosts. A `transport_windows.go` stub exists (build tag `windows`) for cross-compile cleanliness but is untested and not part of acceptance criteria.

---

## 11. Tech Stack

| Concern | Choice | Rationale |
|---------|--------|-----------|
| Runtime | Native Go binary, statically linked | Replaces Bun runtime; pi-extension-loadable via shim.ts; single artifact per arch |
| Language | Go 1.23+ | User-mandated default language; goroutines fit the workload; CGO_ENABLED=0 enables clean cross-compile |
| Dependencies | Go stdlib only | Behavioral parity, minimal supply chain, no third-party HTTP routers |
| Storage | In-memory `map` + atomic file writes for registry/server.json/server.secret.json | Matches TS server semantics exactly; no persistent message store |
| Testing | Go stdlib `testing` + `httptest` + integration tests under `//go:build integration` | Stdlib-only; no third-party assertion libraries; matches "no exotic deps" rule |
| Formatting | `gofmt -s` + `go vet ./...` on every commit | Standard Go pipeline; no linter overlay |
| Distribution | Cross-compiled binaries committed to `extensions/coms-go/bin/<os>-<arch>/coms-go` | Pi extension manifest lists per-arch binaries; shim.ts picks the correct one at session_start |
| Target arches | `linux/arm64`, `linux/amd64`, `darwin/arm64`, `darwin/amd64` | Pi hosts (RPi 4/5 ARM64), dev Linux (AMD64), dev macOS (both arches) |
| TS shim runtime | Bun (pi's existing runtime) | Pi extension contract requires TS; shim is ≤ 200 LOC and contains no business logic |

### Pinned versions

- Go: `go 1.23` (declared in `extensions/coms-go/go.mod`)
- No third-party deps; `go.sum` is committed empty.

---

## 12. Project Infrastructure

### Directory Structure (extensions/coms-go internals — see Section 3 for the project-level tree)

```
extensions/coms-go/
├── go.mod                                    # module github.com/pi-vs-cc/coms-go ; go 1.23
├── go.sum                                    # empty
├── README.md                                 # build/run/dev/troubleshooting
├── manifest.json                             # pi extension manifest
├── shim.ts                                   # ≤ 200 LOC; pi-side tool/lifecycle registration
├── cmd/coms-go/main.go                       # CLI entrypoint; subcommand dispatch
├── internal/
│   ├── proto/proto.go                        # All wire types (envelopes, agent cards, messages)
│   ├── proto/proto_test.go
│   ├── util/ulid.go                          # ULID generator (Crockford base32)
│   ├── util/color.go                         # hexFg, isValidHex, fallbackColor, FallbackPalette
│   ├── util/frontmatter.go                   # parseFrontmatter, findSystemPromptPath, readFrontmatterFromArgv
│   ├── util/util_test.go
│   ├── audit/audit.go                        # JSONL append-only with flock
│   ├── audit/audit_test.go
│   ├── registry/registry.go                  # WriteAtomic, ReadAll, Prune, ResolveUniqueName
│   ├── registry/registry_test.go
│   ├── transport/transport_unix.go           # build tag: !windows
│   ├── transport/transport_windows.go        # build tag: windows
│   ├── transport/transport_test.go
│   ├── localclient/client.go                 # client-local subcommand: session_start equivalent
│   ├── localclient/handlers.go               # handlePrompt, handleResponse, handlePing
│   ├── localclient/tools.go                  # IPC handlers for coms_list/send/get/await
│   ├── localclient/client_test.go
│   ├── netclient/client.go                   # client-net subcommand: register + heartbeat + SSE
│   ├── netclient/sse.go                      # Hand-rolled SSE parser
│   ├── netclient/tools.go                    # IPC handlers for coms_net_list/send/get/await
│   ├── netclient/client_test.go
│   ├── server/server.go                      # serve subcommand: HTTP server + signal handling
│   ├── server/handlers.go                    # Per-route handlers
│   ├── server/state.go                       # ProjectState, ServerState, broadcast helpers
│   ├── server/loops.go                       # staleScanTick, ttlScanTick, keepaliveTick
│   ├── server/log.go                         # Event-line logger (color-aware)
│   ├── server/server_test.go
│   └── ipc/ipc.go                            # JSON-line stdin/stdout protocol between shim and client subcommands
│   └── ipc/ipc_test.go
├── bin/                                       # Build output (gitignored except for committed cross-builds)
│   ├── coms-go-linux-amd64
│   ├── coms-go-linux-arm64
│   ├── coms-go-darwin-amd64
│   └── coms-go-darwin-arm64
└── testdata/
    ├── golden/                                # JSON fixtures captured from TS server
    └── frontmatter/                           # Sample .md files with frontmatter for parser tests
```

### Version Management

`extensions/coms-go/internal/proto/version.go` declares:

```go
const Version = "1.0.0"
var Commit = "dev"  // overridden at build time with: -ldflags "-X 'github.com/pi-vs-cc/coms-go/internal/proto.Commit=$(git rev-parse --short HEAD)'"
```

`coms-go version` prints `coms-go v1.0.0 (commit abc1234)`.

Version bumps: edit `Version` constant manually; commit with message `release: coms-go vX.Y.Z`. No CHANGELOG required for v1.0.0 (single-author migration); future versions get a `CHANGELOG.md`.

### CHANGELOG.md

Not required for v1.0.0 (this is the inaugural Go cut; no prior Go releases exist). Future versions follow Keep a Changelog format under `extensions/coms-go/CHANGELOG.md`.

### CI Workflow

No new CI is added by this spec (pi-vs-claude-code has no CI today per inspection of the repo root). Local verification is the gate:

```bash
cd extensions/coms-go
go vet ./...
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/coms-go-linux-amd64 ./cmd/coms-go
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/coms-go-linux-arm64 ./cmd/coms-go
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o bin/coms-go-darwin-amd64 ./cmd/coms-go
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o bin/coms-go-darwin-arm64 ./cmd/coms-go
```

If/when the project adopts CI, a GitHub Actions workflow at `.github/workflows/coms-go.yml` would run the same four commands on `push` and `pull_request` events touching `extensions/coms-go/**`.

### Scripts (justfile additions after T11)

```make
# Build all four target arches for coms-go
coms-go-build:
    cd extensions/coms-go && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/coms-go-linux-amd64 ./cmd/coms-go && \
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/coms-go-linux-arm64 ./cmd/coms-go && \
    CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o bin/coms-go-darwin-amd64 ./cmd/coms-go && \
    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o bin/coms-go-darwin-arm64 ./cmd/coms-go

# Run coms-go tests
coms-go-test:
    cd extensions/coms-go && go test ./...

# Run coms-go integration tests (two-host simulated)
coms-go-test-integration:
    cd extensions/coms-go && go test -tags=integration ./...

# Run go vet on coms-go
coms-go-vet:
    cd extensions/coms-go && go vet ./...
```

---

## 13. Estimated Size

| Area | Files | LOC (Go) |
|------|-------|----------|
| `internal/proto/` | 2 | ~250 |
| `internal/util/` | 4 | ~200 |
| `internal/audit/` | 2 | ~80 |
| `internal/registry/` | 2 | ~220 |
| `internal/transport/` | 3 | ~250 |
| `internal/localclient/` | 4 | ~500 |
| `internal/netclient/` | 4 | ~550 |
| `internal/server/` | 6 | ~900 |
| `internal/ipc/` | 2 | ~150 |
| `cmd/coms-go/main.go` | 1 | ~120 |
| Total Go | 30 | ~3,220 |
| TS shim | 1 (`shim.ts`) | ~180 |
| Manifest + go.mod + README | 3 | ~80 |
| Test golden fixtures | ~10 JSON files | n/a |
| **Grand Total** | **~44 files** | **~3,480 LOC** |

Reference: `coms.ts` (1597 LOC) + `coms-net.ts` (1636 LOC) + `coms-net-server.ts` (1604 LOC) = **4,837 LOC of TS** today. The Go target is ~28% smaller, primarily because Go expresses the goroutine/channel patterns more compactly than Node's Promise-everything style, and because helper duplication (~300 LOC pasted between `coms.ts` and `coms-net.ts`) collapses into a single `internal/util/` package.

Person-day estimate: 6–9 working days for a single experienced Go engineer (with the spec in hand); 3–4 days if T3/T4/T6 are run in parallel by separate workers. Reviews (T8, T9, T10) add 1–2 person-days. Total: ~10 person-days.

---

## 14. Architecture Diagram (pi-to-pi flow)

```
+--------------------------------------------+   +--------------------------------------------+
|                  PI HOST A                  |   |                  PI HOST B                  |
|                                             |   |                                             |
|  +-----------------+      +--------------+  |   |  +--------------+      +-----------------+  |
|  |  pi runtime     |      |  shim.ts     |  |   |  |  shim.ts     |      |  pi runtime     |  |
|  |  (Bun + jiti)   |<---->|  (~200 LOC)  |  |   |  |  (~200 LOC)  |<---->|  (Bun + jiti)   |  |
|  +-----------------+      +------+-------+  |   |  +------+-------+      +-----------------+  |
|                                  |          |   |         |                                   |
|                          JSON-line IPC      |   |  JSON-line IPC                              |
|                                  v          |   |         v                                   |
|                       +----------+--------+ |   | +-------+---------+                         |
|                       |  coms-go          | |   | |  coms-go        |                         |
|                       |  client-local +   | |   | |  client-local + |                         |
|                       |  client-net       | |   | |  client-net     |                         |
|                       +---+-----------+---+ |   | +--+----------+---+                         |
|                           |           |     |   |    |          |                             |
|             Unix socket   |           |HTTP+|   |    |HTTP+SSE  |Unix socket                  |
|             peer talk     v           |SSE  |   |    |          v   peer talk                 |
|                       +---+----+   +--+---- + - + ---+----+   +---+----+                      |
|                       | local- |   | HTTP   |   |    | HTTP   |   | local- |                  |
|                       | peer A |   | client |   |    | client |   | peer B |                  |
|                       +--------+   +--+-----+   |    +-----+--+   +--------+                  |
+-------------------------------------+----------+    +-----+-------------------------------------+
                                      |                     |
                                      v                     v
                                  +---+---------------------+---+
                                  |       coms-go serve         |
                                  |     (one of N hubs, any host)|
                                  |                              |
                                  |   - GET  /health             |
                                  |   - POST /v1/agents/register |
                                  |   - GET  /v1/events  (SSE)   |
                                  |   - GET  /v1/agents          |
                                  |   - POST /v1/messages        |
                                  |   - GET  /v1/messages/:id    |
                                  |   - GET  /v1/messages/:id/await |
                                  |   - POST /v1/messages/:id/response |
                                  |   - POST /v1/agents/:sid/heartbeat |
                                  |   - DELETE /v1/agents/:sid   |
                                  +------------------------------+

The CLAUDE-CODE window (top-down orchestration) is NOT directly connected; it delegates
to pi subagents which then use the coms-go-backed extension just like any other pi user.
```

---

## Routes Comparison Table (parity contract — every row is verified by T5)

| # | Method | Path | TS handler | Go handler | Auth | Request body shape | Response shape | Status codes |
|---|--------|------|-----------|-----------|------|--------------------|----------------|--------------|
| 1 | GET | `/health` | `handleHealth` (coms-net-server.ts L516) | `server/handlers.go:HandleHealth` | no | none | `{ok, version, server_id, started_at}` | 200 |
| 2 | POST | `/v1/agents/register` | `handleRegister` (L525) | `server/handlers.go:HandleRegister` | yes | `RegisterRequest` | `RegisterResponse` (with `sse_url`) | 200, 400, 401 |
| 3 | GET | `/v1/events?project=&session_id=` | `handleEvents` (L605) | `server/handlers.go:HandleEvents` | yes | none | `text/event-stream` (frames: `hello`, `pool_snapshot`, `agent_*`, `prompt`, `response`, `message_status`) | 200, 400, 401, 404 |
| 4 | POST | `/v1/agents/:sid/heartbeat` | `handleHeartbeat` (L741) | `server/handlers.go:HandleHeartbeat` | yes | `HeartbeatRequest` | `{ok}` | 200, 400, 401, 404 |
| 5 | GET | `/v1/agents?project=&include_explicit=` | `handleListAgents` (L807) | `server/handlers.go:HandleListAgents` | yes | none | `{agents: AgentCard[]}` | 200, 401 |
| 6 | POST | `/v1/messages` | `handleSendMessage` (L823) | `server/handlers.go:HandleSendMessage` | yes | `SendRequest` | `SendResponse` | 200, 400, 401, 404, 409 (hop_limit_exceeded, ambiguous_target), 429 (inbox_full) |
| 7 | GET | `/v1/messages/:id` | `handleGetMessage` (L966) | `server/handlers.go:HandleGetMessage` | yes | none | `{msg_id, status, response, error}` | 200, 401, 404 |
| 8 | GET | `/v1/messages/:id/await?timeout_ms=` | `handleAwaitMessage` (L981) | `server/handlers.go:HandleAwaitMessage` | yes | none | `{msg_id, status, response, error}` (long-poll, server-clamped to `MESSAGE_TTL_MS`) | 200, 401, 404 |
| 9 | POST | `/v1/messages/:id/response` | `handleSubmitResponse` (L1106) | `server/handlers.go:HandleSubmitResponse` | yes | `ResponseSubmitRequest` | `{ok}` | 200, 400, 401, 403 (not_target), 404, 409 (already_terminal) |
| 10 | DELETE | `/v1/agents/:sid?project=` | `handleDeleteAgent` (L1184) | `server/handlers.go:HandleDeleteAgent` | yes | none | `{ok}` | 200, 401, 404 |

### SSE event surface (subset of route 3 — also verified by T5)

| Event name | When emitted | Payload | TS line |
|-----------|-------------|---------|---------|
| `hello` | On SSE open | `{server_time, server_id}` | L654 |
| `pool_snapshot` | On SSE open, after `hello` | `{project, agents: AgentCard[]}` | L670 |
| `agent_joined` | On `handleRegister` (excluding self) | `{project, agent: AgentCard}` | L587 |
| `agent_updated` | On `handleHeartbeat` change (excluding self) | `{project, agent: {session_id, ...delta}}` | L788 |
| `agent_stale` | From `staleScanTick` after `STALE_AFTER_MS` | `{project, session_id, name, last_seen_at}` | L1350 |
| `agent_left` | On `handleDeleteAgent`, on SSE abort, on `staleScanTick` after `OFFLINE_AFTER_MS` | `{project, session_id, name, reason}` | L1337/L703/L1213 |
| `prompt` | On `handleSendMessage` if target stream open | `{msg_id, project, sender:{session_id,name,cwd}, prompt, conversation_id, response_schema, hops}` | L925 |
| `response` | On `handleSubmitResponse` | `{msg_id, project, responder:{session_id,name}, response, error, status}` | L1156 |
| `message_status` | On every status transition | `{msg_id, status}` | L917, L941, L1164 |
| `server_ping` | Reserved (no-op handler in client) | — | n/a in server output; emitted as SSE comment `: ping <iso>` instead, L1402 |
| `error` | Reserved error frames | `{code, message}` | n/a in current server; client handles defensively |

### Unix-socket envelope routes (client-local mode — verified by T5)

| # | Envelope type | TS handler (coms.ts) | Go handler | Ack/Nack/Pong response |
|---|---------------|----------------------|------------|------------------------|
| L1 | `ping` | `handlePing` (L687) | `localclient/handlers.go:HandlePing` | `Pong{type, msg_id, agent_card}` |
| L2 | `prompt` | `handlePrompt` (L604) | `localclient/handlers.go:HandlePrompt` | `Ack{type:"ack", msg_id}` or `Nack{type:"nack", msg_id, error}` |
| L3 | `response` | `handleResponse` (L663) | `localclient/handlers.go:HandleResponse` | `Ack{type:"ack", msg_id}` |

---

## Workflow (from project README)

Verbatim summary of the user-facing workflow as documented in `README.md` (the README is the source of truth — Go must preserve every detail).

> "Subagents, dispatch queues, and agent chains all share one shape: information flows in **one direction** down a hierarchy. `coms` and `coms-net` add the missing pattern — **two equal Pi agents that talk to each other peer-to-peer**, on the same machine or across the network. No orchestrator, no parent/child relationship, no information loss as work travels through layers. The best idea wins, regardless of which agent had it."

> "The entire surface area is four tools. List the agents on the network, send them a prompt, then either poll (non-blocking) or block until the response lands. That's it."

Operationally:

1. Terminal 1 launches an agent with an identity: `just local-coms --name planner --purpose "Plans the work" --color "#36F9F6"` (post-cutover this becomes `pi -e extensions/coms-go/shim.ts --name planner ...`).
2. Terminal 2 launches a second agent: `just local-coms --name coder --purpose "Writes the code"`.
3. Both agents see a live pool widget showing the other.
4. From either agent, the LLM calls `coms_send` with `target: "<peer name>"`, then either polls with `coms_get` (non-blocking) or waits with `coms_await` (blocking).
5. The receiving agent's next turn is automatically triggered with the inbound prompt prepended; when its turn ends, the final assistant text is auto-packaged and sent back as the response. The original sender's `coms_await`/`coms_get` resolves.
6. Networked mode (`coms_net_*`) replaces step 1's transport with a hub: `coms-go serve` runs once on any host; clients (`pi -e extensions/coms-go/shim.ts`) discover the server via `~/.pi/coms-net/projects/<project>/server.json` (loopback) or env var (`PI_COMS_NET_SERVER_URL` + `PI_COMS_NET_AUTH_TOKEN`) for cross-host work.
7. Safety rails: hop limit (`MAX_HOPS=5`); audit log (msg_id + sender + hops, no prompts); self-heal (stale socket prune; SSE backoff reconnect); localhost-by-default for the hub (refuses non-loopback bind without explicit token).

---

## Build & Run

### Build (all four target arches)

```bash
cd /home/n0ko/Programs/ai/pi-vs-claude-code/extensions/coms-go

# Single-arch development build
go build -o bin/coms-go ./cmd/coms-go

# Cross-compile all targets
CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -ldflags="-s -w -X 'github.com/pi-vs-cc/coms-go/internal/proto.Commit=$(git rev-parse --short HEAD)'" -o bin/coms-go-linux-amd64  ./cmd/coms-go
CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -ldflags="-s -w -X 'github.com/pi-vs-cc/coms-go/internal/proto.Commit=$(git rev-parse --short HEAD)'" -o bin/coms-go-linux-arm64  ./cmd/coms-go
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w -X 'github.com/pi-vs-cc/coms-go/internal/proto.Commit=$(git rev-parse --short HEAD)'" -o bin/coms-go-darwin-amd64 ./cmd/coms-go
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w -X 'github.com/pi-vs-cc/coms-go/internal/proto.Commit=$(git rev-parse --short HEAD)'" -o bin/coms-go-darwin-arm64 ./cmd/coms-go
```

### Test

```bash
cd /home/n0ko/Programs/ai/pi-vs-claude-code/extensions/coms-go

# Unit tests (no network/external dependencies)
go test ./...

# Integration tests (real two-host or loopback-simulated round-trip)
go test -tags=integration ./...

# Race detector (recommended on the goroutine-heavy server package)
go test -race ./internal/server/...
```

### Run (standalone — for development)

```bash
# Start the hub on loopback (auto-generates server.secret.json)
coms-go serve

# Start the hub on LAN (requires explicit token)
PI_COMS_NET_AUTH_TOKEN=$(openssl rand -hex 32) PI_COMS_NET_HOST=0.0.0.0 coms-go serve

# Standalone client (debug only — normally invoked by shim.ts):
coms-go client-local --name dev --purpose "Local debug" < /tmp/ipc-in > /tmp/ipc-out
coms-go client-net  --name dev --server-url http://127.0.0.1:43219 --auth-token <tok> < /tmp/ipc-in > /tmp/ipc-out
```

### Run (via pi — production path)

```bash
# Same-machine peer-to-peer
just local-coms --name planner --purpose "Plans the work"

# Networked client (auto-discovers server.json)
just coms-net-server &
just coms --name dev --purpose "Dev work"
```

---

## Test Plan

### Package layout

| Package | Test file | Coverage |
|---------|-----------|----------|
| `internal/proto` | `proto_test.go` | JSON marshal/unmarshal of every wire type against golden fixtures; field name parity |
| `internal/util` | `util_test.go` | ULID format (length, alphabet, time-prefix), `hexFg`, `isValidHex`, `fallbackColor` determinism, `parseFrontmatter` with sample .md files |
| `internal/audit` | `audit_test.go` | JSONL append; concurrent appends from N goroutines produce N lines; no prompt bodies |
| `internal/registry` | `registry_test.go` | Atomic write + read round-trip; `Prune` removes ESRCH PIDs; `ResolveUniqueName` collision suffixing |
| `internal/transport` | `transport_test.go` | `probeStaleSocket` (in_use vs stale verdict); `bindEndpoint` with stale-file cleanup; `readOneLine` 64 KB cap; `sendEnvelope` round-trip |
| `internal/localclient` | `client_test.go` | `handlePrompt` queues inbound + acks; `handleResponse` resolves pending; `handlePing` returns valid Pong |
| `internal/netclient` | `client_test.go` | `register` + heartbeat + SSE open against `httptest.NewServer`; SSE parser frame boundaries; reconnect backoff |
| `internal/server` | `server_test.go` | Every route hit with valid + invalid bodies; auth path (no bearer, wrong bearer); `staleScanTick`/`ttlScanTick` state transitions; SSE delivery to subscribed streams |
| `internal/ipc` | `ipc_test.go` | JSON-line framing; round-trip request/response IDs; shutdown frame closes the loop |

### Golden fixtures (captured in T5)

Procedure for capturing each fixture from the running TS reference (documented in `extensions/coms-go/testdata/golden/README.md` written as part of T5):

```bash
# Start the TS server in a known-good state
PI_COMS_NET_AUTH_TOKEN=test-token PI_COMS_NET_PORT=43210 bun scripts/coms-net-server.ts &
TS_PID=$!
sleep 1

# Capture health
curl -s http://127.0.0.1:43210/health > extensions/coms-go/testdata/golden/health.json

# Register an agent
curl -s -X POST -H "Authorization: Bearer test-token" -H "Content-Type: application/json" \
  -d '{"project":"default","session_id":"01HXNJ0E5Q4M7Z2C1V8YR6F3KT","name":"planner","purpose":"Plans","model":"claude-opus-4-7","color":"#36F9F6","cwd":"/tmp","explicit":false}' \
  http://127.0.0.1:43210/v1/agents/register > extensions/coms-go/testdata/golden/register_resp.json

# ... (repeat for list_agents, send, get, await:complete, await:timeout, sse_pool_snapshot)

# Shut down
kill -INT $TS_PID
```

The Go integration test reads each golden fixture, replays the same request against `httptest.NewServer(server.NewRouter(...))`, and diffs the response JSON with `reflect.DeepEqual` (after re-serializing both through `json.Marshal` to normalize key order).

### Two-host integration test (`//go:build integration`)

`extensions/coms-go/internal/server/integration_test.go` (build tag `integration`):

1. Boot a hub on a random local port via `httptest.NewServer`.
2. Launch goroutine "client A" that POSTs `/v1/agents/register` for `planner`, opens SSE, listens for events.
3. Launch goroutine "client B" that registers as `coder`, opens SSE.
4. Client A POSTs `/v1/messages` with `target=coder` and a known prompt.
5. Client B receives a `prompt` SSE event, calls `/v1/messages/<id>/response` with a known response.
6. Client A's `/v1/messages/<id>/await` resolves with that response.
7. Assert: all status transitions visible (`queued → delivered → complete`); responses match expected bytes.

### Auth failure path tests

| Test name | Setup | Expected |
|-----------|-------|----------|
| `TestRouteRequiresBearer` | Hit any `/v1/*` with no `Authorization` | 401 + `{ok:false,error:"unauthorized"}` |
| `TestRouteRejectsBadBearer` | Hit any `/v1/*` with `Authorization: Bearer wrong` | 401 |
| `TestSubmitResponseNotTarget` | POST `/v1/messages/<id>/response` with `responder_session` != `target_session` | 403 + `{error:"not_target"}` |
| `TestHopLimitExceeded` | POST `/v1/messages` with `hops=5` | 409 + `{error:"hop_limit_exceeded"}` |
| `TestInboxFull` | Fill target inbox to `MAX_INBOX=100`, send 101st | 429 + `{error:"inbox_full"}` |
| `TestTargetNotFound` | POST `/v1/messages` with `target="ghost"` (no such agent) | 404 + `{error:"target_not_found"}` |
| `TestAmbiguousTarget` | Register two agents named `dup` with `explicit=false`, POST send with `target="dup"` | 409 + `{error:"ambiguous_target"}` |

---

## 15. Task Manifest

| ID | Agent | Description | File Scope (read) | File Scope (write) | Depends On | Verify Command |
|----|-------|-------------|--------------------|--------------------|------------|----------------|
| T1 | `unix-coder` | Scaffold `extensions/coms-go/` Go module: `go.mod`, `manifest.json`, `README.md`, empty `cmd/coms-go/main.go` skeleton (just dispatches subcommands), empty `internal/` subdirectories with package declarations. No business logic. | `extensions/coms.ts`, `extensions/coms-net.ts`, `scripts/coms-net-server.ts`, `SPEC/coms_go_port/coms_go_port.md` | `extensions/coms-go/go.mod`, `extensions/coms-go/go.sum`, `extensions/coms-go/manifest.json`, `extensions/coms-go/README.md`, `extensions/coms-go/cmd/coms-go/main.go`, `extensions/coms-go/internal/proto/proto.go`, `extensions/coms-go/internal/util/{ulid,color,frontmatter,util_test}.go`, `extensions/coms-go/internal/audit/audit.go`, `extensions/coms-go/internal/registry/registry.go`, `extensions/coms-go/internal/transport/transport_unix.go`, `extensions/coms-go/internal/localclient/client.go`, `extensions/coms-go/internal/netclient/client.go`, `extensions/coms-go/internal/server/server.go`, `extensions/coms-go/internal/ipc/ipc.go` | — | `cd extensions/coms-go && go vet ./... && go build ./cmd/coms-go && ./coms-go version | grep -q 'coms-go v1.0.0'` |
| T2 | `unix-coder` | Implement all proto types, util helpers, audit JSONL writer, registry I/O, and transport (Unix-socket). Includes table-driven unit tests for each. | `extensions/coms-go/internal/{proto,util,audit,registry,transport}/*.go`, `extensions/coms.ts`, `extensions/coms-net.ts`, `scripts/coms-net-server.ts`, `SPEC/coms_go_port/coms_go_port.md` | `extensions/coms-go/internal/proto/{proto.go,proto_test.go}`, `extensions/coms-go/internal/util/{ulid.go,color.go,frontmatter.go,util_test.go}`, `extensions/coms-go/internal/audit/{audit.go,audit_test.go}`, `extensions/coms-go/internal/registry/{registry.go,registry_test.go}`, `extensions/coms-go/internal/transport/{transport_unix.go,transport_windows.go,transport_test.go}`, `extensions/coms-go/testdata/frontmatter/sample-agent.md` | T1 | `cd extensions/coms-go && go vet ./internal/proto/... ./internal/util/... ./internal/audit/... ./internal/registry/... ./internal/transport/... && go test ./internal/proto/... ./internal/util/... ./internal/audit/... ./internal/registry/... ./internal/transport/...` |
| T3 | `unix-coder` | Implement HTTP server (subcommand `serve`): router, handlers for all 10 routes, SSE writer + frames, broadcast helpers, background ticker loops, signal handling + atomic `server.json`/`server.secret.json` lifecycle, color-aware event logging matching the TS format. | `extensions/coms-go/internal/server/*.go`, `extensions/coms-go/internal/{proto,util,audit}/*.go`, `scripts/coms-net-server.ts` (reference), `SPEC/coms_go_port/coms_go_port.md` | `extensions/coms-go/internal/server/{server.go,handlers.go,state.go,loops.go,log.go,server_test.go}`, `extensions/coms-go/cmd/coms-go/main.go` (add `serve` dispatch) | T2 | `cd extensions/coms-go && go vet ./internal/server/... && go test ./internal/server/... && go build -o bin/coms-go ./cmd/coms-go && ./bin/coms-go serve --help 2>&1 | grep -q 'PI_COMS_NET_HOST'` |
| T4 | `unix-coder` | Implement client-local (subcommand `client-local`) and client-net (subcommand `client-net`): IPC stdin/stdout loop, identity resolution (CLI flags + frontmatter), session_start equivalents, ping/keepalive/heartbeat tickers, SSE reader with exponential backoff. | `extensions/coms-go/internal/{localclient,netclient,ipc}/*.go`, `extensions/coms-go/internal/{proto,util,audit,registry,transport}/*.go`, `extensions/coms.ts` (reference), `extensions/coms-net.ts` (reference), `SPEC/coms_go_port/coms_go_port.md` | `extensions/coms-go/internal/localclient/{client.go,handlers.go,tools.go,client_test.go}`, `extensions/coms-go/internal/netclient/{client.go,sse.go,tools.go,client_test.go}`, `extensions/coms-go/internal/ipc/{ipc.go,ipc_test.go}`, `extensions/coms-go/cmd/coms-go/main.go` (add `client-local`, `client-net` dispatch) | T2 | `cd extensions/coms-go && go vet ./internal/localclient/... ./internal/netclient/... ./internal/ipc/... && go test ./internal/localclient/... ./internal/netclient/... ./internal/ipc/... && go build -o bin/coms-go ./cmd/coms-go && ./bin/coms-go client-local --help 2>&1 | grep -q 'project' && ./bin/coms-go client-net --help 2>&1 | grep -q 'server-url'` |
| T5 | `unix-coder` | Capture golden JSON fixtures from the running TS server (using documented `curl` recipes against `bun scripts/coms-net-server.ts`); write `testdata/golden/README.md`; add integration tests under `//go:build integration` tag that replay each fixture against the Go server (route-parity) AND that perform a two-client end-to-end round-trip (register → SSE → send → response → await). | `extensions/coms-go/internal/server/*.go`, `extensions/coms-go/internal/netclient/*.go`, `extensions/coms-go/testdata/golden/*` (writing), `scripts/coms-net-server.ts` (reference) | `extensions/coms-go/testdata/golden/{README.md,health.json,register_resp.json,list_agents_resp.json,send_resp.json,get_message_resp.json,await_complete.json,await_timeout.json,sse_pool_snapshot.txt}`, `extensions/coms-go/internal/server/integration_test.go` | T3, T4 | `cd extensions/coms-go && go test -tags=integration ./internal/server/...` |
| T6 | `unix-coder` | Write `extensions/coms-go/shim.ts` (≤ 200 LOC): registers the 8 tools + 2 slash commands + 5 identity flags + 3 lifecycle hooks; spawns `coms-go client-local` and `coms-go client-net` child processes; pipes JSON-line IPC. Update `manifest.json` if needed. Touch the pi extension index (none today — pi auto-discovers extensions). | `extensions/coms.ts`, `extensions/coms-net.ts`, `extensions/themeMap.ts`, `TOOLS.md`, `SPEC/coms_go_port/coms_go_port.md`, `extensions/coms-go/cmd/coms-go/main.go` | `extensions/coms-go/shim.ts`, `extensions/coms-go/manifest.json` (refine if needed) | T1 | `wc -l extensions/coms-go/shim.ts | awk '{exit ($1 > 200)}' && cd extensions/coms-go && go build -o bin/coms-go ./cmd/coms-go && pi -e extensions/coms-go/shim.ts --version 2>&1 | grep -qi 'pi' ; echo "Note: pi --version path may differ; the real verify is the next task (T7 README) plus T11 cutover smoke."` |
| T7 | `unix-coder` | Update top-level `README.md` Prerequisites table: the Bun row is annotated as "required only for non-coms-go extensions (most of the repo); coms/coms-net now use Go". Add a new "Go" row (≥ 1.23). Update Extensions table: replace the `coms`/`coms-net` rows with a single `coms-go` row pointing at `extensions/coms-go/shim.ts`. Update CLAUDE.md if it makes any Bun-for-coms claims (it does not — verified). Add `coms-go-build`/`coms-go-test`/`coms-go-vet` recipes to `justfile`. Do NOT delete the TS files yet (that's T11). | `README.md`, `CLAUDE.md`, `justfile`, `SPEC/coms_go_port/coms_go_port.md` | `README.md`, `justfile` | T6 | `grep -q 'extensions/coms-go/shim.ts' README.md && grep -qE 'Go.*1\.23' README.md && grep -q 'coms-go-build' justfile && grep -q 'coms-go-test' justfile` |
| T8 | `tech-lead` | Architecture review of the Go module: confirm goroutine/channel patterns match the spec; confirm no third-party imports; confirm `internal/` packages have clean boundaries (no upward imports from `proto` to `server`); review error handling consistency. Produce review at `SPEC/coms_go_port/REVIEWS/coms_go_port_TECHLEAD_REVIEW.md`. | `extensions/coms-go/**/*.go`, `extensions/coms-go/manifest.json`, `extensions/coms-go/shim.ts`, `SPEC/coms_go_port/coms_go_port.md` | `SPEC/coms_go_port/REVIEWS/coms_go_port_TECHLEAD_REVIEW.md` | T3, T4, T5 | `test -s SPEC/coms_go_port/REVIEWS/coms_go_port_TECHLEAD_REVIEW.md && grep -qE '(APPROVED|CHANGES_REQUESTED)' SPEC/coms_go_port/REVIEWS/coms_go_port_TECHLEAD_REVIEW.md` |
| T9 | `security-review` | Security review focusing on: bearer token handling (constant-time compare, no logging, no echo in errors), auth boundary (every `/v1/*` route enforces bearer), pi-to-pi trust boundary (responder_session vs target_session check, hop limit enforcement, inbox cap, message TTL), file mode on `server.secret.json` (0600 — fail-closed on stricter check), input validation (JSON shape, URL params, oversized lines/bodies), and audit log content (no prompt bodies). Produce review at `SPEC/coms_go_port/REVIEWS/coms_go_port_SECURITY_REVIEW.md`. | `extensions/coms-go/**/*.go`, `SPEC/coms_go_port/coms_go_port.md`, `scripts/coms-net-server.ts` (compare to TS baseline) | `SPEC/coms_go_port/REVIEWS/coms_go_port_SECURITY_REVIEW.md` | T3, T4 | `test -s SPEC/coms_go_port/REVIEWS/coms_go_port_SECURITY_REVIEW.md && grep -qE '(APPROVED|CHANGES_REQUESTED)' SPEC/coms_go_port/REVIEWS/coms_go_port_SECURITY_REVIEW.md` |
| T10 | `code-review` | Code-quality + DRY review of the Go module: idiomatic Go (effective Go style guide), no duplicated helpers between `localclient` and `netclient` where consolidation is appropriate (e.g., shared frontmatter loading in `util/`), test coverage measurable by `go test -cover ./...` ≥ 70%, godoc completeness on every exported symbol in `internal/proto/` and `cmd/coms-go/`. Produce review at `SPEC/coms_go_port/REVIEWS/coms_go_port_CODEREVIEW.md`. | `extensions/coms-go/**/*.go`, `SPEC/coms_go_port/coms_go_port.md`, `SPEC/coms_go_port/REVIEWS/coms_go_port_TECHLEAD_REVIEW.md` | `SPEC/coms_go_port/REVIEWS/coms_go_port_CODEREVIEW.md` | T8 | `test -s SPEC/coms_go_port/REVIEWS/coms_go_port_CODEREVIEW.md && cd extensions/coms-go && go test -cover ./... 2>&1 | tee /tmp/coms-go-cover.txt && awk '/coverage:/{gsub("%","",$2); if ($2+0 < 70) exit 1}' /tmp/coms-go-cover.txt` |
| T11 | `merge-manager` | Cutover commit: delete `extensions/coms.ts`, `extensions/coms-net.ts`, `scripts/coms-net-server.ts`; flip `justfile` recipes `local-coms`/`coms`/`coms-net-server`/`coms-net-server-lan`/`coms1`/`coms2`/`coms3`/`coms4` to invoke `coms-go`-backed paths; update `README.md` to remove references to the deleted TS files (the row update was done in T7; this is the deletion cleanup). Produce ONE commit with message `feat(coms-go): migrate coms + coms-net to Go (cutover T11)`. After commit, run the integration smoke test as the post-cutover verification. | `extensions/coms.ts`, `extensions/coms-net.ts`, `scripts/coms-net-server.ts`, `justfile`, `README.md`, `SPEC/coms_go_port/coms_go_port.md`, T8/T9/T10 reviews | `justfile`, `README.md`, deletes: `extensions/coms.ts`, `extensions/coms-net.ts`, `scripts/coms-net-server.ts` | T5, T8, T9, T10 | `! test -f extensions/coms.ts && ! test -f extensions/coms-net.ts && ! test -f scripts/coms-net-server.ts && grep -q 'extensions/coms-go/shim.ts' justfile && cd extensions/coms-go && go test -tags=integration ./internal/server/...` |

### Verify-command notes

- T1 builds the binary with only stub subcommands; the `version` subcommand is the smallest verifiable surface.
- T2 omits `server` and `client-*` packages from `go test` (they don't exist yet); only the foundational packages.
- T3 + T4 fully exercise `go test ./internal/...` for their respective subdirs.
- T5 is the only task that runs `-tags=integration`.
- T6's verify is split: the LOC budget check is independent; the real shim verification is in T11's post-cutover integration run.
- T8/T9/T10 are gated on the presence of a non-empty review file containing either `APPROVED` or `CHANGES_REQUESTED`; if `CHANGES_REQUESTED`, the supervising agent must re-queue the failing implementation tasks before re-running the review.
- T11's verify requires that the deletions actually happened AND that the integration tests still pass post-cutover.

---

## 16. Dependency Graph

```
Phase 1 (parallel): [T1]
  T1: scaffold Go module + manifest + empty package structure

Phase 2 (after Phase 1): [T2, T6 (parallel)]
  T2: implement proto / util / audit / registry / transport packages + tests
  T6: write shim.ts (depends only on knowing the IPC contract, which is in the spec; shim does not invoke real Go logic until T11 cutover)

Phase 3 (after Phase 2): [T3, T4 (parallel)]
  T3: implement server (depends on proto/util/audit)
  T4: implement client-local + client-net + ipc (depends on proto/util/audit/registry/transport)

Phase 4 (after Phase 3): [T5]
  T5: capture golden fixtures + write integration tests (depends on server and netclient)

Phase 5 (after Phase 4): [T7, T8, T9 (parallel)]
  T7: update README + justfile (depends on shim existing — T6 — and Go module being buildable — T5)
  T8: tech-lead architecture review (depends on T3, T4, T5)
  T9: security review (depends on T3, T4)

Phase 6 (after Phase 5): [T10]
  T10: code-review pass (depends on T8 outcome)

Phase 7 (after Phase 6): [T11]  -- CUTOVER, single commit
  T11: merge-manager deletes TS files, flips justfile, makes one commit, runs post-cutover smoke
```

Acyclicity check: T1 → T2 → {T3, T4} → T5 → {T7, T8, T9} → T10 → T11. T6 branches off T1 alongside T2 and rejoins only at T11. No cycles.

---

## 17. Target State

**Files created:**

| File path | Approx lines | Executable |
|-----------|--------------|------------|
| `extensions/coms-go/go.mod` | 5 | No |
| `extensions/coms-go/go.sum` | 0 | No |
| `extensions/coms-go/manifest.json` | 15 | No |
| `extensions/coms-go/README.md` | 120 | No |
| `extensions/coms-go/shim.ts` | ≤ 200 | No |
| `extensions/coms-go/cmd/coms-go/main.go` | 120 | No |
| `extensions/coms-go/internal/proto/proto.go` | 220 | No |
| `extensions/coms-go/internal/proto/proto_test.go` | 80 | No |
| `extensions/coms-go/internal/util/ulid.go` | 50 | No |
| `extensions/coms-go/internal/util/color.go` | 50 | No |
| `extensions/coms-go/internal/util/frontmatter.go` | 90 | No |
| `extensions/coms-go/internal/util/util_test.go` | 120 | No |
| `extensions/coms-go/internal/audit/audit.go` | 60 | No |
| `extensions/coms-go/internal/audit/audit_test.go` | 60 | No |
| `extensions/coms-go/internal/registry/registry.go` | 180 | No |
| `extensions/coms-go/internal/registry/registry_test.go` | 100 | No |
| `extensions/coms-go/internal/transport/transport_unix.go` | 180 | No |
| `extensions/coms-go/internal/transport/transport_windows.go` | 50 | No |
| `extensions/coms-go/internal/transport/transport_test.go` | 120 | No |
| `extensions/coms-go/internal/localclient/client.go` | 240 | No |
| `extensions/coms-go/internal/localclient/handlers.go` | 120 | No |
| `extensions/coms-go/internal/localclient/tools.go` | 160 | No |
| `extensions/coms-go/internal/localclient/client_test.go` | 140 | No |
| `extensions/coms-go/internal/netclient/client.go` | 260 | No |
| `extensions/coms-go/internal/netclient/sse.go` | 130 | No |
| `extensions/coms-go/internal/netclient/tools.go` | 180 | No |
| `extensions/coms-go/internal/netclient/client_test.go` | 160 | No |
| `extensions/coms-go/internal/server/server.go` | 200 | No |
| `extensions/coms-go/internal/server/handlers.go` | 380 | No |
| `extensions/coms-go/internal/server/state.go` | 140 | No |
| `extensions/coms-go/internal/server/loops.go` | 130 | No |
| `extensions/coms-go/internal/server/log.go` | 130 | No |
| `extensions/coms-go/internal/server/server_test.go` | 220 | No |
| `extensions/coms-go/internal/server/integration_test.go` | 200 | No |
| `extensions/coms-go/internal/ipc/ipc.go` | 90 | No |
| `extensions/coms-go/internal/ipc/ipc_test.go` | 60 | No |
| `extensions/coms-go/testdata/frontmatter/sample-agent.md` | 12 | No |
| `extensions/coms-go/testdata/golden/README.md` | 40 | No |
| `extensions/coms-go/testdata/golden/health.json` | 6 | No |
| `extensions/coms-go/testdata/golden/register_resp.json` | 20 | No |
| `extensions/coms-go/testdata/golden/list_agents_resp.json` | 20 | No |
| `extensions/coms-go/testdata/golden/send_resp.json` | 5 | No |
| `extensions/coms-go/testdata/golden/get_message_resp.json` | 6 | No |
| `extensions/coms-go/testdata/golden/await_complete.json` | 6 | No |
| `extensions/coms-go/testdata/golden/await_timeout.json` | 6 | No |
| `extensions/coms-go/testdata/golden/sse_pool_snapshot.txt` | 12 | No |
| `extensions/coms-go/bin/coms-go-linux-amd64` | binary | Yes |
| `extensions/coms-go/bin/coms-go-linux-arm64` | binary | Yes |
| `extensions/coms-go/bin/coms-go-darwin-amd64` | binary | Yes |
| `extensions/coms-go/bin/coms-go-darwin-arm64` | binary | Yes |
| `SPEC/coms_go_port/REVIEWS/coms_go_port_TECHLEAD_REVIEW.md` | review | No |
| `SPEC/coms_go_port/REVIEWS/coms_go_port_SECURITY_REVIEW.md` | review | No |
| `SPEC/coms_go_port/REVIEWS/coms_go_port_CODEREVIEW.md` | review | No |

**Files modified:**

- `justfile` — coms-related recipes rewritten in T7 (build/test/vet additions) and T11 (recipe flip).
- `README.md` — Prerequisites table updated in T7 (Go row added; Bun annotated). Extensions table updated in T7 (coms/coms-net rows replaced by coms-go row).

**Files deleted (in T11 only):**

- `extensions/coms.ts`
- `extensions/coms-net.ts`
- `scripts/coms-net-server.ts`

Every file in any task's Write Scope appears above; every file above appears in some task's Write Scope.

---

## 18. Verification Plan

**Per-task checks:** (verbatim from Task Manifest Verify Command column)

- T1: `cd extensions/coms-go && go vet ./... && go build ./cmd/coms-go && ./coms-go version | grep -q 'coms-go v1.0.0'`
- T2: `cd extensions/coms-go && go vet ./internal/proto/... ./internal/util/... ./internal/audit/... ./internal/registry/... ./internal/transport/... && go test ./internal/proto/... ./internal/util/... ./internal/audit/... ./internal/registry/... ./internal/transport/...`
- T3: `cd extensions/coms-go && go vet ./internal/server/... && go test ./internal/server/... && go build -o bin/coms-go ./cmd/coms-go && ./bin/coms-go serve --help 2>&1 | grep -q 'PI_COMS_NET_HOST'`
- T4: `cd extensions/coms-go && go vet ./internal/localclient/... ./internal/netclient/... ./internal/ipc/... && go test ./internal/localclient/... ./internal/netclient/... ./internal/ipc/... && go build -o bin/coms-go ./cmd/coms-go && ./bin/coms-go client-local --help 2>&1 | grep -q 'project' && ./bin/coms-go client-net --help 2>&1 | grep -q 'server-url'`
- T5: `cd extensions/coms-go && go test -tags=integration ./internal/server/...`
- T6: `wc -l extensions/coms-go/shim.ts | awk '{exit ($1 > 200)}'` (LOC budget guard)
- T7: `grep -q 'extensions/coms-go/shim.ts' README.md && grep -qE 'Go.*1\.23' README.md && grep -q 'coms-go-build' justfile`
- T8: `test -s SPEC/coms_go_port/REVIEWS/coms_go_port_TECHLEAD_REVIEW.md && grep -qE '(APPROVED|CHANGES_REQUESTED)' SPEC/coms_go_port/REVIEWS/coms_go_port_TECHLEAD_REVIEW.md`
- T9: `test -s SPEC/coms_go_port/REVIEWS/coms_go_port_SECURITY_REVIEW.md && grep -qE '(APPROVED|CHANGES_REQUESTED)' SPEC/coms_go_port/REVIEWS/coms_go_port_SECURITY_REVIEW.md`
- T10: `test -s SPEC/coms_go_port/REVIEWS/coms_go_port_CODEREVIEW.md && cd extensions/coms-go && go test -cover ./... 2>&1 | tee /tmp/coms-go-cover.txt && awk '/coverage:/{gsub("%","",$2); if ($2+0 < 70) exit 1}' /tmp/coms-go-cover.txt`
- T11: `! test -f extensions/coms.ts && ! test -f extensions/coms-net.ts && ! test -f scripts/coms-net-server.ts && grep -q 'extensions/coms-go/shim.ts' justfile && cd extensions/coms-go && go test -tags=integration ./internal/server/...`

**Integration check (run after all tasks):**

```bash
# 1. Build all four target arches cleanly
cd /home/n0ko/Programs/ai/pi-vs-claude-code/extensions/coms-go
CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -o bin/coms-go-linux-amd64  ./cmd/coms-go
CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -o bin/coms-go-linux-arm64  ./cmd/coms-go
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o bin/coms-go-darwin-amd64 ./cmd/coms-go
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o bin/coms-go-darwin-arm64 ./cmd/coms-go

# 2. Run full test suite (unit + integration + race)
go vet ./...
go test ./...
go test -tags=integration ./internal/server/...
go test -race ./internal/server/...

# 3. End-to-end smoke (post-cutover): start the Go hub, register two clients, send a message, verify response
PI_COMS_NET_AUTH_TOKEN=smoke-tok PI_COMS_NET_PORT=43219 ./bin/coms-go serve > /tmp/coms-go-smoke.log 2>&1 &
HUB_PID=$!
sleep 1
test -f ~/.pi/coms-net/projects/default/server.json
curl -sf -H "Authorization: Bearer smoke-tok" http://127.0.0.1:43219/health | grep -q '"ok":true'
# (More extensive smoke is encoded in the integration tests.)
kill -INT $HUB_PID
wait $HUB_PID 2>/dev/null
! test -f ~/.pi/coms-net/projects/default/server.json  # cleanup confirmed
```

**Rollback:**

```bash
cd /home/n0ko/Programs/ai/pi-vs-claude-code
git revert <T11-cutover-commit-sha>
# This restores extensions/coms.ts, extensions/coms-net.ts, scripts/coms-net-server.ts,
# justfile, and README.md in a single atomic operation.
# The Go binary in extensions/coms-go/bin/ remains on disk but is no longer referenced.
```

---

## 19. Success Criteria (Machine-Verifiable)

- [ ] `cd extensions/coms-go && go build ./...` exits 0 with no warnings.
- [ ] `cd extensions/coms-go && go vet ./...` exits 0 with no findings.
- [ ] `cd extensions/coms-go && go test ./...` exits 0.
- [ ] `cd extensions/coms-go && go test -tags=integration ./internal/server/...` exits 0.
- [ ] `cd extensions/coms-go && go test -race ./internal/server/...` exits 0.
- [ ] `cd extensions/coms-go && go test -cover ./... 2>&1 | awk '/coverage:/{gsub("%","",$2); if ($2+0 < 70) exit 1}'` exits 0 (≥ 70% coverage).
- [ ] `(cd extensions/coms-go && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/coms-go-amd64 ./cmd/coms-go) && file /tmp/coms-go-amd64 | grep -q 'statically linked'` exits 0.
- [ ] `(cd extensions/coms-go && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/coms-go-arm64 ./cmd/coms-go)` exits 0.
- [ ] `(cd extensions/coms-go && CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o /tmp/coms-go-darwin-amd64 ./cmd/coms-go)` exits 0.
- [ ] `(cd extensions/coms-go && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o /tmp/coms-go-darwin-arm64 ./cmd/coms-go)` exits 0.
- [ ] `test -x extensions/coms-go/bin/coms-go-linux-amd64` exits 0.
- [ ] `test -x extensions/coms-go/bin/coms-go-linux-arm64` exits 0.
- [ ] `test -x extensions/coms-go/bin/coms-go-darwin-amd64` exits 0.
- [ ] `test -x extensions/coms-go/bin/coms-go-darwin-arm64` exits 0.
- [ ] `./extensions/coms-go/bin/coms-go-linux-amd64 version 2>&1 | grep -qE 'coms-go v[0-9]+\.[0-9]+\.[0-9]+'` exits 0.
- [ ] `wc -l extensions/coms-go/shim.ts | awk '{exit ($1 > 200)}'` exits 0 (shim ≤ 200 LOC).
- [ ] `find extensions/coms-go -name '*.ts' | grep -v '^extensions/coms-go/shim\.ts$' | wc -l | grep -q '^0$'` exits 0 (no TS files under coms-go except `shim.ts`).
- [ ] `find extensions/coms-go -name 'package.json' -o -name 'bun.lock' -o -name 'node_modules' | wc -l | grep -q '^0$'` exits 0 (no Bun/Node artifacts in coms-go).
- [ ] `! test -f extensions/coms.ts` exits 0 (TS deleted at cutover).
- [ ] `! test -f extensions/coms-net.ts` exits 0 (TS deleted at cutover).
- [ ] `! test -f scripts/coms-net-server.ts` exits 0 (TS deleted at cutover).
- [ ] `grep -q 'extensions/coms-go/shim.ts' justfile` exits 0 (justfile points to Go shim).
- [ ] `grep -q 'coms-go serve' justfile` exits 0 (justfile invokes Go server).
- [ ] `grep -q 'extensions/coms-go/shim.ts' README.md` exits 0 (README updated).
- [ ] `grep -qE 'Go.*1\.23' README.md` exits 0 (Go prereq listed).
- [ ] `test -s SPEC/coms_go_port/REVIEWS/coms_go_port_TECHLEAD_REVIEW.md && grep -q 'APPROVED' SPEC/coms_go_port/REVIEWS/coms_go_port_TECHLEAD_REVIEW.md` exits 0 (tech-lead approved).
- [ ] `test -s SPEC/coms_go_port/REVIEWS/coms_go_port_SECURITY_REVIEW.md && grep -q 'APPROVED' SPEC/coms_go_port/REVIEWS/coms_go_port_SECURITY_REVIEW.md` exits 0 (security review approved).
- [ ] `test -s SPEC/coms_go_port/REVIEWS/coms_go_port_CODEREVIEW.md && grep -q 'APPROVED' SPEC/coms_go_port/REVIEWS/coms_go_port_CODEREVIEW.md` exits 0 (code review approved).
- [ ] Two-host pi-to-pi smoke (run as part of `go test -tags=integration`): a `planner` agent and a `coder` agent register, planner sends a prompt with `target=coder`, coder receives the SSE `prompt` event, coder submits a response, planner's `/v1/messages/<id>/await` resolves with the response, all status transitions visible.
- [ ] Extension shim registers with pi: `pi -e extensions/coms-go/shim.ts --help 2>&1 | grep -E 'coms_list|coms_send|coms_net_list|coms_net_send'` exits 0 (tools surface in pi's help output).

### Functional Smoke Criteria

<!-- TUI smoke is not applicable here: coms-go has no TUI pane of its own.
     The pi pool widget is rendered inside pi's TUI by shim.ts at the same
     placement (belowEditor) as the original TS extensions, and is functionally
     unchanged. Verification is via the integration test that drives the SSE
     events that the widget consumes, not via a TUI snapshot. -->

<!-- Binary install verification -->
- [ ] `./extensions/coms-go/bin/coms-go-linux-amd64 version 2>&1 | grep -q 'coms-go v1.0.0'` -- built binary reports correct version
- [ ] `cd extensions/coms-go && go build -o /tmp/coms-go-fresh ./cmd/coms-go && /tmp/coms-go-fresh version >/tmp/fresh.txt && ./bin/coms-go-linux-amd64 version >/tmp/installed.txt && diff /tmp/fresh.txt /tmp/installed.txt` -- installed binary matches build artifact

<!-- Layout/config validation (justfile + README) -->
- [ ] `grep -q 'local-coms' justfile && grep -q 'extensions/coms-go/shim.ts' justfile` -- local-coms recipe updated
- [ ] `grep -q '^coms-net-server:' justfile && grep -q 'coms-go serve' justfile` -- coms-net-server recipe updated
- [ ] `! grep -qE '(bun scripts/coms-net-server\.ts|extensions/coms\.ts|extensions/coms-net\.ts)' justfile` -- no leftover Bun/TS references in justfile

<!-- Integration smoke (server + clients end-to-end) -->
- [ ] `cd extensions/coms-go && go test -tags=integration -run TestPiToPiRoundTrip ./internal/server/... -timeout 60s` exits 0 -- end-to-end pi-to-pi round-trip

---

## Agent Assignments

| Task | Agent | Rationale |
|------|-------|-----------|
| Module scaffold (T1) | `unix-coder` | Standard greenfield directory and package skeleton creation |
| Foundational packages (T2) | `unix-coder` | Implementation work on proto/util/audit/registry/transport with unit tests |
| HTTP server (T3) | `unix-coder` | Implementation of stdlib `net/http` routes, SSE writer, ticker loops |
| Clients + IPC (T4) | `unix-coder` | Implementation of long-lived client subcommands + stdin/stdout JSON framing |
| Golden fixtures + integration tests (T5) | `unix-coder` | Capturing reference outputs and writing build-tagged integration suite |
| Pi shim + manifest (T6) | `unix-coder` | TS bridge code; mechanical translation of registration calls into spawn+IPC |
| README + justfile (T7) | `unix-coder` | Documentation and build-recipe edits |
| Architecture review (T8) | `tech-lead` | Cross-package boundaries, goroutine patterns, stdlib-only enforcement |
| Security review (T9) | `security-review` | Bearer token, auth boundary, audit log, file mode 0600, hop limit, inbox cap |
| Code review (T10) | `code-review` | DRY, idiomatic Go, godoc, test coverage threshold |
| Cutover commit (T11) | `merge-manager` | Single atomic git commit deleting TS, flipping justfile, running post-cutover smoke |

## Execution Order

```
Phase 1: Scaffold
  └── T1 (unix-coder)

Phase 2: Foundations [blocked by Phase 1]
  ├── T2 (unix-coder)
  └── T6 (unix-coder)                       [parallel]

Phase 3: Subcommand implementations [blocked by Phase 2 — T2]
  ├── T3 (unix-coder)
  └── T4 (unix-coder)                       [parallel]

Phase 4: Integration tests [blocked by Phase 3]
  └── T5 (unix-coder)

Phase 5: Docs + Reviews [blocked by Phase 4]
  ├── T7 (unix-coder)
  ├── T8 (tech-lead)                        [parallel]
  └── T9 (security-review)                  [parallel]

Phase 6: Code review [blocked by Phase 5 — T8]
  └── T10 (code-review)

Phase 7: Cutover [blocked by Phase 6]
  └── T11 (merge-manager)
```

Recommended directive: `/swarm` — the dependency graph has natural parallel fan-out points (Phase 2: T2 || T6; Phase 3: T3 || T4; Phase 5: T7 || T8 || T9) which `/swarm`'s supervisor-driven orchestration exploits. `/pai` is the fallback if the supervisor prefers a strictly linear plan-then-implement pipeline.

## Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| Go test against golden fixture produces JSON with different key order | `diff` shows non-empty after both sides re-serialized | Re-serialize via `json.Marshal` on both sides before diff; if still failing, root-cause the field-order mismatch (likely a struct field reordering or omitempty difference) and fix the Go struct |
| `crypto/subtle.ConstantTimeCompare` returns false for what should be equal tokens | Auth test fails with 401 on a correct bearer | Verify both inputs are `[]byte` of identical length; check for trailing whitespace from env-var parsing; verify the bearer prefix `"Bearer "` is stripped before compare |
| SSE reader hangs after the hub restarts | Integration test times out at 60 s | Confirm the client-net subcommand's reconnect logic fires: `audit("sse_disconnect")` should appear in the log, followed by `audit("sse_reconnect_scheduled")` with rising backoff |
| `server.secret.json` written with wrong mode | `stat -c '%a' ~/.pi/coms-net/projects/default/server.secret.json` returns anything other than `600` | Use `os.WriteFile(name, data, 0600)` AND a follow-up `os.Chmod(name, 0600)` (defense in depth, matches TS); never write the file in a directory the user does not own |
| Race detector fires on `state.go` map operations | `go test -race ./internal/server/...` exits non-zero | Wrap every map mutation in the project-scoped `sync.RWMutex` Lock; reads under RLock; never iterate maps without holding at least RLock |
| Bun-talking pi instance (pre-cutover, on another host) cannot talk to Go-talking pi instance | Cross-host smoke fails during Phase A dual-run | Capture the failing request/response in the audit log; diff the JSON field-by-field against the TS golden fixtures; locate the wire-shape divergence and fix the Go struct or marshaller |
| Cutover commit (T11) breaks the integration test | `go test -tags=integration` fails after T11 | Immediately revert the cutover commit (`git revert HEAD`); the TS implementation is restored; root-cause the regression off the critical path |
| Audit log gets prompt body content (security regression) | `grep -E '"prompt":' ~/.pi/coms-log ~/.pi/coms-net-log` returns hits | Locate the offending `appendEntry`/`audit()` call site; remove the prompt field; add a unit test that asserts the audit JSONL line contains no `"prompt"` key |
| Bearer token appears in error message | `safeError()` output to user contains the token substring | Verify the `safeError` (Go: `SafeError`) helper applies token redaction unconditionally; never bypass it on the error path |

## Open Questions

| # | Question | Impact | Suggested Default |
|---|----------|--------|-------------------|
| 1 | Should the committed `bin/` cross-builds be checked into git, or built on demand via `just coms-go-build`? | If checked in, repo size grows ~50 MB; if not, every pi host must have `go` installed to use the extension. | **Suggested:** Check in the four cross-builds (one-time ~50 MB cost; matches "single artifact per arch" rationale and means pi hosts don't need a Go toolchain). T1 should mark `bin/` with a `.gitattributes` `binary` tag. |
| 2 | Should the Go module path be `github.com/pi-vs-cc/coms-go` (organization-style) or `local/coms-go` (private)? | Affects `go.mod` and import paths throughout the codebase. | **Suggested:** `github.com/pi-vs-cc/coms-go` — the repo is `pi-vs-cc` on GitHub and the convention costs nothing to keep aligned even though the module is never `go get`-ed from outside. |
| 3 | Does pi's extension loader recognize JSON manifests (`manifest.json`) today, or does it only auto-discover `.ts` files in `extensions/`? | Affects whether `manifest.json` is functionally meaningful or purely documentary. | **Suggested:** Treat `manifest.json` as documentary metadata for human readers; pi loads via the `.ts` shim. T6 verifies by `pi -e extensions/coms-go/shim.ts` directly. If pi adds manifest support later, `manifest.json` is already in place. |
| 4 | Should the post-cutover `bin/coms-go` invocation use a wrapper script (e.g., `extensions/coms-go/coms-go` that picks the right arch) or rely on shim.ts to pick from `bin/<os>-<arch>/`? | Affects whether `coms-go serve` (used in justfile) is a direct call or via a dispatcher. | **Suggested:** Add a `extensions/coms-go/coms-go` POSIX shell launcher (≤ 30 LOC, exempt from "no shell" rule because it is a launcher, not automation) that `exec`s the right `bin/coms-go-<os>-<arch>` for the current host. This makes the justfile recipe `coms-go serve` work portably. |

---

## Known Issues and Intentional Artifacts

Behavioral quirks of the TS implementation that MUST be preserved in the Go port (verified against the TS sources during T2/T3/T4):

| Issue | TS file | Details | Action |
|-------|---------|---------|--------|
| Coms-local AgentCard does NOT include `session_id`, `cwd`, `project`, `explicit`, `started_at`, `provider`, `status` (those are coms-net-only) | `coms.ts` L75-82 vs `coms-net.ts` L61-75 | Local and network mode use different AgentCard shapes; do not unify them | Reproduce as two distinct Go types: `AgentCardLocal` and `AgentCard` (network) |
| Inbound message hint text in `coms-net.ts` is multi-line including the literal `"DO NOT call coms_net_send/coms_net_await/coms_net_get to reply"` | `coms-net.ts` L681-685 | This text shapes LLM behavior on the receiver side; changing it may cause ping-pong loops | Reproduce verbatim in `localclient/handlers.go` (and the coms variant in `coms.ts` L631 likewise) |
| `LINE_CAP_BYTES = 64 * 1024` for Unix-socket envelopes | `coms.ts` L35 | Lines larger than 64 KB are rejected with `"line too large"` / `"malformed envelope"` | Reproduce verbatim |
| Server.secret.json is rejected by the client if mode != 0600 | `coms-net.ts` L308 | Defense in depth against world-readable tokens | Reproduce verbatim in `internal/netclient/client.go` (use `os.Stat` + `mode & 0o777 == 0o600` check) |
| `abbreviateModel` strips `"claude-"` prefix and truncates to 14 chars | `coms.ts` L205, `coms-net.ts` L244 | Used in widget rendering and log lines | Reproduce in `internal/util/color.go` (alongside `hexFg`, since both are widget-render helpers) |
| ULID returns first 26 chars of the time+random concatenation | `coms.ts` L151, `coms-net.ts` L198, `coms-net-server.ts` L286 | `(timeStr + randStr).slice(0, 26)` — the encoder produces more than 26 chars, then truncates | Reproduce the truncation; do not naïvely emit only 26 chars from the encoder |
| Hub default await timeout `DEFAULT_AWAIT_TIMEOUT_MS = 30_000` (separate from `MESSAGE_TTL_MS = 1_800_000`) | `coms-net-server.ts` L48 | Long-poll defaults to 30 s if `timeout_ms` query param missing | Reproduce verbatim |
| SSE keepalive frame is `: ping <iso>\n\n` (comment line, not an event) | `coms-net-server.ts` L1402 | Clients must accept SSE comments per spec | Reproduce verbatim |
| Server's `entryToCard` strips the `last_seen_at` and `registered_at` fields when projecting `RegistryEntry → AgentCard` | `coms-net-server.ts` L422-453 | The on-wire AgentCard intentionally hides these | Reproduce field-by-field; do not leak `last_seen_at` into the SSE payload |

---

## Go Language Reference (per the spec-builder rule: zero external knowledge required)

### Go stdlib HTTP server pattern

```go
mux := http.NewServeMux()
mux.HandleFunc("GET /health", handleHealth)
mux.HandleFunc("POST /v1/agents/register", requireAuth(handleRegister))
srv := &http.Server{
    Addr:    fmt.Sprintf("%s:%d", host, port),
    Handler: mux,
    // SSE: do NOT set ReadTimeout/WriteTimeout to non-zero (would kill long-lived streams)
}
go func() {
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        log.Fatal(err)
    }
}()
```

Method-prefixed pattern routing (`"GET /health"`, `"POST /v1/agents/register"`) is a Go 1.22+ feature in `net/http.ServeMux`. We rely on it (Go 1.23+ pinned), eliminating the need for a third-party router.

### SSE writer pattern

```go
func sseFrame(event string, data any, id int64) string {
    raw, _ := json.Marshal(data)
    if id > 0 {
        return fmt.Sprintf("event: %s\nid: %d\ndata: %s\n\n", event, id, raw)
    }
    return fmt.Sprintf("event: %s\ndata: %s\n\n", event, raw)
}

func handleEvents(w http.ResponseWriter, r *http.Request) {
    flusher, ok := w.(http.Flusher)
    if !ok {
        http.Error(w, "streaming not supported", http.StatusInternalServerError)
        return
    }
    w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
    w.Header().Set("Cache-Control", "no-cache, no-transform")
    w.Header().Set("Connection", "keep-alive")
    w.Header().Set("X-Accel-Buffering", "no")
    w.WriteHeader(http.StatusOK)
    flusher.Flush()

    // Per-connection goroutine; iterate frames from a channel; exit on r.Context().Done()
    for {
        select {
        case <-r.Context().Done():
            return
        case frame := <-clientChan:
            if _, err := io.WriteString(w, frame); err != nil {
                return
            }
            flusher.Flush()
        }
    }
}
```

### Atomic file write pattern

```go
func AtomicWrite(path string, data []byte, mode os.FileMode) error {
    tmp := path + ".tmp"
    if err := os.WriteFile(tmp, data, mode); err != nil {
        return err
    }
    if mode != 0 {
        // Defense in depth: os.WriteFile may not honor mode bits on all FSes.
        if err := os.Chmod(tmp, mode); err != nil {
            os.Remove(tmp)
            return err
        }
    }
    if err := os.Rename(tmp, path); err != nil {
        os.Remove(tmp)
        return err
    }
    return nil
}
```

### Unix-socket bind with stale-file probe

```go
func ProbeStaleSocket(path string) (string, error) {
    conn, err := net.DialTimeout("unix", path, 250*time.Millisecond)
    if err != nil {
        if errors.Is(err, syscall.ECONNREFUSED) || os.IsNotExist(err) {
            return "stale", nil
        }
        return "stale", nil // unknown errors -> treat as stale (matches TS)
    }
    conn.Close()
    return "in_use", nil
}

func BindEndpoint(path string, handler func(net.Conn)) (net.Listener, error) {
    if _, err := os.Stat(path); err == nil {
        verdict, _ := ProbeStaleSocket(path)
        if verdict == "in_use" {
            return nil, fmt.Errorf("coms: endpoint already in use (%s)", path)
        }
        os.Remove(path)
    }
    l, err := net.Listen("unix", path)
    if err != nil {
        return nil, err
    }
    go func() {
        for {
            c, err := l.Accept()
            if err != nil {
                return
            }
            go handler(c)
        }
    }()
    return l, nil
}
```

### Constant-time bearer compare

```go
func TokensEqual(a, b string) bool {
    ab := []byte(a)
    bb := []byte(b)
    if len(ab) != len(bb) {
        return false
    }
    return subtle.ConstantTimeCompare(ab, bb) == 1
}
```

### JSON-line IPC framing

```go
// Reader side: bufio.Scanner with a generous buffer for prompts up to 64 KB.
scanner := bufio.NewScanner(os.Stdin)
scanner.Buffer(make([]byte, 0, 4096), 1024*1024) // 1 MB max line
for scanner.Scan() {
    var frame IPCFrame
    if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
        // log and continue
        continue
    }
    dispatch(frame)
}

// Writer side: a single goroutine owns os.Stdout to serialize writes.
type Writer struct {
    mu sync.Mutex
    w  io.Writer
}
func (w *Writer) Write(frame IPCFrame) error {
    raw, err := json.Marshal(frame)
    if err != nil {
        return err
    }
    w.mu.Lock()
    defer w.mu.Unlock()
    _, err = fmt.Fprintf(w.w, "%s\n", raw)
    return err
}
```

### Build tags for OS-specific transports

```go
// transport_unix.go
//go:build !windows

package transport

// Unix-socket implementation

// transport_windows.go
//go:build windows

package transport

// Named-pipe stub (currently no-op; pi targets are Linux/macOS)
```

---

End of spec.
