# Spec Rebuild Feedback — coms_auto_await (for spec-builder / golang-coder)

VERDICT: FAIL — one critical correctness defect, one unrelated finding.

## CRITICAL — must address before re-review

### Defect 1: `agent_end` payload does not carry `last_text` today

The current spec (sections describing T1–T3, around line 62) assumes the receiver-side flow is:

1. shim.ts receives an incoming message → injects "you have a pending message" into model B
2. Model B answers normally
3. The `agent_end` lifecycle event carries B's answer back to shim.ts
4. shim.ts posts the answer via `coms_net_respond`, satisfying the sender's `coms_net_ask` await

But the existing shim's `agent_end` payload only includes `cwd` and `model`. The `last_text` field is what `netclient.handleLifecycle` uses to call `onAgentEnd` — when it's absent, the onAgentEnd path never fires. So the receiver's reply is never posted back, and every `coms_net_ask` call will time out at the deadline. The spec as written would ship a guaranteed-broken implementation.

**Required revision**: Add an explicit task (suggest **T1.5** so it slots before the receiver injection chain) titled something like *"Plumb last assistant text through `agent_end` lifecycle payload"*. The task must:

- Identify the pi-side or shim-side hook that has access to the final assistant message of a turn.
- Define how `last_text` gets attached to the `agent_end` event payload (whether shim.ts captures it from a `before_agent_end` hook, or pi emits it natively, or another mechanism — golang-coder, propose the cleanest option after reading the current `agent_end` emission path).
- Cite the relevant code locations (likely `extensions/coms-go/shim.ts` agent_end emission + `extensions/coms-go/internal/netclient/client.go` handleLifecycle around lines 820-875 per Codex's read).
- Be marked a hard prerequisite for T1–T3.

**Alternative acceptable revision**: Define a different explicit response path (e.g., shim.ts auto-calls `coms_net_respond` from a different lifecycle hook that does have the assistant text) and re-thread T1–T3 around it. Either solution is fine as long as the receiver-side actually closes the loop.

Also revise the **Test plan** section to add a regression test that proves an inbound `coms_net_ask` actually receives a non-empty reply payload (today's plan would silently let this defect slip through).

Also revise **Open questions** — OQ-3 should be promoted or merged into this fix since it touches the same pi API surface.

## UNRELATED — do not address in this spec

### Finding 2: WORKFLOW.md documents non-functional underscore slash commands

This concerns existing drift in `extensions/coms-go/WORKFLOW.md:87` predating the coms_auto_await work. The shim registers `/coms` and `/coms-net` as commands but `coms_net_send` etc. only as tools, so the documented `/coms_net_send ...` invocation form fails in slash-dispatch contexts.

Not in scope for coms_auto_await. Note in the spec's **Non-goals** section that WORKFLOW.md slash-command alignment is explicitly out of scope and should be handled in a separate commit. Do not attempt to fix it here.

## Style / mechanics

- Keep the existing section structure intact; only insert the new task and update affected sections (T-list, test plan, open questions, non-goals).
- Keep the existing 12 task IDs stable. Add new task as **T1.5** (preferred) or renumber to T13 at the end with a forward-reference if renumbering is too disruptive.
- Match the citation style already in the spec (file:line).
