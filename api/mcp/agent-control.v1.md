# Agent Control MCP Capability Contract

Version: 0.1.0
Protocol target: MCP 2025-06-18 Streamable HTTP
Endpoint: `/mcp`
Classification: Internal; PII-prohibited

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
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: metadata-only digest run counts and status. The digest stores file metadata and metadata fingerprints only; raw source content and file-content hashes are not stored or returned.

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

Output: manual `content_graph` ingestion run metadata for an opted-in local project. The response does not include absolute roots, source-content hashes, skipped sensitive content, matched sensitive text, secrets, PII, raw prompts, or provider payloads.

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
    "page_size": { "type": "integer", "minimum": 1, "maximum": 100 },
    "page_token": { "type": "string" }
  }
}
```

Output: bounded file metadata using stable opaque `file_id` values. Sensitive skips return reason codes only and omit relative paths.

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

Output: bounded symbol metadata for an opted-in content graph project.

## Resources

Resource templates:

- `mivialabs://tasks/{id}`
- `mivialabs://research-runs/{id}`
- `mivialabs://research-sources/{id}`
- `mivialabs://projects/{id}`
- `mivialabs://projects/{id}/digest-runs/{run_id}`
- `mivialabs://projects/{id}/files/{file_id}`
- `mivialabs://projects/{id}/files/{file_id}/chunks/{chunk_id}`
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
- Raw prompts, skipped sensitive source content, fetched provider payloads, secrets, tokens, credentials, and PII are prohibited in requests, responses, fixtures, logs, and stores.
- Research-run create accepts only a redacted `goal_summary`; live provider execution and broad crawling are out of scope.
- Project responses omit local root paths by default.
- Project responses include `graph_storage` as `persistent` or `in_memory`; they do not expose datastore paths.
- Project digest is manual and metadata-only; it does not store or return raw source content or file-content hashes.
- Content graph ingestion and query tools are localhost-only, manually triggered, bounded by pagination and chunk-byte caps, and use stable opaque IDs instead of absolute roots.
- Ingestion query responses must not return skipped sensitive content, matched sensitive-marker text, secrets, PII, raw prompts, provider payloads, raw database query results, or absolute roots.
