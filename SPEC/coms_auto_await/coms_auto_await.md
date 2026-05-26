# coms_auto_await

Automate the inter-agent request/response handshake so neither the sender model nor the human user has to manage message IDs or manually prompt the receiver to reply.

> **DESIGN CONTRACT**
>
> - `coms-go` server is a **dumb pipe**. Zero server-side changes.
> - The existing 8 tools (`coms_list`, `coms_send`, `coms_get`, `coms_await`, and net variants) are **untouched** — every caller, CI script, and justfile recipe continues to work byte-for-byte.
> - All new logic lives in **`shim.ts`** (receiver injection) and in new IPC handler stubs inside **`localclient/tools.go`** and **`netclient/tools.go`** (sender wrapper). No transport changes, no auth changes, no new routes.
> - `ca()` in `~/.zsh/tooling/coms.zsh` requires **no changes**.

---

## Outputs

The following are the gating, testable acceptance objectives. Every item is a shell-checkable predicate (mirrored in Section 13 as a checkbox list).

- `coms_ask` and `coms_net_ask` appear in pi's registered-tool list when launched with `pi -e extensions/coms-go/shim.ts`.
- Calling `coms_ask { target: "B", prompt: "..." }` from agent A returns the full response text from agent B without any intermediate human interaction — verified by an integration run with two real `pi` agents.
- Calling `coms_net_ask { target: "B", prompt: "..." }` does the same over the HTTP/SSE transport.
- Agent B's pi REPL stays interactive during the tool-block: the user can type and send messages to agent B while A's `coms_ask` call is blocking — verified by the interactive-invariant test described in Section 9.
- When `coms_net_ask` is called with no `target` (broadcast), the returned bag contains all responses received within `timeout_ms` and is empty (not an error) when zero agents respond within the deadline.
- The existing 8 tools pass all existing tests without modification: `(cd extensions/coms-go && go test ./...)` exits 0.
- `shim.ts` stays ≤ 300 LOC (up from ≤ 200 LOC; growth is the two new tool registrations plus the `before_agent_start` injection hook).
- No `msg_id` appears in the model's tool result or prompt unless the model explicitly called `coms_send` / `coms_net_send` directly.

# Outcome

- `coms_ask` and `coms_net_ask` registered and callable.
- Single-call round-trip (send → inject receiver → collect response) works end-to-end.
- Broadcast collect-with-deadline works; empty bag is not an error.
- Pi REPL remains live during sender tool-block; interactive-invariant test passes.
- Existing 8 tools and all callers untouched.
- No server changes; no transport changes; no auth changes.

---

## 1. Problem Statement

### Today's flow (sender burden)

When agent A wants to ask agent B a question today:

1. A calls `coms_send` (local) or `coms_net_send` (net) — receives a `msg_id`.
2. A must remember the `msg_id` and explicitly call `coms_await` / `coms_net_await` to block until B replies.
3. A's model must be capable of chaining these two tool calls without user guidance, and must not drop the `msg_id` between them.

In practice, models sometimes forget to call `coms_await` or lose the `msg_id`, leaving the conversation in an ambiguous state. The user must intervene.

### Today's flow (receiver burden)

When the server delivers an inbound prompt to B:

1. The `handleInboundPrompt` function (netclient/client.go lines 705-747) queues the message and sets `currentInbound`.
2. **Nothing injects a directive into B's pi session.** B's model sits idle, unaware that a message is pending.
3. The user must manually type "await message id X" or equivalent into B's REPL to trigger a response.

### Desired flow

**Sender side:** A calls a single tool — `coms_ask` (local) or `coms_net_ask` (net) — and receives the complete response from B. Internally the tool does `send → await`, but the model sees one atomic tool call.

**Receiver side:** When B's `client-net` process receives an inbound `prompt` SSE event, `shim.ts` automatically injects a directive into B's next agent turn — no human input required. B's model reads the injected message, formulates a reply, and the `agent_end` hook (shim.ts line 162) submits that reply by forwarding the final assistant text as `last_text` in the lifecycle IPC frame; `handleLifecycle` (netclient/client.go line 838) receives it and calls `onAgentEnd` (line 856), which POSTs the response back. **T1.5 is required to plumb `last_text` into this frame — without it the loop never closes.**

Neither A's model nor B's model (nor the human user) manages `msg_id`s.

---

## 2. Non-Goals

The following are explicitly out of scope and must not appear in any implementation:

- **No coms-go server changes.** No new routes, no new wire fields, no long-held connections, no server-side liveness tracking, no cancellation trees, no new auth surface. The server remains a dumb pipe.
- **No new transports.** The Unix-socket (local) and HTTP/SSE (net) transports are unchanged.
- **No auth changes.** Bearer token handling, secret file paths, and `crypto/subtle.ConstantTimeCompare` usage are untouched.
- **No removal of existing 8 tools.** `coms_list`, `coms_send`, `coms_get`, `coms_await`, and their `coms_net_*` counterparts stay registered, documented, and tested. They are the preferred tools for fire-and-forget workflows.
- **No local-transport receiver injection.** The `client-local` (Unix-socket) path does not currently deliver inbound prompts via SSE; it is peer-to-peer and the inbound delivery mechanism is different. Receiver auto-injection is net-only in this spec. `coms_ask` (local) is a wrapper only — it does the send+await chain but does not inject into the receiver. This is documented in Section 5 as a known asymmetry.
- **No steering changes.** Pi's steering mechanism is unchanged.
- **No new IPC frame kinds.** The existing `tool_request`, `tool_response`, `tool_error`, `lifecycle`, `command`, `event`, `shutdown` kinds in `internal/ipc/ipc.go` are sufficient.
- **WORKFLOW.md slash-command alignment is explicitly out of scope.** The pre-existing drift in `extensions/coms-go/WORKFLOW.md:87` (documenting `/coms_net_send` as a slash command when it is registered only as a tool) is unrelated to coms_auto_await and will be addressed in a separate commit.

---

## 3. Architecture

```
                         LOCAL TRANSPORT (Unix socket)
  ┌──────────────────────────────────────────────────────────────────┐
  │  Pi-A REPL                                                       │
  │  model calls coms_ask {target:"B", prompt:"..."}                 │
  │       │                                                           │
  │  shim.ts ──ipc──► localclient/tools.go                           │
  │                     toolAsk()                                     │
  │                       └─ coms_send (existing)                    │
  │                       └─ coms_await (existing, blocking)         │
  │                       └─ return response to model               │
  └──────────────────────────────────────────────────────────────────┘

                         NET TRANSPORT (HTTP/SSE)
  ┌──────────────────────────────────────────────────────────────────┐
  │  Pi-A REPL                                                       │
  │  model calls coms_net_ask {target:"B", prompt:"..."}             │
  │       │                                                           │
  │  shim-A.ts ──ipc──► netclient/tools.go                           │
  │                        toolNetAsk()                              │
  │                          └─ coms_net_send (existing)            │
  │                          └─ coms_net_await (existing, blocking) │
  │                          └─ return response to model            │
  │                                                                  │
  │  [NEW LOGIC LIVES HERE]                                          │
  │  coms-go serve ──SSE "prompt" event──► netclient-B/client.go    │
  │                                          handleInboundPrompt()  │
  │                                          └─ ipc.Writer.Event()  │
  │                                               "inbound_prompt"  │
  │                                                   │              │
  │  shim-B.ts ◄──ipc── { kind:"event",              │              │
  │                        name:"inbound_prompt",      │              │
  │                        data:{msg_id, sender_name,  │              │
  │                              body, hops} }         │              │
  │       │                                            │              │
  │  shim-B registers pending injection                │              │
  │       │                                            │              │
  │  pi fires before_agent_start ──► shim-B handler   │              │
  │       └─ returns { message: { content: directive }}│              │
  │                                                    │              │
  │  Pi-B model reads directive, replies normally      │              │
  │  agent_end fires ──► shim-B.ts (T1.5)             │              │
  │       └─ extracts last_text from AgentEndEvent.messages          │
  │       └─ sends { kind:"lifecycle", event:"agent_end",            │
  │                  data:{cwd,model,last_text} } to netIpc child    │
  │  netclient-B/client.go handleLifecycle()           │              │
  │       └─ onAgentEnd(last_text) (existing)          │              │
  │                        └─ POST /v1/messages/id/response          │
  │                                                    │              │
  │  coms-go serve delivers SSE "response" to shim-A  │              │
  │       └─ netclient-A resolves pendingReply channel  │              │
  │       └─ toolNetAsk returns to model               │              │
  └──────────────────────────────────────────────────────────────────┘

  Legend:
    [NEW LOGIC] = shim.ts agent_end handler now plumbs last_text (T1.5)
                  + shim.ts before_agent_start hook (T3)
                  + ipc event forwarding in netclient/client.go handleInboundPrompt() (T1)
    [UNCHANGED] = coms-go server, all 8 existing tools, ca(), transports
```

---

## 4. New Tools: `coms_ask` and `coms_net_ask`

### 4.1 `coms_ask` (local transport wrapper)

**Purpose:** Atomic send+await over the Unix-socket transport. The model sees one tool call and one response. Internally identical to calling `coms_send` then `coms_await` with the returned `msg_id`.

**Note on receiver injection:** Because the local transport delivers inbound prompts directly over the Unix socket (the `handlePrompt` function in `localclient/handlers.go` — not via SSE), the receiver-side auto-injection described in Section 5 does NOT apply to the local transport. The receiver model must still be running and listening. This is documented here so callers understand the asymmetry: `coms_net_ask` gets full auto-injection; `coms_ask` is a convenience wrapper only.

**Registration location:** `shim.ts` — `pi.registerTool(...)`, forwarded to `localclient` via IPC.

**Handler location:** `extensions/coms-go/internal/localclient/tools.go` — new function `toolAsk`.

**Tool name:** `coms_ask`

**Parameters:**

```typescript
Type.Object({
  target: Type.String({
    description: "Peer name (scoped to project) or session_id."
  }),
  prompt: Type.String({
    description: "The prompt to send. The peer will receive this and reply."
  }),
  timeout_ms: Type.Optional(Type.Number({
    description: "Max ms to wait for reply. Default: PI_COMS_TIMEOUT_MS (1 800 000). Per-call override."
  })),
  conversation_id: Type.Optional(Type.String()),
  response_schema: Type.Optional(Type.Any({
    description: "Optional JSON Schema for the expected response shape."
  }))
})
```

**Return shape (on success):**

```json
{
  "content": [{ "type": "text", "text": "<peer's response text>" }],
  "details": {
    "msg_id": "01HXNJ...",
    "target": "coder",
    "target_session": "01HXNJ...",
    "hops": 1,
    "response": "<raw response, may be JSON>"
  }
}
```

**Error cases:**

| Condition | Error string |
|-----------|-------------|
| `target` not found or unreachable | `"coms_ask: no live agent matching \"<target>\""` |
| Hop limit reached | `"coms_ask: hop limit reached (<n> >= <max>)"` |
| Send transport error | `"coms_ask: send failed: <transport error>"` |
| Await timeout | `"coms_ask: timeout waiting for reply from <target>"` |
| Client not initialised | `"coms: not initialised"` |

**Implementation sketch (`localclient/tools.go`):**

`toolAsk` is not a new IPC path — it is a Go function that calls the existing `toolSend` logic (reusing `resolveTarget`, envelope construction, `transport.SendEnvelope`, pending reply registration) and then immediately blocks on the `pendingReply.ready` channel with the specified timeout. It writes one `tool_response` when the reply arrives or one `tool_error` on timeout. No new goroutines beyond the existing timeout timer that `toolSend` already starts (reuse it).

### 4.2 `coms_net_ask` (net transport wrapper)

**Purpose:** Atomic send+await over the HTTP/SSE transport. The model sees one tool call and one response. Also triggers the receiver-side auto-injection mechanism (Section 5) so the remote model does not need to be manually prompted.

**Registration location:** `shim.ts` — `pi.registerTool(...)`, forwarded to `netclient` via IPC.

**Handler location:** `extensions/coms-go/internal/netclient/tools.go` — new function `toolNetAsk`.

**Tool name:** `coms_net_ask`

**Parameters:**

```typescript
Type.Object({
  target: Type.Optional(Type.String({
    description: "Peer name or session_id. Omit for broadcast (fans to all agents in project)."
  })),
  prompt: Type.String({
    description: "The prompt to send."
  }),
  timeout_ms: Type.Optional(Type.Number({
    description: "Deadline for collecting responses (ms). Default 30 000. For broadcast, all replies arriving before this deadline are collected."
  })),
  conversation_id: Type.Optional(Type.String()),
  response_schema: Type.Optional(Type.Any())
})
```

**Return shape — unicast (target specified):**

```json
{
  "content": [{ "type": "text", "text": "<peer's response text>" }],
  "details": {
    "msg_id": "01HXNJ...",
    "target": "coder",
    "target_session": "01HXNJ...",
    "hops": 1,
    "response": "<raw response>"
  }
}
```

**Return shape — broadcast (no target):**

```json
{
  "content": [{ "type": "text", "text": "2 of 3 peers responded within 30s.\n\ncoder: <response>\nreviewer: <response>" }],
  "details": {
    "broadcast": true,
    "timeout_ms": 30000,
    "total_peers": 3,
    "responded": 2,
    "timed_out": 1,
    "responses": [
      { "agent": "coder",    "session_id": "...", "response": "...", "error": null },
      { "agent": "reviewer", "session_id": "...", "response": "...", "error": null }
    ],
    "no_response": [
      { "agent": "planner",  "session_id": "..." }
    ]
  }
}
```

**Error cases:**

| Condition | Error string |
|-----------|-------------|
| `target` specified but not found | `"coms_net_ask: send failed (404): agent_not_found"` |
| Server unreachable | `"coms_net_ask: send failed: <network error>"` (bearer stripped) |
| Unicast timeout | `"coms_net_ask: timeout waiting for reply from <target>"` |
| Hop limit reached | `"coms_net_ask: hop limit reached (<n> >= <max>)"` |
| Client not initialised | `"coms-net not initialised"` |
| **Broadcast, zero responses** | Not an error — return empty `responses` bag with `responded: 0` |

**Timeout parameter:** `timeout_ms` defaults to **30 000 ms** (30 s) for `coms_net_ask`, not the 1 800 000 ms (30 min) default of `coms_net_await`. The intent is interactive latency, not fire-and-forget durability. Callers needing longer waits must pass `timeout_ms` explicitly.

### 4.3 User-facing slash commands

In addition to the model-callable tools above, `shim.ts` registers positional slash-command wrappers so the human user can drive ask/broadcast without hand-rolling tool-call JSON. Commands dispatch through the same `invokeTool` path as the registered tools — same validation, audit, timeout semantics. See `extensions/coms-go/shim.ts` `askCmd(...)` registrations.

| Command | Wraps | Behavior |
|---------|-------|----------|
| `/ask <peer> <prompt...>` | `coms_net_ask` | Unicast over coms-net; replies surface via `ctx.ui.notify`. |
| `/broadcast <prompt...>` | `coms_net_ask` (no `target`) | Fans to all peers in project; returns the bag synthesis. |
| `/ask-local <peer> <prompt...>` | `coms_ask` | Unix-socket transport; no receiver auto-injection (§5 asymmetry). |

The pre-existing `/coms` and `/coms-net` widget-refresh commands are unchanged.

---

## 5. Receiver-Side Auto-Injection (Net Transport Only)

### 5.1 Mechanism

When `handleInboundPrompt` (netclient/client.go line 705) receives a `prompt` SSE event, it already queues `netInboundCtx` and sets `currentInbound`. The change: after queuing, it also emits an **unsolicited IPC event** to `shim.ts` via `ipc.Writer.Event()` (the existing `Event` method, ipc.go line 139):

```
{ "kind": "event", "name": "inbound_prompt", "data": {
    "msg_id": "01HXNJ...",
    "sender_name": "planner",
    "sender_session": "01HXNJ...",
    "body": "<the prompt text>",
    "hops": 1
} }
```

This is a **one-line addition** to `handleInboundPrompt`. The `ipc.Writer` is already passed to the Go loop (netclient/client.go line 214 — `w := ipc.NewWriter(stdout)`); it needs to be stored on the `Client` struct so `handleInboundPrompt` can reach it.

### 5.2 shim.ts: IPC event listener

`shim.ts` already reads the `rl.on("line", ...)` handler in `makeIpc` (shim.ts lines 43-51). That handler currently handles only `tool_response` and `tool_error`. Extend it to handle `kind === "event"`:

```typescript
if (msg.kind === "event" && msg.name === "inbound_prompt") {
  inboundQueue.push(msg.data);  // enqueue; FIFO order preserved
}
```

`inboundQueue` is a module-scoped `Array<InboundEntry>` per IPC child (one for `netIpc`), where `InboundEntry = { msg_id: string; sender_name: string; sender_session: string; body: string; hops: number }`. Entries accumulate until drained by the next `before_agent_start`. There is no last-wins overwrite — every arriving entry is appended.

### 5.3 shim.ts: `before_agent_start` hook

Register one additional lifecycle hook (net child only):

```typescript
pi.on("before_agent_start", async (_event, _ctx) => {
  if (inboundQueue.length === 0) return {};
  const entries = inboundQueue.splice(0);   // drain all; FIFO order
  const n = entries.length;
  const numbered = entries
    .map((inj, i) =>
      `[${i + 1}/${n}] From ${inj.sender_name} (msg ${inj.msg_id}):\n${inj.body}`
    )
    .join("\n\n");
  const text =
    `You have ${n} pending message${n === 1 ? "" : "s"}:\n\n` +
    numbered +
    `\n\nAddress each pending message in your reply. Your full response will be returned to each sender automatically.`;
  return {
    message: {
      customType: "coms_inbound",
      content: [{ type: "text", text }],
      display: `coms-net: ${n} inbound message${n === 1 ? "" : "s"} from ${entries.map(e => e.sender_name).join(", ")}`,
      details: { entries: entries.map(e => ({ msg_id: e.msg_id, sender_name: e.sender_name, sender_session: e.sender_session, hops: e.hops })) }
    }
  };
});
```

The `before_agent_start` event fires before each agent turn (per the pi ExtensionAPI — types.d.ts line 796). Returning `{ message: {...} }` injects a custom message into the conversation at the start of that turn (types.d.ts line 736). This is the only supported injection point in the current pi API.

**Directive format (N=1 case — single sender):** Degenerates to a readable single-message form; the numbered header still appears so the model always sees the same pattern:

```
You have 1 pending message:

[1/1] From <sender_name> (msg <msg_id>):
<body>

Address each pending message in your reply. Your full response will be returned to each sender automatically.
```

**Directive format (N>1 case — multiple senders):**

```
You have N pending messages:

[1/N] From <sender_A> (msg <id_A>):
<body_A>

[2/N] From <sender_B> (msg <id_B>):
<body_B>

Address each pending message in your reply. Your full response will be returned to each sender automatically.
```

The `msg_id` IS shown to the model in the numbered header so it can address each sender individually in its reply text. The injection explicitly instructs the model to address all N pending messages in a single response. The `onAgentEnd` path in netclient/client.go (line 856) is the canonical submission path — the model's `last_text` is posted as the reply for every unfulfilled `inboundQueue` entry (oldest-first; see §5.4 disambiguation rule). **This submission only fires if the `agent_end` IPC frame carries a non-empty `last_text` field (see T1.5). Without T1.5, `handleLifecycle` returns without calling `onAgentEnd` and replies are never posted.**

### 5.4 FIFO queue semantics and drain-all-per-turn rule

**Queue:** `inboundQueue` is a FIFO array of `InboundEntry` objects, one per IPC child (scoped to `netIpc`). There is no last-wins overwrite — every arriving `"inbound_prompt"` event appends to the tail.

**Drain rule:** On each `before_agent_start`, the hook atomically splices the entire array (`inboundQueue.splice(0)`) and concatenates all entries into one delimited injection. All queued messages are presented to the model in that single turn. The array is left empty after the splice.

**Disambiguation rule (v1 — drain-all-per-turn, oldest-first assignment):** The injection explicitly instructs the model to address all N messages. On `onAgentEnd`, `handleLifecycle` submits the model's `last_text` as the response to the **oldest unanswered** `netInboundCtx` entry in the Go `Client.inboundQueue` map. For N > 1, `onAgentEnd` iterates the inbound map in insertion order and closes each unfulfilled entry with the same `last_text`. This is acceptable for v1 because the injection has already asked the model to address all senders — the model's reply should cover all of them. Per-sender reply parsing is explicitly deferred to a future iteration.

**No-op invariant:** If the user triggers a manual turn before any inbound has arrived, `inboundQueue` is empty and the hook returns `{}` (no-op). The invariant that the hook is always registered but conditionally active is preserved.

### 5.5 Why `before_agent_start`, not `session_start`

`session_start` fires once per pi session (shim.ts line 136). `before_agent_start` fires before each agent invocation (each time the model starts generating). Inbound prompts arrive asynchronously — potentially after session start — so `before_agent_start` is the correct hook. It is already used by other extensions for per-turn prompt injection (pi-pi.ts line 557, agent-chain.ts line 645, agent-team.ts line 631).

### 5.6 Idle auto-turn (OQ-3 resolution)

`before_agent_start` only fires when a turn actually starts; an inbound that lands while the receiver is sitting idle would otherwise wait for the human to type. To close that gap, the `inbound_prompt` handler inside `makeIpc` invokes a per-IPC `onInbound` callback after each enqueue. For the net child, that callback checks `lastCtx.isIdle()` and — when true — drains the FIFO and self-injects via:

```typescript
pi.sendMessage(message, { triggerTurn: true });
```

`pi.sendMessage` (ExtensionAPI; types.ts line 1178) with `triggerTurn:true` appends a `customType: "coms_inbound"` entry to the session and starts a new turn when the agent is not streaming. It is preferred over `pi.sendUserMessage("")` because the latter would render a visible empty user-prompt line in the REPL ("Always triggers a turn" — types.ts line 1184). The shared `buildInboundInjection(entries)` helper builds the same payload used by `before_agent_start`, so the two drain paths are byte-equivalent — whichever fires first wins and the other no-ops on an empty queue. `lastCtx` is captured on `session_start`, `before_agent_start`, and `agent_end`.

---

## 6. Broadcast Collect-With-Deadline

### 6.1 Behavior

When `coms_net_ask` is called with no `target` (or `target` is the empty string):

1. `toolNetAsk` calls `coms_net_list` logic internally to enumerate all peers in the project (excluding self, excluding `--explicit` agents unless `include_explicit: true` is passed).
2. For each peer, it calls `coms_net_send` logic (one HTTP POST per peer, concurrent via goroutines).
3. It collects `msg_id`s into a slice.
4. It starts a single `time.NewTimer(timeout_ms)`.
5. It waits on each peer's `pendingReply.ready` channel using a `select` with the shared deadline timer.
6. Responses that arrive before the deadline are added to the `responses` bag. Peers whose channels have not fired when the timer expires are added to `no_response`.
7. The tool returns the bag regardless of how many responded (zero responses is not an error).

### 6.2 Cancellation

If the sender model's tool call is interrupted (pi process receives SIGINT/SIGTERM — handled by `session_shutdown` in shim.ts line 168, which sends `{ kind: "shutdown" }` to the Go child), the `Run` function's `ctx.Done()` arm fires (netclient/client.go line 219), which cancels all in-flight goroutines via `context.WithCancel`. Pending `pendingReply` entries are abandoned; their goroutines drain naturally since they hold no server resources. The deadline timer is stopped via `defer timer.Stop()`.

### 6.3 Data structure

```go
// BroadcastResponse is the details bag for a broadcast coms_net_ask.
type BroadcastResponse struct {
    Broadcast  bool                `json:"broadcast"`
    TimeoutMs  int                 `json:"timeout_ms"`
    TotalPeers int                 `json:"total_peers"`
    Responded  int                 `json:"responded"`
    TimedOut   int                 `json:"timed_out"`
    Responses  []PeerResponse      `json:"responses"`
    NoResponse []PeerIdentity      `json:"no_response"`
}

type PeerResponse struct {
    Agent     string          `json:"agent"`
    SessionID string          `json:"session_id"`
    Response  json.RawMessage `json:"response"`
    Error     *string         `json:"error"`
}

type PeerIdentity struct {
    Agent     string `json:"agent"`
    SessionID string `json:"session_id"`
}
```

These types live in `internal/netclient/ask.go` (new file, see Section 11 task list).

---

## 7. User-Interactive Invariant

**Invariant:** While agent A's model is blocked inside `coms_net_ask` (waiting for B's response), the human user can freely type and submit messages to agent B's pi REPL, and those messages are processed normally by B. The user is not locked out of either session.

**Why this holds today (verify, do not assume):** Pi's tool execution runs in a goroutine per tool call (the `dispatchTool` goroutine in netclient/client.go line 233). The pi REPL's input handling runs on a separate event loop in the main JS thread. They share no locks. A blocking `select` in a Go goroutine does not block the pi JS event loop.

**Verification test (manual, required before merge):**

1. Start agent A: `ca alpha --color cyan`.
2. Start agent B: `ca beta --color magenta`.
3. In agent A, invoke `coms_net_ask { target: "beta", prompt: "Count to 10 slowly." }`.
4. **Immediately** switch focus to agent B's REPL. Type: "What is 2+2?" and submit.
5. Observe that agent B responds to "What is 2+2?" AND that agent A's `coms_net_ask` eventually returns with beta's "Count to 10" response.
6. PASS: both interactions complete without deadlock or dropped messages.
7. FAIL: either the "2+2" response never appears, or `coms_net_ask` hangs indefinitely, or B's pi REPL freezes during the injection.

This test must be documented in `SPEC/coms_auto_await/REVIEWS/interactive_invariant_test.md` with actual output pasted verbatim when run by the implementer.

---

## 8. `ca` — No Changes Required

`ca()` (coms.zsh line 83) is a thin wrapper around `pi -e "$COMS_GO/shim.ts" --name "$name" "${passthrough[@]}"`. It delegates everything to pi and shim.ts. The new tools and lifecycle hook are registered inside `shim.ts`; `ca` sees them automatically. No changes to `ca`, `coms-agent`, `coms-serve`, `coms-serve-lan`, or any other zsh function in `coms.zsh`.

---

## 9. Backward Compatibility

| Surface | Status | Notes |
|---------|--------|-------|
| `coms_list` | Untouched | shim.ts registration unchanged |
| `coms_send` | Untouched | localclient/tools.go unchanged |
| `coms_get` | Untouched | localclient/tools.go unchanged |
| `coms_await` | Untouched | localclient/tools.go unchanged |
| `coms_net_list` | Untouched | netclient/tools.go unchanged |
| `coms_net_send` | Untouched | netclient/tools.go unchanged |
| `coms_net_get` | Untouched | netclient/tools.go unchanged |
| `coms_net_await` | Untouched | netclient/tools.go unchanged |
| `coms-go serve` | Untouched | server/ unchanged |
| Wire protocol | Untouched | No new HTTP routes, SSE events, or envelope fields |
| `ca()` / `coms-agent` | Untouched | No zsh changes |
| `justfile` | Untouched | No new recipes needed |
| Audit log format | Untouched | `onAgentEnd` submission path unchanged |
| `~/.pi/coms-net-log` | Untouched | One new audit event added: `ask_send` (mirrors `prompt_out`) |

**One new audit event** (`ask_send`) is emitted in `toolNetAsk` before blocking on await. It is additive — existing log parsers ignore unknown event types.

---

## 10. Implementation Notes

### shim.ts IPC event handling

The `makeIpc` function (shim.ts lines 40-62) currently discards all frames that are not `tool_response` or `tool_error` (line 46: `if (msg.kind === "tool_response" || msg.kind === "tool_error")`). To handle `kind === "event"`, change the condition to also handle `"event"` frames and route by `msg.name`. The `pending` Map (line 41) is not involved — events are push-only, not correlated to a request `id`.

### ipc.Writer on Client struct

`handleInboundPrompt` is called from `handleSSEEvent` (netclient/client.go line 589), which is called from `connectSSE` (line 580), which runs in the `sseLoop` goroutine. The `ipc.Writer w` is currently created in the `run` method (line 214) and not stored on the `Client`. Store it as `c.ipcWriter *ipc.Writer` (set in `run` before entering the select loop). `handleInboundPrompt` then calls `c.ipcWriter.Event("inbound_prompt", ...)`. The `ipc.Writer` is already mutex-protected (ipc.go line 87), so no additional locking is needed.

### Local transport asymmetry

`client-local` uses `transport.SendEnvelope` (a synchronous Unix-socket dial — localclient/tools.go line 210). Inbound delivery to the local client happens via the `handlePrompt` function in `localclient/handlers.go` (not via SSE). The auto-injection mechanism requires the SSE event path. Extending auto-injection to the local transport would require either (a) a new IPC event from `localclient` when an inbound prompt arrives on the Unix socket, or (b) restructuring the local transport's receive loop. Both are out of scope for this spec. Document clearly in tool description and in a code comment.

### Broadcast implementation approach

`toolNetAsk` with no target:
1. Enumerate peers via `coms_net_list` logic (inline the HTTP GET, do not call `toolNetList` via IPC — that would double the IPC round-trip).
2. Fan out: for each peer, call `httpPost` to `/v1/messages` concurrently (bounded by a semaphore channel of size `runtime.GOMAXPROCS(0)` to avoid overwhelming the server).
3. Collect `msg_id`s; register `netPendingReply` entries.
4. Wait: single `time.NewTimer(timeoutMs)` shared across all goroutines watching `pendingReply.ready` channels in a `select`.
5. Aggregate and return.

This is a standard fan-out pattern consistent with the project's Ardan Labs Go conventions.

---

## 11. Tasks

| # | Size | Description | Files Touched |
|---|------|-------------|---------------|
| T1 | S | Store `ipc.Writer` on `netclient.Client` struct; emit `"inbound_prompt"` IPC event from `handleInboundPrompt` after queueing | `internal/netclient/client.go` |
| T1.5 | S | **[HARD PREREQUISITE of T1, T2, T3]** Plumb last assistant text through `agent_end` lifecycle payload — see Section 11.1 | `extensions/coms-go/shim.ts` |
| T2 | S | Extend `makeIpc` in `shim.ts` to handle `kind === "event"` frames; append to `inboundQueue` FIFO array per net child (replaces scalar `pendingInjection`) | `extensions/coms-go/shim.ts` |
| T3 | S | Register `before_agent_start` hook in `shim.ts`; drain entire `inboundQueue` via `splice(0)`, concatenate all entries into delimited injection (see Section 5.3/5.4), return `{ message: {...} }` | `extensions/coms-go/shim.ts` |
| T4 | M | Implement `toolAsk` in `localclient/tools.go`; register `coms_ask` in `shim.ts` (dispatch to local child) | `internal/localclient/tools.go`, `extensions/coms-go/shim.ts` |
| T5 | M | Implement `toolNetAsk` (unicast path) in `netclient/tools.go`; register `coms_net_ask` in `shim.ts` | `internal/netclient/tools.go`, `extensions/coms-go/shim.ts` |
| T6 | M | Implement broadcast path in `toolNetAsk`; add `BroadcastResponse` / `PeerResponse` / `PeerIdentity` types | `internal/netclient/ask.go` (new), `internal/netclient/tools.go` |
| T7 | S | Add `ask_send` audit event to `toolNetAsk` send path | `internal/netclient/tools.go` |
| T8 | S | Unit tests: `toolAsk` happy path + timeout; `toolNetAsk` unicast happy path + timeout | `internal/localclient/client_test.go`, `internal/netclient/client_test.go` |
| T9 | M | Unit tests: broadcast collect-with-deadline (mock peers: 3 peers, 2 respond, 1 times out; 0 respond) | `internal/netclient/client_test.go` |
| T10 | S | Integration test: two real pi agents, `coms_net_ask` round-trip; tagged `//go:build integration` | `internal/netclient/client_test.go` |
| T11 | S | Run interactive-invariant test (Section 7); paste output to `SPEC/coms_auto_await/REVIEWS/interactive_invariant_test.md` | Test doc only |
| T12 | S | Regression: verify all 8 existing tools still pass `go test ./...`; verify `shim.ts` ≤ 300 LOC | CI / `justfile` |

**Total: 13 tasks (12 original + T1.5).** T1.5 + T1–T3 are the receiver-side injection chain. T4–T7 are the sender-side wrappers. T8–T12 are verification.

### 11.1 T1.5 — Plumb last assistant text through `agent_end` lifecycle payload

**Why this task exists.** The receiver-side flow (T1–T3) depends on `handleLifecycle` (netclient/client.go line 838) calling `onAgentEnd(lastText)` (line 856). `handleLifecycle` only invokes `onAgentEnd` when `data.LastText != ""` (client.go line 849). But the current shim's `agent_end` handler (shim.ts line 162-165) sends only `{ cwd, model }` — no `last_text` field. Until T1.5 lands, `onAgentEnd` is never called and every `coms_net_ask` will time out.

**Root cause in code:**

```
shim.ts line 162:  pi.on("agent_end", async (_event, ctx) => {
shim.ts line 163:      const data = { cwd: ctx.cwd ?? process.cwd(), model: ctx.model?.id ?? "" };
shim.ts line 164:      localIpc?.send({ kind: "lifecycle", event: "agent_end", data });
shim.ts line 165:      netIpc?.send({ kind: "lifecycle", event: "agent_end", data });
```

The `ctx` argument here is `ExtensionContext`, but pi passes the `AgentEndEvent` as the first argument (`_event`). Per types.d.ts line 484-487:

```typescript
export interface AgentEndEvent {
    type: "agent_end";
    messages: AgentMessage[];
}
```

The `messages` array is the full conversation message list at turn end. The last assistant message is the model's final reply for this turn.

**Mechanism — extract `last_text` from `AgentEndEvent.messages`:**

Update shim.ts line 162-165 to:
1. Rename `_event` → `event` to use the `AgentEndEvent`.
2. Find the last element of `event.messages` whose role is `"assistant"`.
3. Collect all `text`-type content blocks from that message, join them (models can produce multiple text blocks in one message), and pass the result as `last_text` in the IPC lifecycle payload.

The net IPC payload becomes:

```typescript
const lastMsg   = [...event.messages].reverse().find(m => m.role === "assistant");
const lastText  = lastMsg?.content
  ?.filter((b: any) => b.type === "text")
  ?.map((b: any) => b.text as string)
  ?.join("") ?? "";

const data = {
  cwd:       ctx.cwd ?? process.cwd(),
  model:     ctx.model?.id ?? "",
  last_text: lastText,          // ← new field; drives onAgentEnd
};
netIpc?.send({ kind: "lifecycle", event: "agent_end", data });
```

`localIpc` continues to send the unchanged `{ cwd, model }` payload (local transport has no `onAgentEnd` path and the field would be silently ignored even if sent).

**Go receiver (no change required).** `handleLifecycle` in netclient/client.go line 843-851 already unmarshals `last_text` from `req.Data` and gates on it. Adding the field from shim.ts is sufficient.

**File touched:** `extensions/coms-go/shim.ts` (lines 162-165 only).

**Prerequisite ordering:** T1.5 must land before T1. T1 emits the `"inbound_prompt"` IPC event; T2 wires shim to receive it; T3 fires the `before_agent_start` hook that injects the directive. But none of T1–T3 are testable end-to-end unless T1.5 is in place — without `last_text`, the reply never posts back and the round-trip cannot close.

**Ordering constraints:**
- **T1.5 must land before T1, T2, and T3.** (Hard prerequisite of the entire receiver-side chain.)
- T1 must land before T2 (IPC event must be emitted before shim can receive it).
- T2 must land before T3 (IPC listener must be in place before hook is registered).
- T4 and T5 can be developed in parallel (independent of T1.5 and of each other).
- T6 depends on T5.
- T8–T9 can be developed in parallel with T4–T7.
- T10 depends on T1.5 + T1–T7 all complete.
- T11 depends on T10.
- T12 is a gate after all others.

---

## 12. Test Plan

### Unit tests (shim.ts — manual / Bun test)

- `makeIpc` event handler: given a single `{ "kind": "event", "name": "inbound_prompt", "data": {...} }` line, verify `inboundQueue` has length 1 with the correct entry.
- `makeIpc` event handler: given two successive `"inbound_prompt"` lines (senders A and B), verify `inboundQueue` has length 2 and entries are in FIFO order (A first).
- `before_agent_start` handler: given `inboundQueue` with one entry, verify it returns a `message` whose text begins `"You have 1 pending message:"` and that `inboundQueue` is empty afterward.
- `before_agent_start` handler: given `inboundQueue` with two entries (senders A and B), verify the returned text contains `"[1/2] From A"` before `"[2/2] From B"` and that `inboundQueue` is empty afterward.
- `before_agent_start` handler: given empty `inboundQueue`, verify it returns `{}`.

### Unit tests (Go — `go test ./...`)

All Go tests use table-driven style with unicode checkmarks (Ardan Labs convention per `go_context.md`):

**`internal/localclient/client_test.go`:**
- `TestToolAsk_HappyPath`: mock transport returns a response immediately; verify tool result contains response text.
- `TestToolAsk_Timeout`: mock transport never responds; verify tool returns timeout error within `timeout_ms + 100ms`.
- `TestToolAsk_TargetNotFound`: resolveTarget returns nil; verify error string matches spec.

**`internal/netclient/client_test.go`:**
- `TestToolNetAsk_UnicastHappy`: mock server returns send response + SSE delivers response event; verify tool result.
- `TestToolNetAsk_UnicastTimeout`: mock server queues but never delivers response; verify timeout error.
- `TestToolNetAsk_BroadcastPartial`: 3 peers, 2 respond within deadline, 1 does not; verify `responded: 2`, `timed_out: 1`, `no_response` has one entry.
- `TestToolNetAsk_BroadcastZero`: 3 peers, none respond; verify `responded: 0`, empty `responses`, NOT an error return.
- `TestInboundPromptEvent`: verify `handleInboundPrompt` calls `c.ipcWriter.Event("inbound_prompt", ...)` with correct fields.
- `TestHandleLifecycle_LastText`: verify `handleLifecycle` calls `onAgentEnd` when `last_text` is non-empty, and does NOT call `onAgentEnd` when `last_text` is absent or empty (guards the T1.5 gate at client.go line 849).

### Unit tests (shim.ts — T1.5 coverage)

- `agent_end` handler with a non-empty assistant message: verify the IPC lifecycle frame sent to `netIpc` contains `last_text` equal to the assistant's text content.
- `agent_end` handler with an empty message list: verify `last_text` is `""` (not undefined or missing) so the Go side gracefully no-ops.
- `agent_end` handler with a multi-block assistant message (two `text` blocks): verify `last_text` is the concatenation of both blocks.

### Integration test (tagged `//go:build integration`)

- Start a test `coms-go serve` on a random port.
- Start two `client-net` instances (alpha, beta) connected to it.
- From alpha, call `toolNetAsk` targeting beta.
- Verify that beta's `ipcWriter` emits an `"inbound_prompt"` event with correct `sender_name` and `body`.
- Simulate beta's `agent_end` by sending an IPC lifecycle frame with a non-empty `last_text` value (e.g. `"beta's reply"`).
- Verify `handleLifecycle` (netclient/client.go line 838) invokes `onAgentEnd` — confirmed by the `response_out` audit event appearing in the audit log.
- Verify alpha's `toolNetAsk` unblocks and returns a `content[0].text` that matches `"beta's reply"` (non-empty). **This assertion did not exist in iteration 1 and is the regression guard for the T1.5 defect.**

### Regression

- `(cd extensions/coms-go && go test ./...)` exits 0 — all existing tool handler paths untouched.
- Each of the 8 existing tools is called once in the integration harness and returns an expected result.
- `wc -l extensions/coms-go/shim.ts` is ≤ 300 (up from 181 today; the new registrations and hook add ~60-80 LOC).
- **Concurrent-senders regression:** Two distinct agents (sender-A and sender-B) each call `coms_net_ask` targeting the same receiver within a single `agent_start` window (before any `before_agent_start` fires for that receiver). Both sends are delivered as `"inbound_prompt"` IPC events before the next turn starts. Verify that both sender-A and sender-B receive a non-empty reply (i.e., neither `coms_net_ask` times out and neither returns an empty response string). This guards against the last-wins scalar regression where the first sender's message was silently dropped.

---

## 13. Outcomes (Checkbox List)

- [ ] `agent_end` IPC frame carries non-empty `last_text` from `AgentEndEvent.messages` (T1.5); `TestHandleLifecycle_LastText` passes
- [ ] `coms_ask` registered and callable from `pi -e extensions/coms-go/shim.ts`
- [ ] `coms_net_ask` registered and callable
- [ ] `coms_net_ask` unicast round-trip works end-to-end with two real pi agents
- [ ] Receiver auto-injection: agent B's model sees directive without human input
- [ ] Pi REPL stays live during sender tool-block (interactive-invariant test passed and documented)
- [ ] Broadcast collect-with-deadline returns partial bag (not error) when some/all peers miss deadline
- [ ] Zero-response broadcast returns empty bag (not error)
- [ ] Concurrent-senders regression passes: two senders targeting the same receiver within one agent_start window both receive non-empty replies
- [ ] All 8 existing tools pass `go test ./...` unmodified
- [ ] `shim.ts` ≤ 300 LOC
- [ ] No coms-go server changes (diff of `internal/server/` is empty)
- [ ] `ca()` / `coms.zsh` unchanged
- [ ] `ask_send` audit event appears in `~/.pi/coms-net-log` on `coms_net_ask` calls
- [ ] Interactive-invariant test output pasted to `SPEC/coms_auto_await/REVIEWS/interactive_invariant_test.md`

---

## 14. Open Questions

**OQ-1 — Local transport receiver injection scope.** The spec explicitly excludes auto-injection for `coms_ask` (local/Unix-socket). If the user later wants full parity, the local transport's `handlePrompt` function (in `localclient/handlers.go`) would need to emit the same `"inbound_prompt"` IPC event. This is a one-liner in Go plus a no-change in shim.ts (the same hook applies). Should this be added to scope now? The cost is low; the risk is the untested local injection path.

**OQ-2 — RESOLVED (FIFO queue, drain-all-per-turn).** `inboundQueue` is now a FIFO array. On `before_agent_start`, all pending entries are spliced out and concatenated into one delimited injection. `onAgentEnd` posts `last_text` as the response to every unfulfilled `netInboundCtx` entry in insertion order. See Section 5.4 for the full disambiguation rule.

**OQ-3 — RESOLVED (in §5.6).** Wired post-T11. The original sketch proposed `pi.sendUserMessage("...", { deliverAs: "followUp" })`, but `sendUserMessage` "Always triggers a turn" by appending a USER-role entry — an empty / placeholder body renders as a visible empty user prompt in the REPL, which is confusing UX. Final implementation uses `pi.sendMessage(buildInboundInjection(...), { triggerTurn: true })` instead: the same `customType: "coms_inbound"` payload that `before_agent_start` returns, pushed from the `makeIpc` inbound handler when `lastCtx.isIdle()` is true. No fake user line, identical directive text. See §5.6 for full mechanism; mid-turn the path is a no-op and `before_agent_start` drains on the next turn.

**OQ-4 — Broadcast and auto-injection.** When `coms_net_ask` fans out to N peers, N separate `"inbound_prompt"` events are emitted to each peer's shim. Each peer's `before_agent_start` hook fires independently. This is correct for N distinct pi sessions. Confirm: is there any scenario where multiple peers share the same `shim.ts` process (e.g., a multi-agent pi session)? If yes, the `pendingInjection` variable needs to be keyed by `msg_id`, not a single scalar.

**OQ-5 — `before_agent_start` timing relative to SSE delivery.** If the SSE `prompt` event and the `before_agent_start` fire race (the SSE arrives while the model is already mid-turn), the injection lands on the **next** turn, not the current one. The current turn's reply is submitted by `onAgentEnd` as the response to the inbound prompt even though the model did not see the directive. Is this the desired behavior, or should the injection be deferred entirely until the **first** turn that starts after the SSE delivery?

---

## 15. File Index

All files modified or created by this spec:

| File | Action |
|------|--------|
| `extensions/coms-go/internal/netclient/client.go` | Modified: add `ipcWriter *ipc.Writer` field; emit event in `handleInboundPrompt` |
| `extensions/coms-go/internal/netclient/ask.go` | **New**: `BroadcastResponse`, `PeerResponse`, `PeerIdentity` types |
| `extensions/coms-go/internal/netclient/tools.go` | Modified: add `toolNetAsk` function, dispatch case |
| `extensions/coms-go/internal/localclient/tools.go` | Modified: add `toolAsk` function, dispatch case |
| `extensions/coms-go/shim.ts` | Modified: `agent_end` handler plumbs `last_text` (T1.5, lines 162-165); `makeIpc` event handler (T2); `before_agent_start` hook (T3); two `registerTool` calls (T4, T5) |
| `extensions/coms-go/internal/netclient/client_test.go` | Modified: new test cases per Section 12 |
| `extensions/coms-go/internal/localclient/client_test.go` | Modified: new test cases per Section 12 |
| `SPEC/coms_auto_await/coms_auto_await.md` | This file |
| `SPEC/coms_auto_await/REVIEWS/interactive_invariant_test.md` | Created by implementer after T11 |
