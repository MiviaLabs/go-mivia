# Local Project Configuration

Status: Local registry, content graph ingestion, and live update support
Date: 2026-05-30
Classification: Internal; PII-prohibited

Local project configuration is optional. If no local config exists, `agent-server` starts with environment-only defaults and an empty project list.

## Files

- Example config: [configs/agent-server.example.toml](../../configs/agent-server.example.toml)
- Default ignored local config: `configs/agent-server.local.toml`
- Explicit config path override: `MIVIA_CONFIG_PATH`
- ADR: [ADR-0006](../adr/0006-local-project-configuration.md)

Do not commit local config files. Do not put secrets, tokens, PII, raw prompts, raw source content, provider payloads, or personal data in local config.

## Copy Workflow

```sh
cp configs/agent-server.example.toml configs/agent-server.local.toml
```

Edit only placeholder values such as `root_path`, then start the server:

```sh
MIVIA_CONFIG_PATH=configs/agent-server.local.toml go run ./cmd/agent-server
```

`MIVIA_CONFIG_PATH` is fatal when it points to a missing or invalid file. When it is unset and `configs/agent-server.local.toml` is absent, startup keeps the current environment-only defaults.

Environment variables remain final overrides over file values:

- `MIVIA_HTTP_ADDR`
- `MIVIA_LADYBUG_PATH`
- `MIVIA_SQLITE_PATH`
- `MIVIA_MAX_REQUEST_BYTES`
- `MIVIA_REQUEST_TIMEOUT`
- `MIVIA_READ_HEADER_TIMEOUT`
- `MIVIA_SHUTDOWN_TIMEOUT`
- `MIVIA_INGESTION_CONTENT_GRAPH_ENABLED`
- `MIVIA_INGESTION_LIVE_UPDATES_ENABLED`
- `MIVIA_INGESTION_DEBOUNCE_INTERVAL`
- `MIVIA_INGESTION_MAX_FILE_BYTES`
- `MIVIA_INGESTION_MAX_CHUNK_BYTES`
- `MIVIA_INGESTION_QUEUE_DEPTH`
- `MIVIA_INGESTION_WORKER_COUNT`
- `MIVIA_INGESTION_INITIAL_SCAN_ON_START`
- `MIVIA_INGESTION_SENSITIVE_MARKER_POLICY`

## Field Reference

| Field | Required | Notes |
| --- | --- | --- |
| `version` | Yes | Must be `1`. |
| `server.http_addr` | No | Defaults to `127.0.0.1:8080`; must stay loopback. |
| `server.max_request_bytes` | No | Positive integer; defaults to `1048576`. |
| `server.request_timeout` | No | Go duration string, for example `10s`. |
| `server.read_header_timeout` | No | Go duration string, for example `5s`. |
| `server.shutdown_timeout` | No | Go duration string, for example `10s`. |
| `storage.ladybug_path` | No | Local ignored graph datastore path; defaults to `data/mivialabs.lbug`. |
| `storage.sqlite_path` | No | Local ignored app-config datastore path; defaults to `data/mivialabs-config.sqlite`. |
| `ingestion.content_graph_enabled` | No | Global content graph gate; default `false`. |
| `ingestion.live_updates_enabled` | No | Global live watcher gate; requires content graph enabled; default `false`. |
| `ingestion.ast_extraction_enabled` | No | Must remain `true` when content graph is enabled; default `true`. |
| `ingestion.extractor_cache_enabled` | No | Must remain `true` when AST extraction is enabled; default `true`. |
| `ingestion.debounce_interval` | No | Go duration for live event coalescing; default `2s`. |
| `ingestion.max_file_bytes` | No | Global source file cap for ingestion; project value can override. |
| `ingestion.max_chunk_bytes` | No | Global chunk cap for ingestion and query responses; project value can override. |
| `ingestion.queue_depth` | No | Positive live update queue size. |
| `ingestion.worker_count` | No | Backward-compatible live submitter worker count. |
| `ingestion.global_worker_count` | No | Positive global scheduler worker count; default `2`. |
| `ingestion.per_project_worker_limit` | No | Positive per-project scheduler limit, no larger than global worker count; default `1`. |
| `ingestion.live_path_priority` | No | Must remain `true` while live updates are enabled; default `true`. |
| `ingestion.full_scan_batch_size` | No | Positive full-scan graph write batch size up to `5000`; default `500`. |
| `ingestion.max_watched_directory_count` | No | Optional watched-directory cap per project; `0` means unlimited. |
| `ingestion.task_warn_after` | No | Positive duration before slow live ingestion task warning; default `30s`. |
| `ingestion.initial_scan_on_start` | No | Optional startup rescan for live projects; default `false`. |
| `ingestion.sensitive_marker_policy` | No | Only `skip_file` is accepted. |
| `projects.id` | Yes | Stable project slug. |
| `projects.display_name` | Yes | Human-readable name. |
| `projects.description` | No | Non-sensitive summary. |
| `projects.root_path` | Yes | Absolute local path; use placeholders in committed examples. |
| `projects.enabled` | Yes | `true` only for projects allowed by the local developer. |
| `projects.classification` | No | Use `internal` unless an approved policy says otherwise. |
| `projects.graph_namespace` | No | Stable graph namespace; defaults to project ID. |
| `projects.graph_storage` | No | `persistent` or `in_memory`; default `persistent`. |
| `projects.digest_mode` | No | `metadata_only` or `content_graph`; content graph requires global gate and ADR approval. |
| `projects.update_policy` | No | `manual` or `live`; live requires `content_graph` plus global live gate. |
| `projects.include` | No | Root-relative include patterns. |
| `projects.exclude` | No | Root-relative exclude patterns. |
| `projects.follow_symlinks` | No | Keep `false`; symlink traversal is not approved. |
| `projects.max_file_bytes` | No | Per-project file cap for content graph ingestion. |
| `projects.max_chunk_bytes` | No | Per-project chunk cap for storage and response truncation. |
| `projects.sensitive_marker_policy` | No | Only `skip_file` is accepted. |

## Safe Path Examples

Use local Linux or WSL absolute paths:

```toml
root_path = "/home/dev/projects/example-service"
```

Windows drive-letter paths and UNC paths are not approved for the WSL server. Use a WSL-mounted path only after the engineering owner confirms the support model.

Docs must never point to a real developer's local config file or machine-specific project path.

## Validation Failures

Current validation rejects:

- missing explicit `MIVIA_CONFIG_PATH`
- invalid TOML
- unknown top-level sections or fields
- unknown project fields
- unsupported `version`
- unsupported `digest_mode`
- unsupported `update_policy`
- unsupported `graph_storage`
- invalid Go duration strings
- non-loopback `server.http_addr`
- non-positive timeout and request-size values
- missing enabled project roots
- non-directory enabled project roots
- duplicate project IDs
- duplicate graph namespaces
- unsafe include/exclude patterns
- path traversal
- absolute include/exclude patterns
- symlink roots and symlink directory traversal
- `follow_symlinks = true`

## Local Project APIs

The server exposes bounded project metadata on localhost only:

- `GET /api/v1/projects`
- `GET /api/v1/projects/{id}`
- `POST /api/v1/projects/{id}/digest-runs`
- `POST /api/v1/projects/{id}/ingestion-runs`
- `GET /api/v1/projects/{id}/ingestion-runs/{run_id}`
- `GET /api/v1/projects/{id}/ingestion-runs/latest`
- `GET /api/v1/projects/{id}/files`
- `GET /api/v1/projects/{id}/files/{file_id}/chunks`
- `GET /api/v1/projects/{id}/files/{file_id}/outline`
- `GET /api/v1/projects/{id}/symbols`
- `GET /api/v1/projects/{id}/headings`
- MCP tools: `projects.list`, `projects.get`, `projects.digest`, `projects.ingest`, `projects.ingestion_status`, `projects.ingestion_status_latest`, `projects.files.list`, `projects.files.get`, `projects.file.chunks`, `projects.symbols.list`, `projects.headings.list`, `projects.file.outline`
- MCP resources: `mivialabs://projects/{id}`, `mivialabs://projects/{id}/digest-runs/{run_id}`, `mivialabs://projects/{id}/files/{file_id}`, `mivialabs://projects/{id}/files/{file_id}/chunks/{chunk_id}`, `mivialabs://projects/{id}/files/{file_id}/outline`, `mivialabs://projects/{id}/symbols/{symbol_id}`

Project responses omit local root paths and datastore paths by default. Digest runs are manual and metadata-only: graph writes store relative path, extension/language hint, file size, mtime, and a metadata fingerprint. Content graph ingestion stores eligible local source chunks only after all gates pass. AST metadata is promoted for Go, JS, JSX, TS, TSX, C#, Python, Markdown, and lightweight infrastructure/config files. TS/JS/TSX/JSX, C#, and Python parsing is mandatory Tree-sitter; startup validation fails with `extractor_initialization_failed` if a promoted grammar or query cannot initialize.

Extractor cache data is stored in SQLite table `project_extractor_cache`. It stores symbols and headings only; it does not store raw source, AST text, chunks, absolute paths, skipped sensitive data, or matched sensitive text. Cache rows are keyed by project, relative-path hash, content hash, extractor name, and extractor version, and are removed when a file becomes skipped or absent.

Full scans commit graph writes in bounded windows. Manual and live ingestion submissions run through the scheduler. Manual submissions return queued run metadata without waiting for scan completion; clients should use latest status or poll the returned run ID before trusting indexed data. The scheduler prioritizes live path events over full-scan continuation and enforces global and per-project worker limits.

File listing accepts optional `status`, `extension`, `path_prefix`, `skipped_reason`, `present`, `modified_since`, `page_size`, and `page_token` filters. Extension values may be `go` or `.go`; matching is case-insensitive and invalidates whitespace or path separators.

File outlines return file, heading, symbol, and chunk location metadata by default. Large outlines can be bounded with symbol `kind`, `name_prefix`, `symbol_page_size`, and `symbol_page_token`. Agents can request eligible source context inline with `include_chunk_text=true` and `max_chunk_bytes`; skipped sensitive files still have no chunks to return.

## Live Update Mode

Live mode is disabled by default. To use it locally, enable both global gates and configure a project with `digest_mode = "content_graph"` and `update_policy = "live"`. The watcher registers directories, not files. It walks eligible directories because the underlying filesystem notification API is not recursive. New directories created under included paths are watched when observed.

Overflow and queue-full conditions enqueue a bounded project rescan. Manual ingestion remains the fallback when live watcher behavior misses events or the OS watch limit is reached.

Watcher troubleshooting:

- Linux `no space left on device` or `too many open files`: increase OS watch limits or reduce included directories.
- Network, mounted, or special filesystems may not emit reliable events; use manual ingestion.
- Excluded directories such as `.git/**`, `data/**`, `secrets/**`, `.env*`, and `lib-ladybug/**` are not watched for ingestion.

## Local Reset

Stop the server, disable `content_graph_enabled` and `live_updates_enabled` if needed, then delete ignored local datastore files under the configured `data/` path. Do not commit local datastore files.

## Local Use Boundary

Project configuration and digest APIs are intended only for engineer local computers. The server must remain bound to localhost/loopback until a separate auth and exposure review approves a different model.

SQLite stores configured project root paths as local developer-machine configuration state. REST and MCP project responses omit those root paths by default.
