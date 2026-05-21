VERDICT: CHANGES_REQUESTED

# Tech-Lead Architecture Review — coms_go_port

## Scope

Architecture review of the Go port at `extensions/coms-go/` against the spec at `SPEC/coms_go_port/coms_go_port.md`. Read every Go source file under `internal/` and `cmd/coms-go/`, the TS references (`extensions/coms.ts`, `extensions/coms-net.ts`, `scripts/coms-net-server.ts`), and the spec sections governing T8 (§2, §4-5, §7-8, §10, §15, §17). Ran `go vet ./...`, `go test ./...`, `go test -tags=integration ./...`, and `go test -race -tags=integration -run TestPiToPiRoundTrip ./internal/server/...` repeatedly. Read-only — no source modified.

## Findings

### Architecture / Boundaries

- Package dependency graph is a clean DAG. `proto` and `util` have no internal deps. `audit`, `registry`, `transport`, `ipc` only depend on `proto`/`util`. `server`, `localclient`, `netclient` are top-level consumers; none of the three top-level packages import each other. `cmd/coms-go/main.go` is the sole binder of all three modes. No upward imports from `proto` to anywhere, matching the T8 contract.
- `internal/server/server.go` cleanly separates Config parsing, listener bind, atomic file-writes for `server.json` / `server.secret.json`, signal handling, and HTTP wiring. Routes split into `routes.go` (handlers), `state.go` (in-memory project state + helpers under `sync.RWMutex`), `tickers.go` (background loops), `sse.go` (frame format), `log.go` (color-aware event lines), `auth.go` (constant-time bearer + safeError), `secret.go` (token gen + atomic chmod 0600).
- One mild boundary concern: `cmd/coms-go/main.go` does its own ad-hoc flag parsing for client subcommands but delegates flag parsing for `serve` into `internal/server.ParseConfig` (which uses a hand-rolled loop, not `flag.FlagSet`). The asymmetry is cosmetic; both work.
- `internal/proto` includes an `IPCFrame` struct that overlaps with `internal/ipc`'s own `Request` / `toolResponse` / `toolError` / `eventFrame` types. The two type sets never collide (ipc.go uses its own private types on the wire), but the dead-ish `proto.IPCFrame` should either be the canonical IPC type or removed. Non-blocking; flag for T10.
- `internal/server/regexp.go` is a 9-line file holding two compiled regexps for route parameter extraction. The use of regexp for `:sid` / `:msg_id` parsing instead of Go 1.22+ method-prefix patterns (`POST /v1/agents/{sid}/heartbeat`) is acceptable — both are stdlib — but it's worth noting the spec L1712 explicitly cites the 1.22+ pattern feature as the rationale for "no third-party router." Non-blocking.
- TS shim and Go binary boundary: `shim.ts` (not yet authored per the manifest stub at the top of the spec) is out of T8 scope here; T8 evaluates the Go side and the contract surface (`internal/ipc`) is sufficient.

### Concurrency

- **DATA RACE — must fix before cutover.** `go test -race -tags=integration -run TestPiToPiRoundTrip ./internal/server/... -timeout 60s` fails intermittently with a write/read race on `Awaiter.timer`. `routes.go:699` writes `a.timer = timer` AFTER releasing `foundP.mu.Unlock()` at line 684, while a concurrent `releaseAwaiters` invoked from `handleSubmitResponse` (`routes.go:818`) or `ttlScanTick`/`staleScanTick` reads `a.timer.Stop()` at `state.go:193` under `p.mu.Lock`. Repro at `-count=5` reproduced two failures across runs; one in three executions of the targeted test hits the race. Fix: either set `a.timer` BEFORE inserting `a` into `p.awaiters[msgID]` while still holding `foundP.mu`, or guard `a.timer` with `a.mu`.
- Goroutine layout matches §8. One accept goroutine via `http.Server.Serve`, per-request goroutines via `net/http`, three background tickers (`stale`/`ttl`/`keepalive`) launched from `startTickers` and bound to `context.WithCancel`. Ticker goroutines exit on `ctx.Done()`. Signal handler cancels the context, calls `srv.Shutdown`, broadcasts `agent_left` per-project, and closes per-stream `done` channels. Clean.
- Per-SSE-stream goroutine pattern is the http handler itself — there is no separate writer goroutine. `sendFrame` (state.go:176) does a non-blocking send-to-buffered-channel (cap 64) with `default` fallback. This avoids unbounded memory growth on slow consumers. Acceptable.
- `releaseAwaiters` properly stops the per-awaiter timer before delivering on the channel. The send on `a.ch` is itself non-blocking (cap 1) so a goroutine that already dropped out of the select (e.g., via timer-fired path) does not stall the response handler.
- `ProjectState` mutex granularity: one `sync.RWMutex` per project, covering `agents`, `messages`, `streams`, `awaiters`, and `nameIndex`. Per spec §8 ("minimizes lock granularity churn vs separate locks"). Reasonable.
- `audit.Logger` uses an in-process `sync.Mutex` only — no `flock(LOCK_EX)`. Spec §7 ("Audit log appends") explicitly mandates `flock(LOCK_EX) via syscall.Flock` to serialize writes across pi processes. Multiple `coms-go client-local`/`client-net` instances on the same host can interleave partial JSONL lines into `~/.pi/coms-log` and `~/.pi/coms-net-log`. Fix: open with `O_APPEND|O_CREATE|O_WRONLY` and `syscall.Flock(fd, LOCK_EX)` before write, `LOCK_UN` after.

### Wire-level parity (spot-check)

- `/v1/agents/register` Go `routes.go:129` ↔ TS `coms-net-server.ts:525`. Same request shape, same response (with `sse_url`), same name-collision suffix behavior via `resolveUniqueName`. Re-register preserves `started_at` / `registered_at` / `context_used_pct` / `queue_depth`.
- `/v1/events` SSE: Go emits `event: <name>\nid: <n>\ndata: <json>\n\n` (sse.go:18) ↔ TS `sseFrame()` `coms-net-server.ts:357`. Keepalive frame `: ping <iso>\n\n` (sse.go:24) ↔ TS L1402. `hello` payload `{server_time, server_id}` matches. `pool_snapshot` filters out `self` and `explicit` agents matching TS L673.
- `/v1/messages` `routes.go:443` ↔ TS L823: hop limit returns 409 with `hop_limit_exceeded`; `inbox_full` returns 429; `ambiguous_target` returns 409 with `candidates`; `target_not_found` returns 404 with `target` detail. Status codes match the §10 table.
- `/v1/messages/:id/await` returns `{msg_id, status:"timeout", error:"timeout"}` on timeout (routes.go:718-724) — matches TS behavior. Default 30s, clamped to `MessageTTLMS`.
- Error string list (`invalid_json`, `invalid_request`, `unauthorized`, `not_found`, `agent_not_found`, `sender_not_registered`, `missing_session_id`, `missing_target`, `target_not_found`, `ambiguous_target`, `hop_limit_exceeded`, `inbox_full`, `message_not_found`, `not_target`, `already_terminal`, `method_not_allowed`) all present verbatim in `routes.go`.
- ULID `util/ulid.go` is 10-char base32 timestamp + 16-char base32 randomness from 10 random bytes = 26 chars; alphabet matches TS Crockford table (no `I`/`L`/`O`/`U`). Good.
- Bearer auth (`auth.go`): `subtle.ConstantTimeCompare` after length-equality precheck. `safeError` redacts via regex replace. `WWW-Authenticate: Bearer realm="coms-net"` set on 401.
- `server.secret.json` write/read parity: written via `secret.go:writeServerSecret` (mode 0600 with belt-and-suspenders chmod), read by `netclient/client.go:readServerSecret` rejecting if `fi.Mode()&0o777 != 0o600` (TS L308 parity).
- **Minor parity defect — unknown routes return 200 empty body.** `routes.go:39-45` `/` catch-all returns silently for any non-`/` path. Verified by ad-hoc probe: `GET /foobar` returns `200 ""` instead of `404 {"ok":false,"error":"not_found"}`. The TS server returns 404 for unknown routes. Fix: drop the `if r.URL.Path != "/"` early-return; write the `not_found` envelope unconditionally for the catch-all.

### Spec adherence

- Go module `module github.com/pi-vs-cc/coms-go; go 1.23`. `go.sum` does not exist (`stat: no such file`) — equivalent to empty. Stdlib-only confirmed by grep across all files.
- Single static binary, multi-mode dispatch — `cmd/coms-go/main.go` dispatches `serve`, `client-local`, `client-net`, `version`.
- Boot banner (`log.go:BootBanner`) prints `coms-net: listening on <url>`, `project=`, `server.json=`, `server.secret.json=<path> (chmod 0600)` — token contents are NEVER printed. `safeError` redacts inadvertent bearer leakage in error messages.
- Atomic file writes — `util.AtomicWrite` (tmp + chmod + rename), used by `registry.Write`, `server.atomicWrite`, `secret.writeServerSecret`.
- Audit logger: contracts content correctly — `audit.go` docstring forbids prompt bodies, `localclient/handlers.go` and `netclient/client.go` only log event metadata. However the flock requirement is unmet (see Concurrency).
- `internal/server/state.go` uses `sync.RWMutex` per project as required.
- Atomic shutdown — `server.go` signal handler unlinks `server.json` and (if owned) `server.secret.json` before `srv.Shutdown`. Idempotent via `unlinked` flag.
- Cross-platform: `transport_unix.go` build-tagged `!windows`, `transport_windows.go` build-tagged `windows`. Pi targets covered. `localclient` is `!windows`. `netclient` is platform-independent.
- Tests: full unit suite passes (`go test ./...` exit 0), integration suite passes (`go test -tags=integration ./...` exit 0), race passes on plain unit tests. Race FAILS on integration round-trip as described above.
- Spec §17 "Known Issues" items spot-checked: AgentCardLocal vs AgentCard split (two distinct types) ✓; LINE_CAP_BYTES = 64 KB in `transport_unix.go:22` ✓; ULID 26-char truncation ✓; DEFAULT_AWAIT_TIMEOUT_MS = 30_000 in `routes.go:661` ✓; SSE keepalive `: ping <iso>\n\n` ✓; `entryToCard` strips `last_seen_at`/`registered_at` ✓ (returns embedded AgentCard only).
- One omission noted but classified as non-blocking for T8: the inbound-prompt hint string (spec §17 row 2: "DO NOT call coms_net_send/coms_net_await/coms_net_get to reply") is present in `coms-net.ts:683` but appears to live in the tool response wrapper text, which in Go is presumably emitted by `internal/netclient/tools.go` or by the shim. I did not find the verbatim string in the netclient package; verify in T10. Flag for T10 (DRY/parity review).

### Cutover readiness

The code is structurally sound. Package boundaries are correct, the SSE/HTTP routing matches §10 byte-for-byte where I spot-checked, the bearer story is clean, atomic writes are in place, and the boot/shutdown lifecycle is well-formed. Tests pass without `-race`. However, the data race on `Awaiter.timer` is a real concurrency bug reproducible under the spec's own success-criteria command (`go test -race -tags=integration ./internal/server/... → exit 0`). The audit-log `flock` omission is a multi-process correctness gap that will silently corrupt JSONL when two pi instances run on one host (the realistic deployment mode). The 200-empty catch-all is a minor parity defect. Cutover (T11) is paused until these three items are addressed.

## Required changes (if VERDICT != APPROVED)

1. **`internal/server/routes.go:679-699` — fix Awaiter.timer data race.** Either: (a) set `a.timer` BEFORE the `foundP.mu.Unlock()` at line 684, by constructing the timer between line 678 and line 679 and assigning under the held lock; OR (b) add a `sync.Mutex` to `Awaiter` and guard reads/writes of `timer` with it. Option (a) is cleaner because it keeps `Awaiter` immutable post-publication. After fix, `go test -race -tags=integration -run TestPiToPiRoundTrip -count=10 ./internal/server/...` must pass.
2. **`internal/audit/audit.go:35-60` — add `syscall.Flock(LOCK_EX)` around the append.** Open with `os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)`, call `syscall.Flock(int(f.Fd()), syscall.LOCK_EX)` before `f.Write`, defer `syscall.Flock(fd, syscall.LOCK_UN)` (close handles the rest). Required by spec §7 ("Audit log appends") for cross-process safety; the current in-process `sync.Mutex` only serializes within one binary, not across two `coms-go client-*` siblings sharing `~/.pi/coms-log`. Add a test that spawns two goroutines holding two distinct `*Logger` values pointing at the same path and verifies N×M lines arrive intact.
3. **`internal/server/routes.go:39-45` — fix unknown-route catch-all to return 404.** Drop the `if r.URL.Path != "/"` early-return; emit `writeError(w, "not_found", http.StatusNotFound, nil)` unconditionally. Verified failing parity case: `GET /foobar` returns 200 empty body. Add a test for `GET /foobar` → 404 with `not_found` envelope.

## Suggestions (non-blocking)

1. `internal/proto/proto.go:294-310` — `IPCFrame` struct overlaps with `internal/ipc` private types; either promote `ipc`'s types into `proto` or drop `proto.IPCFrame`. Pick one. Currently it's a dead type.
2. `internal/server/server.go:86-141` — replace the hand-rolled `ParseConfig` flag loop with `flag.NewFlagSet`. Matches the style already used in `cmd/coms-go/main.go` for client subcommands. Cosmetic.
3. `internal/server/routes.go:622-635` — `handleAwaitMessage` does an O(N) scan over all projects to find the message. The same scan exists in `handleGetMessage` (L600) and `handleSubmitResponse` (L751). Consider a top-level `msgID → projectName` index on `ServerState` if N grows. Today N=1 in practice (single project per server), so this is purely speculative.
4. `internal/server/sse.go:17` — `json.Marshal` error is silently swallowed (`dataBytes, _ := json.Marshal(data)`). Should not be possible for the structured payload types we use, but a log line on the (impossible) error path would catch future regressions.
5. `cmd/coms-go/main.go:46-91, 93-145` — `runLocalClient` and `runNetClient` duplicate ~30 LOC of flag-binding logic. Factor a shared `bindIdentityFlags(fs, &cfg)` helper. Non-blocking; flag for T10.
6. `internal/server/routes.go:564-585` — when the target stream is not yet open, the message stays `queued` and the prompt is never emitted as an SSE frame to the target. The target will discover the message only via its own `agent_joined`/SSE-open snapshot replay — except the snapshot in `handleEvents` (lines 263-272) snapshots AGENTS, not QUEUED MESSAGES. Verify against TS behavior in T5 fixtures. May not be a bug (TS may have the same behavior), but worth a sanity check.
