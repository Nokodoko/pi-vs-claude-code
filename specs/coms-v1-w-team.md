# Plan: Build the `coms` Pi Extension — Peer-to-Peer Agent Messaging

## Task Description

Implement the `coms` Pi extension as fully specified in [`specs/coms-v1.md`](./coms-v1.md). The extension lets multiple Pi agents on the same machine discover each other via per-project registry files, exchange queued non-blocking prompts over unix sockets / Windows named pipes, and surface the live peer pool in a stackable widget that does NOT override existing footers from other extensions.

The deliverable is one new file (`extensions/coms.ts`), plus a one-line addition to `extensions/themeMap.ts` and a new recipe in `justfile`. No new runtime dependencies — `yaml` is already in `package.json` and the spec uses an inline frontmatter regex anyway.

The spec is the source of truth. This plan is the *execution* plan: who builds what, in what order, with what acceptance criteria.

## Objective

When complete:

1. `pi -e extensions/coms.ts` boots cleanly on macOS/Linux, registers a registry entry under `~/.pi/coms/projects/default/agents/<name>.json`, binds a unix socket at `~/.pi/coms/sockets/<session_id>.sock`, and stays alive across the user's chat session.
2. Two Pi instances launched in parallel (via `just ext-coms` in two terminals) discover each other through `coms_list`, can `coms_send` a prompt that arrives as a `followUp` follow-up message on the receiver's next turn boundary, and the response gets routed back to the sender's `coms_await`.
3. The pool widget renders above the editor (`placement: "belowEditor"`) showing each peer's color dot, name, model, context-bar, percent, and purpose — refreshed every 10 s — and **coexists** with `minimal.ts` / `tool-counter.ts` footers without overriding them.
4. `/coms` force-refreshes the widget; `--all` toggles explicit agents; `--project <name>` switches the displayed pool.
5. Frontmatter `color: "#RRGGBB"` is parsed, validated, propagated through `pong.agent_card`, and rendered as raw ANSI in the widget.

## Problem Statement

The pi-vs-cc playground already showcases in-process orchestration patterns (`agent-team.ts`, `agent-chain.ts`, `subagent-widget.ts`), but every "multi-agent" demo so far is a *single Pi process* spawning child Pi subprocesses. There is no story for **independent, long-running Pi instances coexisting on one machine and collaborating** — the way two engineers would, where each retains their own session, identity, and color, and they negotiate work through a queue rather than a parent/child invocation.

Building this from scratch means hand-rolling: a registry on disk, a transport (AF_UNIX / named pipe), a wire protocol with envelopes and acks and hop limits, queued non-blocking delivery via Pi's `pi.sendMessage` follow-up channel, response-capture from `agent_end`, *and* a widget surface that respects the playground's "extensions stack, never override" convention. Each piece is small but the integration is fiddly — particularly because the widget must use `setWidget` (not `setFooter`), the agent's frontmatter color must be rendered as a raw ANSI escape (not via theme tokens), and the response-capture path crosses three Pi hooks (`session_start` → `tool_call` connection accept → `agent_end`).

## Solution Approach

Follow the spec exactly. Build the whole feature as one file (`extensions/coms.ts`) — same convention as every other entry in `extensions/`. Use the existing playground patterns end-to-end:

- **Frontmatter parser** copy-pasted from `extensions/agent-team.ts:79-105` — same `^---\n([\s\S]*?)\n---\n([\s\S]*)$` regex; do not import `yaml`.
- **`pi.sendMessage` follow-up delivery** verbatim from `extensions/subagent-widget.ts:196-200`.
- **`agent_end` nudge pattern** from `extensions/tilldone.ts:365-383` (but instead of nudging, dispatch a `response` envelope back to the inbound `sender_endpoint`).
- **`ctx.sessionManager.getBranch()` walk** for last assistant text, modeled on `extensions/tool-counter.ts:39-46`.
- **`setWidget(..., { placement: "belowEditor" })`** from `extensions/theme-cycler.ts:42-69` and `extensions/tilldone.ts:179-215` — guarantees stacking with `minimal.ts` / `tool-counter.ts` footers.
- **`setInterval` polling + `tui.requestRender()`** from `extensions/agent-team.ts:333-336`.
- **TypeBox tool registration with `renderCall` / `renderResult`** from `extensions/tilldone.ts:392-708`.
- **Raw ANSI for per-agent hex colors** from `extensions/cross-agent.ts:20-37` (escape pattern `\x1b[38;2;R;G;Bm…\x1b[39m`).

Decompose the build into **scout → 3-phase builder (resumed) → meta-files builder (parallel) → reviewer → validator**. A single resumed builder owns `coms.ts` so internal state (helpers, types, in-memory maps) stays coherent — splitting one file across builders risks merge conflicts and behavioral drift. Meta-file changes (`themeMap.ts` one-liner, `justfile` recipe) run in parallel because they touch independent files.

## Relevant Files

Use these files to complete the task:

- [`specs/coms-v1.md`](./coms-v1.md) — the source-of-truth spec; every section maps to a code region in `coms.ts`.
- [`specs/coms-v1/`](./coms-v1/) — 14 reference diagrams, especially `04-protocol.jpg` (envelope shape), `05-lifecycle.jpg` (hook timeline), `08-worked-example.jpg` (sequence), `14-pool-widget.jpg` (widget mockup).
- `extensions/subagent-widget.ts` — pattern for `pi.sendMessage` followUp delivery (lines 196-200), background subprocess management, and per-card widget rendering.
- `extensions/agent-team.ts` — pattern for frontmatter parsing (79-105), `setInterval` polling (333-336), TypeBox tool registration (469-561), `setActiveTools` (line 699 — *do not* use here, coms tools are additive).
- `extensions/agent-chain.ts` — pattern for YAML config consumption and per-step state cards (372-375 for the polling timer).
- `extensions/tilldone.ts` — pattern for `agent_end` hook (365-383), reconstructing state from session entries (300-323), and TypeBox tool with `renderCall` / `renderResult` (392-708), and `belowEditor` widget placement (179-215).
- `extensions/system-select.ts` — pattern for scanning frontmatter `.md` files across multiple discovery directories.
- `extensions/cross-agent.ts` — pattern for raw ANSI color helpers (20-37) and multi-line `ctx.ui.notify` banners; will be re-used for converting hex `color` → ANSI `38;2;R;G;B`.
- `extensions/damage-control.ts` — pattern for `pi.appendEntry("…-log", …)` audit logging (191, 201).
- `extensions/minimal.ts` — pattern for `ctx.getContextUsage()` and the 10-block context bar (21-24); the coms widget will render an analogous bar per peer.
- `extensions/tool-counter.ts` — pattern for `ctx.sessionManager.getBranch()` traversal to extract last assistant message (39-46).
- `extensions/theme-cycler.ts` — canonical `setWidget(..., { placement: "belowEditor" })` example (42-69) showing keyed-widget stacking.
- `extensions/themeMap.ts` — one-line addition: `"coms": "ocean-breeze"` (between existing entries).
- `justfile` — new recipe `ext-coms` between section `#g3` and `#ext`.
- `THEME.md` — color role conventions for the widget chrome (brackets, dim labels).
- `package.json` — confirm no new deps needed; `yaml` is already declared.

### New Files

- `extensions/coms.ts` — the entire extension; estimated 600–800 lines. Single file by convention.

## Implementation Phases

### Phase 1: Foundation

**Owner: `builder-coms` (Phase A)** — pure-Node groundwork, no Pi UI yet:

1. Imports (`node:net`, `node:fs`, `node:path`, `node:os`, `node:crypto`, `@mariozechner/pi-coding-agent`, `@sinclair/typebox`, `@mariozechner/pi-tui`).
2. Type definitions (`Envelope`, `PromptEnvelope`, `ResponseEnvelope`, `PingEnvelope`, `Pong`, `AgentCard`, `RegistryEntry`, `PendingReply`, `InboundContext`).
3. Helpers: ULID generation (inline, no dep — use `crypto.randomBytes(10)` + Crockford base32, ≤ 25 lines), `makeEndpoint(sessionId)`, frontmatter parser, color validator + fallback palette (sha256-of-session-id mod 8 over the 8-color synthwave palette in §10), hex → ANSI converter (`\x1b[38;2;R;G;Bm`).
4. Config resolution: env vars (`PI_COMS_DIR`, `PI_COMS_MAX_HOPS`, `PI_COMS_TIMEOUT_MS`, `PI_COMS_PING_INTERVAL_MS`), CLI flags via `pi.registerFlag(...)` + `pi.getFlag(...)` for `--name`/`--purpose`/`--project`/`--color`/`--explicit` (required so pi's parser accepts them — pi 0.73+ otherwise rejects unknown flags with "Unknown options: ..."), frontmatter overrides from `--system-prompt <path>` (preferred) or `--append-system-prompt <path>` (fallback) — these are pi-builtin so they're scanned from `process.argv`.
5. Registry I/O: `writeRegistryAtomic(entry)` (write `.tmp`, `renameSync`), `readAllRegistryEntries(project)`, `removeRegistryEntry(name)`, `pruneDeadEntries()` using `process.kill(pid, 0)` + `ESRCH`.
6. Transport: `bindEndpoint(endpoint)` with stale-socket probe-`connect` on POSIX, `net.createServer(connHandler)`. `connHandler` reads one line (64 KB cap), parses, validates the envelope, dispatches to a stub `handlePrompt` / `handleResponse` / `handlePing`, writes `ack` / `nack`, closes.
7. Outbound: `sendEnvelope(endpoint, envelope)` opens a connection, writes one line + `\n`, reads ack/nack on the same connection, throws on `nack`, otherwise resolves.

### Phase 2: Core Implementation

**Owner: `builder-coms` (Phase B)** — Pi integration via `resume: true`:

1. Pending-replies table: `Map<msg_id, { resolve, reject, timer }>`. `coms_await` registers; inbound `response` resolves; timeout rejects.
2. Inbound queue: `Map<msg_id, InboundContext>` (records `hops`, `sender_endpoint`, `response_schema`). Set when injecting a follow-up; cleared in `agent_end` after response dispatch.
3. `handlePrompt`: validate `hops < MAX_HOPS`; record inbound context; `pi.sendMessage({ customType: "coms-inbound", content: "[from <sender_name> @ <sender_cwd>]\n\n<prompt>", display: true, details: { msg_id, sender_session, response_schema } }, { deliverAs: "followUp", triggerTurn: true })`; ack.
4. `handleResponse`: look up `msg_id`; if found, resolve the awaiting Promise; ack regardless (sender may have restarted).
5. `handlePing`: build `agent_card` from current `ctx` (name, purpose, model, color, `ctx.getContextUsage().percent`, queue depth); write `pong` *inline on the same connection* (not via callback); close.
6. Tool registration with TypeBox + `renderCall` + `renderResult`:
   - `coms_list` — read registry, prune dead, optionally ping each peer to fetch live `context_used_pct`.
   - `coms_send` — resolve target by name (preferred) or session_id, build envelope (with `hops = inbound?.hops + 1 ?? 0`), dispatch, return `{ msg_id }`.
   - `coms_get` — non-blocking poll of pending-replies.
   - `coms_await` — Promise.race against timer.
7. `agent_end` hook: if there's an unfulfilled inbound, walk `ctx.sessionManager.getBranch()` for the last `assistant` message, validate against `response_schema` if present, `sendEnvelope(inbound.sender_endpoint, responseEnvelope)`. Clear the inbound entry.
8. `pi.appendEntry("coms-log", { event, msg_id?, peer?, … })` for boot, every send, every receive, shutdown — provides an audit trail without bloating the LLM context.

### Phase 3: Integration & Polish

**Owner: `builder-coms` (Phase C)** — UI surface and lifecycle, again `resume: true`:

1. Pool widget: `ctx.ui.setWidget("coms-pool", (tui, theme) => ({ render(width) {…}, invalidate() {…} }), { placement: "belowEditor" })`. Render reads from `peerCards: Map<sessionId, AgentCard>` cache only — never the filesystem during render.
2. Per-row composition (monospace): swatch dot in agent's hex (raw ANSI), name (`accent`), model abbreviated (`dim`), bracketed bar with hashes in agent's hex / dashes in `dim`, percent (`accent`), em-dash (`dim`), truncated purpose (`muted`).
3. Widget header: `📡 coms · <project>` in `accent`. Empty: dim line "coms · no peers connected".
4. Ping cycle: `setInterval(refreshPool, PI_COMS_PING_INTERVAL_MS)`. `refreshPool` reads registry, pings each entry in parallel with `Promise.allSettled`, updates `peerCards`, marks rows with no successful ping for 3 cycles as stale (✗ swatch, dim row), removes after 6. Calls `tui.requestRender()` only when state actually changed.
5. `/coms` slash command: bare invocation triggers `refreshPool()` immediately; `--all` toggles `includeExplicit` flag; `--project <name>` overrides displayed project. Per spec, the slash command does NOT print a one-shot notification — it mutates widget state.
6. `pi.on("session_start", …)`: `applyExtensionDefaults(import.meta.url, ctx)` first; resolve config; bind socket; write registry atomically; install SIGINT/SIGTERM handlers; start the 30 s `utimes` keepalive AND the 10 s ping cycle; install widget; install `ctx.ui.setStatus("coms", …)` indicator (status, not footer); notify a one-line boot summary.
7. `pi.on("session_shutdown", …)` (and SIGINT/SIGTERM): stop accepting new connections, drain in-flight (5 s timeout), unlink socket file (POSIX), unlink registry entry, append final `coms-log` entry, exit.
8. `pi.on("agent_end", …)` already registered in Phase B.
9. Edge-case polish: `nack` on hops, queue full, malformed envelope, unknown type. Sender-side `coms_send` clear error messages.

## Team Orchestration

- You operate as the team lead and orchestrate the team to execute the plan.
- You're responsible for deploying the right team members with the right context to execute the plan.
- IMPORTANT: You NEVER operate directly on the codebase. You use `Task` and `Task*` tools to deploy team members.
  - This is critical. Your job is to act as a high level director of the team, not a builder.
  - Your role is to validate all work is going well and make sure the team is on track to complete the plan.
  - You'll orchestrate this by using the Task* Tools to manage coordination between the team members.
  - Communication is paramount. You'll use the Task* Tools to communicate with the team members and ensure they're on track to complete the plan.
- Take note of the session id of each team member. This is how you'll reference them.

### Team Members

- Scout
  - Name: `scout-coms`
  - Role: Read `specs/coms-v1.md` end-to-end, then walk every existing extension referenced in §13 (`subagent-widget.ts:196`, `tilldone.ts:365`, `agent-team.ts:79`, `minimal.ts:21`, `tool-counter.ts:39`, `damage-control.ts:191`, `theme-cycler.ts:42`, `cross-agent.ts:20`). Produce a "Build Sheet" markdown blob (NOT saved to disk — returned as the agent's final message) that lists, per concept: the file:line reference, the exact API signature copied from the source, and any gotchas. The builder consumes this blob through the orchestrator's prompt to avoid re-discovering patterns.
  - Agent Type: `general-purpose`
  - Resume: false
- Builder (main)
  - Name: `builder-coms`
  - Role: Build all of `extensions/coms.ts`. Three phases (Foundation / Core / Polish per §Implementation Phases). Same agent across all three so in-memory map types, helper signatures, and lifecycle wiring stay coherent. Receives the Build Sheet from `scout-coms` on first invocation; for phase 2 and 3 it's resumed with phase-specific instructions only.
  - Agent Type: `general-purpose`
  - Resume: true (resumed across Phase A → B → C)
- Builder (meta-files)
  - Name: `builder-meta`
  - Role: Add `"coms": "ocean-breeze"` to `extensions/themeMap.ts` (alphabetical-ish, near the existing `cross-agent` entry). Add a new `ext-coms` recipe to `justfile` between the `#g3` and `#ext` section markers. Verify both with `grep`.
  - Agent Type: `general-purpose`
  - Resume: false
- Reviewer
  - Name: `reviewer-coms`
  - Role: Static spec-compliance review. Read `specs/coms-v1.md` and the produced `extensions/coms.ts` end-to-end. Verify every section's requirements are met. Specifically: (a) no `ctx.ui.setFooter` call (`grep -n "setFooter" extensions/coms.ts` must be empty), (b) `setWidget("coms-pool", …, { placement: "belowEditor" })` present, (c) all four tools (`coms_list`, `coms_send`, `coms_get`, `coms_await`) registered with TypeBox + `renderCall` + `renderResult`, (d) `pi.sendMessage` called with `deliverAs: "followUp", triggerTurn: true`, (e) frontmatter regex matches the canonical pattern, (f) hex color validated with `/^#[0-9a-fA-F]{6}$/`, (g) raw ANSI helper for hex → `38;2;R;G;B`, (h) `process.kill(pid, 0)` for liveness, (i) atomic registry write via `renameSync`, (j) stale-socket probe-`connect` on POSIX. Returns a pass/fail report with line numbers; if fail, the orchestrator hands back to `builder-coms` (resumed) with the failure list.
  - Agent Type: `general-purpose`
  - Resume: false
- Validator
  - Name: `validator-coms`
  - Role: Runtime smoke test. (1) Confirm `pi --version` and `bun --version` return cleanly. (2) Run `pi -p --no-extensions` to confirm Pi launches in non-interactive mode. (3) `pi -e extensions/coms.ts -p "exit"` to confirm the extension loads without throwing (jiti compile + session_start succeeds). (4) Verify `ls ~/.pi/coms/projects/default/agents/*.json` produces an entry after launch. (5) Verify the socket exists: `ls ~/.pi/coms/sockets/*.sock`. (6) Optional manual test instructions for the user (since two-instance interactive verification needs a terminal): document the exact commands to run in two `just ext-coms` terminals and what to look for.
  - Agent Type: `general-purpose`
  - Resume: false
- Tester (protocol e2e)
  - Name: `tester-protocol-e2e`
  - Role: End-to-end runtime test using the `drive` skill (`~/.claude/skills/drive/SKILL.md` — tmux automation CLI). Spawn TWO Pi instances in two tmux sessions running `pi -e extensions/coms.ts --name <a|b>`. Wait for boot. Then in a third tmux session, run a Node script that opens AF_UNIX connections directly and exercises the wire protocol: ping → pong (validate `agent_card` shape including `color`), valid prompt → ack, `hops: 99` prompt → nack with `"hops exceeded"`, malformed line → nack with `"malformed envelope"`. Validate every assertion. Cleanly send Ctrl+C to both Pi sessions; verify registry + socket files are unlinked. Report PASS/FAIL per assertion + final READY/BLOCKED.
  - Agent Type: `general-purpose`
  - Resume: false
- Tester (widget visual)
  - Name: `tester-widget-visual`
  - Role: Visual confirmation that the pool widget renders correctly AND that footers from stacked extensions (minimal.ts) survive — i.e. the spec's "stacks, never overrides" guarantee. Use `drive` to spawn TWO Pi instances via `just ext-coms` (this stacks coms + minimal + theme-cycler). After ~12 s (long enough for one ping cycle), `drive screenshot` each tmux session. Use `grep`-like text inspection on `drive read --json` output to assert: (a) the widget header line `📡 coms · default` appears, (b) the OTHER agent's name appears in the widget, (c) the minimal footer's `[#####...]` context bar pattern is still visible (proves no `setFooter` clobber), (d) a hex-color ANSI escape (`\x1b[38;2;`) is present in the widget rows (proves color swatch rendering). Send `/coms` to one session, screenshot again to confirm force-refresh works. Tear down cleanly. Save the screenshots to `specs/coms-v1/runtime/` for the user to review.
  - Agent Type: `general-purpose`
  - Resume: false

## Step by Step Tasks

- IMPORTANT: Execute every step in order, top to bottom. Each task maps directly to a `TaskCreate` call.
- Before you start, run `TaskCreate` to create the initial task list that all team members can see and execute.

### 1. Scout the Spec and Existing Patterns

- **Task ID**: `scout-context`
- **Depends On**: none
- **Assigned To**: `scout-coms`
- **Agent Type**: `general-purpose`
- **Parallel**: false
- Read `specs/coms-v1.md` end-to-end.
- Read each referenced file:line citation in §13 of the spec, plus the patterns called out in this plan's "Relevant Files" section.
- For each of the following Pi extension API surfaces, copy the *exact* invocation form into a "Build Sheet" markdown blob: `pi.registerTool(...)` (TypeBox + render hooks), `pi.registerCommand(...)`, `pi.on("session_start", ...)`, `pi.on("agent_end", ...)`, `pi.on("session_shutdown", ...)`, `pi.sendMessage(..., { deliverAs: "followUp", triggerTurn: true })`, `ctx.ui.setWidget(id, renderer, { placement: "belowEditor" })`, `ctx.ui.setStatus(id, text)`, `ctx.ui.notify(text, "info")`, `ctx.getContextUsage()`, `ctx.sessionManager.getBranch()`, `ctx.model`, `applyExtensionDefaults(import.meta.url, ctx)`.
- Also list: the raw-ANSI hex helper from `cross-agent.ts:20-37`, the frontmatter regex from `agent-team.ts:79-105`, the atomic-rename pattern from any existing usage (or note absence), and any existing extension that calls `setFooter` so the reviewer can ensure `coms.ts` does NOT.
- Return the Build Sheet as the agent's final message (no file write). Keep it under 400 lines.

### 2. Build Phase A — Foundation (Helpers + Transport)

- **Task ID**: `build-foundation`
- **Depends On**: `scout-context`
- **Assigned To**: `builder-coms`
- **Agent Type**: `general-purpose`
- **Parallel**: false (`builder-meta` may run in parallel, see Task 5)
- Receive the Build Sheet from `scout-coms` in the prompt.
- Create `extensions/coms.ts` with imports, type definitions, and all helper functions per "Implementation Phases — Phase 1".
- Implement `bindEndpoint`, `sendEnvelope`, the connection handler (with stub dispatch for `prompt`/`response`/`ping` that just acks), and registry I/O.
- File should be syntactically complete: every function has a body; no `// TODO`. Stubs for handlers are explicit (e.g. `handlePrompt` returns ack only, real injection comes in Phase B).
- Verify by running `bun --silent -e "import('/Users/indydevdan/Documents/projects/experimental/pi-vs-cc/extensions/coms.ts').then(m => console.log('OK', typeof m.default))"` — must print `OK function`.

### 3. Build Phase B — Pi Integration (Tools + Hooks)

- **Task ID**: `build-pi-integration`
- **Depends On**: `build-foundation`
- **Assigned To**: `builder-coms` (resumed)
- **Agent Type**: `general-purpose`
- **Parallel**: false
- Resume the same agent — keeps Phase A's helper signatures and types in working memory.
- Implement pending-replies table, inbound queue, and the real `handlePrompt` / `handleResponse` / `handlePing` bodies per "Implementation Phases — Phase 2".
- Register all four tools (`coms_list`, `coms_send`, `coms_get`, `coms_await`) with TypeBox schemas and `renderCall` + `renderResult`.
- Wire `pi.on("agent_end", …)` to capture the assistant's last text via `ctx.sessionManager.getBranch()` and dispatch a `response` envelope.
- Add `pi.appendEntry("coms-log", …)` calls at key points (boot, send, receive, shutdown).
- Re-run the bun import smoke test (Task 2's verification) — must still print `OK function`.

### 4. Build Phase C — Widget + Lifecycle + Slash Command

- **Task ID**: `build-ui-widget`
- **Depends On**: `build-pi-integration`
- **Assigned To**: `builder-coms` (resumed)
- **Agent Type**: `general-purpose`
- **Parallel**: false
- Resume the same agent — keeps full implementation context.
- Implement `setWidget("coms-pool", …, { placement: "belowEditor" })` with per-peer rows in monospace, raw-ANSI hex swatches, and the column composition table from spec §6.
- Implement the ping cycle (`setInterval` + `Promise.allSettled` + `tui.requestRender()` only on state change).
- Implement the `/coms` slash command (force-refresh + `--all` + `--project` filter; widget mutation, no notification).
- Wire `session_start` (config resolve, bind, register, install widget + status + boot notify), `session_shutdown` + `SIGINT` + `SIGTERM` (drain, unlink socket on POSIX, unlink registry entry, final audit log).
- Final assertion: `grep -n "setFooter" extensions/coms.ts` MUST return nothing.
- Re-run the bun import smoke test.

### 5. Build Meta-Files (themeMap + justfile) — Parallel

- **Task ID**: `build-meta-files`
- **Depends On**: `scout-context` (so we have the Build Sheet pattern, but otherwise independent of the main builder)
- **Assigned To**: `builder-meta`
- **Agent Type**: `general-purpose`
- **Parallel**: true (runs simultaneously with Tasks 2 / 3 / 4)
- In `extensions/themeMap.ts`, add `"coms": "ocean-breeze",` to the `THEME_MAP` object near the existing `"cross-agent": "ocean-breeze"` entry. Preserve the comment style of neighboring entries.
- In `justfile`, after the `#g3` block and before the `#ext` block, add:
  ```just
  # 17. Coms: peer-to-peer messaging between Pi agents on the same machine
  ext-coms:
      pi -e extensions/coms.ts -e extensions/minimal.ts -e extensions/theme-cycler.ts
  ```
- Verify with `just --list | grep ext-coms` (must show the recipe) and `grep '"coms"' extensions/themeMap.ts` (must show the entry).

### 6. Spec-Compliance Review

- **Task ID**: `review-impl`
- **Depends On**: `build-ui-widget`, `build-meta-files`
- **Assigned To**: `reviewer-coms`
- **Agent Type**: `general-purpose`
- **Parallel**: false
- Read `specs/coms-v1.md` and `extensions/coms.ts` end-to-end.
- Run the static checklist in the Reviewer role description (a–j).
- Additionally verify: tool names use snake_case (`coms_list`, not `coms:list`); `MAX_HOPS` defaults to 5 and is overridable via `PI_COMS_MAX_HOPS`; ULID generation does NOT add a runtime dep (no `import("ulid")`); `package.json` is unchanged.
- Output a pass/fail report listing every check. If any FAIL, the orchestrator MUST resume `builder-coms` with the failure list and re-run Tasks 4 → 6 until pass. Cap retries at 2.

### 7. Runtime Smoke Test + Manual-Test Instructions

- **Task ID**: `validate-runtime`
- **Depends On**: `review-impl`
- **Assigned To**: `validator-coms`
- **Agent Type**: `general-purpose`
- **Parallel**: false
- Run the validation commands listed in §"Validation Commands" below in order.
- For each, capture the exit code and a one-line summary.
- Produce a final report: `READY` or `BLOCKED` with the failing command's output.
- Append a "Manual Test" section to the report with the exact two-terminal commands the user runs to verify peer discovery + prompt exchange + response routing (so the user can finish the validation interactively, since the runtime needs interactive terminals).

### 8. End-to-End Wire Protocol Test (drive)

- **Task ID**: `test-protocol-e2e`
- **Depends On**: `validate-runtime`
- **Assigned To**: `tester-protocol-e2e`
- **Agent Type**: `general-purpose`
- **Parallel**: true (with task 9 — each spawns its own tmux sessions, no contention)
- First, read `~/.claude/skills/drive/SKILL.md` to learn the drive CLI (`drive new-session`, `drive send-keys`, `drive read --json`, `drive screenshot`, `drive kill`).
- Pre-flight: clean any leftover state with `rm -rf ~/.pi/coms/projects/test-e2e ~/.pi/coms/sockets/*.sock` (only the test project; do not touch `default`).
- Spawn tmux session `coms-a` running `cd <project> && pi -e extensions/coms.ts --name planner --purpose "wire test A" --project test-e2e`. Spawn `coms-b` similarly with `--name coder --purpose "wire test B" --project test-e2e`. Wait until each session's stdout shows `📡 coms ready` (poll with `drive read --json` for up to 15s; fail fast if absent).
- Verify `ls ~/.pi/coms/projects/test-e2e/agents/` shows both `planner.json` and `coder.json`. Verify `ls ~/.pi/coms/sockets/*.sock` shows two sockets.
- Write a small Node tester script to `/tmp/coms-wire-test.mjs` that:
  1. Reads the `endpoint` field from `~/.pi/coms/projects/test-e2e/agents/coder.json`.
  2. Runs five sub-tests, each opening its own `net.createConnection({ path: endpoint })`:
     - **T1 — ping/pong**: send `{type:"ping", msg_id, sender_session, sender_endpoint, hops:0, timestamp}`; expect a `pong` with `agent_card.name === "coder"`, `agent_card.color` matching `/^#[0-9a-fA-F]{6}$/`, `typeof agent_card.context_used_pct === "number"`.
     - **T2 — valid prompt**: send well-formed `prompt` envelope with `hops:0`; expect `{type:"ack", msg_id}`.
     - **T3 — hops exceeded**: send prompt with `hops: 99`; expect `{type:"nack", error: /hops/i}`.
     - **T4 — malformed JSON**: write `not-json\n`; expect `{type:"nack", error: /malformed/i}`.
     - **T5 — unknown type**: send `{type:"hello", ...}`; expect `nack` with `unknown type`.
  3. Print one line per assertion: `T<n> PASS` or `T<n> FAIL: <reason>`.
  4. Exit 0 only if all 5 pass.
- Run `node /tmp/coms-wire-test.mjs` from a third tmux session. Capture full output.
- Cleanly shut down: `drive send-keys` Ctrl+C to both Pi sessions, wait 3s. Verify `~/.pi/coms/projects/test-e2e/agents/` is empty AND `~/.pi/coms/sockets/` no longer contains the two test sockets.
- Kill the tmux sessions.
- Final report: PASS/FAIL per sub-test + cleanup state + READY or BLOCKED.

### 9. Widget Visual + Stacking Confirmation (drive)

- **Task ID**: `test-widget-visual`
- **Depends On**: `validate-runtime`
- **Assigned To**: `tester-widget-visual`
- **Agent Type**: `general-purpose`
- **Parallel**: true (with task 8)
- Read `~/.claude/skills/drive/SKILL.md` for tmux CLI usage.
- Create `specs/coms-v1/runtime/` for screenshots (`mkdir -p`).
- Spawn tmux session `coms-vis-a`: `cd <project> && just ext-coms` plus pass `--name vis-a --project test-vis` via env or args (the recipe stacks coms + minimal + theme-cycler). Same for `coms-vis-b` with `--name vis-b --project test-vis`. Wait for both to print `📡 coms ready`.
- Wait an additional 12 s (one full `PI_COMS_PING_INTERVAL_MS` cycle plus margin) so each agent has ping-pong'd the other and populated its `peerCards` cache.
- `drive screenshot` each tmux session, save as `specs/coms-v1/runtime/01-vis-a.png` and `02-vis-b.png`.
- `drive read --json --tail 200` each session, capture into a string. Assert each capture contains:
  - `📡 coms · test-vis` — the widget header
  - The OTHER agent's name (`vis-b` in A's output, `vis-a` in B's)
  - At least one `\x1b[38;2;` escape sequence — proves a hex color was rendered (color swatch or progress bar fill)
  - The minimal footer's bar pattern (a `[` followed by `#` and `-` characters and `%`) — proves the footer was NOT clobbered by a stray `setFooter` call
- In session A only, `drive send-keys` `/coms\n` to force a refresh. Wait 2 s. Screenshot again as `specs/coms-v1/runtime/03-vis-a-after-refresh.png`.
- Cleanly tear down: send Ctrl+C to both Pi sessions, kill the tmux sessions.
- Final report: PASS/FAIL for each of the 4 visual assertions × 2 sessions, plus paths to the saved screenshots, plus READY or BLOCKED.

## Acceptance Criteria

- `extensions/coms.ts` exists and is between 400 and 1000 lines (single-file convention; less than 400 likely means features were trimmed, more than 1000 means it should have been refactored into a helper module — flag for v1.1 if exceeded).
- `extensions/coms.ts` contains zero references to `setFooter`. Confirmed by `grep -n "setFooter" extensions/coms.ts` returning empty.
- `extensions/coms.ts` registers exactly one widget with key `"coms-pool"` and `{ placement: "belowEditor" }`.
- `extensions/coms.ts` registers exactly four tools (`coms_list`, `coms_send`, `coms_get`, `coms_await`) and one slash command (`coms`).
- `extensions/coms.ts` calls `pi.sendMessage(…, { deliverAs: "followUp", triggerTurn: true })` at least once (in `handlePrompt`).
- `extensions/coms.ts` calls `pi.appendEntry("coms-log", …)` at least four times (boot, send, receive, shutdown).
- `extensions/themeMap.ts` contains the line `"coms": "ocean-breeze",`.
- `justfile` contains a recipe named `ext-coms` whose body matches `pi -e extensions/coms.ts -e extensions/minimal.ts -e extensions/theme-cycler.ts`.
- `package.json` is unchanged (no new runtime deps).
- Bun import smoke test (`bun --silent -e "import('extensions/coms.ts').then(m => console.log('OK', typeof m.default))"`) prints `OK function`.
- `pi -e extensions/coms.ts -p "exit"` exits 0 within 5 seconds. After the run, `ls ~/.pi/coms/projects/default/agents/` shows zero stale entries (clean shutdown), AND `ls ~/.pi/coms/sockets/` shows zero stale `.sock` files.
- The reviewer's spec-compliance checklist (a-j plus snake_case/MAX_HOPS/no-ulid-dep) returns full pass.
- The validator's runtime report ends with `READY`.
- `tester-protocol-e2e` reports PASS on all five wire sub-tests (T1 ping/pong, T2 valid-prompt ack, T3 hops-exceeded nack, T4 malformed-JSON nack, T5 unknown-type nack) AND clean teardown.
- `tester-widget-visual` reports PASS on all four visual assertions × both sessions, AND saves at least three screenshots to `specs/coms-v1/runtime/`.

## Validation Commands

Execute these commands to validate the task is complete:

- `bun --version` — confirm toolchain present.
- `pi --version` — confirm Pi installed.
- `test -f extensions/coms.ts && wc -l extensions/coms.ts` — confirm file exists and is in the 400–1000 line range.
- `grep -n "setFooter" extensions/coms.ts || echo NO_SETFOOTER` — must print `NO_SETFOOTER`.
- `grep -nE 'setWidget\("coms-pool"' extensions/coms.ts` — must return at least one hit.
- `grep -nE 'placement:\s*"belowEditor"' extensions/coms.ts` — must return at least one hit.
- `grep -cE 'pi\.registerTool\(' extensions/coms.ts` — must return `4`.
- `grep -cE 'pi\.registerCommand\("coms"' extensions/coms.ts` — must return `1`.
- `grep -cE 'deliverAs:\s*"followUp"' extensions/coms.ts` — must return at least `1`.
- `grep -cE 'pi\.appendEntry\("coms-log"' extensions/coms.ts` — must return at least `4`.
- `grep -nE '"coms":\s*"ocean-breeze"' extensions/themeMap.ts` — must return one hit.
- `grep -A2 '^ext-coms:' justfile | grep 'pi -e extensions/coms.ts'` — must return a hit.
- `git diff package.json` — must show no changes.
- `bun --silent -e "import('/Users/indydevdan/Documents/projects/experimental/pi-vs-cc/extensions/coms.ts').then(m => console.log('OK', typeof m.default)).catch(e => { console.error(e); process.exit(1); })"` — must print `OK function`.
- `timeout 10 pi -e extensions/coms.ts -p "exit" >/dev/null 2>&1; echo $?` — must print `0`.
- `ls ~/.pi/coms/projects/default/agents/ 2>/dev/null; ls ~/.pi/coms/sockets/ 2>/dev/null` — both should be empty after the previous command (clean shutdown).
- **Manual** (validator-coms documents this for the user, does not run it): two terminals running `just ext-coms`, then in terminal A type `/coms` (widget refreshes showing terminal B), then call the `coms_send` tool to deliver a prompt to B, observe B's chat receive a follow-up message, B replies, A's `coms_await` resolves.

## Notes

- **No new dependencies.** `yaml` is already in `package.json` and unused by this feature (we use the inline frontmatter regex from `agent-team.ts:79-105`). `node:net`, `node:fs`, `node:path`, `node:os`, `node:crypto` are all stdlib. `ulid` is intentionally NOT added — generate inline with `crypto.randomBytes(10)` + Crockford base32 (≤ 25 lines per spec §3).
- **Cross-platform note.** Phase A's `bindEndpoint` must branch on `process.platform === "win32"`. The validator runs on macOS only; Windows verification is deferred to a future device-availability event.
- **Footer NEVER touched.** The cardinal rule. The reviewer enforces this with a grep. If a builder accidentally calls `setFooter` (e.g. by copying too liberally from `tool-counter.ts`), the review FAILS and the builder is resumed with that explicit instruction.
- **Hex → ANSI helper.** Use the `\x1b[38;2;R;G;Bm…\x1b[39m` form from `cross-agent.ts:20-37`. Three or four lines of code. Do not pull in a color package.
- **Widget render is read-only.** The `render(width)` closure must read from the `peerCards` cache and never call `fs.*` or `net.*`. Re-renders happen on `tui.requestRender()` after `refreshPool` mutates the cache.
- **Audit trail.** `pi.appendEntry("coms-log", …)` is cheap and survives across sessions — invaluable for debugging "why didn't the prompt arrive?" without bloating the LLM context.
- **Retry budget.** If `review-impl` returns FAIL twice, the orchestrator pauses and surfaces the failure list to the user — don't loop indefinitely.
- **Out of scope.** Cross-host (HTTP/A2A bridge), streaming `notify`, broadcast, persistent message log, encrypted endpoints, capability tokens — all v2. Spec §12.

