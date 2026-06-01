# Local Project Configuration

Status: Local registry, content graph ingestion, and live update support
Date: 2026-05-30
Classification: Internal; local project-integration rich-content exception only

Local project configuration is optional. If no local config exists, `mivia-server` starts with environment-only defaults and an empty project list.

## Files

- Example config: [configs/mivia-server.example.toml](../../configs/mivia-server.example.toml)
- Default ignored local config: `configs/mivia-server.local.toml`
- Explicit config path override: `MIVIA_CONFIG_PATH`
- ADR: [ADR-0006](../adr/0006-local-project-configuration.md)

Do not commit local config files. Do not put secrets, tokens, PII, raw prompts, raw source content, provider payloads, or personal data in local config.

## Copy Workflow

```sh
cp configs/mivia-server.example.toml configs/mivia-server.local.toml
```

Edit only placeholder values such as `root_path`, then start the server:

```sh
MIVIA_CONFIG_PATH=configs/mivia-server.local.toml go run ./cmd/mivia-server
```

`MIVIA_CONFIG_PATH` is fatal when it points to a missing or invalid file. When it is unset and `configs/mivia-server.local.toml` is absent, startup keeps the current environment-only defaults.

Environment variables remain final overrides over file values:

- `MIVIA_CPU_COUNT`
- `MIVIA_HTTP_ADDR`
- `MIVIA_LADYBUG_PATH`
- `MIVIA_SQLITE_PATH`
- `MIVIA_SQLITE_WAL_ENABLED`
- `MIVIA_SQLITE_BUSY_TIMEOUT`
- `MIVIA_SQLITE_SYNCHRONOUS`
- `MIVIA_SQLITE_CHECKPOINT_AFTER_INGESTION`
- `MIVIA_DEBUG_ENABLED`
- `MIVIA_DEBUG_PPROF_ENABLED`
- `MIVIA_DEBUG_EXPVAR_ENABLED`
- `MIVIA_DEBUG_RUNTIME_METRICS_ENABLED`
- `MIVIA_LOG_FILE_ENABLED`
- `MIVIA_LOG_FILE_PATH`
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
- `MIVIA_INGESTION_GLOBAL_WORKER_COUNT`
- `MIVIA_INGESTION_PER_PROJECT_WORKER_LIMIT`
- `MIVIA_INGESTION_INITIAL_SCAN_ON_START`
- `MIVIA_INGESTION_SENSITIVE_MARKER_POLICY`
- `MIVIA_WORKSPACE_ENABLED`

Docker Compose also reads host-side publishing overrides that are not server config fields:

- `MIVIA_HOST_BIND`, default `127.0.0.1`
- `MIVIA_HOST_PORT`, default `8080`

Keep `MIVIA_HOST_BIND=127.0.0.1` unless an approved local-only exposure requires a different host bind. The Compose image keeps `mivia-server` bound to container loopback and forwards container port `8080` for Docker publishing.

Compose loads [configs/mivia-server.compose.toml](../../configs/mivia-server.compose.toml). That file mirrors the local global runtime defaults without project roots, project names, Jira/Confluence URLs, or credential refs. It enables content graph ingestion, live updates, diagnostics, runtime metrics, and the global workspace gate by default. Workspace tools still require each project to opt in with `workspace_mode = "read_only"` or `"edit"`.

Use ignored `.docker-compose.local.yml` only when the container must load ignored local project config or credential files:

```sh
docker compose -f docker-compose.yml -f .docker-compose.local.yml up
```

Keep `.docker-compose.local.yml`, `configs/*.local.toml`, and real files under `secrets/` uncommitted.

The commented local override template at the bottom of `docker-compose.yml` shows the required mounts with placeholder paths only.

The default Compose path builds locally. For the future public release, `docker-compose.yml` carries a commented image example using a container registry tag:

```yaml
# image: ghcr.io/mivialabs/go-mivia:0.1.7
```

Do not treat that as a Go module proxy path. Go module publication uses repository semantic-version tags such as `v0.1.7`; container publication uses registry image tags such as `0.1.7` when the release workflow chooses that tag.

## Field Reference

| Field | Required | Notes |
| --- | --- | --- |
| `version` | Yes | Must be `1`. |
| `server.http_addr` | No | Defaults to `127.0.0.1:8080`; must stay loopback. |
| `server.cpu_count` | No | `"auto"` or a positive integer; default `"auto"` uses every logical CPU visible to the process and is applied to Go `GOMAXPROCS`. |
| `server.max_request_bytes` | No | Positive integer; defaults to `1048576`. |
| `server.request_timeout` | No | Go duration string, for example `10s`. |
| `server.read_header_timeout` | No | Go duration string, for example `5s`. |
| `server.shutdown_timeout` | No | Go duration string, for example `10s`. |
| `storage.ladybug_path` | No | Local ignored Ladybug metadata path and parent for project graph/search stores; defaults to `data/mivialabs.lbug`. Persistent project stores derive as `<parent>/projects/<project-id>/mivialabs.lbug` and `<parent>/projects/<project-id>/mivialabs-search.sqlite`. |
| `storage.sqlite_path` | No | Local ignored app-config datastore path; defaults to `data/mivialabs-config.sqlite`. |
| `sqlite.wal_enabled` | No | Enables WAL for file-backed local SQLite paths; default `true`, forced off for `sqlite_path = ":memory:"`. Set `false` as the rollback switch on unsupported filesystems. |
| `sqlite.busy_timeout` | No | Positive Go duration for SQLite lock waits; default `5s`. |
| `sqlite.synchronous` | No | SQLite synchronous mode: `OFF`, `NORMAL`, `FULL`, or `EXTRA`; default `NORMAL`. |
| `sqlite.checkpoint_after_ingestion` | No | Allows checkpointing after ingestion work when storage code supports it; default `true`. |
| `debug.enabled` | No | Master switch for local diagnostics; default `false`. Debug routes must stay loopback-only and redacted. |
| `debug.pprof_enabled` | No | Opt-in pprof diagnostics; requires `debug.enabled = true`; default `false`. |
| `debug.expvar_enabled` | No | Opt-in expvar diagnostics; requires `debug.enabled = true`; default `false`. |
| `debug.runtime_metrics_enabled` | No | Opt-in runtime metric sampling; requires `debug.enabled = true`; default `false`. |
| `logging.file_enabled` | No | Opts into writing JSON logs to `logging.file_path` as well as stdout; default `false`. |
| `logging.file_path` | When file logging enabled | Ignored local log path. Required only when `logging.file_enabled = true`. |
| `ingestion.content_graph_enabled` | No | Global content graph gate; default `false`. |
| `ingestion.live_updates_enabled` | No | Global live watcher gate; requires content graph enabled; default `false`. |
| `ingestion.ast_extraction_enabled` | No | Must remain `true` when content graph is enabled; default `true`. |
| `ingestion.extractor_cache_enabled` | No | Must remain `true` when AST extraction is enabled; default `true`. |
| `ingestion.debounce_interval` | No | Go duration for live event coalescing; default `2s`. |
| `ingestion.max_file_bytes` | No | Global source ingestion coverage cap. Unset or `0` means unlimited; positive values explicitly cap indexed file bytes. Project value can override. |
| `ingestion.max_chunk_bytes` | No | Global chunk cap for ingestion and query responses; project value can override. |
| `ingestion.queue_depth` | No | Positive live update queue size. |
| `ingestion.worker_count` | No | `"auto"` or a positive integer; default `"auto"` resolves to `4` for local SQLite-backed ingestion. |
| `ingestion.global_worker_count` | No | `"auto"` or a positive integer; default `"auto"` resolves to `4` and caps scheduler workers plus full-scan file workers globally. |
| `ingestion.per_project_worker_limit` | No | `"auto"` or a positive integer no larger than global worker count; default `"auto"` resolves to `2` and caps full-scan/path workers per project. |
| `ingestion.live_path_priority` | No | Boolean; default `true`. Set `false` to process live path events through the regular scheduler queue. |
| `ingestion.full_scan_batch_size` | No | Positive hard file-count cap for full-scan prepared storage flushes; default `500`. Heavy prepared files may flush earlier because graph/search writes are also bounded by internal write weight. Large values can increase memory and write latency. |
| `ingestion.max_watched_directory_count` | No | Optional watched-directory cap per project; `0` means unlimited. |
| `ingestion.task_warn_after` | No | Positive duration before slow live ingestion task warning; default `30s`. |
| `ingestion.initial_scan_on_start` | No | Optional startup rescan for live projects; default `false`. |
| `ingestion.sensitive_marker_policy` | No | Only `skip_file` is accepted. |
| `workspace.enabled` | No | Global workspace status/diff/read/edit gate; default `false`. Must stay loopback-only. |

Persisted ingestion runs in `pending` or `running` state are local in-memory queue leftovers after a server restart. Current builds mark them failed with `error_category=server_restarted` during startup; use live startup scans or submit a fresh `projects.ingest` run to repair freshness.
| `projects.id` | Yes | Stable project slug. |
| `projects.display_name` | Yes | Human-readable name. |
| `projects.description` | No | Non-sensitive summary. |
| `projects.root_path` | Yes | Absolute local path; use placeholders in committed examples. |
| `projects.enabled` | Yes | `true` only for projects allowed by the local developer. |
| `projects.classification` | No | Use `internal` unless an approved policy says otherwise. |
| `projects.graph_namespace` | No | Stable graph namespace; defaults to project ID. |
| `projects.graph_storage` | No | `persistent` or `in_memory`; default `persistent`. Persistent content-graph projects use derived project graph/search stores under the `storage.ladybug_path` parent. |
| `projects.digest_mode` | No | `metadata_only` or `content_graph`; content graph requires global gate and ADR approval. |
| `projects.update_policy` | No | `manual` or `live`; live requires `content_graph` plus global live gate. |
| `projects.workspace_mode` | No | `disabled`, `read_only`, or `edit`; `read_only` and `edit` require `digest_mode = "content_graph"`. |
| `projects.include` | No | Root-relative include patterns. |
| `projects.exclude` | No | Root-relative exclude patterns. |
| `projects.follow_symlinks` | No | Keep `false`; symlink traversal is not approved. |
| `projects.max_file_bytes` | No | Per-project file cap for content graph ingestion. Unset or `0` means unlimited; positive values explicitly cap indexed file bytes. |
| `projects.max_chunk_bytes` | No | Per-project chunk cap for storage and response truncation. |
| `projects.sensitive_marker_policy` | No | Only `skip_file` is accepted. |

For Dart and Flutter projects, generated files are included by default after the normal path, size, binary, UTF-8, and sensitive-marker gates pass. Keep `.g.dart`, `.freezed.dart`, `.mocks.dart`, `.generated.dart`, and similar files indexable unless a project deliberately trades generated-code visibility for less noise. Exclude build outputs such as `build/**` rather than generated Dart source that Flutter engineers may need for references, calls, or symbol navigation.

## Project Integrations

Project integrations are optional per-project Atlassian Cloud providers under `[projects.integrations.jira]` and `[projects.integrations.confluence]`. They are localhost-only, polling-only, and scoped by explicit allowlists. See [Project integrations security policy](../security/project-integrations.md) for the approved local rich-content and PII boundary.

Credentials are references only. Use either `credentials_file`, or exactly one email ref plus exactly one API-token ref:

- `credentials_file`
- `email_env`
- `email_file`
- `api_token_env`
- `api_token_file`

Do not put raw email addresses, API tokens, passwords, Basic auth values, or real Atlassian content in TOML, examples, fixtures, logs, SQLite, LadybugDB, or MCP status responses. `credentials_file` points to an ignored local JSON file and its path/content must not be exposed in MCP/status/errors.

Provider page size controls request chunking, not total coverage. Jira defaults to page size `100`; Confluence defaults to page size `50`. `max_results = 0` means unlimited local ingestion coverage and provider polling should continue until provider exhaustion, repeated cursor/token, context cancellation, or provider backoff/stop conditions. Positive `max_results` values are explicit operator caps. Provider clients should respect `Retry-After` when rate-limited.

### Configure Jira And Confluence For A Project

1. Copy the committed example to an ignored local config and restart the server with `MIVIA_CONFIG_PATH` after edits.
2. Put Atlassian credentials only in an ignored local credential file or env/file refs. The local credential file uses email and API-token entries, but committed TOML must reference only the file path with `credentials_file`.
3. Add `[projects.integrations.jira]` inside the target `[[projects]]` block. Jira requires `site_url`, `auth_mode = "api_token_basic"`, `credentials_file`, and a non-empty `project_keys` allowlist.
4. Add `[projects.integrations.confluence]` separately when Confluence is needed. Confluence requires its own non-empty `space_keys` allowlist; Jira project keys and Confluence space keys are separate even when they happen to share the same text.
5. Keep `ingestion_enabled = true` only for providers that may poll. Use `initial_full_sync = "manual"` unless startup polling is explicitly wanted.
6. For Jira ticket titles, include `summary` in `default_fields` or `allowed_fields`. The Jira title field is `summary`.
7. For rich local graph content, opt in narrowly: Jira uses `include_rich_fields` plus `allowed_fields`; Confluence uses `include_body`, `include_comments`, `include_labels`, and `include_properties`.
8. Trigger an initial run with `projects.integrations.poll`, then poll the returned run ID with `projects.integrations.poll_status`. The poll call queues work and returns quickly.
9. Use `projects.integrations.search`, `projects.jira.issue.get`, and `projects.confluence.page.get` after polling completes. These search/read tools use local graph data only and do not call Atlassian.

Safe TOML shape:

```toml
[projects.integrations.jira]
enabled = true
site_url = "https://example.atlassian.net"
auth_mode = "api_token_basic"
credentials_file = "secrets/atlassian-credentials.json"
project_keys = ["ABC"]
ingestion_enabled = true
initial_full_sync = "manual"
incremental_interval = "1m"
empty_poll_sleep = "10m"
max_idle_sleep = "30m"
overlap_window = "2m"
initial_page_size = 100
incremental_page_size = 100
max_results = 0
default_fields = ["summary", "status", "updated", "issuetype", "project"]
allowed_fields = ["description", "comment"]
include_rich_fields = true
include_comments = true

[projects.integrations.confluence]
enabled = true
site_url = "https://example.atlassian.net"
auth_mode = "api_token_basic"
credentials_file = "secrets/atlassian-credentials.json"
space_keys = ["DOCS"]
ingestion_enabled = true
initial_full_sync = "manual"
incremental_interval = "1m"
empty_poll_sleep = "10m"
max_idle_sleep = "30m"
overlap_window = "2m"
initial_page_size = 50
incremental_page_size = 50
max_results = 0
body_representation = "storage"
include_body = true
include_comments = true
include_labels = true
include_properties = true
```

| Field | Required | Notes |
| --- | --- | --- |
| `projects.integrations.jira.enabled` | No | Enables configured Jira Cloud provider metadata; default `false`. |
| `projects.integrations.jira.site_url` | When enabled | HTTPS Atlassian Cloud host only. |
| `projects.integrations.jira.cloud_id` | No | Optional Atlassian Cloud ID. |
| `projects.integrations.jira.auth_mode` | When enabled | Must be `api_token_basic`. |
| `projects.integrations.jira.project_keys` | When enabled | Required Jira project allowlist. |
| `projects.integrations.jira.credentials_file` | Conditional | Ignored local credential file reference; do not combine with email/token refs. |
| `projects.integrations.jira.email_env` / `email_file` | Conditional | Email reference; required with API-token ref when `credentials_file` is absent. |
| `projects.integrations.jira.api_token_env` / `api_token_file` | Conditional | API-token reference; required with email ref when `credentials_file` is absent. |
| `projects.integrations.jira.ingestion_enabled` | No | Allows scheduler/manual polling for this provider; default `false`. |
| `projects.integrations.jira.initial_full_sync` | No | `manual` or `on_start`; default `manual`. |
| `projects.integrations.jira.incremental_interval` | No | Incremental polling period; default `1m`. |
| `projects.integrations.jira.empty_poll_sleep` | No | Idle sleep after empty incremental polls; default `10m`. |
| `projects.integrations.jira.max_idle_sleep` | No | Upper bound for empty-poll sleep; default `30m`. |
| `projects.integrations.jira.overlap_window` | No | Incremental overlap window; default `2m`. |
| `projects.integrations.jira.initial_page_size` | No | Initial full-sync page size; default `100`. |
| `projects.integrations.jira.incremental_page_size` | No | Incremental page size; default `100`. |
| `projects.integrations.jira.max_results` | No | Per-run result cap. Unset or `0` means poll until provider exhaustion, repeated cursor/token, context cancellation, or provider stop; positive values explicitly cap a run. |
| `projects.integrations.jira.default_fields` | No | Base Jira fields; defaults include summary/status/updated/type/project. |
| `projects.integrations.jira.allowed_fields` | No | Explicit rich/custom fields eligible for local ingestion. Include `comment` to ingest comments. |
| `projects.integrations.jira.include_rich_fields` | No | Ingest configured `allowed_fields`; default `false`. |
| `projects.integrations.jira.include_comments` | No | Ingest comments only when `comment` is also in `allowed_fields`; default `false`. |
| `projects.integrations.jira.jql_extra_filter` | No | Extra local polling filter appended to allowlisted project query. |
| `projects.integrations.confluence.enabled` | No | Enables configured Confluence Cloud provider metadata; default `false`. |
| `projects.integrations.confluence.site_url` | When enabled | HTTPS Atlassian Cloud host only. |
| `projects.integrations.confluence.cloud_id` | No | Optional Atlassian Cloud ID. |
| `projects.integrations.confluence.auth_mode` | When enabled | Must be `api_token_basic`. |
| `projects.integrations.confluence.space_keys` | When enabled | Required Confluence space allowlist. |
| `projects.integrations.confluence.credentials_file` | Conditional | Ignored local credential file reference; do not combine with email/token refs. |
| `projects.integrations.confluence.email_env` / `email_file` | Conditional | Email reference; required with API-token ref when `credentials_file` is absent. |
| `projects.integrations.confluence.api_token_env` / `api_token_file` | Conditional | API-token reference; required with email ref when `credentials_file` is absent. |
| `projects.integrations.confluence.ingestion_enabled` | No | Allows scheduler/manual polling for this provider; default `false`. |
| `projects.integrations.confluence.initial_full_sync` | No | `manual` or `on_start`; default `manual`. |
| `projects.integrations.confluence.incremental_interval` | No | Incremental polling period; default `1m`. |
| `projects.integrations.confluence.empty_poll_sleep` | No | Idle sleep after empty incremental polls; default `10m`. |
| `projects.integrations.confluence.max_idle_sleep` | No | Upper bound for empty-poll sleep; default `30m`. |
| `projects.integrations.confluence.overlap_window` | No | Incremental overlap window; default `2m`. |
| `projects.integrations.confluence.initial_page_size` | No | Initial full-sync page size; default `50`. |
| `projects.integrations.confluence.incremental_page_size` | No | Incremental page size; default `50`. |
| `projects.integrations.confluence.max_results` | No | Per-run result cap. Unset or `0` means poll until provider exhaustion, repeated cursor/token, context cancellation, or provider stop; positive values explicitly cap a run. |
| `projects.integrations.confluence.body_representation` | No | Page body representation flag passed to the provider client; default `storage`. |
| `projects.integrations.confluence.include_body` | No | Ingest configured page body text; default `false`. |
| `projects.integrations.confluence.include_comments` | No | Ingest comments; default `false`. |
| `projects.integrations.confluence.include_labels` | No | Ingest labels; default `false`. |
| `projects.integrations.confluence.include_properties` | No | Ingest properties; default `false`. |
| `projects.integrations.confluence.root_page_ids` | No | Optional configured page ID scope metadata. |
| `projects.integrations.confluence.cql_extra_filter` | No | Extra local polling filter appended to allowlisted space query. |

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
- unsupported `workspace_mode`
- workspace `read_only` or `edit` without `content_graph`
- invalid Go duration strings
- non-loopback `server.http_addr`
- non-positive timeout and request-size values
- negative source/provider coverage caps
- non-positive full scan batch size
- debug subfeatures enabled while `debug.enabled = false`
- invalid SQLite busy timeout or synchronous mode
- missing enabled project roots
- non-directory enabled project roots
- raw credential-like fields in integration config
- enabled Jira integration without `project_keys`
- enabled Confluence integration without `space_keys`
- enabled integration without valid env/file credential references
- integration `credentials_file` combined with email/token refs
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
- `POST /api/v1/projects/{id}/search-index/rebuild`
- `GET /api/v1/projects/{id}/ingestion-runs/{run_id}`
- `GET /api/v1/projects/{id}/ingestion-runs/latest`
- `GET /api/v1/projects/{id}/files`
- `GET /api/v1/projects/{id}/files/{file_id}/chunks`
- `GET /api/v1/projects/{id}/files/{file_id}/outline`
- `GET /api/v1/projects/{id}/symbols`
- `GET /api/v1/projects/{id}/search/text`
- `GET /api/v1/projects/{id}/search/files`
- `GET /api/v1/projects/{id}/search/symbols`
- `GET /api/v1/projects/{id}/search/references`
- `GET /api/v1/projects/{id}/search/calls`
- `GET /api/v1/projects/{id}/search/ast/queries`
- `GET /api/v1/projects/{id}/search/ast`
- `GET /api/v1/projects/{id}/symbols/{symbol_id}/source`
- `GET /api/v1/projects/{id}/symbols/{symbol_id}/references`
- `GET /api/v1/projects/{id}/symbols/{symbol_id}/callers`
- `GET /api/v1/projects/{id}/symbols/{symbol_id}/callees`
- `GET /api/v1/projects/{id}/symbols/{symbol_id}/call-graph`
- `GET /api/v1/projects/{id}/headings`
- `GET /api/v1/projects/{id}/workspace/git/status`
- `GET /api/v1/projects/{id}/workspace/git/diff`
- `GET /api/v1/projects/{id}/workspace/files/read`
- `POST /api/v1/projects/{id}/workspace/files/edit`
- MCP tools: `projects.list`, `projects.get`, `projects.digest`, `projects.ingest`, `projects.search_index.rebuild`, `projects.ingestion_status`, `projects.ingestion_status_latest`, `projects.files.list`, `projects.files.get`, `projects.file.chunks`, `projects.symbols.list`, `projects.search.text`, `projects.search.files`, `projects.search.symbols`, `projects.search.references`, `projects.search.calls`, `projects.search.ast.queries`, `projects.search.ast`, `projects.symbol.source`, `projects.symbol.references`, `projects.symbol.callers`, `projects.symbol.callees`, `projects.symbol.call_graph`, `projects.headings.list`, `projects.file.outline`, `projects.workspace.git_status`, `projects.workspace.git_diff`, `projects.workspace.file_read`, `projects.workspace.file_edit`, `projects.integrations.list`, `projects.integrations.status`, `projects.integrations.poll`, `projects.integrations.poll_status`, `projects.integrations.search`, `projects.jira.issue.get`, `projects.confluence.page.get`
- MCP resources: `mivialabs://projects/{id}`, `mivialabs://projects/{id}/digest-runs/{run_id}`, `mivialabs://projects/{id}/files/{file_id}`, `mivialabs://projects/{id}/files/{file_id}/chunks/{chunk_id}`, `mivialabs://projects/{id}/files/{file_id}/outline`, `mivialabs://projects/{id}/symbols/{symbol_id}`

Project responses omit local root paths and datastore paths by default. Digest runs are manual and metadata-only: graph writes store relative path, extension/language hint, file size, mtime, and a metadata fingerprint. Content graph ingestion stores eligible local source chunks only after all gates pass. Persistent content-graph projects store graph/search data under derived project-scoped paths below the `storage.ladybug_path` parent. SQLite FTS5 rows are maintained for eligible chunks, files, symbols, references, and calls; text search is literal-only and raw FTS syntax is not exposed. AST metadata is promoted for Go, JS, JSX, TS, TSX, C#, Python, Dart/Flutter, Markdown, and lightweight infrastructure/config files. Generated Dart files such as `.g.dart`, `.freezed.dart`, `.mocks.dart`, and similar files are indexed by default unless include/exclude config filters them. Named AST structural search supports Go, Python, JavaScript, JSX, TypeScript, TSX, C#, and Dart through `projects.search.ast.queries` and `projects.search.ast`. TS/JS/TSX/JSX, C#, Python, and Dart parsing is mandatory Tree-sitter; startup validation fails with `extractor_initialization_failed` if a promoted grammar or query cannot initialize.

Project integration MCP tools expose configured provider listing/status, manual one-shot polling, and local graph search/read for Jira and Confluence content. Status tools are redacted metadata only. Polling tools resolve credentials at call time from env/file refs, call Atlassian Cloud only for configured allowlists, and write approved local metadata/content according to provider config. Search/read tools never call Atlassian and return only bounded content already stored in the local graph.

Extractor cache data is stored in SQLite table `project_extractor_cache`. It stores symbols, headings, references, and calls only; it does not store raw source, AST text, chunks, absolute paths, skipped sensitive data, or matched sensitive text. Cache rows are keyed by project, relative-path hash, content hash, extractor name, and extractor version, and are removed when a file becomes skipped or absent. Symbol source responses derive text only from eligible indexed chunks and enforce request/project caps.

Full scans commit graph and FTS writes in bounded windows. `ingestion.full_scan_batch_size` is the hard file-count cap; heavy files can trigger earlier graph/search flushes by write weight, and search writes are split into bounded subtransactions while preserving per-file atomicity. Manual and live ingestion submissions run through the scheduler. Manual submissions and search index repair return queued run metadata without waiting for scan completion; clients should use latest status or poll the returned run ID before trusting indexed data. The scheduler enforces global and per-project worker limits and holds same-project path work behind active or pending project-wide scans.

File listing accepts optional `status`, `extension`, `path_prefix`, `skipped_reason`, `present`, `modified_since`, `page_size`, and `page_token` filters. Extension values may be `go` or `.go`; matching is case-insensitive and invalidates whitespace or path separators.

File outlines return file, heading, symbol, and chunk location metadata by default. Large outlines can be bounded with symbol `kind`, `name_prefix`, `symbol_page_size`, and `symbol_page_token`. Agents can request eligible source context inline with `include_chunk_text=true` and `max_chunk_bytes`; skipped sensitive files still have no chunks to return.

Workspace tools are disabled by default. To use them locally, set `[workspace].enabled = true` and opt a project into `workspace_mode = "read_only"` or `"edit"` with `digest_mode = "content_graph"`. Read-only mode allows governed git status, capped git diff, and current eligible file reads. Edit mode also allows token-guarded exact byte-span edits; clients must first read the current file and use the returned opaque edit token. Successful non-dry-run edits queue path ingestion. There is no arbitrary shell endpoint, public exposure, auth change, provider call, embeddings/vector/crawling path, raw DB query endpoint, raw patch upload endpoint, or git commit/push/checkout/reset/branch/merge/rebase/stash/clean/restore tool.

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
