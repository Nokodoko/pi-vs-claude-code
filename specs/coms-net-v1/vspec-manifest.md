# vspec Manifest — coms-net-v1

**Source**: `specs/coms-net-v1.md`
**Updated**: 2026-05-07T09:55:23
**Output directory**: `specs/coms-net-v1`
**Image count**: 18

## 00. Whole plan

- **Slug**: `hero`
- **File**: `00-hero.png`
- **Status**: success
- **Labels (8):** `Pi agents`, `HTTP hub`, `SSE stream`, `LAN`, `Remote URL`, `Auth`, `Messages`, `Pool widget`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: A high-level one-shot architecture map: multiple Pi agent terminals around a central Bun HTTP/SSE hub, arrows for HTTP JSON requests toward the hub and SSE event streams back out, with LAN and remote cloud boundary shown as two network zones.
Composition: structured technical diagram with strong hierarchy, meaningful arrows, labeled nodes, balanced whitespace, and no decorative illustration.
Visual elements: architecture boxes, protocol arrows, state badges, data cards, network boundaries, and minimal abstract glyphs where useful.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for success/live, yellow #FEDE5D for warnings/security.
Exact text labels, complete set, no other words: "Pi agents", "HTTP hub", "SSE stream", "LAN", "Remote URL", "Auth", "Messages", "Pool widget".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```

## 01. 1. Purpose

- **Slug**: `purpose`
- **File**: `01-purpose.png`
- **Status**: success
- **Labels (8):** `Old coms`, `Unix sockets`, `Bun hub`, `HTTP JSON`, `SSE events`, `LAN`, `Remote`, `Anywhere`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: Before/after transformation diagram: left side local socket mesh, center migration arrow, right side central Bun hub connecting agents across LAN and remote URL.
Composition: structured technical diagram with strong hierarchy, meaningful arrows, labeled nodes, balanced whitespace, and no decorative illustration.
Visual elements: architecture boxes, protocol arrows, state badges, data cards, network boundaries, and minimal abstract glyphs where useful.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for success/live, yellow #FEDE5D for warnings/security.
Exact text labels, complete set, no other words: "Old coms", "Unix sockets", "Bun hub", "HTTP JSON", "SSE events", "LAN", "Remote", "Anywhere".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```

## 02. 2. Design goals

- **Slug**: `design-goals`
- **File**: `02-design-goals.png`
- **Status**: success
- **Labels (8):** `Server hub`, `Local first`, `LAN ready`, `Remote URL`, `SSE push`, `HTTP JSON`, `Auth`, `Simple Bun`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: Radial design-principles map with central coms-net node and eight goal spokes grouped by transport, reach, safety, and simplicity.
Composition: structured technical diagram with strong hierarchy, meaningful arrows, labeled nodes, balanced whitespace, and no decorative illustration.
Visual elements: architecture boxes, protocol arrows, state badges, data cards, network boundaries, and minimal abstract glyphs where useful.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for success/live, yellow #FEDE5D for warnings/security.
Exact text labels, complete set, no other words: "Server hub", "Local first", "LAN ready", "Remote URL", "SSE push", "HTTP JSON", "Auth", "Simple Bun".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```

## 03. 5. Deployment modes

- **Slug**: `deployment-modes`
- **File**: `03-deployment-modes.png`
- **Status**: success
- **Labels (8):** `Auto local`, `Manual local`, `LAN mode`, `Remote HTTPS`, `Server URL`, `Auth token`, `Agents`, `Hub`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: Four-panel deployment topology showing the same agents connecting to hub in local auto, local manual, LAN, and remote HTTPS configurations.
Composition: structured technical diagram with strong hierarchy, meaningful arrows, labeled nodes, balanced whitespace, and no decorative illustration.
Visual elements: architecture boxes, protocol arrows, state badges, data cards, network boundaries, and minimal abstract glyphs where useful.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for success/live, yellow #FEDE5D for warnings/security.
Exact text labels, complete set, no other words: "Auto local", "Manual local", "LAN mode", "Remote HTTPS", "Server URL", "Auth token", "Agents", "Hub".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```

## 04. 6. Local port claiming and server discovery

- **Slug**: `port-claiming`
- **File**: `04-port-claiming.png`
- **Status**: success
- **Labels (8):** `Start server`, `Bun serve`, `Port 0`, `Open port`, `Print URL`, `server.json`, `Agents connect`, `No lock`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: Show simplified local port claiming for coms-net: the user starts one explicit Bun server process, Bun/OS claims an open port using port 0, the server prints and records the URL, then Pi agents connect to it. Emphasize that there is no lock directory, no hidden auto-spawn, and no port scanning.
Composition: left-to-right lifecycle flow with four main stages: explicit start, atomic bind, URL discovery, agent connection. Put a small crossed-out side badge for lock/auto-spawn complexity.
Visual elements: terminal command block, Bun server box, port badge, server registry card, arrows to multiple Pi agent terminals, clean status check marks.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for successful port claim, yellow #FEDE5D for caution/no-lock badge.
Exact text labels, complete set, no other words: "Start server", "Bun serve", "Port 0", "Open port", "Print URL", "server.json", "Agents connect", "No lock".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```

## 05. 7. Protocol choice

- **Slug**: `protocol-choice`
- **File**: `05-protocol-choice.png`
- **Status**: success
- **Labels (8):** `HTTP JSON`, `SSE`, `Register`, `Send`, `Await`, `Prompt event`, `Response event`, `Reconnect`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: Protocol split diagram with client-to-server HTTP JSON lane and server-to-agent SSE lane, showing request endpoints and pushed event types.
Composition: structured technical diagram with strong hierarchy, meaningful arrows, labeled nodes, balanced whitespace, and no decorative illustration.
Visual elements: architecture boxes, protocol arrows, state badges, data cards, network boundaries, and minimal abstract glyphs where useful.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for success/live, yellow #FEDE5D for warnings/security.
Exact text labels, complete set, no other words: "HTTP JSON", "SSE", "Register", "Send", "Await", "Prompt event", "Response event", "Reconnect".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```

## 06. 8. Agent identity

- **Slug**: `agent-identity`
- **File**: `06-agent-identity.png`
- **Status**: success
- **Labels (8):** `CLI flags`, `Frontmatter`, `Defaults`, `session_id`, `Name`, `Project`, `Color`, `Status`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: Identity resolution stack feeding an AgentCard object, with session_id as authoritative key and name as human alias.
Composition: structured technical diagram with strong hierarchy, meaningful arrows, labeled nodes, balanced whitespace, and no decorative illustration.
Visual elements: architecture boxes, protocol arrows, state badges, data cards, network boundaries, and minimal abstract glyphs where useful.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for success/live, yellow #FEDE5D for warnings/security.
Exact text labels, complete set, no other words: "CLI flags", "Frontmatter", "Defaults", "session_id", "Name", "Project", "Color", "Status".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```

## 07. 9. HTTP API

- **Slug**: `http-api`
- **File**: `07-http-api.png`
- **Status**: success
- **Labels (9):** `/health`, `/register`, `/events`, `/heartbeat`, `/agents`, `/messages`, `/await`, `/response`, `DELETE`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: Endpoint surface map arranged by lifecycle: health, registration, stream, heartbeat, listing, messaging, awaiting, response, unregister.
Composition: structured technical diagram with strong hierarchy, meaningful arrows, labeled nodes, balanced whitespace, and no decorative illustration.
Visual elements: architecture boxes, protocol arrows, state badges, data cards, network boundaries, and minimal abstract glyphs where useful.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for success/live, yellow #FEDE5D for warnings/security.
Exact text labels, complete set, no other words: "/health", "/register", "/events", "/heartbeat", "/agents", "/messages", "/await", "/response", "DELETE".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```

## 08. 10. Message model

- **Slug**: `message-model`
- **File**: `08-message-model.png`
- **Status**: success
- **Labels (8):** `Message`, `Sender`, `Target`, `Prompt`, `Status`, `Response`, `TTL`, `Hops`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: Data model card diagram for ComsMessage showing identity fields, payload fields, status lifecycle, expiry, and hop protection.
Composition: structured technical diagram with strong hierarchy, meaningful arrows, labeled nodes, balanced whitespace, and no decorative illustration.
Visual elements: architecture boxes, protocol arrows, state badges, data cards, network boundaries, and minimal abstract glyphs where useful.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for success/live, yellow #FEDE5D for warnings/security.
Exact text labels, complete set, no other words: "Message", "Sender", "Target", "Prompt", "Status", "Response", "TTL", "Hops".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```

## 09. 11. SSE payloads

- **Slug**: `sse-payloads`
- **File**: `09-sse-payloads.png`
- **Status**: success
- **Labels (8):** `Snapshot`, `Joined`, `Updated`, `Left`, `Prompt`, `Response`, `Status`, `Ping`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: SSE event taxonomy fan-out from server stream to agents, grouped into pool events, message events, and keepalive events.
Composition: structured technical diagram with strong hierarchy, meaningful arrows, labeled nodes, balanced whitespace, and no decorative illustration.
Visual elements: architecture boxes, protocol arrows, state badges, data cards, network boundaries, and minimal abstract glyphs where useful.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for success/live, yellow #FEDE5D for warnings/security.
Exact text labels, complete set, no other words: "Snapshot", "Joined", "Updated", "Left", "Prompt", "Response", "Status", "Ping".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```

## 10. 12. Pi extension client lifecycle

- **Slug**: `client-lifecycle`
- **File**: `10-client-lifecycle.png`
- **Status**: success
- **Labels (8):** `session_start`, `Register`, `Open SSE`, `Heartbeat`, `Prompt follow-up`, `agent_end`, `Post response`, `Shutdown`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: Client lifecycle timeline from Pi session_start through registration, SSE loop, prompt injection, response capture, and unregister shutdown.
Composition: structured technical diagram with strong hierarchy, meaningful arrows, labeled nodes, balanced whitespace, and no decorative illustration.
Visual elements: architecture boxes, protocol arrows, state badges, data cards, network boundaries, and minimal abstract glyphs where useful.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for success/live, yellow #FEDE5D for warnings/security.
Exact text labels, complete set, no other words: "session_start", "Register", "Open SSE", "Heartbeat", "Prompt follow-up", "agent_end", "Post response", "Shutdown".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```

## 11. 13. Preserved tool surface

- **Slug**: `tool-surface`
- **File**: `11-tool-surface.png`
- **Status**: success
- **Labels (8):** `coms_list`, `coms_send`, `coms_get`, `coms_await`, `/coms`, `HTTP API`, `SSE cache`, `Agent tools`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: Tool mapping diagram: Pi tools on left mapped to HTTP endpoints and SSE cache on right, showing same user-facing capability preserved.
Composition: structured technical diagram with strong hierarchy, meaningful arrows, labeled nodes, balanced whitespace, and no decorative illustration.
Visual elements: architecture boxes, protocol arrows, state badges, data cards, network boundaries, and minimal abstract glyphs where useful.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for success/live, yellow #FEDE5D for warnings/security.
Exact text labels, complete set, no other words: "coms_list", "coms_send", "coms_get", "coms_await", "/coms", "HTTP API", "SSE cache", "Agent tools".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```

## 12. 14. Pool widget behavior

- **Slug**: `pool-widget`
- **File**: `12-pool-widget.png`
- **Status**: success
- **Labels (8):** `Pool widget`, `Agent row`, `Color dot`, `Context bar`, `Online`, `Stale`, `Project`, `Server URL`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: Terminal UI mockup of stacked below-editor pool widget with rows, colored dots, context bars, online/stale status, and server URL header.
Composition: structured technical diagram with strong hierarchy, meaningful arrows, labeled nodes, balanced whitespace, and no decorative illustration.
Visual elements: architecture boxes, protocol arrows, state badges, data cards, network boundaries, and minimal abstract glyphs where useful.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for success/live, yellow #FEDE5D for warnings/security.
Exact text labels, complete set, no other words: "Pool widget", "Agent row", "Color dot", "Context bar", "Online", "Stale", "Project", "Server URL".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```

## 13. 15. Server internals

- **Slug**: `server-internals`
- **File**: `13-server-internals.png`
- **Status**: success
- **Labels (8):** `ServerState`, `Projects`, `Agents`, `Streams`, `Messages`, `Awaiters`, `Stale scan`, `TTL cleanup`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: Internal state architecture diagram of Bun hub maps and cleanup loops: projects contain agents, streams, messages, awaiters.
Composition: structured technical diagram with strong hierarchy, meaningful arrows, labeled nodes, balanced whitespace, and no decorative illustration.
Visual elements: architecture boxes, protocol arrows, state badges, data cards, network boundaries, and minimal abstract glyphs where useful.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for success/live, yellow #FEDE5D for warnings/security.
Exact text labels, complete set, no other words: "ServerState", "Projects", "Agents", "Streams", "Messages", "Awaiters", "Stale scan", "TTL cleanup".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```

## 14. 16. Security model

- **Slug**: `security-model`
- **File**: `14-security-model.png`
- **Status**: success
- **Labels (8):** `Bearer token`, `127.0.0.1`, `LAN bind`, `HTTPS`, `No CORS`, `Prompt origin`, `Audit log`, `Trust boundary`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: Security boundary diagram showing auth gate around server, safe local default, explicit LAN/remote exposure, prompt trust boundary, and audit logging.
Composition: structured technical diagram with strong hierarchy, meaningful arrows, labeled nodes, balanced whitespace, and no decorative illustration.
Visual elements: architecture boxes, protocol arrows, state badges, data cards, network boundaries, and minimal abstract glyphs where useful.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for success/live, yellow #FEDE5D for warnings/security.
Exact text labels, complete set, no other words: "Bearer token", "127.0.0.1", "LAN bind", "HTTPS", "No CORS", "Prompt origin", "Audit log", "Trust boundary".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```

## 15. 17. Differences from socket coms v1

- **Slug**: `differences`
- **File**: `15-differences.png`
- **Status**: success
- **Labels (8):** `Socket coms`, `Registry files`, `HTTP hub`, `SSE joins`, `Remote support`, `Heartbeat`, `Server routing`, `Same tools`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: Side-by-side migration matrix visual contrasting old socket peer-to-peer with new central HTTP/SSE hub while preserving tools.
Composition: structured technical diagram with strong hierarchy, meaningful arrows, labeled nodes, balanced whitespace, and no decorative illustration.
Visual elements: architecture boxes, protocol arrows, state badges, data cards, network boundaries, and minimal abstract glyphs where useful.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for success/live, yellow #FEDE5D for warnings/security.
Exact text labels, complete set, no other words: "Socket coms", "Registry files", "HTTP hub", "SSE joins", "Remote support", "Heartbeat", "Server routing", "Same tools".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```

## 16. 18. Implementation plan

- **Slug**: `implementation-plan`
- **File**: `16-implementation-plan.png`
- **Status**: success
- **Labels (8):** `Protocol`, `Server`, `Client`, `Tests`, `Docs`, `Bun hub`, `Pi extension`, `Release`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: Five-phase roadmap timeline from protocol finalization to Bun server, Pi client, testing, and documentation.
Composition: structured technical diagram with strong hierarchy, meaningful arrows, labeled nodes, balanced whitespace, and no decorative illustration.
Visual elements: architecture boxes, protocol arrows, state badges, data cards, network boundaries, and minimal abstract glyphs where useful.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for success/live, yellow #FEDE5D for warnings/security.
Exact text labels, complete set, no other words: "Protocol", "Server", "Client", "Tests", "Docs", "Bun hub", "Pi extension", "Release".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```

## 17. 19. Acceptance criteria for finished future implementation

- **Slug**: `acceptance`
- **File**: `17-acceptance.png`
- **Status**: success
- **Labels (8):** `Port binds`, `Auth works`, `SSE joins`, `Tools work`, `Responses return`, `No sockets`, `LAN pass`, `Remote pass`

```text
Create a 16:9 dense information-rich diagram for a technical plan/spec.
Visual register: clean vector technical architecture diagram, dark navy documentation aesthetic, crisp thin lines, high contrast cyan pink green yellow accents, dense but readable, no decorative filler.
Purpose: Acceptance dashboard with check tiles for server, security, SSE, tools, response routing, transport replacement, LAN, and remote validation.
Composition: structured technical diagram with strong hierarchy, meaningful arrows, labeled nodes, balanced whitespace, and no decorative illustration.
Visual elements: architecture boxes, protocol arrows, state badges, data cards, network boundaries, and minimal abstract glyphs where useful.
Color system: dark navy #07111F background, off-white #F8FAFC lines/text, cyan #36F9F6 for transport, pink #FF7EDB for agents, green #72F1B8 for success/live, yellow #FEDE5D for warnings/security.
Exact text labels, complete set, no other words: "Port binds", "Auth works", "SSE joins", "Tools work", "Responses return", "No sockets", "LAN pass", "Remote pass".
Text rules: no extra text, no paragraphs, no captions, no lorem ipsum, maximum 10 labels total.
Output role: technical spec illustration embedded in markdown.
```
