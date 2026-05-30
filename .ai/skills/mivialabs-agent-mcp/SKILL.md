---
name: mivialabs-agent-mcp
description: Use with the MiviaLabs localhost MCP server for any indexed project when an agent needs project discovery, ingestion state, bounded file chunks, or symbol lists.
---

# MiviaLabs Agent MCP

Portable skill. It can be copied into any repository indexed by a running MiviaLabs `agent-server`.

## Mandatory Use Gate

When a MiviaLabs MCP server is available for the target project, agents must use it for indexed project context before broad shell scans, manual file walking, or chat-only assumptions.

Mandatory MCP-first surfaces:

- Project discovery, enabled state, digest mode, update policy, and graph storage.
- Ingestion run state, live/manual freshness, skipped reason counts, and rescan status.
- Indexed file discovery, opaque file IDs, file metadata, outlines, headings, symbols, references, call sites, and bounded chunks.
- Any task asking what the indexed project graph knows or whether local content graph ingestion is current.
- Planning and review context that can be answered from indexed files, symbols, references, calls, headings, or chunks.

Do not bypass MCP with raw database queries, absolute root inspection, or broad shell scans when an MCP tool can answer the indexed-context question. Use shell only for current git/disk/runtime facts, tests, build output, logs, generated files, and edits not yet ingested.

Do not use Serena for indexed project discovery, symbol overview/listing, references, call sites, search, bounded source chunks, or planning context when MiviaLabs MCP is available and current.

If MCP is unavailable, stale, missing the project, or lacks the needed semantic operation, state that explicitly, then fall back to Serena plus shell for the minimum evidence needed.

## Inputs

Know or discover:

- MCP endpoint, default `http://127.0.0.1:8080/mcp`.
- Project ID, from the user or `projects.list`.
- Host repository rules, tests, and privacy/security boundaries.

Do not assume the current repository is the server repo. Do not assume any specific language or directory layout.

## Tool Choice

| Need | First choice | Avoid |
| --- | --- | --- |
| Code symbols, references, call sites, edit targets | MiviaLabs MCP when indexed and current | Serena as first resort in indexed MiviaLabs projects |
| Indexed project map, ingestion state, file IDs, chunks, symbols | MiviaLabs MCP | Raw DB queries, absolute paths, broad shell scans |
| Routine indexed text, path, symbol, reference, or call discovery | `projects.search.*` | Serena `search_for_pattern`, raw DB queries, broad shell scans |
| Current git/test/runtime state, builds, logs, generated files | Shell or host tooling | MCP as proof of git or runtime state |

If unclear:

1. Indexed code structure -> MiviaLabs MCP.
2. Indexed project discovery -> MCP.
3. Current local state -> shell.
4. Non-indexed semantic gap -> Serena or host semantic tool, with the fallback stated.

## Safe Sequence

Use the smallest sequence that answers the task:

1. Confirm the MCP endpoint is localhost or loopback.
2. Call `tools/list`.
3. Call `projects.list` or `projects.get` to confirm `enabled`, `digest_mode`, `update_policy`, and `graph_storage`.
4. Call `projects.search.text`, `projects.search.files`, `projects.search.symbols`, `projects.search.references`, or `projects.search.calls` for routine indexed discovery before broad text scans.
5. Call `projects.files.list`, `projects.symbols.list`, or `projects.headings.list` with small `page_size` to confirm indexed content exists and narrow to stable opaque IDs.
6. Call `projects.ingestion_status_latest` before relying on indexed data. If the latest run is missing, failed, stale for the task, or older than current disk changes, call `projects.ingest`.
7. Treat `projects.ingest` as asynchronous. It returns quickly with queued run metadata and a `run_id`; poll `projects.ingestion_status` with that `run_id` until `completed` or `failed`.
8. If search metadata reports `degraded: true`, call `projects.search_index.rebuild` only when the user or task explicitly asks to repair the local search index. Treat the rebuild as asynchronous: it returns queued run metadata and a `run_id`; poll `projects.ingestion_status` with that `run_id` until `completed` or `failed` before relying on search again.
9. Call `projects.files.get` when you need one file's bounded metadata by opaque `file_id`.
10. Call `projects.file.outline` first when file structure is enough. Use `kind`, `name_prefix`, `name_contains`, `symbol_page_size`, and `symbol_page_token` to keep large symbol maps bounded. Use `projects.symbol.references`, `projects.symbol.callers`, `projects.symbol.callees`, and `projects.symbol.call_graph` for common indexed navigation. Use `projects.symbol.source` only when bounded eligible source text for one symbol is needed. Set `include_chunk_text=true` with a small `max_chunk_bytes` when eligible file source context is needed directly in the outline. Call `projects.file.chunks` when separate chunk paging is needed.
11. Switch to Serena or another semantic tool only if MCP cannot answer the required symbol body, reference, call, or edit-planning question.
12. Switch to shell for tests, diffs, logs, and generated files. For edited indexed files, rely on live ingestion as the normal freshness path and poll latest ingestion status when search results look unexpected.

If MCP is down, the project is not listed, or live ingestion cannot provide current indexed context, say so and fall back to Serena or another semantic tool plus shell. Do not invent MCP facts.

## Tools

Use dotted names when available. Codex-style underscore aliases are accepted by the server.

| Purpose | Tools |
| --- | --- |
| Tasks | `tasks.create`, `tasks.get` |
| Research metadata only | `research_runs.create`, `research_runs.get`, `research_sources.create`, `research_sources.get` |
| Project registry | `projects.list`, `projects.get` |
| Metadata digest | `projects.digest` |
| Content graph | `projects.ingest`, `projects.search_index.rebuild`, `projects.ingestion_status`, `projects.ingestion_status_latest`, `projects.files.list`, `projects.files.get`, `projects.file.chunks`, `projects.symbols.list`, `projects.search.text`, `projects.search.files`, `projects.search.symbols`, `projects.search.references`, `projects.search.calls`, `projects.symbol.source`, `projects.symbol.references`, `projects.symbol.callers`, `projects.symbol.callees`, `projects.symbol.call_graph`, `projects.headings.list`, `projects.file.outline` |

## Indexed Metadata Contract

- Promoted AST metadata currently covers Go stdlib AST, Tree-sitter JS/JSX/TS/TSX, Tree-sitter C#, Tree-sitter Python, Markdown headings, and lightweight infrastructure/config metadata.
- TS/JS/TSX/JSX, C#, and Python have no regex fallback. If a promoted grammar or embedded query cannot initialize, server startup fails with `extractor_initialization_failed`.
- Per-file parser failures are file-local `parse_error` skips; full scans continue.
- Extractor cache rows store only symbols, headings, references, and calls keyed by hashes, extractor name, and version. Skipped or absent files must not have cache rows or content hashes.
- Full scans run through bounded graph write batches and the fair scheduler; live path events have priority over full-scan continuation.

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
