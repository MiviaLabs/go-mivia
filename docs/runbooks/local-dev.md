# Local Development Runbook

Status: Bootstrap
Date: 2026-05-30
Classification: Internal; no PII

## Prerequisites

- Go `1.26.3`
- `make`
- `curl`
- Optional Docker path: Docker Desktop for Windows with WSL 2 integration enabled for the Ubuntu distro
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

## Run Mivia Server

```sh
MIVIA_HTTP_ADDR=127.0.0.1:8080 \
MIVIA_SQLITE_PATH=:memory: \
go run ./cmd/mivia-server
```

Default bind is localhost-only. Do not bind to `0.0.0.0` or a public interface until authn/authz, origin policy, rate limits, and audit logging are approved.

## Run With Docker Compose

Use Docker Compose when Docker is available but Go is not installed on the host:

```sh
docker compose up
```

Use the modern plugin command `docker compose`, not the legacy `docker-compose` binary. If WSL prints `docker-compose: command not found` or says to activate WSL integration:

1. Install and start Docker Desktop for Windows.
2. Docker Desktop -> Settings -> General -> enable `Use the WSL 2 based engine`.
3. Docker Desktop -> Settings -> Resources -> WSL Integration -> enable integration for `Ubuntu`.
4. In WSL, verify:

```sh
docker version
docker compose version
```

Official setup reference: [Docker Desktop WSL 2 backend](https://docs.docker.com/desktop/features/wsl/).

The default Compose file builds from this checkout. After the first public image is published, the Compose file includes a commented pull example:

```yaml
# image: ghcr.io/mivialabs/go-mivia:0.2.0
```

Go module releases and container images are versioned in different registries. The Go module release tag should be `v0.2.0`; the container image tag can be `0.2.0` when the release workflow publishes it that way.

Defaults:

- Host bind: `MIVIA_HOST_BIND=127.0.0.1`
- Host port: `MIVIA_HOST_PORT=8080`
- Content graph ingestion: `MIVIA_INGESTION_CONTENT_GRAPH_ENABLED=true`
- Live updates: `MIVIA_INGESTION_LIVE_UPDATES_ENABLED=true`
- Global workspace gate: `MIVIA_WORKSPACE_ENABLED=true`
- Container user: `MIVIA_CONTAINER_USER=10001:10001` by default. For local edit-capable bind mounts across mixed WSL/Windows ownership boundaries, use the ignored local override with `MIVIA_CONTAINER_USER=0:0`; the workspace atomic write path preserves original Unix ownership when supported and tolerates chmod-unsupported Windows mounts.
- Container data paths: `MIVIA_LADYBUG_PATH=/var/lib/mivia/mivialabs.lbug` and `MIVIA_SQLITE_PATH=/var/lib/mivia/mivialabs-config.sqlite`; persistent project graph/search stores live under `/var/lib/mivia/projects/<project-id>/`, with search SQLite filenames tied to the Pebble graph storage epoch.
- Container storage: named Compose volume `mivia-data`
- Config file: `configs/mivia-server.compose.toml`

Change the published host address or port from the host environment:

```sh
MIVIA_HOST_BIND=127.0.0.1 MIVIA_HOST_PORT=18080 docker compose up
```

`MIVIA_HOST_BIND=0.0.0.0` publishes beyond loopback. Use it only for approved local-only network exposure; the server has no production authn/authz posture. The container still runs `mivia-server` on internal loopback and forwards container port `8080` for Docker publishing.

The default Compose file mounts only `configs/mivia-server.compose.toml`. It does not mount ignored local config or secret files, so project roots, project names, Jira/Confluence URLs, and credential refs are not loaded accidentally.

For this checkout, an ignored `.docker-compose.local.yml` may be used to mount an ignored local config, credential file, and the project roots referenced by that config:

```sh
docker compose -f docker-compose.yml -f .docker-compose.local.yml up
```

Use the commented local override template at the bottom of `docker-compose.yml`, copy it into `.docker-compose.local.yml`, and replace only placeholder paths.

Workspace access still requires a configured project with `workspace_mode = "read_only"` or `"edit"`. Leave project `workspace_mode = "disabled"` for projects that must not expose governed git status/diff/read/create/delete/edit tools. In edit mode, use `file_read` before `file_edit` or `file_delete` on existing eligible files, and use `file_create` for new eligible text files. These tools do not provide recursive delete, arbitrary patch upload, arbitrary shell, or a shell replacement. If exact workspace edits fail while reads and dry-runs succeed, check the container user and bind mount permissions before changing host drive permissions.

## Optional Local Project Config

Project config is local-only and intended for engineer local computers. Copy the committed example and replace placeholder paths:

```sh
cp configs/mivia-server.example.toml configs/mivia-server.local.toml
```

Start with an explicit config path:

```sh
MIVIA_CONFIG_PATH=configs/mivia-server.local.toml go run ./cmd/mivia-server
```

`MIVIA_CONFIG_PATH` is fatal when it points to a missing or invalid file. If it is unset and `configs/mivia-server.local.toml` is absent, the server starts with environment-only defaults and an empty project list.

Do not put secrets, tokens, PII, raw prompts, raw source content, provider payloads, or personal data in local config. Local configs are ignored and must not be committed.

For a longer-running WSL process launched from Windows, build a binary first:

```powershell
wsl -d Ubuntu --cd <repo-root> env PATH=<go-bin-path>:$PATH go build -o <ignored-runtime-dir>/mivia-server ./cmd/mivia-server
wsl -d Ubuntu --cd <repo-root> env MIVIA_HTTP_ADDR=127.0.0.1:8080 MIVIA_SQLITE_PATH=:memory: <ignored-runtime-dir>/mivia-server
```

Keep that terminal open while testing. If you need a detached process, launch `wsl.exe` from Windows process management and redirect logs to a local temp file.

The server writes JSON logs to stdout by default. Persistent file logging is opt-in only: set `logging.file_enabled = true` and `logging.file_path = "data/mivia-server.log"` in local TOML, or set `MIVIA_LOG_FILE_ENABLED=true` plus `MIVIA_LOG_FILE_PATH`.

## Optional Jira And Confluence Project Context

Jira and Confluence integrations are configured per local project in the ignored TOML file. They are Atlassian Cloud only, polling-only, and local graph backed.

Setup:

1. Keep credentials in an ignored local credential file, then reference it from TOML with `credentials_file = "secrets/atlassian-credentials.json"`.
2. Add `[projects.integrations.jira]` inside the project block with `project_keys = ["ABC"]`. Jira ticket titles are the `summary` field, so keep `summary` in `default_fields`.
3. Add `[projects.integrations.confluence]` with `space_keys = ["DOCS"]`. Space keys are separate from Jira project keys.
4. Set `ingestion_enabled = true` only when the provider may poll. Tune `initial_page_size`, `incremental_page_size`, and `max_results` for large projects.
5. Restart the server after config changes.

Agent flow through MCP:

- `projects.integrations.status` confirms the redacted local configuration and latest run state.
- `projects.integrations.poll` queues one provider poll and returns a `run_id`.
- `projects.integrations.poll_status` checks that run until completion.
- `projects.integrations.search`, `projects.jira.issue.get`, and `projects.confluence.page.get` read already-ingested local graph content only.

Do not use Jira or Confluence connectors while working in this repo. The local server's provider client is the only approved Atlassian path for these project integrations.

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
curl -fsS http://127.0.0.1:8080/api/v1/projects/example-service/ingestion-runs/latest
curl -fsS http://127.0.0.1:8080/api/v1/projects/example-service/files
curl -fsS 'http://127.0.0.1:8080/api/v1/projects/example-service/symbols?page_size=25'
```

Manual ingestion submits work asynchronously through the scheduler. Use the returned `run_id` with `/ingestion-runs/{run_id}` or check `/ingestion-runs/latest` until the run is `completed` before relying on indexed files and symbols.

Governed workspace tools are disabled unless `[workspace].enabled = true` and the project sets `workspace_mode = "read_only"` or `"edit"` with `digest_mode = "content_graph"`. Read-only smoke:

```sh
curl -fsS 'http://127.0.0.1:8080/api/v1/projects/example-service/workspace/git/status'
curl -fsS 'http://127.0.0.1:8080/api/v1/projects/example-service/workspace/git/diff?scope=working_tree&max_diff_bytes=65536'
curl -fsS 'http://127.0.0.1:8080/api/v1/projects/example-service/workspace/files/read?relative_path=README.md'
```

Edit mode requires `workspace_mode = "edit"`. Existing eligible files must use `projects.workspace.file_read` first, then `projects.workspace.file_edit` or `projects.workspace.file_delete` with the returned edit token. New eligible text files should use `projects.workspace.file_create`. There is no arbitrary shell, raw patch, recursive delete, public exposure, provider call, embedding/vector/crawling path, raw DB query endpoint, or git commit/push/checkout/reset/branch/merge/rebase/stash/clean/restore tool.

Chunk reads require stable opaque IDs from the file list response:

```sh
curl -fsS 'http://127.0.0.1:8080/api/v1/projects/example-service/files/<file_id>/chunks?page_size=10&max_chunk_bytes=4096'
```

Content graph responses are bounded and must not include absolute roots, skipped sensitive content, matched sensitive text, secrets, PII, raw prompts, provider payloads, or raw database query results.

## Dashboard Activity

Open `http://127.0.0.1:8080/dashboard/`, select a project, then use `Agent activity` on the project details page to watch project-scoped MCP calls. The drawer streams recent persisted redacted activity and live in-memory activity over `GET /api/v1/projects/{id}/agent-activity/stream` and shows method/tool, status, duration, failure category, client class, request metadata, summary classes, and available local-debug payload details.

SSE reconnects can resume replay by sending the browser-managed `Last-Event-ID` header or an explicit `after_id` query value. The server returns project-scoped events with IDs greater than that cursor before continuing with live events.

The drawer is for localhost debugging. Full payloads may include source snippets, prompts, secrets, or personal data if a caller sent them. Persistent storage omits raw payloads and payload-derived hashes by default; raw payload/hash retention requires explicit local debug opt-in with `MIVIA_DEBUG_ENABLED=true` and `MIVIA_AGENT_ACTIVITY_RETAIN_RAW_PAYLOADS=true`. Do not paste full payloads into docs, commits, issue trackers, Slack, or agent-run metadata unless the task explicitly requires that exposure.

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
codex mcp add mivia-server --url http://127.0.0.1:8080/mcp
codex mcp get mivia-server
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
- `projects.search_index.rebuild`
- `projects.ingestion_status`
- `projects.ingestion_status_latest`
- `projects.files.list`
- `projects.files.get`
- `projects.file.chunks`
- `projects.symbols.list`
- `projects.search.text`
- `projects.search.files`
- `projects.search.symbols`
- `projects.search.references`
- `projects.search.calls`
- `projects.workspace.git_status`
- `projects.workspace.git_diff`
- `projects.workspace.file_read`
- `projects.workspace.file_edit`
- `projects.workspace.file_create`
- `projects.workspace.file_delete`
- `projects.search.ast.queries`
- `projects.search.ast`
- `projects.symbol.source`
- `projects.symbol.references`
- `projects.symbol.callers`
- `projects.symbol.callees`
- `projects.symbol.call_graph`
- `projects.headings.list`
- `projects.file.outline`

Codex may expose underscore-normalized callable names such as `tasks_create`, `projects_digest`, or `projects_ingest`; the server accepts both dotted MCP tool names and underscore aliases.

Search smoke after a completed `content_graph` ingestion:

```sh
curl -fsS 'http://127.0.0.1:8080/api/v1/projects/example-service/search/text?query=main&page_size=5&max_snippet_bytes=200'
curl -fsS 'http://127.0.0.1:8080/api/v1/projects/example-service/search/symbols?name_contains=Run&page_size=5'
curl -fsS 'http://127.0.0.1:8080/api/v1/projects/example-service/search/ast/queries'
curl -fsS 'http://127.0.0.1:8080/api/v1/projects/example-service/search/ast?language=go&query=function_declarations&page_size=5'
```

Search index repair is asynchronous:

```sh
curl -fsS -X POST http://127.0.0.1:8080/api/v1/projects/example-service/search-index/rebuild
curl -fsS http://127.0.0.1:8080/api/v1/projects/example-service/ingestion-runs/latest
```

Use repair only when search metadata reports degraded indexed state or after local datastore recovery. Normal freshness comes from live ingestion or manual `projects.ingest`.

## Live Project Updates

Live updates are disabled unless both gates are enabled:

```toml
[ingestion]
content_graph_enabled = true
live_updates_enabled = true
ast_extraction_enabled = true
extractor_cache_enabled = true
debounce_interval = "2s"
queue_depth = 128
worker_count = "auto"
global_worker_count = "auto"
per_project_worker_limit = "auto"
live_path_priority = true
full_scan_batch_size = 500
max_watched_directory_count = 0
task_warn_after = "30s"
initial_scan_on_start = false

[[projects]]
digest_mode = "content_graph"
update_policy = "live"
graph_storage = "persistent"
```

On server startup, persisted `pending` or `running` ingestion runs from a prior process are marked failed with `error_category=server_restarted`; queued work is in memory and cannot resume after a restart. Use live startup scans or submit a new `projects.ingest` run for freshness.

The watcher uses `github.com/fsnotify/fsnotify` and watches directories, not individual files. It registers each eligible directory because filesystem notifications are not recursive. Overflow or full queues trigger a scheduled project rescan. The scheduler prioritizes live path events over full-scan continuation and enforces global and per-project worker limits. Manual ingestion remains available as fallback, returns queued metadata immediately, and runs through the same scheduler.

Promoted AST extraction validates at startup when content graph ingestion is enabled. Supported promoted extractors are Go stdlib AST, Tree-sitter JavaScript/TypeScript/TSX, Tree-sitter C#, Tree-sitter Python, Markdown headings, and lightweight infrastructure/config metadata. Tree-sitter failures must be fixed as dependency or query initialization issues; do not re-enable regex fallback for TS/JS/TSX/JSX, C#, or Python.

Named AST structural search uses server-owned query IDs for Go, Python, JavaScript, JSX, TypeScript, TSX, and C#. Raw Tree-sitter query syntax is not exposed. Oversized, sensitive, denied, absent, parse-error, and other skipped files are not searched; oversized files can appear only as safe coverage counts.

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
- `MIVIA_CONFIG_PATH` missing or invalid: copy `configs/mivia-server.example.toml` to an ignored local config and replace placeholder roots with absolute local Linux or WSL paths.
- SQLite open failure: check the configured directory is writable or use `MIVIA_SQLITE_PATH=:memory:`.
- Persistent graph/search open failure: check the `storage.ladybug_path` parent and derived `projects/<project-id>/` directories are writable, local, ignored, and not inside an included project path unless excluded.
- Live watcher misses events: run manual ingestion and check whether the project is on a network, mounted, or special filesystem.
- Live watcher reports `no space left on device` or `too many open files`: reduce included directories or increase OS watch limits.
- Tree-sitter build fails under WSL: verify CGO-capable toolchain support and the pinned Tree-sitter module versions, then rerun the package tests. Do not fall back to regex extraction for promoted languages.
- `extractor_initialization_failed`: treat as a startup validation failure for the named extractor. Check grammar/query dependency setup; do not log or paste local source content while debugging.
- MCP 406: include both `application/json` and `text/event-stream` in `Accept`.
- MCP 403: use a localhost or loopback `Origin`.
- Codex MCP tool returns `-32602 invalid tool arguments`: confirm the server binary includes support for `_meta`, JSON-string arguments, and underscore tool aliases.

## Local Reset

Stop the server, then delete ignored local datastore files under the configured `data/` directory. This resets SQLite app configuration and persistent project graph data. Keep local config files and datastore files uncommitted.
