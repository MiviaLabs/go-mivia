---
name: mivia-mcp
description: Use with the Mivia localhost MCP server for any indexed project when an agent needs project discovery, ingestion state, search, bounded chunks, symbol navigation, call graph, named AST discovery, governed git status/diff, current eligible file reads, exact token-guarded edits, or locally ingested Jira/Confluence context.
---

# Mivia Agent MCP

Portable skill. It can be copied into any repository indexed by a running Mivia `mivia-server`.

## Mandatory Use Gate

When a Mivia MCP server is available for the target project, agents must use it before broad shell scans, manual file walking, Serena indexed-context discovery, or chat-only assumptions for every capability listed below.

Mandatory MCP-first surfaces:

- Project discovery, enabled state, digest mode, update policy, workspace mode, and graph storage.
- Ingestion run state, live/manual freshness, skipped reason counts, and rescan status.
- Indexed file discovery, opaque file IDs, file metadata, outlines, headings, symbols, references, call sites, and bounded chunks.
- Governed workspace git status/diff, current eligible file reads, and token-guarded exact edits when `[workspace].enabled = true` and the project is opted in.
- Configured Jira/Confluence integration provider listing/status, async manual poll submission/status, and local integration graph search/read.
- Any task asking what the indexed project graph knows or whether local content graph ingestion is current.
- Planning and review context that can be answered from indexed files, symbols, references, calls, headings, or chunks.

Do not bypass MCP with raw database queries, absolute root inspection, broad shell scans, ad hoc `git status`/`git diff`, or direct file reads/edits when an MCP tool can answer or perform the operation. Use shell only for tests, build output, logs, process control, generated-file verification, arbitrary commands outside the MCP contract, non-opted-in repositories, and files not yet eligible or allowed by MCP.

Do not use Serena for indexed project discovery, symbol overview/listing, references, call sites, search, bounded source chunks, or planning context when Mivia MCP is available and current.

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
| Code symbols, references, call sites, edit targets | Mivia MCP when indexed and current | Serena as first resort in indexed Mivia projects |
| Indexed project map, ingestion state, file IDs, chunks, symbols | Mivia MCP | Raw DB queries, absolute paths, broad shell scans |
| Routine indexed text, path, symbol, reference, call, named AST discovery, or AST query-catalog discovery | `projects.search.*` | Serena `search_for_pattern`, raw DB queries, broad shell scans |
| Governed git status/diff for opted-in projects | MCP workspace tools | Broad shell scans as first resort |
| Configured Jira/Confluence status, poll, search, or read | Mivia MCP integration tools | Jira/Confluence connectors, provider dashboards, live Atlassian reads during local search/read |
| Current tests/runtime state, builds, logs, generated files, process control, arbitrary commands, non-opted-in repos | Shell or host tooling | MCP as proof of those runtime facts |

If unclear:

1. Indexed code structure -> Mivia MCP.
2. Indexed project discovery -> MCP.
3. Governed git status/diff/read/edit for opted-in projects -> MCP workspace tools.
4. Local Jira/Confluence context -> MCP integration tools.
5. Tests, builds, logs, process control, generated files, arbitrary commands, or non-opted-in repos -> shell.
6. Non-indexed semantic gap -> Serena or host semantic tool, with the fallback stated.

## Safe Sequence

Use the smallest sequence that answers the task:

1. Confirm the MCP endpoint is localhost or loopback.
2. Call `tools/list`.
3. Call `projects.list` or `projects.get` to confirm `enabled`, `digest_mode`, `update_policy`, and `graph_storage`.
4. Call `projects.search.text`, `projects.search.files`, `projects.search.symbols`, `projects.search.references`, `projects.search.calls`, `projects.search.ast.queries`, or `projects.search.ast` for routine indexed discovery before broad text scans.
5. Call `projects.files.list`, `projects.symbols.list`, or `projects.headings.list` with small `page_size` to confirm indexed content exists and narrow to stable opaque IDs.
6. Call `projects.ingestion_status_latest` before relying on indexed data. If the latest run is missing, failed, stale for the task, or older than current disk changes, call `projects.ingest`.
7. Treat `projects.ingest` as asynchronous. It returns quickly with queued run metadata and a `run_id`; poll `projects.ingestion_status` with that `run_id` until `completed` or `failed`.
   - A `pending` or `running` run from before the current server process is an interrupted local queue entry, not active work. Current server builds fail interrupted runs on startup with `error_category=server_restarted`; restart onto a current build before trusting a long-pending zero-file run.
8. If search metadata reports `degraded: true`, call `projects.search_index.rebuild` only when the user or task explicitly asks to repair the local search index. Treat the rebuild as asynchronous: it returns queued run metadata and a `run_id`; poll `projects.ingestion_status` with that `run_id` until `completed` or `failed` before relying on search again.
9. Call `projects.files.get` when you need one file's bounded metadata by opaque `file_id`.
10. Call `projects.file.outline` first when file structure is enough. Use `kind`, `name_prefix`, `symbol_page_size`, and `symbol_page_token` to keep large symbol maps bounded. Use `projects.symbol.references`, `projects.symbol.callers`, `projects.symbol.callees`, and `projects.symbol.call_graph` for common indexed navigation. Use `projects.symbol.source` only when bounded eligible source text for one symbol is needed. Set `include_chunk_text=true` with a small `max_chunk_bytes` when eligible file source context is needed directly in the outline. Call `projects.file.chunks` when separate chunk paging is needed.
11. For configured Jira/Confluence context, call `projects.integrations.list` or `projects.integrations.status` first. Use `projects.integrations.poll` to queue a manual provider run and then poll `projects.integrations.poll_status` with the returned `run_id`; `projects.integrations.poll` is asynchronous. Use `projects.integrations.search`, `projects.jira.issue.get`, and `projects.confluence.page.get` only for already-ingested local graph content. Search/read tools do not call Atlassian or resolve credentials.
12. Switch to Serena or another semantic tool only if MCP cannot answer the required symbol body, reference, call, or edit-planning question.
13. For opted-in workspaces, use `projects.workspace.git_status`, `projects.workspace.git_diff`, `projects.workspace.file_read`, and `projects.workspace.file_edit` before shell for status, diff, eligible current file reads, and exact edits. `file_edit` requires the opaque token from a current file read and queues path ingestion after successful non-dry-run edits.
14. Switch to shell for tests, builds, logs, generated files, process control, arbitrary commands, and non-opted-in repos. For edited indexed files, rely on live ingestion as the normal freshness path and poll latest ingestion status when search results look unexpected.

If MCP is down, the project is not listed, or live ingestion cannot provide current indexed context, say so and fall back to Serena or another semantic tool plus shell. Do not invent MCP facts.

## Tools

Use dotted names when available. Codex-style underscore aliases are accepted by the server.

| Purpose | Tools |
| --- | --- |
| Tasks | `tasks.create`, `tasks.get` |
| Research metadata only | `research_runs.create`, `research_runs.get`, `research_sources.create`, `research_sources.get` |
| Project registry | `projects.list`, `projects.get` |
| Metadata digest | `projects.digest` |
| Content graph | `projects.ingest`, `projects.search_index.rebuild`, `projects.ingestion_status`, `projects.ingestion_status_latest`, `projects.files.list`, `projects.files.get`, `projects.file.chunks`, `projects.symbols.list`, `projects.search.text`, `projects.search.files`, `projects.search.symbols`, `projects.search.references`, `projects.search.calls`, `projects.search.ast.queries`, `projects.search.ast`, `projects.symbol.source`, `projects.symbol.references`, `projects.symbol.callers`, `projects.symbol.callees`, `projects.symbol.call_graph`, `projects.headings.list`, `projects.file.outline` |
| Governed workspace | `projects.workspace.git_status`, `projects.workspace.git_diff`, `projects.workspace.file_read`, `projects.workspace.file_edit` plus underscore aliases |
| Project integrations | `projects.integrations.list`, `projects.integrations.status`, `projects.integrations.poll`, `projects.integrations.poll_status`, `projects.integrations.search`, `projects.jira.issue.get`, `projects.confluence.page.get` |

## Indexed Metadata Contract

- Promoted AST metadata currently covers Go stdlib AST, Tree-sitter JS/JSX/TS/TSX, Tree-sitter C#, Tree-sitter Python, Markdown headings, and lightweight infrastructure/config metadata.
- `projects.search.ast.queries` returns the supported named AST query catalog: query IDs, languages, capture names, query versions, matching file extensions, and safe per-language coverage counts. It does not return raw Tree-sitter query text.
- `projects.search.ast` runs named Tree-sitter structural queries over eligible indexed chunks for Go, Python, JavaScript, JSX, TypeScript, TSX, and C#. It accepts catalog IDs such as `function_declarations`, `class_declarations`, `call_expressions`, `imports`, `test_functions`, `assignments`, and `error_handling`; it does not accept raw Tree-sitter query syntax.
- Sensitive, denied, absent, parse-error, and other skipped files are unreachable from AST search. Oversized files are reported only as safe coverage gaps through ingestion/file metadata such as `skipped_reason=file_too_large`, size, and counts; their source text, chunks, snippets, content hashes, raw parser/SQLite/FTS/Tree-sitter errors, roots, secrets, PII, raw prompts, and provider payloads are not exposed.
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
- `mivialabs://projects/{id}/files/{file_id}/outline`
- `mivialabs://projects/{id}/symbols/{symbol_id}`

## Workspace Boundary

Workspace tools are default-disabled and require both global `[workspace].enabled = true` and per-project `workspace_mode = "read_only"` or `"edit"` with `digest_mode = "content_graph"`. `read_only` allows governed git status/diff and current eligible file reads. `edit` additionally allows exact byte-span edits guarded by an opaque per-process token from `projects.workspace.file_read`. There is no arbitrary shell endpoint, public exposure, auth change, provider call, embedding/vector/crawling path, raw DB query endpoint, raw patch upload endpoint, or git commit/push/checkout/reset/branch/merge/rebase/stash/clean/restore tool.

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
- Secrets, credentials, tokens, raw prompts, or raw provider payload blobs.
- PII, except owner-approved Jira/Confluence rich content returned through bounded local integration search/read under the project integration policy.
- Skipped sensitive content or matched sensitive text.
- Public exposure, embeddings, vectors, crawling, production deployment, symlink traversal, or auth-model changes.
- Provider calls, except configured local integration polling through `projects.integrations.poll`.

Stop and report the blocked condition if the workflow requires any of those.

## Project Integration Boundary

Project integration tools cover configured Jira Cloud and Confluence Cloud providers only. They are local, polling-backed, and configured per project. Status responses are redacted and must omit raw site URLs, raw allowlists, env var names, file paths, credentials, auth headers, local roots, raw provider payloads, and raw cursor values.

Polling:

- `projects.integrations.poll` accepts `id`, `provider` (`jira` or `confluence`), and optional `kind` (`initial_full` or `incremental`).
- It returns queued run metadata with a `run_id`; always use `projects.integrations.poll_status` or `projects.integrations.status` before relying on new data.
- The background run may call Atlassian Cloud using configured env/file credential refs at execution time. The response must not expose credentials, credential refs, raw provider payloads, raw cursors, roots, or datastore paths.

Local graph search/read:

- `projects.integrations.search` searches already-ingested local integration chunks only.
- `projects.jira.issue.get` reads one locally ingested Jira issue by key or ID.
- `projects.confluence.page.get` reads one locally ingested Confluence page by page ID.
- These search/read tools do not call Atlassian and must return only bounded local graph content.
