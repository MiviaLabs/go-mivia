---
name: mivialabs-agent-mcp
description: Use for this repo's localhost MCP server when an agent needs indexed project context, ingestion status, bounded file chunks, or MCP contract verification.
---

# MiviaLabs Agent MCP

Repo-local only. Do not promote globally until the MCP contract and operating pattern are explicitly approved.

## Tool Choice

Use the right source first:

| Need | Use | Do not use |
| --- | --- | --- |
| Code symbols, references, call sites, edit targets | Serena | MCP file chunks as a substitute for semantic navigation |
| Indexed project map, ingestion status, file IDs, bounded chunks, symbol lists | MiviaLabs MCP | Raw DB queries, absolute paths, broad shell scans |
| Git state, unindexed new files, tests, builds, logs, generated artifacts | Shell | MCP as proof of current working-tree state |

If the choice is unclear:

1. Code structure -> Serena.
2. Indexed repo discovery -> MCP.
3. Current disk/git/runtime fact -> shell.

## Required Reads

For normal MCP use:

- `.ai/INDEX.md`
- `.ai/rules/10-security-privacy.md`
- This skill.

For MCP behavior, contract, or API changes also read:

- `api/mcp/agent-control.v1.md`
- `api/openapi/agent-control.v1.yaml`
- `internal/agentcontrol/mcpapi`
- `internal/projectregistry/mcpapi`
- `internal/projectregistry/httpapi`
- `internal/projectingestion`

Do not use Jira or Confluence unless the user explicitly overrides that repo rule in the same request.

## Safe Sequence

Use the smallest sequence that answers the task:

1. Confirm `/mcp` is localhost or loopback.
2. Use `projects.list` or `projects.get` to confirm `enabled`, `digest_mode`, `update_policy`, and `graph_storage`.
3. Use `projects.files.list` or `projects.symbols.list` with small `page_size` to confirm indexed content exists.
4. Use `projects.ingest` only when a manual rescan is needed; then use `projects.ingestion_status` with that returned `run_id`.
5. Use `projects.file.chunks` only after a `file_id` is known; keep `max_chunk_bytes` bounded.
6. Switch to Serena for symbol bodies, references, and edit planning.
7. Switch to shell for tests, diffs, logs, generated files, or anything changed after the last ingestion.

If MCP is down or the project is not indexed, say so and fall back to Serena plus shell. Do not invent MCP facts.

## Tools

Use dotted names when available; Codex-style underscore aliases are accepted.

| Purpose | Tools |
| --- | --- |
| Tasks | `tasks.create`, `tasks.get` |
| Research metadata only | `research_runs.create`, `research_runs.get`, `research_sources.create`, `research_sources.get` |
| Project registry | `projects.list`, `projects.get` |
| Metadata digest | `projects.digest` |
| Content graph | `projects.ingest`, `projects.ingestion_status`, `projects.files.list`, `projects.file.chunks`, `projects.symbols.list` |

Resources:

- `mivialabs://tasks/{id}`
- `mivialabs://research-runs/{id}`
- `mivialabs://research-sources/{id}`
- `mivialabs://projects/{id}`
- `mivialabs://projects/{id}/digest-runs/{run_id}`
- `mivialabs://projects/{id}/files/{file_id}`
- `mivialabs://projects/{id}/files/{file_id}/chunks/{chunk_id}`
- `mivialabs://projects/{id}/symbols/{symbol_id}`

## Raw HTTP Check

Only when no native MCP client is available:

- `POST http://127.0.0.1:<port>/mcp`
- `Content-Type: application/json`
- `Accept: application/json, text/event-stream`
- Optional `MCP-Protocol-Version: 2025-06-18`

Start with `tools/list`, then use tool calls. Do not use raw HTTP to bypass MCP boundaries.

## Hard Boundaries

Never request or expose:

- Absolute roots or datastore paths.
- Raw DB queries or query results.
- Secrets, credentials, tokens, PII, raw prompts, provider payloads.
- Skipped sensitive content or matched sensitive text.
- Public exposure, provider calls, embeddings, vectors, crawling, production deployment, symlink traversal, or auth-model changes.

Stop and report the blocked condition if the workflow requires any of those.
