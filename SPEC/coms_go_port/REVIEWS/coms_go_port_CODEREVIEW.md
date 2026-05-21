VERDICT: CHANGES_REQUESTED

# Code Review — coms_go_port

## Scope

T10 code-quality review of the Go module at `/home/n0ko/Programs/ai/pi-vs-claude-code/extensions/coms-go/` covering idiomatic Go, DRY, test coverage, godoc, dead code, logging discipline, and build hygiene. Architecture (T8) and security (T9) are out of scope. The module ports `coms.ts` + `coms-net.ts` + `scripts/coms-net-server.ts` to a single static Go binary; the implementation is functionally complete (build, unit tests, race, and integration tests all pass) but does not yet meet the §19 quality gates.

## Build / Test results

- go vet: PASS (exit 0)
- go test ./...: PASS (exit 0)
- go test -tags=integration: PASS (exit 0; pi_to_pi round-trip + golden fixtures pass)
- go test -race: PASS (exit 0, 120s timeout)
- gofmt -l: 11 files report formatting drift (struct/map alignment regressions). Files: `internal/audit/audit_test.go`, `internal/localclient/client.go`, `internal/localclient/tools.go`, `internal/netclient/client.go`, `internal/netclient/client_test.go`, `internal/netclient/sse.go`, `internal/proto/proto.go`, `internal/proto/proto_test.go`, `internal/server/server.go`, `internal/server/state.go`, `internal/util/util_test.go`.
- coverage:

| Package | Coverage | Meets ≥70% gate? |
|---------|----------|------------------|
| `cmd/coms-go` | 0.0% | NO |
| `internal/audit` | 77.8% | yes |
| `internal/ipc` | 88.6% | yes |
| `internal/localclient` | 38.8% | NO |
| `internal/netclient` | 35.2% | NO |
| `internal/proto` | [no statements] | n/a (types only) |
| `internal/registry` | 75.4% | yes |
| `internal/server` | 53.3% | NO |
| `internal/transport` | 65.8% | NO |
| `internal/util` | 80.0% | yes |
| **Overall (per `go tool cover -func`)** | **45.4%** | **NO** |

The spec §19 / T10 verify clause runs `awk '/coverage:/{gsub("%","",$2); if ($2+0 < 70) exit 1}'`, which fails on every line below 70 — five packages currently fail that gate.

## Findings

### Idiomatic Go issues

- `internal/netclient/sse.go:96-114` (`readSSEStream`) allocates a `bufio.Scanner` it never uses; the comment "Simpler: just feed raw bytes" plus `_ = sc // suppress unused warning` is a code smell. Either remove the scanner allocation or implement the scanner-based read path. Currently dead code at a hot path.
- `internal/localclient/client.go:566` and `internal/netclient/client.go:1213` use `fmt.Sscanf(v, "%d", &n)` and ignore the returned error. Prefer `strconv.Atoi(v)` with explicit error handling (already the pattern in `server/server.go` `envInt`).
- `internal/server/server.go:286` formats a fatal-ish event to stdout (`fmt.Printf("coms-net: %s received, shutting down\n", sig)`) inside the signal handler. Per Design Principle §5, fatal/panic conditions should go to stderr; a SIGTERM shutdown isn't fatal but the line is operator notification, not a structured event. Minor.
- `internal/server/server.go:305` swallows `srv.Shutdown` error with `//nolint:errcheck` rather than logging it. At least `_ = srv.Shutdown(...)` or a one-line log on non-nil error.
- `internal/server/server.go:215` `ln.Addr().(*net.TCPAddr).Port` is an unchecked type assertion; if the listener is ever changed to a non-TCP type it panics. Add `if tcp, ok := ln.Addr().(*net.TCPAddr); ok { ... }`.
- `internal/localclient/client.go:204-218`: the accept loop's goroutine has no shutdown signal — when `ctx.Done()` fires, the only escape is for `ln.Close()` to return an error from `Accept()`. Works but coupling is implicit. Documenting the contract or passing ctx explicitly is more idiomatic.
- `internal/localclient/client.go:466` `QueueDepth: func() *int { v := queueDepth; return &v }()` — IIFE just to take address of a local int. Replace with `queueDepth := queueDepth; entry.QueueDepth = &queueDepth` (or a tiny `intPtr` helper). Same pattern in netclient.
- `internal/localclient/handlers.go:82-91`: writes to `c.inboundQueue[env.MsgID]` then assigns `c.currentInbound = c.inboundQueue[env.MsgID]` — second map lookup is unnecessary; assign once: `entry := &inboundCtx{...}; c.inboundQueue[env.MsgID] = entry; c.currentInbound = entry`.
- `internal/server/state.go:14`: `timer interface{ Stop() bool } // *time.Timer` — using an interface to weaken type for testing is unusual; document it or use `*time.Timer` directly and inject via test seam.
- `internal/server/server.go:288-303`: signal handler iterates `st.allProjects()` (which RLocks then returns a snapshot), then takes `p.mu.Lock()` on each — fine, but reads `p.streams` to close all `done` channels while broadcasting; the broadcast happens under p.mu.Lock so any blocked `sendFrame` is bounded by buffer size (64). Reasonable but the comment is missing.
- `internal/netclient/client.go:498`: `backoffMs := reconnectBaseMs * (1 << attempts)` — if `attempts` exceeds 31, this overflows int32 (and 63 on int64); cap before shift. Practically benign because the cap below clamps it but not idiomatic.

### DRY

- **`atomicWrite` duplicated.** `internal/server/secret.go:25-42` implements `atomicWrite` privately; `internal/util/atomic.go:13` exports `util.AtomicWrite` (used by `internal/registry`). Server should call `util.AtomicWrite` and delete the private copy. Spec §2 design principle §8 ("Atomic writes everywhere persistence touches disk") implies one canonical implementation.
- **`envOr`, `envInt`, `mustGetwd` duplicated three ways.** `internal/localclient/client.go:553-579` and `internal/netclient/client.go:1200-1226` are byte-for-byte copies. `internal/server/server.go:326-352` has the same `envStr` / `envInt` under different names. Move to `internal/util/env.go` (e.g. `util.EnvStr`, `util.EnvInt`, `util.MustGetwd`).
- **URL encoding split.** `internal/server/routes.go:909` uses `url.QueryEscape`; `internal/netclient/client.go:1179-1194` implements a hand-rolled `urlEscape` percent-encoder. Pick one (stdlib `url.QueryEscape` is fine). If specific encoding is needed for parity, the rationale should be a comment.
- **`safeError` doubles.** `internal/server/auth.go:35-43` redacts Bearer tokens from server-side error strings; `internal/netclient/sse.go:171-188` exposes `safeError(err, token)` + `safeErrorStr(msg, token)`. The two serve distinct shapes (regex strip vs literal token replace) and the rename `safeErrorStr` is fine; document why both exist in package docs.
- **Tool dispatcher symmetry.** `internal/localclient/tools.go:25-39` and `internal/netclient/tools.go:15-28` are structurally identical switch statements; a shared `dispatchByName(map[string]handler, req, w)` would deduplicate the boilerplate, but the four tool names differ between modes so the win is marginal. Acceptable as-is; consider only if a third client mode is ever added.
- **Frontmatter / hex / ULID dedup OK.** `util.ParseFrontmatter`, `util.FindSystemPromptPath`, `util.ReadFrontmatterFromArgv`, `util.FallbackColor`, `util.IsValidHex`, `util.NewULID` are properly centralized in `internal/util/` and called from both clients. This part of the migration is clean.

### Test coverage gaps

The §19 / T10 gate fails on five packages (see table above). High-value gaps:

- `internal/netclient/client.go:705 handleInboundPrompt` (0%) — handles the inbound `prompt` SSE event, central to the round-trip behavior. The pi_to_pi integration test covers this via the network seam, but a focused unit test (synthetic SSEEvent → expected `inboundQueue` mutation) is missing.
- `internal/netclient/client.go:751 handleInboundResponse` (0%) — symmetric gap on the response path; resolves pending replies and is the seam where the orphan-response audit fires.
- `internal/netclient/client.go:799 sendHeartbeat` (0%) — heartbeat ticker payload. Mock HTTP server roundtrip would also cover the `urlEscape` path.
- `internal/netclient/client.go:856 onAgentEnd` (0%) — agent_end → response submission, includes the schema-vs-plain-text branch.
- `internal/netclient/client.go:1126 mapToAgentCard` and `:1150 mergePatch` (0%) — pure functions with simple inputs; trivial to test.
- `internal/netclient/sse.go:141 intField` / `:152 boolField` / `:158 rawJSON` / `:183 safeErrorStr` (0%) — pure helpers, trivial tests.
- `internal/localclient/client.go:420 refreshPool`, `:442 keepalive`, `:476 handleLifecycle`, `:538 listProjects` (0%) — long-running tickers and lifecycle event handler. Unit-testable with table-driven inputs.
- `internal/localclient/tools.go:161 toolSend`, `:317 toolAwait`, `:368 deliverAwaitResult`, `:388 resolveTarget`, `:428 buildPingEnvelope` (0%) — the entire `coms_send` / `coms_await` path is untested. Existing tests exercise `coms_list` and `coms_get` only.
- `internal/localclient/handlers.go:108 handleResponse` (0%), `:179 writeAck` (0%), `:222 onAgentEnd` (0%) — same shape.
- `internal/server/routes.go` — `handleAwaitMessage`, `handleSubmitResponse`, `handleDeleteAgent` all have moderate coverage via integration tests but the unit-level error-path tests (already-terminal, not-target, ambiguous target, hop_limit_exceeded, inbox_full) are sparse. §19 requires "at least one auth/permission failure path per route family" which is present in `server_test.go` but the request-shape failures are thinner.
- `internal/server/log.go:142 BootBanner` (0%), `:23 initColors` (0%), `:120-126 logStale/logOffline/logExpired` (0%) — exercised in real runs but untested. Simple stdout-capture tests would lift the package to ≥70%.
- `internal/server/secret.go` — `generateToken`, `atomicWrite`, `writeServerSecret` all 0%. After consolidating with `util.AtomicWrite` per the DRY finding, only `generateToken` and `writeServerSecret` remain to test.
- `internal/transport` — sits at 65.8%, just below the gate. Add tests for `WriteEnvelope` size-cap error and `ProbeStaleSocket` "stale" branch (touching a path that doesn't accept) to clear ≥70%.

### godoc gaps

- `internal/server/log.go` — `logLine`, `dim`, `tail6`, `logRegister`, `logUnregister`, `logSseOpen`, `logSseClose`, `logMessageSend`, `logResponse`, `logStale`, `logOffline`, `logExpired`, `logHeartbeat`, `logRejected` are all unexported; godoc tolerates missing comments on unexported. `BootBanner` (exported) has a doc comment. OK.
- `internal/server/state.go:11 Awaiter`, `:17 SseWriter`, `:26 ProjectState`, `:46 ServerState` all have doc comments starting with the type name. Pass.
- `internal/server/server.go:51 Config` doc comment is one-liner; the individual fields lack godoc. Per `go doc` convention, exported struct fields ought to have inline comments when their semantics aren't obvious — fields like `TokenOwned`, `OfflineAfterMS`, `StaleAfterMS` benefit from a sentence each. Minor.
- `internal/proto/proto.go` — exported types and fields are well-documented; ports the TS comments through. Pass.
- `internal/netclient/sse.go:22 SSEEvent`, `:29 SSEParser`, `:195 HTTPError` all have godoc. `HTTPError.Err` field is exported but undocumented.
- `cmd/coms-go/main.go` — package doc is a comment, not a `// Package main` heading. Acceptable for `main` but `// Package main implements the coms-go CLI...` is preferred. Minor.

### Dead code

- `internal/netclient/sse.go:96-114` — unused `bufio.Scanner` + `_ = sc` suppression (called out under Idiomatic Go too).
- `internal/server/server.go:215` — `claimedPort` is captured then re-used via `cfg.Port` (mostly unused in the rest of the file beyond `localURL`). Fine.
- No unused imports, unused variables, or unused functions detected by `go vet`. `/simplify` ran on T3 + T5 and the residue is small.

### Other

- **Logging discipline:** `internal/server/log.go` writes to stdout via `fmt.Printf` (correct per spec §5 — `serve` uses stdout for parity with the TS console.log output). Client packages emit operator-readable text via `fmt.Fprintf(os.Stderr, ...)` (`localclient/client.go:212, 232`; `netclient/client.go:186, 193, 523`). This is correct: stdout is reserved for IPC frames in client modes. Verified.
- **No `panic()` outside main or init.** `rg -n "panic\(" --type=go | grep -v _test.go` returns no matches. Pass.
- **Defer placement.** Spot-checked `handleConn`, `dispatchTool`, `handleSendMessage`, `staleScanTick` — all defers sit immediately after the resource acquisition. Pass.
- **`time.Sleep` in tests.** Eight call sites across `localclient/client_test.go`, `netclient/client_test.go`, `util/util_test.go`. Each is a short (≤300 ms) post-condition settle for SSE or tick interactions; the prevalent pattern is "send message, sleep 80-300 ms, assert state". These are justified given the cross-goroutine setup, but would be more robust with `assert.Eventually`-style polling against a deadline. Non-blocking.
- **CLI ergonomics.** `coms-go serve --help` prints flag table to stderr (`internal/server/server.go:144-155`). `client-local` and `client-net` use Go's `flag` package which auto-emits usage on `-h`/`--help`. All three subcommands document their flags. Pass.

## Required fixes (because VERDICT != APPROVED)

1. **Raise per-package coverage to ≥70%.** Spec §19 gates on each `coverage:` line individually. Lift `internal/netclient` (35.2 → ≥70), `internal/localclient` (38.8 → ≥70), `internal/server` (53.3 → ≥70), `internal/transport` (65.8 → ≥70). High-value targets are listed under "Test coverage gaps" above. Quickest path: unit tests for the pure helpers (`mapToAgentCard`, `mergePatch`, `intField`, `boolField`, `rawJSON`, `safeErrorStr`, `nilStr`, `buildPingEnvelope`, `resolveTarget`) plus mock-HTTP tests for `sendHeartbeat`, `handleInboundPrompt`, `handleInboundResponse`, `onAgentEnd`, `refreshPeerCards`, `toolSend`, `toolAwait`, `deliverAwaitResult`. `cmd/coms-go` (0%) is acceptable to leave low if a subprocess `version` smoke test is added (~10 LOC).
2. **Run `gofmt -w .` across the module.** 11 files have formatting drift. The diffs are all alignment-only (no semantics change). `extensions/coms-go && gofmt -w .` then re-verify with `gofmt -l . ; echo "EXIT=$?"` and expect an empty list.
3. **Delete `internal/server/secret.go:25-42 atomicWrite`; call `util.AtomicWrite` instead.** Line 50 (`atomicWrite(path, data, 0600)`) → `util.AtomicWrite(path, data, 0600)`. Line 245 in `server.go` (`atomicWrite(serverJSONPath, sjData, 0)`) → `util.AtomicWrite(...)`. Keeps Design Principle §8 single-sourced.
4. **Move `envOr`, `envInt`, `mustGetwd` (and server's `envStr`) into `internal/util/`** and call them from both `localclient`, `netclient`, and `server`. Three call sites today have byte-for-byte duplicates.
5. **Remove the unused `bufio.Scanner` allocation in `internal/netclient/sse.go:96-114`.** Either drop the scanner (the raw-byte loop is what runs) or wire it in. `_ = sc // suppress unused warning` is not idiomatic.
6. **Replace `fmt.Sscanf(v, "%d", &n)` with `strconv.Atoi(v)`** in `internal/localclient/client.go:566` and `internal/netclient/client.go:1213` and handle the error like `server/server.go envInt` does.
7. **Guard the `ln.Addr().(*net.TCPAddr)` type assertion** in `internal/server/server.go:215` with `, ok` or document that the listener is always TCP.

## Suggestions (non-blocking)

1. Replace the IIFE `func() *int { v := queueDepth; return &v }()` patterns in `localclient/client.go:466` and any matching netclient site with a `util.IntPtr(n int) *int` helper or a one-line local.
2. Annotate `internal/server/state.go:14 Awaiter.timer interface{ Stop() bool }` with a comment explaining why it's an interface rather than `*time.Timer`.
3. `internal/server/server.go:215` — log claimed port at info level for operator visibility when `--port 0` is used (the boot banner already shows `localURL` which contains the port; verify this in your manual smoke).
4. `BootBanner` should be unit-tested by injecting a `*bytes.Buffer` writer (current implementation hardcodes `fmt.Printf`). A simple refactor (`BootBanner(w io.Writer, ...)`) lifts log.go's coverage well into the green and aligns with the testable-stdout convention from the IPC layer.
5. Consider replacing the `time.Sleep` calls in client_test.go with `assert.Eventually`-style polling (a tiny `waitUntil(t, cond, deadline)` helper) for flake resistance under heavy CI load.
6. `internal/netclient/client.go:498` exponential backoff: clamp `attempts` before the shift (e.g. `if attempts > 5 { attempts = 5 }`) to avoid future overflow if the cap logic ever changes.

## Notes

The implementation is in solid shape architecturally: build is clean, race detector is silent on a 120 s budget, integration suite passes (including the two-host pi_to_pi round-trip in §19), no panics outside init, goroutines all take `ctx`, defer placement is consistent, and the wire-protocol parity is intact. The blocker for APPROVED is purely the §19/T10 quality gates: coverage and gofmt. The DRY violations (`atomicWrite` duplicated, env helpers duplicated 3x) are small but worth fixing now while the package layout is fresh — they'll be hard to unwind once any of these clients gets reused. Once the seven required fixes land and per-package coverage clears 70 %, this module is ready for T11 cutover.
