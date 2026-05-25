# coms-go — Workflow

Practical runbook for the coms-net hub server + pi agents. All commands run from the repo root (`pi-vs-claude-code/`).

## 1. Stand up a server

### Local-only (loopback)

```sh
just coms-net-server
```

This auto-kills any stale process on the pinned port, then runs `coms-go serve` bound to `127.0.0.1`. The port is OS-assigned by default unless `PI_COMS_NET_PORT` is set; the actual port (and a fresh bearer token) is written to `~/.pi/coms-net/server.json` for local agents to auto-discover.

Boot output goes to stdout. Stop with `Ctrl-C` — the server unlinks `server.json` and exits cleanly.

### LAN-visible (other hosts can reach it)

```sh
export PI_COMS_NET_AUTH_TOKEN=<a-strong-random-token>
just coms-net-server-lan
```

Same as above but binds `0.0.0.0`. Requires `PI_COMS_NET_AUTH_TOKEN` to be set in the environment — the server uses it as the bearer that all clients must present. Without it, the server refuses to start.

### Direct binary (skip `just`)

```sh
./extensions/coms-go/bin/coms-go-$(go env GOOS)-$(go env GOARCH) serve --help
```

Flags: `--host`, `--port`, `--secret-path`, `--heartbeat-ms`, `--no-color`. Env equivalents: `PI_COMS_NET_HOST`, `PI_COMS_NET_PORT`, `PI_COMS_NET_MAX_INBOX`, `PI_COMS_NET_LOG_QUIET`, `PI_COMS_NET_LOG_HEARTBEAT`.

## 2. Connect a pi agent (and name it)

In a second terminal on the **same host** as the server:

```sh
just coms2 --name planner
```

The agent registers with the server as `planner` in the `default` project namespace. The `coms2` recipe pins the model (claude-opus-4-7); other model shortcuts: `coms1` (gpt-5.5), `coms3` (deepseek-v4-pro), `coms4` (z-ai/glm-5.1). Plain `just coms` is model-agnostic.

The server auto-discovers via `~/.pi/coms-net/server.json` — no `--server-url` needed for local-host agents. The bearer token is read from the same file.

### What `--name` does

`--name planner` becomes the agent's identity on the server. Other agents address messages to `planner`. If you omit `--name`, the agent uses (in order): the `name:` field from its frontmatter, then an auto-generated name.

### Full flag set (via the shim)

| Flag | Purpose |
|---|---|
| `--name` | Identity on the server (other agents address this) |
| `--purpose` | Agent description, shown in discovery |
| `--project` | Project namespace (default `default`); agents only see peers in the same project |
| `--color` | Hex color `#RRGGBB` for UI distinction (quote it: `"#72F1B8"`) |
| `--explicit` | Hide from auto-discovery; only addressable by exact name |
| `--server-url` | Override server URL (otherwise read from `server.json`) |
| `--auth-token` | Override bearer (otherwise read from `server.secret.json`) |

## 3. Open a second agent (same host)

In a third terminal:

```sh
just coms2 --name coder
```

Now `planner` and `coder` are both registered on the same hub. They can:

- Discover each other: `coms_net_list` tool
- Send: `coms_net_send` with `target: "coder"`
- Receive via SSE: `coms_net_await`
- Reply: `coms_net_respond`

Each agent gets a unique session ID server-side; the name is its addressable handle.

Repeat for as many agents as you want — different names, optionally different `--project` namespaces if you want isolated peer groups on the same server.

## 4. How many agents can connect?

**No fixed cap on the number of registered agents.** The server holds them in per-project in-memory maps; scaling is bounded by host RAM and the goroutine cost per SSE stream (one goroutine per connected agent, plus the heartbeat ticker shared across all).

**Per-agent inbox cap: 100 messages** by default. If an agent's inbox depth hits the cap, further `coms_net_send` calls targeting that agent return HTTP 409 with `inbox_full`. Tune via `PI_COMS_NET_MAX_INBOX=<n>` on the server.

**Hop limit per message: configurable**, prevents forwarding loops. Excess returns 409 `hop_limit_exceeded`.

Practical guidance: tens of agents per server is uncontroversial. Hundreds: probably fine but unmeasured — the server is stdlib `net/http` + goroutines, so the ceiling is whatever the host can handle. Thousands: build a benchmark before betting on it.

## 5. Multi-host: agents from other machines

### On the server host

```sh
export PI_COMS_NET_AUTH_TOKEN=<strong-random-token>
just coms-net-server-lan
```

Confirm the bind address and port from the boot banner. Note the host's LAN IP (e.g. `192.168.1.50`). The bearer token in `PI_COMS_NET_AUTH_TOKEN` is what remote clients must present.

### Distribute the bearer

Remote hosts need the same bearer token. Options:

- Share manually (env var on the remote, or written to `~/.pi/coms-net/server.secret.json` on each peer)
- Read it from the server host's `~/.pi/coms-net/server.secret.json` and copy it over a secure channel (ssh, password manager, etc.)

**Never put the bearer in a URL, a query string, a log line, or a committed file.** The Go binary reads it from `server.secret.json` (mode 0600) or `--auth-token` and only ever sends it as an `Authorization: Bearer <token>` header.

### On each remote host

```sh
# Either set the bearer in the env...
export PI_COMS_NET_AUTH_TOKEN=<same-token>

# ...or write it to the local secret file (mode 0600)
mkdir -p ~/.pi/coms-net && \
  printf '{"bearer":"%s"}\n' "$TOKEN" > ~/.pi/coms-net/server.secret.json && \
  chmod 600 ~/.pi/coms-net/server.secret.json

# Then launch the agent pointing at the server
just coms2 --name remote-coder --server-url http://192.168.1.50:<port>
```

The `--server-url` is required for remote agents (they can't read the server host's `server.json`). Once registered, the remote agent participates identically to local ones — same tool surface, same SSE stream, same addressing rules.

### Quick smoke check

From a remote host, before launching pi, confirm reachability:

```sh
curl -sS http://192.168.1.50:<port>/health
```

200 + JSON body = server is reachable. 401/403 = bearer mismatch. Connection refused = bind address (`0.0.0.0`) or firewall problem.

### Project namespaces across hosts

`--project <name>` works the same across hosts — agents in different projects don't see each other even if they share a server. Useful for keeping multiple unrelated agent swarms on one hub.

---

## Quick reference

| Action | Command |
|---|---|
| Start local server | `just coms-net-server` |
| Start LAN server (needs token) | `PI_COMS_NET_AUTH_TOKEN=... just coms-net-server-lan` |
| Connect local agent | `just coms2 --name <agent-name>` |
| Connect remote agent | `just coms2 --name <name> --server-url http://<host>:<port>` |
| Isolate a group of agents | add `--project <name>` to every agent in the group |
| Hide from auto-discovery | add `--explicit` |
| End-to-end self-test | `just test-coms-go-integration` |
