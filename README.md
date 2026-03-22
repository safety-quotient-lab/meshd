# meshd

Event-driven mesh daemon for the safety-quotient interagent mesh.

Serves HTTP API endpoints, manages event-driven spawns, communicates
with peers via ZMQ, and provides SSE for real-time dashboard updates.

## Build

```bash
go build -o meshd ./cmd/meshd/
```

## Run

```bash
./meshd --port 8081 --project-root /path/to/agent --agent-id agent-name
```

## Flags

| Flag | Description |
|------|-------------|
| `--port` | HTTP port (override MESHD_PORT from .dev.vars) |
| `--project-root` | Path to agent project root |
| `--agent-id` | Agent identity within the mesh |
| `--zmq-pub` | ZMQ PUB bind address |
| `--zmq-peers` | ZMQ peer addresses (comma-separated key=value) |
| `--config` | Path to .dev.vars config file |
| `--log-level` | Override LOG_LEVEL (debug/info/warn/error) |

## Ownership

Meshd runs as a standalone service for the safety-quotient agent mesh.
Originally factored from the dissolved operations-agent repository.
