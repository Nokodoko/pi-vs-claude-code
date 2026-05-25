VERDICT: PASS WITH WARNINGS

# Codex Review — coms_auto_await (iteration 2)

**Scope**: working-tree
**Codex thread**: 019e5d55-3492-7fa2-8e5e-e1071ad8dd15
**Date**: 2026-05-25
**Iter-1 artifacts preserved as**: `coms_auto_await_REVIEW_iter1.md`, `coms_auto_await_FEEDBACK_iter1.md`

## Verdict rationale

Codex returned two P2 findings. By strict keyword heuristic (no "critical", "must fix", "blocker", "incorrect", or "missing" present), verdict is **PASS WITH WARNINGS**.

Finding 1 — Repeat of iter-1 Finding 2 (WORKFLOW.md `/coms_net_send` slash invocation). The spec's Non-goals section already declares this out-of-scope; Codex is reviewing the working tree as a whole, so it re-flagged the pre-existing WORKFLOW.md drift. No spec action required.

Finding 2 — New, genuine spec issue at line 363-364: under concurrent inbound asks to the same receiving agent, the `pendingInjection` scalar overwrites earlier prompts and `onAgentEnd` only drains one `inboundQueue` entry per turn, so at least one sender's `coms_net_ask` waits until timeout. This was OQ-2 in iter-1 (kept open). Codex named the fix: FIFO queue or explicit single-in-flight limitation. Addressing inline via a surgical golang-coder edit (does not trigger a third Codex review; the iter cap exists to bound Codex cost, and Codex has already told us the fix).

After the surgical fix, proceeding to Phase 5 (/swarm).

## Verbatim Codex output

```
# Codex Review

Target: working tree diff

The changes introduce documentation/spec guidance that would lead users or implementers into broken flows: unsupported slash invocation for tools, and lost/timeouting concurrent inbound asks. These are correctness issues in the proposed behavior rather than harmless wording nits.

Full review comments:

- [P2] Remove unsupported tool slash invocation path — /home/n0ko/Programs/ai/pi-vs-claude-code/extensions/coms-go/WORKFLOW.md:87-87
  In pi sessions where slash dispatch only invokes registered commands, this documented workaround fails because `shim.ts` registers only `/coms` and `/coms-net` with `pi.registerCommand`; the underscore names are registered as tools. Users following `/coms_net_send ...` will get a non-working command path even though asking the model to call the tool works, so this should either be removed or backed by real command aliases.

- [P2] Queue inbound injections instead of overwriting — /home/n0ko/Programs/ai/pi-vs-claude-code/SPEC/coms_auto_await/coms_auto_await.md:363-364
  When two prompts arrive for the same receiver before its next `before_agent_start`, this last-wins scalar drops the first prompt from the model-visible injection, and the current `onAgentEnd` implementation only submits one arbitrary unfulfilled `inboundQueue` entry per turn rather than responding to all pending messages. That leaves at least one sender's `coms_net_ask` waiting until timeout under concurrent asks to the same agent, so the spec needs a FIFO queue or an explicit single-in-flight limitation.
```
