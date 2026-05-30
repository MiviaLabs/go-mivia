# Local Development Runbook

Status: Bootstrap
Date: 2026-05-30
Classification: Internal; no PII

## Prerequisites

- Go `1.26.3`
- `make`
- `curl`
- Optional: `gitleaks`
- Optional gated native path: LadybugDB native libraries through `scripts/ladybug-libs.sh`

## Verify

```sh
go version
go mod tidy
go test ./...
make check
make secret-scan
```

`make secret-scan` skips locally when `gitleaks` is not installed. CI runs secret scanning through the configured GitHub Action.

## Run Agent Server

```sh
MIVIA_HTTP_ADDR=127.0.0.1:8080 \
MIVIA_SQLITE_PATH=:memory: \
go run ./cmd/agent-server
```

Default bind is localhost-only. Do not bind to `0.0.0.0` or a public interface until authn/authz, origin policy, rate limits, and audit logging are approved.

## Optional Local Project Config

Project config is local-only and intended for engineer local computers. Copy the committed example and replace placeholder paths:

```sh
cp configs/agent-server.example.toml configs/agent-server.local.toml
```

Start with an explicit config path:

```sh
MIVIA_CONFIG_PATH=configs/agent-server.local.toml go run ./cmd/agent-server
```

`MIVIA_CONFIG_PATH` is fatal when it points to a missing or invalid file. If it is unset and `configs/agent-server.local.toml` is absent, the server starts with environment-only defaults and an empty project list.

Do not put secrets, tokens, PII, raw prompts, raw source content, provider payloads, or personal data in local config. Local configs are ignored and must not be committed.

For a longer-running WSL process launched from Windows, build a binary first:

```powershell
wsl -d Ubuntu --cd /home/mac/mivialabs/mivialabs-agents-monorepo env PATH=/home/mac/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin go build -o /tmp/mivialabs-agent-server ./cmd/agent-server
wsl -d Ubuntu --cd /home/mac/mivialabs/mivialabs-agents-monorepo env MIVIA_HTTP_ADDR=127.0.0.1:8080 MIVIA_SQLITE_PATH=:memory: /tmp/mivialabs-agent-server
```

Keep that terminal open while testing. If you need a detached process, launch `wsl.exe` from Windows process management and redirect logs to a local temp file.

## REST Smoke

For a short explanation of when to use REST, MCP, Serena, or shell, see the [agent context server guide](../agent-context-guide.md).

```sh
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS -H 'Content-Type: application/json' \
  -d '{"title":"local smoke"}' \
  http://127.0.0.1:8080/api/v1/tasks
curl -fsS http://127.0.0.1:8080/api/v1/projects
```

Manual project metadata digest, after configuring an enabled local project:

```sh
curl -fsS -X POST http://127.0.0.1:8080/api/v1/projects/example-service/digest-runs
```

Digest runs are metadata-only. They store relative path, extension/language hint, file size, mtime, and metadata fingerprints; they do not store raw source content or file-content hashes.

Manual content graph ingestion, after enabling `ingestion.content_graph_enabled = true` and setting the project to `digest_mode = "content_graph"`:

```sh
curl -fsS -X POST http://127.0.0.1:8080/api/v1/projects/example-service/ingestion-runs
curl -fsS http://127.0.0.1:8080/api/v1/projects/example-service/files
curl -fsS 'http://127.0.0.1:8080/api/v1/projects/example-service/symbols?page_size=25'
```

Chunk reads require stable opaque IDs from the file list response:

```sh
curl -fsS 'http://127.0.0.1:8080/api/v1/projects/example-service/files/<file_id>/chunks?page_size=10&max_chunk_bytes=4096'
```

Content graph responses are bounded and must not include absolute roots, skipped sensitive content, matched sensitive text, secrets, PII, raw prompts, provider payloads, or raw database query results.

## MCP Smoke

```sh
curl -fsS \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-06-18' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize"}' \
  http://127.0.0.1:8080/mcp
```

Tool discovery:

```sh
curl -fsS \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-06-18' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  http://127.0.0.1:8080/mcp
```

Task tool call:

```sh
curl -fsS \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-06-18' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tasks.create","arguments":{"title":"MCP smoke"}}}' \
  http://127.0.0.1:8080/mcp
```

Project tool call:

```sh
curl -fsS \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-06-18' \
  -d '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"projects.list","arguments":{}}}' \
  http://127.0.0.1:8080/mcp
```

## Codex Desktop MCP Setup

Register the running server:

```powershell
codex mcp add mivialabs-agent-server --url http://127.0.0.1:8080/mcp
codex mcp get mivialabs-agent-server
```

After registration, new Codex Desktop sessions can discover these tools:

- `tasks.create`
- `tasks.get`
- `research_runs.create`
- `research_runs.get`
- `research_sources.create`
- `research_sources.get`
- `projects.list`
- `projects.get`
- `projects.digest`
- `projects.ingest`
- `projects.ingestion_status`
- `projects.files.list`
- `projects.files.get`
- `projects.file.chunks`
- `projects.symbols.list`
- `projects.headings.list`
- `projects.file.outline`

Codex may expose underscore-normalized callable names such as `tasks_create`, `projects_digest`, or `projects_ingest`; the server accepts both dotted MCP tool names and underscore aliases.

## Live Project Updates

Live updates are disabled unless both gates are enabled:

```toml
[ingestion]
content_graph_enabled = true
live_updates_enabled = true
debounce_interval = "2s"
queue_depth = 128
worker_count = 2
initial_scan_on_start = false

[[projects]]
digest_mode = "content_graph"
update_policy = "live"
graph_storage = "persistent"
```

The watcher uses `github.com/fsnotify/fsnotify` and watches directories, not individual files. It registers each eligible directory because filesystem notifications are not recursive. Overflow or full queues trigger a project rescan. Manual ingestion remains available as fallback.

Verified local smoke:

- create/get task through Codex MCP tools
- create research run through Codex MCP tools
- create research source through Codex MCP tools
- sensitive source URL token and email are redacted in the response

## Ladybug Native Gate

Normal builds use the in-memory Ladybug graph abstraction and do not import `go-ladybug` in normal build paths.

Native import verification is gated:

```sh
./scripts/ladybug-libs.sh
export CGO_LDFLAGS="-L$(pwd)/lib-ladybug -llbug -Wl,-rpath,$(pwd)/lib-ladybug"
go test -tags 'ladybug_native system_ladybug' ./internal/platform/ladybug/...
```

Do not commit `lib-ladybug/` or local database files.

## Troubleshooting

- `MIVIA_HTTP_ADDR` rejected: use `127.0.0.1` or `localhost`.
- `MIVIA_CONFIG_PATH` missing or invalid: copy `configs/agent-server.example.toml` to an ignored local config and replace placeholder roots with absolute local Linux or WSL paths.
- SQLite open failure: check the configured directory is writable or use `MIVIA_SQLITE_PATH=:memory:`.
- Persistent graph open failure: check the `storage.ladybug_path` directory is writable, local, ignored, and not inside an included project path unless excluded.
- Live watcher misses events: run manual ingestion and check whether the project is on a network, mounted, or special filesystem.
- Live watcher reports `no space left on device` or `too many open files`: reduce included directories or increase OS watch limits.
- MCP 406: include both `application/json` and `text/event-stream` in `Accept`.
- MCP 403: use a localhost or loopback `Origin`.
- Codex MCP tool returns `-32602 invalid tool arguments`: confirm the server binary includes support for `_meta`, JSON-string arguments, and underscore tool aliases.

## Local Reset

Stop the server, then delete ignored local datastore files under the configured `data/` directory. This resets SQLite app configuration and persistent project graph data. Keep local config files and datastore files uncommitted.
