# Spec Surgical-Edit Feedback — coms_auto_await iter-2 (for golang-coder)

VERDICT: PASS WITH WARNINGS — one substantive fix to apply, then exit loop.

## What to change

### Address: receiver-side FIFO under concurrent inbound asks

Codex iter-2 finding (verbatim, the only one in scope):

> [P2] Queue inbound injections instead of overwriting — coms_auto_await.md:363-364
> When two prompts arrive for the same receiver before its next `before_agent_start`, this last-wins scalar drops the first prompt from the model-visible injection, and the current `onAgentEnd` implementation only submits one arbitrary unfulfilled `inboundQueue` entry per turn rather than responding to all pending messages. That leaves at least one sender's `coms_net_ask` waiting until timeout under concurrent asks to the same agent, so the spec needs a FIFO queue or an explicit single-in-flight limitation.

**Required spec change (surgical, in-place):**

Around lines 363-364 (where the `pendingInjection` scalar is described), replace the last-wins scalar contract with **FIFO queue semantics**. Specifically:

1. The receiver shim's pending-injection store becomes a FIFO of `{msg_id, sender, body}` entries (the spec already calls this `inboundQueue` in `onAgentEnd` discussion — unify naming so it's the same structure throughout).
2. On `before_agent_start`, if the queue is non-empty, the shim concatenates ALL pending entries into a single injection payload (clearly delimited per-message: e.g. "You have N pending messages:\n\n[1/N] From sender-A (msg <id-A>): …\n\n[2/N] From sender-B (msg <id-B>): …\n\nRespond to each via `coms_net_respond`.") so the model sees every pending ask before its turn.
3. On `onAgentEnd`, instead of draining one arbitrary entry, the shim must close out **every** queued ask whose response is now available in `last_text`. Define how it disambiguates: simplest contract is "the model's reply is parsed/assigned to the oldest unanswered queued message" or "the model is told to respond per-message and shim parses delimiters." Pick the simpler one (single-reply-to-oldest is acceptable as a v1 if combined with the explicit instruction in the injection that the model SHOULD address all N — the spec should commit to one).
4. Update **OQ-2** to RESOLVED (FIFO queue, drain-all-per-turn) and remove it from the open questions list.
5. Update the test plan to add a regression: two senders broadcasting to the same receiver within a single agent_start window — both must receive non-empty replies.

### Do NOT change

- Do not touch the Non-goals section (WORKFLOW.md slash invocation is already out-of-scope per iter-1 — that's why Codex's Finding 1 in iter-2 is a non-issue for this spec).
- Do not re-architect T1.5 or T1-T3. The fix is local to the queue data structure and the injection/drain semantics.
- Do not add new tasks unless the FIFO change crosses a meaningful task boundary. Prefer updating the existing T1/T2 task descriptions in-place.

## After you finish

Return a 4-line summary:
- Which line range you edited.
- The exact disambiguation rule you committed to (drain-all-per-turn with which response-to-message assignment).
- Confirmation that OQ-2 was removed from open questions.
- Any constraint you had to flag.
