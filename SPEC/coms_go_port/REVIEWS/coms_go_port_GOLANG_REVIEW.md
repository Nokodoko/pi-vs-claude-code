VERDICT: CHANGES_REQUESTED

# Golang Specialist Review — coms_go_port

## Scope

Reviewed all `.go` files under `extensions/coms-go/` (46 files across 10 packages). Skipped
`shim.ts`, JSON, markdown, and justfile. Prior-review context (T8 arch, T9 security, T10 code
review) was treated as settled baseline — verified that fixes landed, not re-litigated. Static
analysis tools not available on this host; used `go vet`, `go test -race`, manual reads, and
targeted grep queries.

---

## Prior-fix verification

- **T8 Awaiter.timer race fix**: PASS. `handleAwaitMessage` constructs `a.timer` before
  publishing `a` into `foundP.awaiters`, with a clear comment explaining the ordering guarantee.
  `go test -race` passes clean across all packages.
- **T8 audit flock fix**: PASS. `internal/audit/audit.go` acquires `syscall.LOCK_EX` on the open
  fd and defers `LOCK_UN`. Per-process `sync.Mutex` handles in-process ordering; flock handles
  multi-process.
- **T8 unknown-route catch-all**: PASS. `routes.go:41–43` returns a 404 `not_found` envelope for
  any path not matched by `/health` or `/v1/`.
- **T10 gofmt**: PASS. `gofmt -l .` produces no output.
- **T10 DRY consolidation**: PASS. `atomicWrite`, `envOr`, `envInt`, and `mustGetwd` are all in
  `internal/util/`; no duplicates found.
- **T10 dead bufio.Scanner**: PASS. `netclient/sse.go` uses a raw `[]byte` read loop; no
  `bufio.Scanner` present.

---

## Findings (Go-specialist angle)

### Concurrency

- **REQUIRED — goroutine leak in `toolNetAwait` (no-server branch)**
  `netclient/tools.go:387`:
  ```go
  go func() { <-make(chan struct{}) }()
  ```
  When no server is configured this goroutine is spawned and blocks forever on a freshly-made
  channel that nothing will ever close. It outlives the tool call, the client run loop, and the
  process (at shutdown the goroutine simply leaks). The `serverCh` branch path is also never
  drained in the no-server case because the select will take the `timer.C` arm, leaving the
  goroutine permanently blocked. Fix: either skip the goroutine entirely (the buffered `serverCh`
  will just never receive) or use a `context.WithCancel` from the enclosing function's context so
  the goroutine exits cleanly.

- **Non-blocking: `signal.Stop` never called on `sigCh`**
  `server/server.go:282–307`: `signal.Notify(sigCh, ...)` is called but `signal.Stop(sigCh)` is
  never called when the server exits. The goroutine reading `sigCh` also has no shutdown path
  other than receiving a signal — if `ctx` is cancelled externally the goroutine will block on
  `sig := <-sigCh` forever (goroutine leak in tests that create a server and cancel the context
  without sending a signal). In `Run()` (the CLI entrypoint) this is harmless because the process
  exits, but `NewServeMux` is used in tests without `Run` so the leak only materialises in `Run`
  contexts — still worth fixing with `defer signal.Stop(sigCh)`.

- **Non-blocking: `sendHeartbeat` fired via `go c.sendHeartbeat(ctx)`**
  `netclient/client.go:249`: A new goroutine is spawned every heartbeat interval. Under normal
  operation they complete quickly, but if the HTTP timeout fires at exactly the same time as many
  heartbeat ticks pile up (e.g., server slow) many concurrent heartbeat goroutines can accumulate.
  The `toolWg` does NOT track these goroutines, so `toolWg.Wait()` at shutdown does not gate on
  them. Not a correctness bug but a goroutine hygiene issue.

- **Non-blocking: accept-loop goroutine exit on `ln.Accept` error is irreversible**
  `localclient/client.go:204–217`: When `ln.Accept` returns an error that is not a context
  cancellation the goroutine logs to stderr and returns, permanently killing the listener without
  notifying the run loop. The run loop will then spin on `pingTicker.C` and `keepaliveTicker.C`
  indefinitely. This is a degenerate case (accept errors on Unix sockets are rare) but a
  correctness gap — returning from the accept goroutine with a non-nil error should signal the
  parent via a channel or `cancel()`.

### Error handling / Idioms

- **Non-blocking: `fmt.Sscan` error silently ignored**
  `server/routes.go:662`:
  ```go
  fmt.Sscan(ts, &requestedMS)
  ```
  If the `timeout_ms` query param is malformed, `Sscan` returns an error that is silently
  discarded and `requestedMS` stays zero. The effect is that a malformed `timeout_ms` falls
  through to the default 30 000 ms, which is benign. The canonical Go idiom is
  `strconv.ParseInt(ts, 10, 64)` with an explicit error check; `fmt.Sscan` is the right tool for
  free-form text scanning, not URL query params.

- **Non-blocking: `bytes` → `string` → `strings.NewReader` in hot path**
  `netclient/client.go:1008–1012`:
  ```go
  b, err := json.Marshal(body)
  // ...
  req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(b)))
  ```
  `string(b)` copies the entire body byte slice to create a string, then `strings.NewReader`
  wraps it. Use `bytes.NewReader(b)` directly to eliminate the copy.

- **Non-blocking: `(*HTTPError).Unwrap` absent**
  `netclient/sse.go:188–200`: `HTTPError` is a custom error type but does not implement
  `Unwrap() error`. When `Err` is set, `errors.Is` and `errors.As` cannot see through it to the
  wrapped error. Call sites use `err.(*HTTPError)` type assertions so this does not affect
  correctness today, but it breaks the standard chain idiom.

### HTTP / SSE server

- **REQUIRED — `http.Server` has zero timeouts**
  `server/server.go:262`:
  ```go
  srv := &http.Server{Handler: mux}
  ```
  `ReadTimeout`, `WriteTimeout`, `ReadHeaderTimeout`, and `IdleTimeout` are all zero (unlimited).
  A slow or malicious client can hold a connection open indefinitely, consuming a goroutine from
  the `net/http` internal pool. This is a production safety requirement, not a style preference.
  The SSE handler is legitimately long-lived so `WriteTimeout` must be left at zero for that
  path, but `ReadHeaderTimeout` (≤5 s) and `ReadTimeout` (≤30 s for non-SSE routes) must be
  set. The standard pattern is to set `ReadHeaderTimeout` on the `http.Server` and accept that
  SSE connections hold the write side open; see `golang.org/x/net/http2` guidance.

- **Non-blocking: `hasFlusher` guard is correct but the false branch is silent**
  `server/routes.go:289–299`: If the `http.ResponseWriter` does not implement `http.Flusher`
  (e.g., wrapped by a test middleware), SSE frames are written but never flushed. This is benign
  in `httptest.Server` (which implements Flusher) but fragile in general. A log warning when
  `!hasFlusher` would surface misconfigured proxies early.

- **Non-blocking: `decodeJSON` does not limit request body size**
  `server/routes.go:909–911`: `json.NewDecoder(r.Body).Decode(v)` with an unbounded body. T9
  flagged `http.MaxBytesReader` as a Medium finding. The fix did not land — `MaxBytesReader` is
  still absent on all POST routes. This is a known gap (T9 noted it); flagging as non-blocking
  here since it was already categorised.

### Tests / Build tags

- **Non-blocking: pervasive `time.Sleep` in tests**
  Tests in `netclient/coverage_test.go`, `netclient/client_test.go`, and `localclient/` use
  `time.Sleep(80–300ms)` to wait for goroutine state to settle. These are fragile on heavily
  loaded CI machines. The correct idiom is a channel notification or polling on the observable
  state. Under race detector the sleep windows are shorter than the overhead of scheduling,
  making occasional flakiness plausible.

- **Non-blocking: build tag blank line present**
  All `//go:build` directives are followed by a blank line — the required Go 1.17+ syntax. No
  issues.

- **Non-blocking: `integration` tests never run without explicit `-tags=integration`**
  `server/integration_test.go` and `pi_to_pi_test.go` gate on `//go:build integration`. The
  plain `go test ./...` run (which CI likely uses by default) skips these. Not a bug, but worth
  noting so CI explicitly includes `-tags=integration` in the gate.

- **Non-blocking: `t.Helper()` absent in test helper functions**
  `integration_helpers_test.go` defines multi-line helper functions (`startServer`,
  `sseCollect`) without calling `t.Helper()`. Failing assertions in those helpers will attribute
  the error to the helper line rather than the call site.

### Naming / godoc

- **Non-blocking: `ServerJson` / `ServerSecretJson` use non-Go-standard casing**
  `proto/proto.go:273,286`: Go convention for acronyms is `JSON`, not `Json`. These should be
  `ServerJSON` and `ServerSecretJSON`. The TS-parity comment explains the naming but these types
  are Go-internal; the JSON _tags_ are what match the wire format, not the type names.

- **Non-blocking: exported `NewServeMux` in `internal/server` lacks a doc comment**
  `server/server.go:44`: Has a comment but it starts with "creates" rather than "NewServeMux
  creates" per Go convention.

### Performance (hot-path allocs, time usage)

- **Non-blocking: `bytes` → `string` copy in `httpPost`** (see Error handling section above).

- **Non-blocking: `sseFrameWithID` allocates a new `string` on every frame**
  `server/sse.go:16`: `fmt.Sprintf` allocates. For the keepalive ping path this is called every
  15 s per stream — fine. For high-throughput message delivery it could be replaced with a
  `strings.Builder` or `bytes.Buffer` if profiling ever identifies it as hot.

- **Non-blocking: `inboxDepthFor` is O(messages) per send**
  `server/state.go:138–150`: Iterates all messages to count inbox depth for a target session.
  With `MaxInbox = 100` and message TTL = 30 min this is bounded in practice, but a per-target
  counter would make this O(1). Not an issue at current scale.

### Other Go-specific

- **Non-blocking: `map[string]any` in audit entries**
  `audit/audit.go:17`: `Entry` is typed as `map[string]any`. This is an intentional design
  choice (flexible log schema) and not violating the project's no-`any` rule on the _data model_
  — it is appropriate here since the audit log is deliberately schemaless. Noting for completeness.

- **Non-blocking: `_ = err` in `AtomicWrite` Chmod branch**
  `util/atomic.go:22–24`: `os.Chmod` failure is silently swallowed with `_ = err` and a comment
  "best-effort". This mirrors TS behaviour and is intentional; acceptable.

---

## Required fixes (if VERDICT != APPROVED)

1. **`netclient/tools.go:385–388` — goroutine leak in no-server branch of `toolNetAwait`.**
   Replace the sentinel `go func() { <-make(chan struct{}) }()` with either nothing (the buffered
   `serverCh` will simply never fire, and the `timer.C` arm will win) or a proper ctx-aware
   goroutine that exits when the tool call completes. The simplest fix is to delete lines 386–388
   and let `serverCh` remain a buffered channel that is never sent to in the no-server case —
   the `select` already handles that arm gracefully.

2. **`server/server.go:262` — set `ReadHeaderTimeout` on `http.Server`.**
   Add at minimum `ReadHeaderTimeout: 5 * time.Second` to the `http.Server` literal. Leave
   `WriteTimeout` at zero to allow SSE streams to remain open, but an unbounded header read is
   indefensible in a networked server.

---

## Suggestions (non-blocking)

1. `server/server.go:282–283`: Add `defer signal.Stop(sigCh)` before or after `signal.Notify`
   to prevent goroutine leaks in test contexts where `Run` exits without a signal.

2. `netclient/client.go:1012`: Replace `strings.NewReader(string(b))` with `bytes.NewReader(b)`
   to eliminate one allocation per outbound HTTP POST.

3. `netclient/sse.go:188`: Add `Unwrap() error { return e.Err }` to `HTTPError` so wrapped
   errors are visible to `errors.Is`/`errors.As` callers.

4. `server/routes.go:662`: Replace `fmt.Sscan(ts, &requestedMS)` with
   `requestedMS, _ = strconv.ParseInt(ts, 10, 64)` for idiomatic query-param parsing.

5. `localclient/client.go:204–217`: Have the accept-loop goroutine call a `cancel()` function
   (from a `context.WithCancel` wrapping the run loop) when it returns due to a non-ctx error,
   so the run loop can exit cleanly instead of spinning on tickers.

6. Test helpers in `server/integration_helpers_test.go`: Add `t.Helper()` at the top of each
   helper function for accurate failure attribution.

7. `proto/proto.go:273,286`: Rename `ServerJson` → `ServerJSON` and `ServerSecretJson` →
   `ServerSecretJSON` per Go acronym convention.

---

## Build/Test results (raw)

- `go vet`: PASS (exit 0, no output)
- `go test ./...`: PASS (all 10 packages, clean)
- `go test -race ./... -count=1`: PASS (no race conditions detected)
- `go test -tags=integration ./...`: PASS (cached; integration tests gated behind build tag)
- `gofmt -l`: PASS (empty — no drift)

---

## Notes

The codebase is well-structured and demonstrates genuine Go literacy: two-layer locking
(`ServerState.mu` + `ProjectState.mu`) with documented lock-ordering, correct timer construction
before awaiter publication to eliminate the data race, proper `defer t.Stop()` in all ticker
loops, and clean `context.Context` propagation through the entire server lifecycle. The two
required changes are not subtle: the goroutine leak in `toolNetAwait` is a textbook
`go func() { <-make(chan struct{}) }()` antipattern that will be caught by any goroutine leak
detector in test, and the missing HTTP server timeouts are a production hardening gap that a
Go-specialist reviewer would block on. Everything else is non-blocking polish. Once those two
items are addressed this code is ready to ship.
