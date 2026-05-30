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

## Resources

Resource templates:

- `mivialabs://tasks/{id}`
- `mivialabs://research-runs/{id}`

`resources/read` returns `application/json` text content for the requested task or research-run metadata.

## Security And Privacy Constraints

- No raw LadybugDB or SQLite query execution is exposed.
- Raw prompts, source content, fetched provider payloads, secrets, tokens, credentials, and PII are prohibited in requests, responses, fixtures, logs, and stores.
- Research-run create accepts only a redacted `goal_summary`; live provider execution and broad crawling are out of scope.
