# Agent Control MCP Capability Contract

Version: 0.1.0
Protocol target: MCP 2025-06-18 Streamable HTTP
Endpoint: `/mcp`
Classification: Internal; local project-integration rich-content exception only

## Transport

- HTTP `POST /mcp` accepts one JSON-RPC request, notification, or response per request.
- `Accept` must include `application/json` and `text/event-stream`.
- `Content-Type` must be `application/json`.
- `MCP-Protocol-Version` must be `2025-06-18` when present.
- `Origin`, when present, must be localhost or loopback.
- HTTP `GET /mcp` returns 405 until SSE streams are implemented.
- MCP `_meta` fields are accepted in tool-call params and arguments for client compatibility.
- Tool-call `arguments` may be an object or a JSON-encoded object string.
- The server accepts both dotted tool names, for example `tasks.create`, and Codex-style underscore aliases, for example `tasks_create`.

## Tools

### `tasks.create`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["title"],
  "properties": {
    "title": { "type": "string", "minLength": 1, "maxLength": 200 }
  }
}
```

Output: structured `Task` JSON plus a JSON text content block.

### `tasks.get`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: structured `Task` JSON plus a JSON text content block.

### `research_runs.create`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["task_id", "goal_summary"],
  "properties": {
    "task_id": { "type": "string", "minLength": 1 },
    "goal_summary": { "type": "string", "minLength": 1, "maxLength": 500 }
  }
}
```

Output: structured `ResearchRun` metadata plus a JSON text content block.

### `research_runs.get`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: structured `ResearchRun` metadata plus a JSON text content block.

### `research_sources.create`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["research_run_id", "artifact_ref", "source_type", "summary"],
  "properties": {
    "research_run_id": { "type": "string", "minLength": 1 },
    "artifact_ref": { "type": "string", "minLength": 1 },
    "source_type": { "type": "string", "minLength": 1 },
    "summary": { "type": "string", "minLength": 1 }
  }
}
```

Output: structured redacted `ResearchSource` metadata plus a JSON text content block.

### `research_sources.get`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: structured redacted `ResearchSource` metadata plus a JSON text content block.

### `projects.list`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": [],
  "properties": {}
}
```

Output: configured local project metadata without root paths, include/exclude patterns, raw source content, or file-content hashes.

### `projects.get`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: one configured local project metadata object without local root path exposure.

### `projects.digest`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": [],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "project_id": { "type": "string", "minLength": 1 }
  }
}
```

Output: metadata-only digest run counts and status. Pass either `id` or `project_id`; `id` remains the preferred contract. The digest stores file metadata and metadata fingerprints only; raw source content and file-content hashes are not stored or returned.

### Workspace Tools

Workspace tools are available only when `[workspace].enabled = true` and the target project has `workspace_mode = "read_only"` or `"edit"` with `digest_mode = "content_graph"`. They never expose roots, datastore paths, raw command lines, raw stderr, content hashes, skipped sensitive content, secrets, PII, raw prompts, provider payloads, raw parser/SQLite/FTS errors, or stack traces.

- `projects.workspace.git_status` / `projects_workspace_git_status`: parsed git status with `id`, optional `include_untracked`, `path_prefix`, `page_size`, and `page_token`.
- `projects.workspace.git_diff` / `projects_workspace_git_diff`: capped safe diff with `id`, optional `scope` (`working_tree`, `staged`, `head`), one optional file selector, `path_prefix`, `context_lines`, `max_diff_bytes`, and `page_token`.
- `projects.workspace.file_read` / `projects_workspace_file_read`: current eligible file text by `file_id` or `relative_path`, capped by `max_bytes`, with an opaque edit token.
- `projects.workspace.file_edit` / `projects_workspace_file_edit`: `workspace_mode = "edit"` only; applies ordered exact byte-span edits with `edit_token`, `old_text`, and `new_text`. Successful non-dry-run edits queue path ingestion.

No workspace tool executes arbitrary shell commands, accepts raw patches, or performs git commit, push, checkout, reset, branch, merge, rebase, stash, clean, or restore operations. Shell remains required for tests, builds, logs, process control, arbitrary commands, generated-file verification, and non-opted-in repositories.

### Project Integration Tools

Project integration tools are available for configured Jira Cloud and Confluence Cloud providers only. They are local, polling-backed, and use local SQLite/LadybugDB state. Status responses are redacted. Search/read responses return only locally ingested, bounded graph content and never call Atlassian or resolve credentials.

Integration status responses omit raw site URLs, raw allowlists, env var names, file paths, credentials, auth headers, local roots, raw provider payloads, and raw cursor values. Local rich-content search/read responses may include approved Jira/Confluence content and PII under [Project Integrations Security Policy](../../docs/security/project-integrations.md), but still omit credentials, auth headers, raw provider payload blobs, local roots, datastore paths, and credential refs.

### `projects.integrations.list`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: configured provider summaries for one local project, including provider name, enabled flag, auth mode, credential source type, allowlist kind/count, ingestion flag, and polling interval. Raw allowlist values and credential refs are not returned.

### `projects.integrations.status`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "provider"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "provider": { "type": "string", "enum": ["jira", "confluence"] }
  }
}
```

Output: redacted config-derived provider status plus local source/sync metadata when available. Cursor presence may be reported as a boolean, but raw cursors are not returned.

### `projects.integrations.poll`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "provider"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "provider": { "type": "string", "enum": ["jira", "confluence"] },
    "kind": { "type": "string", "enum": ["initial_full", "incremental"] }
  }
}
```

Output: redacted run metadata and sync state after one manual provider poll. The tool uses configured env/file credential refs at execution time, but does not return credentials, credential refs, raw provider payloads, raw cursors, raw roots, or datastore paths.

### `projects.integrations.search`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "query"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "provider": { "type": "string", "enum": ["jira", "confluence"] },
    "query": { "type": "string", "minLength": 1 },
    "max_results": { "type": "integer", "minimum": 1, "maximum": 50 },
    "max_snippet_bytes": { "type": "integer", "minimum": 1, "maximum": 4096 },
    "case_sensitive": { "type": "boolean" }
  }
}
```

Output: bounded local graph matches across locally ingested integration chunks. The response includes artifact/chunk metadata and capped snippets only. No remote provider call is made.

### `projects.jira.issue.get`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "key"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "key": { "type": "string", "minLength": 1 },
    "max_chunk_bytes": { "type": "integer", "minimum": 1, "maximum": 16384 }
  }
}
```

Output: one locally ingested Jira issue artifact and bounded chunks by issue key or ID. The tool reads local graph state only.

### `projects.confluence.page.get`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "page_id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "page_id": { "type": "string", "minLength": 1 },
    "max_chunk_bytes": { "type": "integer", "minimum": 1, "maximum": 16384 }
  }
}
```

Output: one locally ingested Confluence page artifact and bounded chunks by page ID. The tool reads local graph state only.

### `projects.ingest`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: queued manual `content_graph` ingestion run metadata for an opted-in local project. The tool submits work through the scheduler and returns quickly with a `run_id`; poll `projects.ingestion_status` or call `projects.ingestion_status_latest` before relying on indexed data. The response does not include absolute roots, source-content hashes, skipped sensitive content, matched sensitive text, secrets, PII, raw prompts, or provider payloads.

### `projects.search_index.rebuild`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: queued repair `content_graph` ingestion run metadata for the opted-in project. The tool submits local SQLite FTS repair through the scheduler, returns quickly with a `run_id`, and callers must poll `projects.ingestion_status` or call `projects.ingestion_status_latest` before relying on search results. The tool does not expose raw database queries, absolute roots, content hashes, skipped sensitive content, matched sensitive text, secrets, PII, raw prompts, provider payloads, or raw SQLite/FTS errors.

### `projects.ingestion_status`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "run_id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "run_id": { "type": "string", "minLength": 1 }
  }
}
```

Output: non-sensitive ingestion run metadata.

### `projects.ingestion_status_latest`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: latest non-sensitive ingestion run metadata for the project: run ID, status, trigger, counts, reason counts, timestamps, and error category only.

### `projects.files.list`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "status": { "type": "string", "enum": ["eligible", "skipped", "absent"] },
    "extension": { "type": "string", "minLength": 1 },
    "page_size": { "type": "integer", "minimum": 1, "maximum": 100 },
    "page_token": { "type": "string" }
  }
}
```

Output: bounded file metadata using stable opaque `file_id` values. `extension` accepts values with or without a leading dot, matches case-insensitively, and rejects whitespace or path separators. Sensitive skips return reason codes only and omit relative paths.

### `projects.files.get`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "file_id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "file_id": { "type": "string", "minLength": 1 }
  }
}
```

Output: bounded file metadata for one opaque file id. Safe relative paths include a normalized lowercase `extension` field. Sensitive skips omit relative paths, extensions, content hashes, skipped sensitive content, and matched sensitive text.

### `projects.file.chunks`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "file_id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "file_id": { "type": "string", "minLength": 1 },
    "page_size": { "type": "integer", "minimum": 1, "maximum": 100 },
    "page_token": { "type": "string" },
    "max_chunk_bytes": { "type": "integer", "minimum": 1 }
  }
}
```

Output: bounded eligible chunk text for one opaque file id. Skipped sensitive files have no chunks.

### `projects.symbols.list`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "page_size": { "type": "integer", "minimum": 1, "maximum": 100 },
    "page_token": { "type": "string" }
  }
}
```

Output: bounded symbol metadata for an opted-in content graph project. Supports `name_prefix`, `name_contains`, `extension`, `package`, `receiver`, and `case_sensitive`.

### `projects.search.text`

Input schema: `id`, required `query`, optional `mode` (`literal` only), `case_sensitive`, `extension`, `path_prefix`, `page_size`, `page_token`, `max_snippet_bytes`, and `max_matches`.

Output: deterministic paginated matches from eligible indexed `ContentChunk` nodes only. Each result includes safe file metadata, chunk location metadata with no full chunk text, match span, and a capped snippet. Results may be stale until ingestion catches up. Skipped, denied, sensitive, absent, and unindexed files are excluded.

### `projects.search.files`

Input schema: `id`, optional `extension`, `path_prefix`, `path_contains`, `case_sensitive`, `page_size`, and `page_token`.

Output: eligible indexed file metadata only. Absolute roots, skipped sensitive paths, content hashes, absent files, and denied files are not returned.

### `projects.search.symbols`

Input schema: `id`, optional `kind`, `name_prefix`, `name_contains`, `file_id`, `extension`, `package`, `receiver`, `case_sensitive`, `page_size`, and `page_token`.

Output: bounded symbol metadata from eligible indexed files. Use this before broad text pattern searches when symbol names are enough.

### `projects.search.references`

Input schema: `id`, optional `name_contains`, `target_name_contains`, `enclosing_contains`, `extension`, `path_prefix`, `resolution_status`, `confidence`, `case_sensitive`, `page_size`, and `page_token`.

Output: bounded indexed reference metadata. No source text, roots, content hashes, skipped sensitive content, raw parser errors, or raw datastore details are returned.

### `projects.search.calls`

Input schema: `id`, optional `name_contains`, `caller_name_contains`, `callee_name_contains`, `extension`, `path_prefix`, `resolution_status`, `confidence`, `case_sensitive`, `page_size`, and `page_token`.

Output: bounded indexed call metadata, including unresolved call nodes where available. No source text, roots, content hashes, skipped sensitive content, raw parser errors, or raw datastore details are returned.

### `projects.search.ast.queries`

Input schema: `id`.

Output: supported named AST query catalog entries for the project surface. Each entry includes query ID, language, supported capture names, query version, and matching file extensions. The response also includes safe per-language coverage counters scoped to `file_too_large`. Raw Tree-sitter query text is not returned, and raw Tree-sitter query syntax is not accepted by the search surface. Sensitive, denied, absent, parse-error, and other skipped files are not catalog inputs. Oversized files are represented only as safe coverage gaps through file/ingestion metadata such as `skipped_reason=file_too_large`, size, and reason counts; source text, chunks, snippets, content hashes, roots, skipped sensitive text, raw parser/SQLite/FTS/Tree-sitter errors, raw prompts, provider payloads, secrets, and PII are not returned.

### `projects.search.ast`

Input schema: `id`, required `language` (`go`, `python`, `javascript`, `jsx`, `typescript`, `tsx`, `csharp`), required `query` named catalog id, optional `captures`, `extension`, `path_prefix`, `page_size`, `page_token`, `max_matches`, and `max_snippet_bytes`.

Named query ids: `function_declarations`, `class_declarations`, `type_declarations`, `call_expressions`, `imports`, `test_functions`, `assignments`, and `error_handling` where supported by the language.

Output: bounded capture results from eligible indexed chunks only, including safe file metadata, chunk location metadata without full chunk text, capture name/text, span, capped snippet, `query_language`, `query_version`, `result_truncated`, `coverage`, and search index metadata. Raw Tree-sitter query syntax is not accepted. Sensitive, denied, absent, parse-error, oversized, and other skipped files are unreachable from AST search. Oversized files may appear only as `file_too_large` coverage counts, file metadata with `skipped_reason=file_too_large`, and ingestion reason counts. Roots, content hashes, skipped sensitive text, raw parser/Tree-sitter errors, raw SQLite/FTS errors, raw prompts, provider payloads, secrets, and PII are not returned.

### `projects.symbol.source`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "symbol_id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "symbol_id": { "type": "string", "minLength": 1 },
    "max_source_bytes": { "type": "integer", "minimum": 1 }
  }
}
```

Output: bounded source text for one eligible indexed symbol. Text is derived only from eligible stored chunks and capped by `max_source_bytes` and project limits.

### `projects.symbol.references`

Input schema: `id`, `symbol_id`, optional `page_size`, and optional `page_token`.

Output: bounded reference metadata resolved to the requested symbol. Source text, roots, content hashes, skipped sensitive content, and matched sensitive text are not returned.

### `projects.symbol.callers`

Input schema: `id`, `symbol_id`, optional `page_size`, and optional `page_token`.

Output: bounded direct caller edges for the requested symbol.

### `projects.symbol.callees`

Input schema: `id`, `symbol_id`, optional `page_size`, and optional `page_token`.

Output: bounded direct callee edges for the requested symbol.

### `projects.symbol.call_graph`

Input schema: `id`, `symbol_id`, optional `direction` (`callers`, `callees`, `both`), optional `max_depth` (`1..5`), and optional `max_nodes` (`1..100`).

Output: bounded call graph nodes and edges with `resolution_status` and confidence metadata. Unresolved dynamic-language cases are represented as metadata, not guessed edges.

### `projects.file.outline`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "file_id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "file_id": { "type": "string", "minLength": 1 },
    "kind": { "type": "string" },
    "name_prefix": { "type": "string" },
    "symbol_page_size": { "type": "integer", "minimum": 1, "maximum": 100 },
    "symbol_page_token": { "type": "string" },
    "include_chunk_text": { "type": "boolean" },
    "max_chunk_bytes": { "type": "integer", "minimum": 1 }
  }
}
```

Output: bounded file metadata, headings, symbols, symbol pagination token, and chunk IDs/line ranges. By default, outline chunks omit text. When `include_chunk_text` is true, outline chunks may include eligible stored chunk text truncated by `max_chunk_bytes` and project caps. Skipped sensitive files, matched sensitive text, raw AST node text, absolute roots, and raw local config values are never returned.

## Resources

Resource templates:

- `mivialabs://tasks/{id}`
- `mivialabs://research-runs/{id}`
- `mivialabs://research-sources/{id}`
- `mivialabs://projects/{id}`
- `mivialabs://projects/{id}/digest-runs/{run_id}`
- `mivialabs://projects/{id}/files/{file_id}`
- `mivialabs://projects/{id}/files/{file_id}/chunks/{chunk_id}`
- `mivialabs://projects/{id}/files/{file_id}/outline`
- `mivialabs://projects/{id}/symbols/{symbol_id}`

`resources/read` returns `application/json` text content for the requested task, research-run, research-source, project, project-digest-run, project-file, project-file-chunk, or project-symbol metadata.

## Codex Desktop Registration

Run the server locally, then register:

```powershell
codex mcp add mivialabs-agent-server --url http://127.0.0.1:8080/mcp
codex mcp get mivialabs-agent-server
```

Codex Desktop exposes the tools through generated callable names. In this environment, `tasks.create` appeared as `tasks_create` and was verified through native Codex MCP invocation.

## Security And Privacy Constraints

- No raw LadybugDB or SQLite query execution is exposed.
- Raw prompts, skipped sensitive source content, fetched provider payload blobs, secrets, tokens, and credentials are prohibited in requests, responses, fixtures, logs, and stores.
- Approved local Jira/Confluence rich content, including possible PII, is allowed only under [Project Integrations Security Policy](../../docs/security/project-integrations.md), in ignored local stores, through bounded local MCP search/read responses.
- Research-run create accepts only a redacted `goal_summary`; live provider execution and broad crawling are out of scope.
- Project responses omit local root paths by default.
- Project responses include `graph_storage` as `persistent` or `in_memory`; they do not expose datastore paths.
- Project digest is manual and metadata-only; it does not store or return raw source content or file-content hashes.
- Content graph ingestion and query tools are localhost-only, manually triggered, bounded by pagination and chunk-byte caps, and use stable opaque IDs instead of absolute roots.
- Ingestion query responses must not return skipped sensitive content, matched sensitive-marker text, secrets, PII, raw prompts, provider payloads, raw database query results, or absolute roots.
