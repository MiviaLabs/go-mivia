# Project Search And Embedded AST Search Plan

Status: Implementation-ready plan
Date: 2026-05-30
Classification: Internal; PII-prohibited
Mode: Free-text plan; no Jira or Confluence used by repository constraint.

## 1. Intent

Agents should not need Serena `search_for_pattern` for routine project discovery. Add governed MiviaLabs MCP/REST search tools over indexed project content first, then add embedded AST structural search for supported languages so `ast-grep` is no longer required for read-only structural discovery.

Priority order:

1. Project search quick wins: text search, symbol search, file search, reference/call search, bounded snippets, REST/MCP docs, privacy tests.
2. Agent workflow updates: README, MCP skill guidance, OpenAPI/MCP docs, examples that route pattern-search use cases to MiviaLabs MCP first.
3. Embedded AST structural search: Tree-sitter query powered search for supported parser languages; Go support requires an embedded Go grammar or a separate Go AST query layer.
4. `ast-grep` replacement boundary: read-only structural search only at first; codemods/rewrites remain out of scope until explicitly designed.

## 2. Current State Evidence

Code-grounded facts:

- `internal/projectingestion.Service` already exposes project files, chunks, symbols, symbol source, references, callers, callees, and call graph through methods such as `ListFiles`, `ListChunks`, `ListSymbols`, `GetSymbolSource`, `ListSymbolReferences`, `ListSymbolCallers`, `ListSymbolCallees`, and `GetSymbolCallGraph`.
- `internal/projectingestion.GraphStore.ListSymbols` filters by exact kind, file ID, package, extension, and name prefix. It does not support substring, text query, fuzzy matching, or general pattern search.
- `ContentChunk` graph nodes store bounded eligible chunk text; `ListChunks` can return capped chunk text per file, but there is no cross-file search endpoint.
- `CodeReference` and `CodeCall` nodes exist and are queryable by symbol ID through references/callers/callees/call graph, but there is no search by caller/callee/target name when the agent does not yet know a symbol ID.
- REST routes exist under `internal/projectregistry/httpapi` for files, chunks, symbols, symbol source, references, callers, callees, and call graph.
- MCP tool routing exists under `internal/projectregistry/mcpapi` for matching project tools.
- OpenAPI and MCP contracts already document project file/chunk/symbol/call graph capabilities.
- Existing extractor support: Go stdlib AST for Go; Tree-sitter for JavaScript, TypeScript, TSX/JSX, C#, and Python; Markdown heading extraction; lightweight infrastructure/config extraction.
- `go.mod` already includes `github.com/tree-sitter/go-tree-sitter`, `tree-sitter-c-sharp`, `tree-sitter-javascript`, `tree-sitter-python`, and `tree-sitter-typescript`. It does not currently include a Go Tree-sitter grammar.

Library-grounded facts:

- Tree-sitter queries support capture-based matching, predicates such as `#eq?`, `#match?`, `#any-of?`, and match/capture structures with node spans. This is enough for an embedded read-only structural search API with safe span/snippet output.

## 3. Non-Goals

- No public exposure.
- No auth changes.
- No provider calls.
- No embeddings or vectors.
- No crawling.
- No raw DB query endpoint.
- No broad arbitrary filesystem search outside opted-in indexed projects.
- No source text for skipped, denied, sensitive, absent, or unindexed files.
- No matched sensitive text, secrets, PII, raw prompts, provider payloads, absolute roots, raw local config, or raw datastore errors in responses.
- No codemod/rewrite engine in the first AST phase.
- No claim that `ast-grep` is fully replaced until structural queries cover the needed supported-language cases and tests prove parity for read-only discovery.

## 4. Proposed Capability Map

```mermaid
flowchart TB
  Agent["Agent or Codex Desktop"]
  MCP["MiviaLabs MCP"]
  REST["REST /api/v1"]
  Search["Project search service"]
  Text["projects.search.text"]
  Files["projects.search.files"]
  Symbols["projects.search.symbols"]
  Refs["projects.search.references_and_calls"]
  AST["projects.search.ast later"]
  Graph["Ladybug graph: chunks, symbols, refs, calls"]
  SQLite["SQLite ingestion state"]
  Safety["Safety gates and response caps"]

  Agent --> MCP
  Agent --> REST
  MCP --> Search
  REST --> Search
  Search --> Text
  Search --> Files
  Search --> Symbols
  Search --> Refs
  Search --> AST
  Text --> Graph
  Files --> SQLite
  Symbols --> Graph
  Refs --> Graph
  AST --> Graph
  Search --> Safety
  Safety --> Agent
```

## 5. Phase 1: Governed Project Search Quick Wins

### 5.1 Add Search DTOs

Add project search models in `internal/projectingestion`:

- `TextSearchOptions`
  - `Query string`
  - `Mode string`: initially `literal`; optional `regexp` only if RE2 validation and caps are implemented.
  - `CaseSensitive bool`
  - `Extension string`
  - `PathPrefix string`
  - `PageSize int`
  - `PageToken string`
  - `MaxSnippetBytes int`
  - `MaxMatches int`
- `TextSearchResult`
  - `File FileMetadata`
  - `Chunk ChunkMetadata` without full unbounded text
  - `LineStart`, `LineEnd`, `ByteStart`, `ByteEnd`
  - `Snippet string`
  - `SnippetTruncated bool`
- `SymbolSearchOptions`
  - existing `SymbolFilter` fields plus `NameContains`, `Receiver`, `CaseSensitive`
- `ReferenceSearchOptions`
  - `NameContains`, `TargetNameContains`, `CallerNameContains`, `CalleeNameContains`, `Extension`, `PathPrefix`, `ResolutionStatus`, `Confidence`, pagination
- `FileSearchOptions`
  - existing `FileStateFilter` fields plus `PathContains`, `CaseSensitive`

Use existing `Pagination`, `NormalizeFileExtension`, `NormalizePathPrefix`, opaque ID validation, and max page-size limits.

### 5.2 Add Service Methods

Add to `projectingestion.API` and implementations:

- `SearchText(ctx, projectID string, options TextSearchOptions) (TextSearchResultList, error)`
- `SearchSymbols(ctx, projectID string, options SymbolSearchOptions) (SymbolList, error)`
- `SearchReferences(ctx, projectID string, options ReferenceSearchOptions) (SymbolReferenceList, error)`
- `SearchCalls(ctx, projectID string, options ReferenceSearchOptions) (SymbolCallEdgeList, error)`
- `SearchFiles(ctx, projectID string, options FileSearchOptions) (FileList, error)`

Implementation constraints:

- Search only `ContentChunk` nodes for text and only chunks belonging to eligible indexed file versions.
- Return snippets, not entire chunks, unless existing chunk caps explicitly permit the bounded text.
- For literal search, use safe substring matching with case-folding only when requested.
- For regexp search, if included, use Go RE2 only; reject invalid patterns with sanitized `ErrInvalidInput`, cap pattern length, cap matches, and cap snippet bytes.
- Do not scan skipped sensitive content because it is not stored in chunks.
- Do not expose graph internals, root paths, raw DB errors, or local config values.
- Keep pagination stable by deterministic sort: relative path, chunk index, byte offset, opaque ID.

### 5.3 Add REST Endpoints

Add under `/api/v1/projects/{id}/search/...`:

- `GET /api/v1/projects/{id}/search/text`
- `GET /api/v1/projects/{id}/search/files`
- `GET /api/v1/projects/{id}/search/symbols`
- `GET /api/v1/projects/{id}/search/references`
- `GET /api/v1/projects/{id}/search/calls`

Use query parameters only. Do not add a raw query body endpoint.

### 5.4 Add MCP Tools

Add tools:

- `projects.search.text`
- `projects.search.files`
- `projects.search.symbols`
- `projects.search.references`
- `projects.search.calls`

Codex will expose underscore variants such as `projects_search_text`.

Tool descriptions must state:

- Results are from eligible indexed content only.
- Search is bounded and may be stale until ingestion catches up.
- Source snippets are capped.
- Skipped sensitive files and matched sensitive text are never returned.

### 5.5 Add OpenAPI, MCP, README, Skill Guidance

Update:

- `api/openapi/agent-control.v1.yaml`
- `api/mcp/agent-control.v1.md`
- `README.md`
- `docs/agent-context-guide.md`
- `docs/architecture/system-architecture.md`
- `.ai/skills/mivialabs-agent-mcp/SKILL.md`

Guidance should say:

1. Use MiviaLabs MCP search first for indexed project text/symbol/reference/call discovery.
2. Use `rg` for current dirty working-tree text when ingestion freshness matters.
3. Use Serena for LSP/editor-aware symbol navigation and edits.
4. Use `ast-grep` only for structural search not yet covered by embedded AST search or for rewrite/codemod tasks.

## 6. Phase 2: Search Freshness And Diagnostics

Add search response metadata:

- `ingestion_run_id`
- `indexed_at` or latest completed/running run timestamps when available
- `index_status`: `completed`, `running`, `stale`, or `unknown`
- `result_truncated`
- `scanned_count` only as a count, never raw paths/content

Add MCP guidance:

- If results are empty and latest ingestion is running, check `projects.ingestion_status_latest` before assuming no matches.
- If current working-tree edits matter, use shell/`rg` as source of truth until live ingestion catches up.

## 7. Phase 3: Embedded AST Structural Search

### 7.1 Structural Search Contract

Add `projects.search.ast` only after Phase 1 is stable.

Inputs:

- `id`
- `language`: `go`, `python`, `javascript`, `typescript`, `tsx`, `jsx`, `csharp`
- `query`: Tree-sitter query text or a constrained named-query ID
- `captures`: optional capture-name allowlist
- `extension`, `path_prefix`, pagination
- `max_matches`, `max_snippet_bytes`

Outputs:

- file metadata
- match span: lines, bytes, columns
- capture name
- optional bounded snippet from eligible indexed chunk text
- `query_language`
- `query_version`
- `result_truncated`

### 7.2 Query Safety

Validation:

- Reject empty query.
- Cap query bytes.
- Compile query before execution and return sanitized errors.
- Cap files scanned, matches returned, captures returned, snippet bytes, and total response bytes.
- Allow only language IDs backed by embedded parser registry.
- Do not support arbitrary shell commands, external `ast-grep`, or filesystem traversal.

Privacy:

- Execute only against eligible indexed files.
- Snippets must come from eligible chunks and obey source caps.
- Do not return raw parser errors that include absolute paths or source text.
- Do not query skipped/sensitive/absent files.

### 7.3 Parser Support

Supported immediately from existing dependencies:

- Python: Tree-sitter parser already present.
- JavaScript/JSX: Tree-sitter JavaScript parser already present.
- TypeScript/TSX: Tree-sitter TypeScript parser already present.
- C#: Tree-sitter C# parser already present.

Go decision:

- Current Go extraction uses the Go stdlib AST, not Tree-sitter.
- To replace `ast-grep` for Go structural search, add an embedded Go Tree-sitter grammar for structural search only, or build a separate Go AST predicate/query layer.
- Recommended: add Tree-sitter Go grammar for structural search, while keeping stdlib AST extraction as-is until there is a separate reason to migrate extractor behavior.

### 7.4 Named Query Catalog

Do not expose only raw query text. Add a curated query catalog for common agent tasks:

- function/method declarations
- class/type declarations
- call expressions
- imports/requires
- decorators/annotations
- error handling branches
- test functions/classes
- assignments to identifiers

Each query entry should define:

- language
- query text
- expected captures
- tests with fixture code
- version

Raw Tree-sitter query input can be allowed later under stricter caps, but named queries should be the default MCP path.

## 8. Phase 4: Optional AST-Grep Parity Layer

Only after `projects.search.ast` is proven useful:

- Add compatibility aliases for common ast-grep-style intentions, not full syntax compatibility.
- Keep this read-only.
- Do not implement rewrites/codemods until there is a separate design covering patch generation, review, and rollback.

## 9. File-Level Work Plan

Expected files to touch in Phase 1:

- `internal/projectingestion/types.go` or equivalent model file: add search DTOs/results.
- `internal/projectingestion/service.go`: add API methods and validation.
- `internal/projectingestion/graph_store.go`: add graph-backed search methods over `ContentChunk`, `CodeSymbol`, `CodeReference`, and `CodeCall`.
- `internal/projectingestion/service_test.go`: service tests for text/symbol/reference/call search and privacy.
- `internal/projectregistry/httpapi/httpapi.go`: add REST routes and handlers.
- `internal/projectregistry/httpapi/httpapi_test.go`: REST tests.
- `internal/projectregistry/mcpapi/mcpapi.go`: add MCP tools, routing, schemas, and tool descriptions.
- `internal/projectregistry/mcpapi/mcpapi_test.go`: MCP tests.
- `internal/agentcontrol/mcpapi`: update top-level routing/tests if required.
- `api/openapi/agent-control.v1.yaml`: document REST search endpoints.
- `api/mcp/agent-control.v1.md`: document MCP search tools.
- `README.md`, `docs/agent-context-guide.md`, `docs/architecture/system-architecture.md`, `.ai/skills/mivialabs-agent-mcp/SKILL.md`: update guidance and diagrams.

Expected files to touch in Phase 3:

- `internal/projectingestion/treesitter_search.go`: parser/query execution service.
- `internal/projectingestion/treesitter_search_queries.go`: named query catalog.
- `internal/projectingestion/treesitter_search_test.go`: language fixtures and privacy tests.
- `go.mod`, `go.sum`: add Go Tree-sitter grammar if Go structural search is included.
- REST/MCP/OpenAPI/MCP docs: add `projects.search.ast`.

## 10. Tests

Phase 1 tests:

- Text search returns bounded snippets for eligible chunks.
- Text search does not return skipped sensitive files, denied paths, absent files, matched sensitive text, content hashes, roots, or raw errors.
- Text search respects extension, path prefix, pagination, match caps, and snippet caps.
- Symbol search supports substring and existing exact filters without breaking prefix behavior.
- Reference search finds target names without knowing symbol ID.
- Call search finds caller/callee names without knowing symbol ID.
- REST endpoints return correct status codes and sanitized validation errors.
- MCP tools return bounded JSON payloads and support underscore-normalized tool names.
- Search reports freshness/index-status metadata without leaking roots.

Phase 3 tests:

- Tree-sitter AST search finds named-query captures for Python, JS, TS, TSX, C#, and Go if Go grammar is added.
- Query compile errors are sanitized.
- Query caps stop large result sets.
- Captures include stable file IDs and spans.
- Snippets are bounded and only from eligible indexed chunks.
- Sensitive/skipped/denied/absent files never participate.
- MCP/REST AST tests cover supported language validation and unsupported language rejection.

## 11. Verification

Run focused tests first:

```sh
/home/mac/.local/go1.26.3/bin/go test ./internal/projectingestion
/home/mac/.local/go1.26.3/bin/go test ./internal/projectregistry/httpapi ./internal/projectregistry/mcpapi ./internal/agentcontrol/mcpapi
```

Then run:

```sh
/home/mac/.local/go1.26.3/bin/go test ./...
git diff --check
```

Before commit:

```sh
git status --short
git diff --stat
git diff --cached --stat
git diff --cached --check
```

Review diff explicitly for leaks:

- roots
- secrets
- PII
- raw prompts
- provider payloads
- skipped sensitive content
- matched sensitive text
- content hashes in public responses
- raw DB/query/parser errors

## 12. Risks

- Indexed search can be stale relative to dirty working-tree edits. Mitigation: response freshness metadata and guidance to use `rg` for current disk truth when needed.
- Graph-backed text search may be slow on very large graphs if implemented as full node scans. Mitigation: hard scan/result caps in Phase 1; consider a later SQLite FTS table only for eligible chunk text if needed.
- Regex search can become expensive or leak raw validation detail. Mitigation: start with literal search; add RE2 regexp only with caps and sanitized errors.
- AST structural search can become a raw parser-query escape hatch. Mitigation: named query catalog first; raw query mode only with strict caps.
- Adding Tree-sitter Go grammar increases dependency surface. Mitigation: use it only for structural search and keep existing Go stdlib AST extraction unchanged.
- Replacing `ast-grep` for rewrites is a separate problem. Do not claim codemod parity.

## 13. Open Questions

1. Should Phase 1 include regexp search, or literal-only plus symbol/reference/call search first?
2. Should text search use graph node scans initially, or add SQLite FTS for eligible chunk text from the start?
3. For AST search, should raw Tree-sitter query input be enabled initially, or should MCP expose only named query IDs?
4. Should Go structural search add Tree-sitter Go grammar, or should Go keep a separate stdlib AST query layer?

Recommended defaults:

- Phase 1: literal-only text search, symbol substring search, file path contains, reference/call name search.
- Phase 2: freshness metadata and agent guidance.
- Phase 3: named Tree-sitter query catalog; add Go Tree-sitter grammar for structural search; no raw query mode until named queries are stable.

## 14. References

- `README.md`: current feature map, MCP/REST project APIs, safety boundaries.
- `docs/architecture/system-architecture.md`: current service shape, ingestion flow, data classification, operational boundaries.
- `docs/agent-context-guide.md`: current agent usage guidance for Serena plus MiviaLabs MCP.
- `docs/configuration/local-projects.md`: local project config, ingestion, scheduler, and worker settings.
- `api/mcp/agent-control.v1.md`: current MCP project tools.
- `api/openapi/agent-control.v1.yaml`: current REST project endpoints.
- `internal/projectingestion/service.go`: current project query service methods and ingestion safety boundaries.
- `internal/projectingestion/graph_store.go`: current graph nodes and query methods for chunks, symbols, references, calls.
- `internal/projectregistry/httpapi/httpapi.go`: current REST route registration.
- `internal/projectregistry/mcpapi/mcpapi.go`: current MCP tool routing and schemas.
- Context7 `/tree-sitter/tree-sitter`: Tree-sitter query captures, predicates, match/capture API concepts.
