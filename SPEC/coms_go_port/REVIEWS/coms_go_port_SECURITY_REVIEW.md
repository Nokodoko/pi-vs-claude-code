VERDICT: APPROVED

# Security Review — coms_go_port

## Scope

Read-only security audit of the Go port of `coms.ts` + `coms-net.ts` + `scripts/coms-net-server.ts` at `extensions/coms-go/`. Focus: bearer-token handling, audit-log integrity, file permissions, input validation, auth boundaries, concurrency-safety, crypto choices, and information leakage. The bar is parity with the TS reference (Design Principle 1) plus the explicit security rails in Principles 9 and 10 and §15 T9. General code quality is out of scope (T10).

Files audited:
- `internal/server/{auth.go,secret.go,server.go,routes.go,state.go,sse.go,log.go,tickers.go,regexp.go}`
- `internal/audit/audit.go`
- `internal/registry/registry.go`
- `internal/transport/transport_unix.go`
- `internal/util/{atomic.go,ulid.go}`
- `internal/localclient/{client.go,handlers.go,tools.go}`
- `internal/netclient/{client.go,sse.go,tools.go}`

## Findings (by severity)

### Critical

None.

### High

None.

### Medium

- **No HTTP body size cap on `/v1/*` POSTs** — `internal/server/routes.go:905` `decodeJSON(r,v)` uses `json.NewDecoder(r.Body).Decode(v)` with no `http.MaxBytesReader` and no `Server.ReadTimeout`/`ReadHeaderTimeout`/`WriteTimeout`. An authenticated client can post a multi-gigabyte body to `/v1/agents/register`, `/v1/messages`, `/v1/messages/:id/response`, or `/v1/agents/:sid/heartbeat` and exhaust memory. The 64 KB `LineCap` enforced in `transport_unix.go:22` applies only to the Unix-socket path. Auth-gated (bearer required), so the practical attacker is a compromised local agent. The TS reference (`scripts/coms-net-server.ts`) lacks an equivalent cap as well, so this is parity-permitted per Principle 1; recording as Medium because the Go server inherits the same DoS surface and an `http.MaxBytesReader(w, r.Body, 1<<20)` wrapper would close it for free without breaking parity. Not blocking T11, but worth a follow-up.

### Low / Informational

- **`safeError()` regex in `internal/server/auth.go:11` is anchored with `^`** and lacks `(?m)`. It only redacts `Bearer xxx` when the token text begins the string. An error like `"upstream: Authorization: Bearer <tok> rejected"` would pass through unredacted. In practice this is dead code: every server-side `writeError` call passes a hardcoded error code (`"invalid_json"`, `"hop_limit_exceeded"`, etc.), never user-controlled text, so no token can reach it. The netclient `safeError`/`safeErrorStr` at `internal/netclient/sse.go:171-188` uses `strings.ReplaceAll(msg, token, "<redacted>")` which is correct. Either tighten the regex (drop `^`, or replace with the netclient's substring approach) or document that the server's `safeError` is defense-in-depth only.
- **Length-leak before constant-time compare** — `internal/server/auth.go:27` returns early when `len(got) != len(token)`. `crypto/subtle.ConstantTimeCompare` already returns 0 on length mismatch, so the early return is redundant and adds a microscopic timing distinguisher between "right length, wrong content" and "wrong length". The server token is a fixed 64-char hex string (32 bytes from `crypto/rand.Read`, see `internal/server/secret.go:14-20`), so this is publicly inferable and not exploitable. Comment claims it mirrors the TS guard; if strict parity is required, leave as-is.
- **Temp-file mode race in `atomicWrite`** — `internal/server/secret.go:31` and `internal/util/atomic.go:18` write the temp file with `0644` then `Chmod` to `0600`. There is a brief window where the bearer-secret temp file exists with world-readable mode. Parent directory is `~/.pi/coms-net/projects/<project>/` which is normally owner-private (created with 0755 at `internal/server/server.go:190`). Mirrors the TS atomic-write semantics (Principle 8). Use `os.OpenFile(tmp, O_WRONLY|O_CREATE|O_EXCL, 0o600)` + `Write` to eliminate the window. Same-user threat only.
- **Filesystem-escape on agent name** — `internal/registry/registry.go:38` joins user-controlled `name` (CLI flag / frontmatter) into `<root>/projects/<project>/agents/<name>.json` via `filepath.Join`, which calls `filepath.Clean` and resolves `../` segments. A name like `../../tmp/evil` would write outside the agents directory. Same-user-only threat (the agent runs as the user); TS reference has the same shape (`registry.Write` is the lift of `writeRegistryAtomic` in `coms.ts` lines 248-256). Add a sanity-check that `name` matches `^[A-Za-z0-9._-]{1,64}$` if any tightening is desired post-T11.
- **ULID fallback uses non-CSPRNG bytes on `rand.Read` failure** — `internal/util/ulid.go:31-34` falls back to `binary.BigEndian.PutUint64(randBuf[:8], uint64(ms^0xDEADBEEFCAFE))` if `crypto/rand.Read` fails. ULIDs are not used as security tokens (only as msg/session IDs which are not auth-bearing), and `crypto/rand.Read` on Linux is effectively infallible. Informational.
- **No panic recovery in HTTP handlers** — `internal/server/routes.go` handlers do not install a `defer recover()`. Go's `net/http.Server` ships with a built-in handler that recovers panics and writes the stack trace to `srv.ErrorLog` (defaults to `log.Default()` → stderr). A handler-triggered panic from malformed input would dump a stack trace to stderr; under `journalctl -fu coms-go` that surfaces internal paths. The `decodeJSON` path is the most likely panic surface (oversized input → `bufio.Scanner: token too long` does not panic; oversized JSON nests can recurse but Go's decoder is iterative). Acceptable for v1.0.0 parity.
- **Audit log file mode is `0644`** — `internal/audit/audit.go:51`. `~/.pi/coms-log` and `~/.pi/coms-net-log` contain only event metadata (no payloads, no tokens — verified at every call site in `localclient/{client,handlers,tools}.go` and `netclient/{client,tools}.go`), so world-readable is not a token-leak risk. Matches TS behavior. Tightening to `0600` would be a hardening win at zero behavioral cost.
- **Stdout event-line log includes a 50-char prompt preview** — `internal/server/log.go:87-101` `logMessageSend` truncates the prompt to 50 runes and prints it on stdout. This is the operator-facing event log (`journalctl -fu coms-go`), not the JSONL audit file. Principle 9 ("no payload bodies in audit logs, ever") targets the JSONL files at `~/.pi/coms-log` / `~/.pi/coms-net-log`, which the Go port correctly omits prompt text from. The TS server emits the same preview to stdout for the same operator-visibility reason. Documented behavior; informational only.

## Required fixes (if VERDICT != APPROVED)

VERDICT is APPROVED. No required fixes for the T11 gate. Recommended (non-blocking) follow-ups:

1. `internal/server/routes.go:905` — wrap each POST handler's body with `http.MaxBytesReader(w, r.Body, 1<<20)` (or similar) before `json.NewDecoder` to close the body-size DoS.
2. `internal/server/auth.go:35-43` — switch `safeError` to `strings.ReplaceAll(msg, token, "[REDACTED]")` style (requires plumbing the live token into the helper) or remove the regex helper entirely and rely on the literal error codes already in use; add a unit test that proves a synthetic "leaked bearer" message is redacted.
3. `internal/util/atomic.go:13-32` and `internal/server/secret.go:25-42` — open the temp file directly with `0600` (`os.OpenFile(tmp, O_WRONLY|O_CREATE|O_EXCL, 0o600)`) so the secret never lives on disk with 0644.
4. `internal/audit/audit.go:51` — open the log with `0600` instead of `0644`.
5. `internal/registry/registry.go:38` — reject `name` values containing `/`, `..`, or non-printable bytes.

## Test coverage gaps (security-related)

- No test asserts that the JSONL audit file does NOT contain the substrings `"prompt"`, `"response"`, `"token"`, or `"Bearer "` after a representative send/receive cycle (spec §16 hand-rolls this as a `grep` check on the live file; add a unit-level assertion in `audit/audit_test.go` mirroring the spec wording).
- No test asserts `server.secret.json` ends up at mode `0600` after `writeServerSecret`. Easy assert: `fi.Mode().Perm() == 0o600`. The netclient already enforces `0o600` on read at `netclient/client.go:1108`, but there is no symmetric writer-side test.
- No test exercises `safeError` against a synthetic input containing a bearer fragment (e.g. `"upstream HTTP 401: Bearer abcdef rejected"`); adding one would have caught the anchored-regex defect above.
- No test covers the constant-time-compare branch (`subtle.ConstantTimeCompare`) with an equal-length-wrong-content token — `TestAuthWrongToken` uses a different-length token (`"wrong-token"` vs 64-char hex), so the early length-guard short-circuits before the constant-time compare ever runs.
- No test asserts that the boot banner output (`BootBanner` in `server/log.go:142`) contains the secret file PATH and not the token contents.
- No test enforces a hop-limit or inbox-cap exceedance produces the `hop_limit_exceeded` / `inbox_full` error codes with the expected HTTP status (likely covered in `integration_test.go` — not blocking).

## Notes

The implementation faithfully reproduces the TS server's security posture, which is the explicit acceptance bar (Principle 1, §11 "Does NOT improve security beyond TS parity"). Bearer handling is correct on every measured axis: stored only in `server.secret.json` (mode 0600 enforced post-rename and re-validated on read at `netclient/client.go:1107-1108`), generated from `crypto/rand` (`secret.go:14-20`), compared via `subtle.ConstantTimeCompare` (`auth.go:30`), transported only in the `Authorization: Bearer` header (`netclient/client.go:1016,1038,1059`), never logged (no `audit.Append` call site contains `token`/`authToken`; the boot banner at `log.go:142` prints the path, not the contents), and redacted from netclient error strings via `strings.ReplaceAll` (`sse.go:171-188`). Audit-log content was hand-verified at every `audit.Append` site across `localclient` and `netclient`: every entry contains only `event`, `msg_id`, `session_id`, `sender`/`target` IDs or names, `hops`, `ts`, and (where applicable) a `safeError`-sanitised `reason` — no prompt or response payload bytes. The auth boundary is correctly enforced at `routes.go:31-35`: `/health` is the only unauthenticated route; everything under `/v1/` runs through `authed(r, cfg.Token)` before dispatch, and the unauthenticated response is the constant body `{"ok":false,"error":"unauthorized"}` with no internal state echoed. Hop limit (`routes.go:474-480`) and inbox cap (`routes.go:530-537`) are enforced before message acceptance, and the `responder_session != target_session` check at `routes.go:770-772` returns 403 `"not_target"` — the trust boundary spec §15 T9 calls out.

The Medium DoS finding and the Low items above are all parity-acceptable, parity-required, or defence-in-depth gaps that do not block T11. T11 may proceed.
