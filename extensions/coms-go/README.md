# coms-go

Native Go replacement for `extensions/coms.ts`, `extensions/coms-net.ts`, and
`scripts/coms-net-server.ts`. Single statically-linked binary, multiple modes.

## Prerequisites

- Go ≥ 1.23
- `CGO_ENABLED=0` for static builds (cross-compile to any target)

## Build

```bash
# Development (current platform)
cd extensions/coms-go
go build -o bin/coms-go ./cmd/coms-go

# Static build (recommended for deployment)
CGO_ENABLED=0 go build -o bin/coms-go ./cmd/coms-go

# Cross-compile for pi hosts
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bin/coms-go-linux-arm64 ./cmd/coms-go
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/coms-go-linux-amd64 ./cmd/coms-go
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o bin/coms-go-darwin-arm64 ./cmd/coms-go
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -o bin/coms-go-darwin-amd64 ./cmd/coms-go
```

## Run

```bash
# Hub server (replaces: bun scripts/coms-net-server.ts)
./bin/coms-go serve

# Local Unix-socket client (replaces: bun extensions/coms.ts)
./bin/coms-go client-local --name planner --purpose "Plans the work" --project default

# Networked SSE client (replaces: bun extensions/coms-net.ts)
./bin/coms-go client-net --name planner --server-url http://127.0.0.1:43219

# Version check
./bin/coms-go version
```

## Subcommands

| Subcommand     | Replaces                          | Status       |
|----------------|-----------------------------------|--------------|
| `serve`        | `scripts/coms-net-server.ts`      | T3 (pending) |
| `client-local` | `extensions/coms.ts`              | T4 (pending) |
| `client-net`   | `extensions/coms-net.ts`          | T4 (pending) |
| `version`      | —                                 | Done         |

## Environment Variables

### Server (`serve`)

| Variable                    | Default           | Description                                      |
|-----------------------------|-------------------|--------------------------------------------------|
| `PI_COMS_NET_HOST`          | `127.0.0.1`       | Bind address                                     |
| `PI_COMS_NET_PORT`          | `0` (random)      | Bind port; 0 = OS-assigned                       |
| `PI_COMS_NET_AUTH_TOKEN`    | (generated)       | Bearer token; generated and written to secret file if unset |
| `PI_COMS_NET_LOG_QUIET`     | (unset)           | Set to `1` to suppress non-error log lines      |
| `PI_COMS_NET_LOG_HEARTBEAT` | (unset)           | Set to `1` to log heartbeat ticks               |
| `PI_COMS_NET_STALE_MS`      | `60000`           | Milliseconds before an agent entry is stale      |
| `PI_COMS_NET_TTL_MS`        | `300000`          | Milliseconds before a message is expired         |
| `PI_COMS_NET_KA_MS`         | `25000`           | SSE keepalive interval in milliseconds           |

### Client-local (`client-local`)

| Variable              | Default    | Description                                         |
|-----------------------|------------|-----------------------------------------------------|
| `PI_COMS_TIMEOUT_MS`  | `1800000`  | Default await timeout (30 min)                      |
| `PI_COMS_SOCKET_DIR`  | `~/.pi/coms/sockets` | Unix socket directory                    |

### Client-net (`client-net`)

| Variable                 | Default    | Description                                      |
|--------------------------|------------|--------------------------------------------------|
| `PI_COMS_NET_SERVER_URL` | (required) | Hub server URL (overridden by `--server-url`)    |
| `PI_COMS_NET_AUTH_TOKEN` | (required) | Bearer token (overridden by `--auth-token`)      |
| `PI_COMS_TIMEOUT_MS`     | `1800000`  | Default await timeout (30 min)                   |

## Test

```bash
cd extensions/coms-go
go test ./...
go vet ./...
```

## Pi Extension

Pi loads this extension via the shim:

```bash
pi -e extensions/coms-go/shim.ts
```

The shim spawns `coms-go client-local` and `coms-go client-net` as child processes
and bridges tool calls over JSON-line IPC. The `manifest.json` declares the platform
binary paths for each supported target.

## Module Layout

```
extensions/coms-go/
├── cmd/coms-go/main.go       CLI dispatcher
├── internal/
│   ├── proto/                Shared wire types (Envelope, AgentCard, etc.)
│   ├── util/                 ULID, hex color, frontmatter parser, fallback palette
│   ├── audit/                JSONL append-only audit log writer
│   ├── registry/             Local agent registry I/O (~/.pi/coms/...)
│   ├── transport/            Unix-socket bind/send/probe
│   ├── localclient/          client-local subcommand implementation
│   ├── netclient/            client-net subcommand implementation
│   ├── server/               serve subcommand implementation
│   └── ipc/                  JSON-line stdio IPC between shim.ts and coms-go
├── manifest.json             Pi extension manifest
└── shim.ts                   Thin TS bridge (pi extension entry point)
```
