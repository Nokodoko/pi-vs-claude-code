VERDICT: FAIL

# Codex Review — coms_auto_await (iteration 1)

**Scope**: working-tree
**Codex thread**: 019e5d33-956f-7721-aa93-9a4f13555e72
**Date**: 2026-05-24

## Verdict rationale

Codex returned two P2 findings. Strict keyword heuristic (looks for "critical", "must fix", "blocker", "incorrect", "missing") would map this to **PASS WITH WARNINGS**.

Overriding to **FAIL** because Finding 1 is a substantive correctness defect: the spec's receiver-side flow (T1–T3) depends on the `agent_end` lifecycle event carrying the model's reply, but the existing shim's `agent_end` payload only contains `cwd` and `model`. The netclient's `handleLifecycle` only invokes `onAgentEnd` when `data.last_text` is present, so as written the receiver's reply is never posted back and `coms_net_ask` times out on every call. This is not a stylistic nit — it's a guaranteed implementation failure that wastes an entire execution cycle.

Finding 2 (WORKFLOW.md underscore slash commands) is unrelated to the coms_auto_await spec and concerns pre-existing WORKFLOW.md drift. Not blocking; flagged for a separate cleanup.

## Verbatim Codex output

```
# Codex Review

Target: working tree diff

The changes add documentation/spec guidance that is inconsistent with the current extension contract and would lead either users or implementers into non-working flows.

Full review comments:

- [P2] Add a sender for last_text before relying on agent_end — /home/n0ko/Programs/ai/pi-vs-claude-code/SPEC/coms_auto_await/coms_auto_await.md:62-62
  This desired flow depends on `agent_end` submitting B's reply, but the current shim's `agent_end` lifecycle payload only includes `cwd` and `model`, while `netclient.handleLifecycle` calls `onAgentEnd` only when `data.last_text` is present. If implementers follow T1–T3 as written, the receiver can get the injected prompt but its reply is never posted back, so `coms_net_ask` will time out. The spec should include passing the last assistant text from `shim.ts` (or another explicit response path) as a required task.

- [P2] Do not document tools as hidden slash commands — /home/n0ko/Programs/ai/pi-vs-claude-code/extensions/coms-go/WORKFLOW.md:87-87
  The shim only registers `/coms` and `/coms-net` via `pi.registerCommand`; the underscore names are registered with `pi.registerTool`, not as commands. In environments where slash dispatch is limited to registered commands, following this documented workaround (`/coms_net_send ...`) will fail even though asking the model to call the tool would work. Either register these command aliases or remove this invocation path from the workflow.
```
