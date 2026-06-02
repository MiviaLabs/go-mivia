---
name: mivia-mcp
description: Use with the Mivia localhost MCP server for any indexed project when an agent needs project discovery, ingestion state, context health, impact analysis, context packs, stale-claim checks, search, bounded chunks, symbol navigation, call graph, named AST discovery, governed git status/diff, current eligible file reads, exact token-guarded edits, redacted agent-run metadata, promotion-gate decisions, or locally ingested Jira/Confluence context.
---

# Mivia Agent MCP

Portable skill. It can be copied into any repository indexed by a running Mivia `mivia-server`.

## MCP-First Routing

When a Mivia MCP server is available for the target project, use it as the first choice for indexed project discovery and bounded context. Keep the MCP call set proportional to the task; do not run reliability or handoff tools by default when a smaller read/search/status call answers the question.

Review and implementation guidance:

- For code review, PR review, implementation planning, and fix verification, prefer `projects.list` -> `projects.get` -> `projects.graph_status` or `projects.context_health` when freshness affects the answer. Do not use `projects.ingestion_status_latest` alone to decide whether indexed MCP context is usable; it is one run record, not the authoritative graph inventory.
- If `projects.graph_status.status` / `projects.context_health.status` is not `ready`, state the status and freshness gap only when the answer relies on indexed freshness. Treat `syncing` as normal active indexing, not corruption. If `indexed_content_available=true`, indexed MCP tools remain usable while ingestion catches up.
- For changed-path review, use `projects.impact.analyze` when blast radius is unclear, the change is security/privacy/API-sensitive, or the user asks for review/audit confidence. If the result is partial with `index_syncing`, treat it as active indexing and fall back to focused source inspection for the current task rather than treating the index as degraded.
- For source evidence, prefer indexed MCP search/navigation when available: `projects.context_pack.build`, `projects.search.*`, `projects.symbols.list`, `projects.symbol.source`, `projects.symbol.references`, callers/callees, call graph, headings, outlines, and bounded chunks.
- For workspace tools, `id` always means the canonical project id or a safe alias returned by `projects.list` / `projects.get`. Never pass the current working directory, repository root, UNC path, WSL path, local filesystem path, or workspace URI as `id`. When workspace mode is `read_only` or `edit`, prefer `projects.workspace.file_read` for current eligible file content. When workspace mode is `edit` and the change is exact/token-guarded, prefer `projects.workspace.file_edit` before shell, `apply_patch`, or manual file edits.
- For actual runtime proof, use shell: tests, builds, logs, process control, generated files, and exact runtime facts. Use shell or manual edits only when MCP workspace edit is unavailable, the file is ineligible, the repository is not opted in, or the change is a broad/generated rewrite outside the exact-edit contract.
- For stable docs/contracts that changed or are cited in a review, use `projects.claims.check` when the task depends on MCP tool names, REST route names, or `.ai/tasks/*` link claims being current.
- Before commit, use the smallest verification set appropriate to the changed files and risk. Add `projects.context_health`, `projects.impact.analyze`, `projects.claims.check`, or `agent_runs.*` only when they materially improve confidence, support a review/handoff, or are explicitly requested.
- For multi-step reviews, fix loops, implementation handoffs, or resumable work, agents should use `agent_runs.*` to record redacted breadcrumbs and `agent_runs.promote_artifact` to record promotion-gate decisions for existing artifact refs. Store only safe metadata; never store raw prompts, completions, source dumps, raw stderr, roots, secrets, provider payloads, skipped sensitive content, or PII.

MCP-first surfaces:

- Project discovery, enabled state, digest mode, update policy, workspace mode, and graph storage.
- Ingestion run state, live/manual freshness, skipped reason counts, search-index degradation, repair status, and redacted ingestion diagnostics, including project-scoped storage keys but not raw datastore paths.
- Indexed file discovery, opaque file IDs, file metadata, outlines, headings, symbols, references, call sites, and bounded chunks.
- Governed workspace git status/diff, current eligible file reads, and token-guarded exact edits when `[workspace].enabled = true` and the project is opted in. Prefer `projects.workspace.file_read` before shell reads for `read_only` or `edit` workspaces, and prefer `projects.workspace.file_edit` before shell, `apply_patch`, or manual edits for exact/token-guarded edits in `edit` mode.
- Context health, deterministic changed-path impact analysis, and selected stable-doc stale-claim checks.
- Context packs that combine bounded search snippets, indexed file metadata, symbol metadata, optional impact analysis, and manifest-only reproducibility metadata.
- Redacted agent-run metadata for run status, steps, changed safe paths, verifier metadata, artifact refs, promotion-gate decisions, and optional `trace_id` correlation. When callers omit `trace_id`, the generated run id is the trace anchor.
- Configured Jira/Confluence integration provider listing/status/counts, async manual poll submission/status, and local integration graph search/read.
- Any task asking what the indexed project graph knows or whether local content graph ingestion is current.
- Planning and review context that can be answered from indexed files, symbols, references, calls, headings, or chunks.

Do not bypass MCP with raw database queries, absolute root inspection, broad shell scans, ad hoc `git status`/`git diff`, direct file reads, `apply_patch`, or manual file edits when an MCP workspace tool can answer or perform the operation. For opted-in projects, use `projects.workspace.file_read` before shell reads; in `workspace_mode = "edit"`, use `projects.workspace.file_edit` for exact/token-guarded edits before shell or manual editing. Treat read maxes as caps that may truncate returned text; truncation is a reason to page, narrow, or re-read through MCP, not to fall back. Use shell only for tests, build output, logs, process control, generated-file verification, arbitrary commands outside the MCP contract, non-opted-in repositories, and files not yet eligible or allowed by MCP.

Do not use Serena for indexed project discovery, symbol overview/listing, references, call sites, search, bounded source chunks, or planning context when Mivia MCP is available and current.

If MCP is unavailable, stale, missing the project, or lacks the needed semantic operation, state that explicitly, then fall back to Serena plus shell for the minimum evidence needed.

## Inputs

Know or discover:

- MCP endpoint, default `http://127.0.0.1:8080/mcp`.
- Project ID, from the user or `projects.list`. Project-scoped tools also accept safe aliases returned by `projects.list` / `projects.get`, including configured repo/module aliases and auto-discovered Go module paths.
- Host repository rules, tests, and privacy/security boundaries.
- Operator config validation reports are available with `mivia-server config check --config <path> --redacted-json`; use this before hand-inspecting config for support triage when a redacted machine-readable validity report is enough. The report must not expose roots, URLs, Cloud IDs, credential references, or config paths.
- Release examples in docs, Docker Compose, and devcontainer snippets must stay on the current public release pair: Go module tag `v0.1.12` and container tag `0.1.12`.

Do not assume the current repository is the server repo. Do not assume any specific language or directory layout.

## Tool Choice

| Need | First choice | Avoid |
| --- | --- | --- |
| Code symbols, references, call sites, edit targets | Mivia MCP when indexed and current | Serena as first resort in indexed Mivia projects |
| Indexed project map, ingestion state, file IDs, chunks, symbols | Mivia MCP | Raw DB queries, absolute paths, broad shell scans |
| Routine indexed text, path, symbol, reference, call, named AST discovery, or AST query-catalog discovery | `projects.search.*` | Serena `search_for_pattern`, raw DB queries, broad shell scans |
| Governed git status/diff/read, and exact token-guarded edits for opted-in projects | MCP workspace tools | Broad shell scans, `apply_patch`, or manual edits as first resort |
| Context freshness/readiness, changed-path impact, stale docs/contracts | Mivia MCP reliability tools | LLM judgment, broad crawling, raw diff echoing |
| Bounded task context package | `projects.context_pack.build` | Manual broad scans, raw diffs, provider calls, full chunk dumps |
| Redacted agent-run metadata and promotion decisions | `agent_runs.*` | Raw prompts, completions, source dumps, raw stderr, roots, secrets, provider payloads, or PII |
| Configured Jira/Confluence status, poll, search, or read | Mivia MCP integration tools | Jira/Confluence connectors, provider dashboards, live Atlassian reads during local search/read |
| Live project agent activity inspection | Local dashboard `Agent activity` drawer or `GET /api/v1/projects/{id}/agent-activity/stream` | Persistent logs or external telemetry by default |
| Current tests/runtime state, builds, logs, generated files, process control, arbitrary commands, non-opted-in repos | Shell or host tooling | MCP as proof of those runtime facts |

If unclear:

1. Indexed code structure -> Mivia MCP.
2. Indexed project discovery -> MCP.
3. Governed git status/diff/read for `read_only` or `edit` workspaces, plus exact token-guarded edits for `edit` workspaces -> MCP workspace tools.
4. Context health, impact analysis, stale-claim checks, or agent-run metadata -> MCP reliability/control tools.
5. Bounded multi-source project context -> `projects.context_pack.build`.
6. Local Jira/Confluence context -> MCP integration tools.
7. Tests, builds, logs, process control, generated files, arbitrary commands, or non-opted-in repos -> shell.
8. Non-indexed semantic gap -> Serena or host semantic tool, with the fallback stated.

## Safe Sequence

Use the smallest sequence that answers the task. Do not call every tool by default; call the smallest MCP set that proves the answer.

1. Confirm the MCP endpoint is localhost or loopback.
2. Call `tools/list`.
3. Call `projects.list` to discover visible project IDs and aliases. If the user supplies a repo identity such as a Go module path, try it as a project ID/alias, then call `projects.get` and use the returned canonical `id` for follow-up calls. If the expected alias is missing, report that the server config should set the project's `aliases` list. Confirm `enabled`, `digest_mode`, `update_policy`, `workspace_mode`, `graph_storage`, and `validation_status`.
   - `graph_storage=persistent` means durable local Ladybug graph persistence backed by lazy-opened per-project Pebble stores for content-graph projects; the open-DB limit is derived from enabled persistent content-graph projects and capped at 16. Project search SQLite files are versioned with the Pebble graph storage epoch, so old search rows must not be treated as current graph coverage. Diagnostics may report `persistent_pebble_project`. It does not mean a remote graph database, Cypher, SQLite graph storage, or Jira/Confluence read-through.
4. Call `projects.graph_status` or `projects.context_health` before relying on indexed code/content if the answer depends on freshness. Use the returned status, `indexed_content_available`, indexed file/symbol/chunk counts, search-index state, latest run, and active run metadata as the authoritative graph inventory. Use `projects.ingestion_status_latest` only when you need the latest run record specifically.
5. Call `projects.search.text`, `projects.search.files`, `projects.search.symbols`, `projects.search.references`, `projects.search.calls`, `projects.search.ast.queries`, or `projects.search.ast` for routine indexed discovery before broad text scans.
6. Call `projects.graph_status` or `projects.context_health` before relying on indexed context when the task depends on freshness or readiness. If the status is not `ready`, state the status and either use MCP with the active-sync caveat when `indexed_content_available=true`, wait/poll, run ingestion when appropriate, or fall back with the freshness gap explicit. Status `syncing` means normal active indexing or a bounded probe under load, not a degraded index.
7. Use `projects.impact.analyze` before reviewing or explaining a changed path set when the blast radius is not obvious. Prefer explicit `changed_paths`; use governed workspace diff mode only when the workspace is opted in and you need metadata from current changes. If it returns partial `index_syncing`, state that graph fanout is temporarily skipped under active ingestion and inspect the changed source directly.
8. Use `projects.context_pack.build` when one bounded response should combine search snippets, indexed file metadata, symbol metadata, optional impact analysis, and a manifest-only reproducibility record. The manifest records normalized query/options, graph/search-index status, selected file/symbol/chunk IDs, file timestamps, warnings, limitations, and truncated redacted hashes over manifest metadata identifiers only. It does not persist context packs, call providers, return raw diffs, include full chunk text, or include full source by default.
9. Use `projects.claims.check` before trusting selected stable docs/contracts that name MCP tools or REST routes. It is for selected files or pasted snippets, not broad crawling or LLM judgment.
10. Use `agent_runs.create`, `agent_runs.step_append`, `agent_runs.promote_artifact`, `agent_runs.complete`, and `agent_runs.get` to leave redacted execution breadcrumbs and promotion-gate decisions when a workflow benefits from resumability or handoff. Use a safe `trace_id` when correlating several agent runs, MCP calls, workspace edits, ingestion runs, verifier attempts, failures, and promotion decisions; otherwise the created run id becomes the trace anchor. Store only project/task IDs, statuses, changed project-relative paths, verifier command metadata, artifact refs, promotion states, and short safe summaries/notes.
11. Call `projects.files.list`, `projects.symbols.list`, or `projects.headings.list` with small `page_size` to confirm indexed content exists and narrow to stable opaque IDs.
12. Treat `projects.ingest` as asynchronous. It returns quickly with queued run metadata and a `run_id`; poll `projects.ingestion_status` with that `run_id` until `completed` or `failed`.
   - A `pending` or `running` run from before the current server process is an interrupted local queue entry, not active work. Current server builds fail interrupted runs on startup with `error_category=server_restarted`; restart onto a current build before trusting a long-pending zero-file run.
13. If search metadata reports `degraded: true`, call `projects.search_index.rebuild` only when the user or task explicitly asks to repair the local search index. Treat the rebuild as asynchronous: it returns queued run metadata and a `run_id`; poll `projects.ingestion_status` with that `run_id` until `completed` or `failed` before relying on search again.
14. Call `projects.diagnostics.ingestion` when ingestion, watcher, scheduler, or search-index behavior looks inconsistent. It is diagnostics-only and redacted; do not use it as a substitute for tests or logs.
15. Call `projects.files.get` when you need one file's bounded metadata by opaque `file_id`.
16. Call `projects.file.outline` first when file structure is enough. Use `kind`, `name_prefix`, `symbol_page_size`, and `symbol_page_token` to keep large symbol maps bounded. Use `projects.symbol.references`, `projects.symbol.callers`, `projects.symbol.callees`, and `projects.symbol.call_graph` for common indexed navigation. Use `projects.symbol.source` only when bounded eligible source text for one symbol is needed. Set `include_chunk_text=true` with a small `max_chunk_bytes` when eligible file source context is needed directly in the outline. Call `projects.file.chunks` when separate chunk paging is needed.
17. For configured Jira/Confluence context, call `projects.integrations.list` first. Use `projects.integrations.status` for provider config/sync state, `projects.integrations.counts` for total locally ingested items by provider, `projects.integrations.poll` to queue a manual provider run, and `projects.integrations.poll_status` with the returned `run_id` to watch that run. Use `projects.integrations.search`, `projects.jira.issue.get`, and `projects.confluence.page.get` only for already-ingested local graph content. Search/read/count tools do not call Atlassian or resolve credentials.
18. For opted-in workspaces, use `projects.workspace.git_status`, `projects.workspace.git_diff`, and `projects.workspace.file_read` before shell for status, diff, and eligible current file reads when `workspace_mode` is `read_only` or `edit`. In `workspace_mode = "edit"`, use `projects.workspace.file_edit` before shell, `apply_patch`, or manual file edits when the edit is exact/token-guarded. First resolve the project through `projects.list` or `projects.get`, then pass the returned canonical `id` or listed alias. Do not pass cwd/root/UNC/WSL/filesystem paths in `id`; use `relative_path` or `path_prefix` for project-relative file selectors. `file_edit` requires the opaque token from a current file read and queues path ingestion after successful non-dry-run edits. Treat `max_bytes` and other read limits as caps; if `text_truncated=true`, page, narrow, or re-read through MCP instead of falling back only because the response was capped. If workspace git tools report `git is not available in the mivia-server runtime`, state that MCP git status/diff is unavailable and fall back to shell for exact git facts.
19. Switch to Serena or another semantic tool only if MCP cannot answer the required symbol body, reference, call, or edit-planning question.
20. Switch to shell for tests, builds, logs, generated files, process control, arbitrary commands, and non-opted-in repos. For edited indexed files, rely on live ingestion as the normal freshness path and poll latest ingestion status when search results look unexpected.

If MCP is down, the project is not listed, or live ingestion cannot provide current indexed context, say so and fall back to Serena or another semantic tool plus shell. Do not invent MCP facts.

## Tools

Use dotted names when available. Codex-style underscore aliases are accepted by the server for tool calls. If a tool is absent from `tools/list`, treat it as unavailable in that running server build even if this skill documents it.

| Purpose | Tools |
| --- | --- |
| Tasks | `tasks.create`, `tasks.get` |
| Research metadata only | `research_runs.create`, `research_runs.get`, `research_sources.create`, `research_sources.get` |
| Agent run metadata only | `agent_runs.create`, `agent_runs.step_append`, `agent_runs.promote_artifact`, `agent_runs.complete`, `agent_runs.get` |
| Project registry | `projects.list`, `projects.get` |
| Metadata digest and reliability | `projects.digest`, `projects.graph_status`, `projects.context_health`, `projects.impact.analyze`, `projects.context_pack.build`, `projects.claims.check` |
| Content graph | `projects.ingest`, `projects.search_index.rebuild`, `projects.ingestion_status`, `projects.ingestion_status_latest`, `projects.files.list`, `projects.files.get`, `projects.file.chunks`, `projects.symbols.list`, `projects.search.text`, `projects.search.files`, `projects.search.symbols`, `projects.search.references`, `projects.search.calls`, `projects.search.ast.queries`, `projects.search.ast`, `projects.symbol.source`, `projects.symbol.references`, `projects.symbol.callers`, `projects.symbol.callees`, `projects.symbol.call_graph`, `projects.headings.list`, `projects.file.outline` |
| Governed workspace | `projects.workspace.git_status`, `projects.workspace.git_diff`, `projects.workspace.file_read`, `projects.workspace.file_edit` plus underscore aliases |
| Diagnostics | `projects.diagnostics.ingestion` |
| Project integrations | `projects.integrations.list`, `projects.integrations.status`, `projects.integrations.counts`, `projects.integrations.poll`, `projects.integrations.poll_status`, `projects.integrations.search`, `projects.jira.issue.get`, `projects.confluence.page.get` |

### Tool Use Notes

- `tasks.create` / `tasks.get`: local agent task metadata only. Do not use for project implementation plans unless the repository asks for MCP task records.
- `research_runs.create` / `research_runs.get` and `research_sources.create` / `research_sources.get`: redacted research metadata only. They do not fetch providers and must not contain raw source content, prompts, secrets, or personal data.
- `agent_runs.create` / `agent_runs.step_append` / `agent_runs.complete` / `agent_runs.get`: redacted agent-run metadata only. Use for resumability, review/fix loops, handoffs, and trace correlation. Keep the returned `id` and pass it as `run_id` to step, promotion, and completion calls; `agent_runs.get` uses `id`. `trace_id` is optional and must be a safe identifier; if omitted on create, the generated run id becomes the trace id, and steps inherit it. They must not contain raw prompts, completions, source dumps, raw stderr, roots, secrets, credentials, provider payloads, or PII. For verifier metadata, prefer `command` as the executable and put flags/paths in `args`; simple space-separated words in `command` are normalized into args. Verifier args may include loopback URLs without credentials, query strings, or fragments; external URLs remain out of bounds.
- `agent_runs.promote_artifact`: redacted promotion-gate metadata only. Use for `candidate`, `validated`, `promoted`, and `rejected` decisions on existing artifact refs. Validated, promoted, and rejected decisions require a verifier ref and bounded decision text; raw payloads, roots, secrets, and PII remain out of bounds.
- `projects.list`: first project-discovery call. Returns configured project metadata without root paths, including safe lookup aliases when available.
- `projects.get`: use before project-specific work to confirm the selected project is enabled and validate content/workspace modes. The returned `id` is canonical; use it for follow-up calls even when you started from an alias.
- `projects.digest`: metadata-only digest for projects that support digest mode. Content-graph projects may reject this as unsupported; use ingestion/search tools instead.
- `projects.graph_status`: authoritative graph inventory and sync-state summary for one configured project. Prefer this over `projects.ingestion_status_latest` when deciding if indexed MCP tools are usable.
- `projects.context_health`: readiness/freshness summary for one configured project using safe config, ingestion, search-index, indexed file/symbol/chunk counts, active/latest run metadata, and workspace-git metadata. A `syncing` response with `indexed_content_available=true` means MCP indexed tools can still be used with the active-sync caveat.
- `projects.impact.analyze`: deterministic changed-path impact analysis. It may use governed workspace diff file metadata but must not return raw diff content. During active ingestion it may return partial `index_syncing` metadata instead of waiting behind busy graph/search stores.
- `projects.context_pack.build`: bounded context package from existing indexed search, file metadata, symbol metadata, optional impact analysis, and manifest-only reproducibility metadata. It does not create storage, call providers, return roots, return raw diffs, include full chunk text, or include full source by default.
- `projects.claims.check`: deterministic stale-claim check for selected stable docs/contracts. Default output is concise: summary counts plus actionable findings only; pass `include_verified: true` only when a full audit/debug list is needed. It does not use LLM judgment, broad crawling, or document-content echoing.
- `projects.ingest`: queue bounded content-graph ingestion. Always poll with `projects.ingestion_status`.
- `projects.search_index.rebuild`: repair degraded local search index only when asked or when degradation blocks the task. Always poll with `projects.ingestion_status`.
- `projects.ingestion_status`: read one ingestion/rebuild run by `run_id`.
- `projects.ingestion_status_latest`: latest run metadata only. Do not use it alone as a graph-readiness or MCP-usability decision.
- `projects.files.list`: discover eligible indexed files with filters such as path/status/extension and a small `page_size`.
- `projects.files.get`: fetch one file metadata record by opaque `file_id`.
- `projects.file.chunks`: page bounded chunk text for one eligible file. Keep `max_chunk_bytes` small.
- `projects.file.outline`: preferred first read for one file's structure; use it before chunk text when symbols/headings are enough.
- `projects.symbols.list`: list bounded symbol metadata; filter by `kind`, `package`, `name_prefix`, `name_contains`, `receiver`, `file_id`, and page tokens.
- `projects.search.text`: literal indexed text search. Use for known strings, error names, config keys, or prose.
- `projects.search.files`: indexed file metadata search by safe project-relative path. Use before file list when you know part of a path.
- `projects.search.symbols`: symbol search by prefix/substr. Use before references/call graph when you need stable symbol IDs.
- `projects.search.references`: indexed reference metadata search by name/target/enclosing symbol.
- `projects.search.calls`: indexed call edge search by caller/callee names.
- `projects.search.ast.queries`: list available named AST query IDs and safe coverage before AST search.
- `projects.search.ast`: run only named AST queries from the catalog; never send raw Tree-sitter query text.
- `projects.symbol.source`: bounded source for one eligible symbol. Use only after selecting a stable symbol ID.
- `projects.symbol.references`: references resolving to one symbol ID.
- `projects.symbol.callers`: direct callers for one symbol ID.
- `projects.symbol.callees`: direct callees for one symbol ID.
- `projects.symbol.call_graph`: bounded traversal around one symbol ID; set depth/limits conservatively.
- `projects.headings.list`: Markdown/document heading metadata. Use for docs discovery before broad text reads.
- `projects.workspace.git_status`: governed git status for opted-in workspaces. `id` must be a project id or alias from `projects.list` / `projects.get`, not a filesystem path. Prefer before shell `git status` when available. If it reports Git unavailable or times out, fall back to shell and report the MCP gap.
- `projects.workspace.git_diff`: governed capped diff for opted-in workspaces. `id` must be a project id or alias from `projects.list` / `projects.get`, not a filesystem path; use `relative_path` or `path_prefix` for file filtering. Prefer before shell `git diff` when available. If it reports Git unavailable, fall back to shell and report the MCP gap.
- `projects.workspace.file_read`: current eligible file content plus edit token. `id` must be a project id or alias from `projects.list` / `projects.get`, not a filesystem path; select files with `file_id` or project-relative `relative_path`. Prefer it before shell reads for opted-in `read_only` or `edit` workspaces. Treat `max_bytes` and returned truncation flags as caps to page/narrow/re-read through MCP, not as fallback permission by themselves. Required before `projects.workspace.file_edit`.
- `projects.workspace.file_edit`: exact token-guarded edit only. `id` must be a project id or alias from `projects.list` / `projects.get`, not a filesystem path. Do not use for broad rewrites, generated files, or arbitrary patches.
- `projects.diagnostics.ingestion`: redacted scheduler/watcher/runtime/storage diagnostics. Use when ingestion/search behavior is suspect; switch to logs only if runtime proof is required.
- `projects.integrations.list`: discover configured Jira/Confluence providers and redacted config metadata for one project.
- `projects.integrations.status`: provider coverage, sync state, last run, active run, polling config, and cursor presence only.
- `projects.integrations.counts`: total locally ingested item counts by configured provider. Counts are local-store counts, not live provider totals.
- `projects.integrations.poll`: queue manual local integration polling. This may call Atlassian Cloud in the background using configured credentials; response remains redacted.
- `projects.integrations.poll_status`: fetch one local poll run by `run_id`.
- `projects.integrations.search`: search already-ingested local Jira/Confluence chunks only.
- `projects.jira.issue.get`: read one locally ingested Jira issue by issue key with bounded chunks. Default page is 3 chunks; pass `chunk_offset` from `next_chunk_offset` to continue.
- `projects.confluence.page.get`: read one locally ingested Confluence page by page ID with bounded chunks. Default page is 3 chunks; pass `chunk_offset` from `next_chunk_offset` to continue.

## Indexed Metadata Contract

- Promoted AST metadata currently covers Go stdlib AST, Tree-sitter JS/JSX/TS/TSX, Tree-sitter C#, Tree-sitter Python, Markdown headings, and lightweight infrastructure/config metadata.
- `projects.search.ast.queries` returns the supported named AST query catalog: query IDs, languages, capture names, query versions, matching file extensions, and safe per-language coverage counts. It does not return raw Tree-sitter query text.
- `projects.search.ast` runs named Tree-sitter structural queries over eligible indexed chunks for Go, Python, JavaScript, JSX, TypeScript, TSX, and C#. It accepts catalog IDs such as `function_declarations`, `class_declarations`, `call_expressions`, `imports`, `test_functions`, `assignments`, and `error_handling`; it does not accept raw Tree-sitter query syntax.
- Sensitive, denied, absent, parse-error, and other skipped files are unreachable from AST search. Oversized text files can be chunk-indexed when they pass streaming safety checks, but semantic extraction is skipped and represented as `skipped_reason=semantic_too_large`; unsafe oversized files remain safe coverage gaps. Source text, chunks, snippets, content hashes, raw parser/SQLite/FTS/Tree-sitter errors, roots, secrets, PII, raw prompts, and provider payloads are not exposed for skipped or unsafe files.
- TS/JS/TSX/JSX, C#, and Python have no regex fallback. If a promoted grammar or embedded query cannot initialize, server startup fails with `extractor_initialization_failed`.
- Per-file parser failures are file-local `parse_error` skips; full scans continue.
- Extractor cache rows store only symbols, headings, references, and calls keyed by hashes, extractor name, version, and fingerprint. Legacy empty-fingerprint rows are treated as cache misses when fingerprint-aware lookup is used; agents should not interpret a one-time refresh as file content change.
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

Read resources only when a resource URI is already known and a template exactly matches the target. Prefer tools for discovery, pagination, status, search, counts, and writes.

## Workspace Boundary

Workspace tools are default-disabled and require both global `[workspace].enabled = true` and per-project `workspace_mode = "read_only"` or `"edit"` with `digest_mode = "content_graph"`. `read_only` allows governed git status/diff and current eligible file reads. `edit` additionally allows exact byte-span edits guarded by an opaque per-process token from `projects.workspace.file_read`; use that path before shell, `apply_patch`, or manual edits when the change is exact/token-guarded. Read limits are caps and may return truncated text; narrow, page, or re-read through MCP instead of falling back only because a response was capped. There is no arbitrary shell endpoint, public exposure, auth change, provider call, embedding/vector/crawling path, raw DB query endpoint, raw patch upload endpoint, or git commit/push/checkout/reset/branch/merge/rebase/stash/clean/restore tool.

## Raw HTTP Fallback

Use raw HTTP only when no native MCP client is available:

- `POST http://127.0.0.1:<port>/mcp`
- `Content-Type: application/json`
- `Accept: application/json, text/event-stream`
- Optional `MCP-Protocol-Version: 2025-06-18`

Start with `tools/list`, then use `tools/call`. Do not use raw HTTP to bypass MCP boundaries.

The local dashboard exposes a project-scoped `Agent activity` drawer backed by `GET /api/v1/projects/{id}/agent-activity/stream`. It streams persisted redacted recent MCP activity plus agent-run lifecycle events, verifier metadata, promotion decisions, policy events, and live in-memory activity for the selected project, including method/tool, status, duration, failure category, client class, request metadata, `trace_id`, `run_id`, `parent_id`, `correlation_kind`, and input/output summary classes. Reconnecting clients can resume with `Last-Event-ID` or `after_id`. Live in-memory events may include collapsed raw request/params/arguments/result payloads for localhost debugging; persistent storage omits raw payloads and payload-derived hashes by default unless explicit local debug retention is enabled with `MIVIA_DEBUG_ENABLED=true` and `MIVIA_AGENT_ACTIVITY_RETAIN_RAW_PAYLOADS=true`. Treat full payloads as sensitive: they are useful for local inspection, but do not copy them into docs, commits, agent-run metadata, or external tools unless the task explicitly requires that exposure.

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

Counts:

- `projects.integrations.counts` accepts `id` only.
- It returns local item counts for configured providers only.
- A zero count means no local items currently match that project/provider in the local integration store; it does not prove the remote provider has zero items.
- Counts are read-only and do not call Jira, Confluence, or credential providers.

Polling:

- `projects.integrations.poll` accepts `id`, `provider` (`jira` or `confluence`), and optional `kind` (`initial_full` or `incremental`).
- It returns queued run metadata with a `run_id`; always use `projects.integrations.poll_status` or `projects.integrations.status` before relying on new data.
- The background run may call Atlassian Cloud using configured env/file credential refs at execution time. The response must not expose credentials, credential refs, raw provider payloads, raw cursors, roots, or datastore paths.

Local graph search/read:

- `projects.integrations.search` searches already-ingested local integration chunks only.
- `projects.jira.issue.get` reads one locally ingested Jira issue by issue key.
- `projects.confluence.page.get` reads one locally ingested Confluence page by page ID.
- These search/read tools do not call Atlassian and must return only bounded local graph content.
- A local miss returns a typed MCP tool error such as `not_indexed`; it does not prove upstream absence. For read tools, `id` is the Mivia project slug, not a Jira numeric issue ID.
