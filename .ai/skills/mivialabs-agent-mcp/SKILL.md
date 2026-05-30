---
name: mivialabs-agent-mcp
description: Use with the MiviaLabs localhost MCP server for any indexed project when an agent needs project discovery, ingestion state, bounded file chunks, or symbol lists.
---

# MiviaLabs Agent MCP

Portable skill. It can be copied into any repository indexed by a running MiviaLabs `agent-server`.

## Inputs

Know or discover:

- MCP endpoint, default `http://127.0.0.1:8080/mcp`.
- Project ID, from the user or `projects.list`.
- Host repository rules, tests, and privacy/security boundaries.

Do not assume the current repository is the server repo. Do not assume any specific language or directory layout.

## Tool Choice

| Need | First choice | Avoid |
| --- | --- | --- |
| Code symbols, references, call sites, edit targets | Serena or host semantic tool | MCP chunks as a substitute for semantic navigation |
| Indexed project map, ingestion state, file IDs, chunks, symbols | MiviaLabs MCP | Raw DB queries, absolute paths, broad shell scans |
| Current git/disk/runtime state, tests, builds, logs, unindexed edits | Shell or host tooling | MCP as proof of current working-tree state |

If unclear:

1. Code structure -> Serena or host semantic tool.
2. Indexed project discovery -> MCP.
3. Current local state -> shell.

## Safe Sequence

Use the smallest sequence that answers the task:

1. Confirm the MCP endpoint is localhost or loopback.
2. Call `tools/list`.
3. Call `projects.list` or `projects.get` to confirm `enabled`, `digest_mode`, `update_policy`, and `graph_storage`.
4. Call `projects.files.list` or `projects.symbols.list` with small `page_size` to confirm indexed content exists and narrow to stable opaque IDs.
5. Call `projects.ingest` only when a manual rescan is needed; then call `projects.ingestion_status` with the returned `run_id`.
6. Call `projects.files.get` when you need one file's bounded metadata by opaque `file_id`.
7. Call `projects.file.chunks` only after a `file_id` is known; keep `page_size` and `max_chunk_bytes` bounded.
8. Switch to semantic tools for symbol bodies, references, and edit planning.
9. Switch to shell for tests, diffs, logs, generated files, and anything changed after the last ingestion.

If MCP is down, the project is not listed, or indexed content is stale for the task, say so and fall back to semantic tools plus shell. Do not invent MCP facts.

## Tools

Use dotted names when available. Codex-style underscore aliases are accepted by the server.

| Purpose | Tools |
| --- | --- |
| Tasks | `tasks.create`, `tasks.get` |
| Research metadata only | `research_runs.create`, `research_runs.get`, `research_sources.create`, `research_sources.get` |
| Project registry | `projects.list`, `projects.get` |
| Metadata digest | `projects.digest` |
| Content graph | `projects.ingest`, `projects.ingestion_status`, `projects.files.list`, `projects.files.get`, `projects.file.chunks`, `projects.symbols.list` |

Resources:

- `mivialabs://tasks/{id}`
- `mivialabs://research-runs/{id}`
- `mivialabs://research-sources/{id}`
- `mivialabs://projects/{id}`
- `mivialabs://projects/{id}/digest-runs/{run_id}`
- `mivialabs://projects/{id}/files/{file_id}`
- `mivialabs://projects/{id}/files/{file_id}/chunks/{chunk_id}`
- `mivialabs://projects/{id}/symbols/{symbol_id}`

## Raw HTTP Fallback

Use raw HTTP only when no native MCP client is available:

- `POST http://127.0.0.1:<port>/mcp`
- `Content-Type: application/json`
- `Accept: application/json, text/event-stream`
- Optional `MCP-Protocol-Version: 2025-06-18`

Start with `tools/list`, then use `tools/call`. Do not use raw HTTP to bypass MCP boundaries.

## A/B Agent Tests

When measuring MCP impact:

1. Create two clean worktrees from the same commit.
2. Give both agents the same task and acceptance criteria.
3. MCP run: require `tools/list`, `projects.get`, and small `projects.files.list` or `projects.symbols.list` before broad shell reads.
4. Non-MCP run: forbid MCP and raw `/mcp` HTTP.
5. Require each agent to save a run log with elapsed time, tool calls, files changed, diff stats, tests run, and failures.
6. Save the evaluator report in the host repo's test-report location; if none exists, use `docs/reports/tests/`.
7. Do not let either implementation agent review the other implementation.

## Hard Boundaries

Never request, store, or expose:

- Absolute roots or datastore paths.
- Raw DB queries or raw query results.
- Secrets, credentials, tokens, PII, raw prompts, or provider payloads.
- Skipped sensitive content or matched sensitive text.
- Public exposure, provider calls, embeddings, vectors, crawling, production deployment, symlink traversal, or auth-model changes.

Stop and report the blocked condition if the workflow requires any of those.
