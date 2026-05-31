# Graph Impact Health Performance Audit

## Executive Summary

MCP was available but not healthy during this audit. `projects.context_health` returned `degraded` with `search_index_unknown`; `projects.ingestion_status_latest`, `projects.impact.analyze`, and indexed search calls timed out while server logs showed repeated `/readyz` dependency failures. Runtime diagnostics showed active ingestion for this repo and very slow storage stages: `storage.search_write` max ~184s, `storage.state_write` max ~183s, `runtime.full_scan` ~268s, heap ~6.9GB allocated.

Confirmed bug fixed: `MemoryGraph.ListNodes` and `MemoryGraph.ListRelationships` ignored existing in-memory indexes and scanned the whole graph. This directly affects per-file symbol lookup and call graph traversal used by impact analysis. Fixed in `internal/platform/ladybug/ladybug.go`; tests added in `internal/platform/ladybug/ladybug_test.go`.

The graph model is useful but not yet sufficient for accurate impact analysis. The biggest accuracy gap is symbol resolution: reference, caller, callee, and implementer IDs are resolved only against symbols declared in the same file (`internal/projectingestion/graph_store.go:1033`, `1097`, `1168`, `1239`). Cross-file references/calls are stored mostly as unresolved candidate metadata, so impact analysis will miss many real dependents.

## Current Graph Model

### nodes

- `Project`: project id, namespace, classification, digest mode, update policy, enabled flag (`internal/projectingestion/graph_store.go:1265`).
- `IngestionRun`: run id, trigger, run kind, status, file/chunk/symbol counts, phase, timestamps (`internal/projectingestion/graph_store.go:1280`).
- `RepoFile`: project id, namespace, path hash, safe relative path flag, status, present flag, size, modified time, skip reason, extension when path-safe (`internal/projectingestion/graph_store.go:1307`).
- `FileVersion`: repo file id, content SHA, size, modified time, present flag (`internal/projectingestion/graph_store.go:196`; unchanged path writes at `internal/projectingestion/graph_store.go:165`).
- `ContentChunk`: repo file id, file version id, chunk index, line/byte spans, text, chunk hash (`internal/projectingestion/graph_store.go:216`).
- `CodeSymbol`: kind, name, package, import path, receiver, line/byte/column spans (`internal/projectingestion/graph_store.go:242`).
- `CodeReference`: kind, name, target name/id, package, receiver, import path, enclosing symbol name/id, spans, resolution status, confidence (`internal/projectingestion/graph_store.go:1033`).
- `CodeCall`: caller/callee names and ids, receiver/import path, spans, resolution status, confidence (`internal/projectingestion/graph_store.go:1097`).
- `CodeImplementation`: implementer/implemented names and ids, relation kind, package, receiver/import path, spans, resolution status, confidence (`internal/projectingestion/graph_store.go:1168`).
- `DocumentHeading`: level, text, parent index, spans (`internal/projectingestion/graph_store.go:292`).
- Schema also contains legacy/research/integration labels such as `Agent`, `Task`, `ResearchRun`, `Document`, `Chunk`, `IntegrationArtifact`, `IntegrationContentChunk` (`internal/platform/ladybug/schema/schema.go:5`).

### edges

- Project/file/run: `PROJECT_HAS_REPO_FILE`, `PROJECT_HAS_INGESTION_RUN`, `INGESTION_RUN_TOUCHED_FILE`, `INGESTION_RUN_SKIPPED_FILE` (`internal/projectingestion/graph_store.go:189`, `192`, `326`, `349`).
- File/chunk/symbol: `REPO_FILE_HAS_VERSION`, `VERSION_HAS_CHUNK`, `REPO_FILE_DECLARES_SYMBOL`, `SYMBOL_IN_CHUNK` (`internal/projectingestion/graph_store.go:212`, `237`, `265`, `270`).
- References/calls/implementations: `SYMBOL_HAS_REFERENCE`, `SYMBOL_REFERENCES_SYMBOL`, `REFERENCE_IN_CHUNK`, `SYMBOL_CALLS_SYMBOL`, `CALL_IN_CHUNK`, `SYMBOL_IMPLEMENTS_SYMBOL`, `IMPLEMENTATION_IN_CHUNK` (`internal/projectingestion/graph_store.go:1078`, `1082`, `1088`, `1139`, `1159`, `1212`, `1225`).
- Schema declares `DOCUMENT_HAS_HEADING`, but repo-file heading ingestion currently writes `DocumentHeading` nodes without creating that edge (`internal/platform/ladybug/schema/schema.go:48`; `internal/projectingestion/graph_store.go:292`).

### metadata

- File state persists path hash, safe relative path, status, present, content SHA, extension, size, event/ingest timestamps, skip reason (`internal/projectingestion/sqlite_store.go:339`).
- Extractor cache stores symbols, headings, references, calls, implementations keyed by project, file hash, content SHA, extractor name/version (`internal/projectingestion/sqlite_store.go:396`).
- Search index FTS tables duplicate chunks/files/symbols/references/calls for text search (`internal/platform/sqlite/schema/schema.go:267`, `283`, `292`, `311`, `336`).

## Impact Analysis Assessment

### strengths

- It explicitly marks degraded/unknown index state as partial before graph traversal (`internal/projectreliability/impact.go:120`).
- It combines changed-path heuristics with graph-derived source anchors (`internal/projectreliability/impact.go:105`, `108`).
- For each changed file it walks declared symbols, direct references, callers, implementers for types/classes, and fallback name references (`internal/projectreliability/impact.go:169`, `181`).
- It records residual unknowns for missing paths, lookup failures, files with no symbols, affected-file lookup failures, and skipped workspace diff entries (`internal/projectreliability/impact.go:92`, `147`, `162`, `177`, `253`).

### false-positive risks

- `addNameReferences` searches by `TargetNameContains`, not exact symbol identity, for type/class fallback, so common names can pull unrelated files (`internal/projectreliability/impact.go:221`).
- Extractors mark many references/calls as `unresolved` + `candidate`, especially Go identifiers/selectors and Tree-sitter language references (`internal/projectingestion/parser_go.go:162`, `178`, `200`; `internal/projectingestion/treesitter_extractor.go:411`, `825`, `929`).
- Path heuristics always add domains from path prefixes even when graph evidence is partial or absent (`internal/projectreliability/impact.go:108`).

### false-negative risks

- Cross-file Go/TS/Python/C#/Dart symbol resolution is not modeled. `symbolIDIndex` is built from the current file's symbols only, then used to resolve reference/call/implementation ids (`internal/projectingestion/graph_store.go:1036`, `1100`, `1171`, `1239`).
- Go implementer edges are not extracted; Go parser collects packages/imports/types/functions/methods/references/calls but no `Implementation` entries (`internal/projectingestion/parser_go.go:61`, `86`, `105`, `111`).
- `ListSymbolReferences` only returns references with `target_symbol_id == symbol.ID`; unresolved candidate references to the same name are invisible unless the type/class fallback runs (`internal/projectingestion/graph_store.go:850`; `internal/projectreliability/impact.go:207`).
- Call graph traversal only follows resolved `SYMBOL_CALLS_SYMBOL` relationships, so unresolved call nodes do not contribute to callers/callees (`internal/projectingestion/graph_store.go:937`, `954`).

### degraded-index behavior

- Impact analysis does not trust degraded indexes silently: it sets `Partial`, `PartialReason`, and residual unknowns when `SearchIndexHealth` is degraded or unknown (`internal/projectreliability/impact.go:121`).
- Risk remains: after marking partial, it still returns ordinary path heuristic domains and any graph anchors it can find. Callers may treat that as actionable unless they enforce `partial == false`.
- Context health itself is too expensive and fragile under ingestion: it uses one 250ms probe context across latest run, active run, file count, symbol count, chunk count, search health, and workspace git (`internal/projectreliability/service.go:126`). Counts page through files and symbols rather than using O(1) count queries (`internal/projectreliability/service.go:347`, `354`).

## Missing Graph Relations / Data

- Cross-file symbol table: no project/package-level symbol identity keyed by language, package/module/import path, receiver, exported name, and file.
- Import/module dependency edges: imports are stored as symbols but there is no `FILE_IMPORTS_FILE`, `PACKAGE_IMPORTS_PACKAGE`, or import alias resolution edge (`internal/projectingestion/parser_go.go:45`).
- Interface/implementation extraction for Go; current implementation extraction is Tree-sitter class-based for C#/JS/TS-style classes (`internal/projectingestion/treesitter_extractor.go:590`).
- Unresolved references/calls are not linked to candidate target symbol sets. Only same-file direct resolutions create `SYMBOL_REFERENCES_SYMBOL` and `SYMBOL_CALLS_SYMBOL` (`internal/projectingestion/graph_store.go:1078`, `1139`).
- Document headings have nodes but no actual `DOCUMENT_HAS_HEADING` or repo-file-to-heading relation in the repo ingestion path (`internal/projectingestion/graph_store.go:292`).
- No test-to-production, route-to-handler, MCP-tool-to-handler, config-to-consumer, schema/migration-to-code, or public API surface relations are currently modeled as first-class graph edges.

## Ingestion And Index Health Assessment

- Stale/deleted file cleanup is mostly correct: live missing-path ingestion marks file state absent, deletes extractor cache, updates graph file state, and deletes search rows (`internal/projectingestion/service.go:1012`, `1024`, `1027`, `1030`, `1033`). Full-scan reconcile does the same for unseen prior files (`internal/projectingestion/service.go:2331`, `2340`, `2343`, `2346`, `2349`).
- Derived graph cleanup is file-scoped and removes `CodeReference`, `CodeCall`, `CodeImplementation`, `CodeSymbol`, `DocumentHeading`, `ContentChunk`, and `FileVersion` before rewriting a file (`internal/projectingestion/graph_store.go:181`, `318`, `338`; `internal/platform/ladybug/ladybug.go:316`).
- Context health can misreport under load because all probes share one short deadline and some probes do full pagination or drift scans (`internal/projectreliability/service.go:126`, `347`, `354`; `internal/projectingestion/search_store.go:461`).
- `ActiveRuns` in context health is derived from latest run only; if a completed zero-delta/live run is newer than a still-running full scan, active run reporting can be missed (`internal/projectreliability/service.go:331`).

## Performance Assessment

- Confirmed fixed: graph `ListNodes` and `ListRelationships` scanned all nodes/relationships despite existing indexes. This was patched to use `repo_file_id` and endpoint indexes where available (`internal/platform/ladybug/ladybug.go:124`, `204`, `274`, `294`).
- Search index health drift check is too heavy for request paths: it loads all eligible states, all FTS file ids across five FTS tables, then checks chunks per file (`internal/projectingestion/search_store.go:482`, `496`, `509`, `675`).
- FTS tables mark `project_id`, `file_id`, symbol ids, and caller/callee ids as `UNINDEXED`, but queries filter by those columns constantly (`internal/platform/sqlite/schema/schema.go:267`, `292`, `311`, `336`; `internal/projectingestion/search_store.go:726`, `879`, `951`, `997`).
- Search queries often read large result sets then apply filtering/sorting/pagination in Go (`internal/projectingestion/search_store.go:740`, `750`, `879`, `938`, `951`, `985`, `997`, `1030`).
- SQLite readiness uses `PingContext` through the shared DB pool with a 250ms timeout (`cmd/mivia-server/main.go:211`; `internal/platform/sqlite/sqlite.go:67`). Under long writes, `/readyz` can fail even though liveness is fine.
- Watcher event forwarding uses unbuffered fsnotify wrapper channels (`internal/projectingestion/watcher.go:44`, `69`, `78`); project-level queues are buffered and coalesce overflow to a rescan (`internal/projectingestion/orchestrator.go:200`, `356`, `466`, `511`), but wrapper backpressure can still block fsnotify forwarding while downstream is slow.

## Confirmed Bugs Fixed

- Fixed graph traversal bottleneck: `MemoryGraph.ListNodes` now uses the existing `nodesByLabelFileID` index for `repo_file_id` filters; `ListRelationships` now uses the existing per-node relationship index for `From`/`To` filters (`internal/platform/ladybug/ladybug.go:124`, `204`, `274`, `294`).
- Added regression tests for repo-file node index updates and endpoint relationship index lookup (`internal/platform/ladybug/ladybug_test.go:10`, `51`).

## Recommended Improvements

| priority | expected benefit | implementation notes |
| --- | --- | --- |
| P0 | Stop MCP/readiness collapse during ingestion | Move readiness SQLite dependency to a non-blocking lightweight query or longer isolated timeout; log dependency names/status in readiness warnings. Do not make readiness depend on expensive index drift. |
| P0 | Make context health reliable | Replace paginated file/symbol counts with direct count methods; split probe deadlines per probe or run probes concurrently with bounded total timeout; expose active runs from ingestion service instead of inferring from latest run. |
| P0 | Reduce false negatives in impact | Add project/package/global symbol index and resolve references/calls/implementations across files after per-file extraction. Store candidate edges separately from direct edges. |
| P1 | Improve call/reference accuracy | Add language-aware import/module resolution for Go and TS/JS first. Model `FILE_IMPORTS_FILE` / `PACKAGE_IMPORTS_PACKAGE`; use import aliases and receiver/package metadata when resolving symbols. |
| P1 | Improve implementer modeling | Add Go interface implementation extraction using type/method sets, at least package-local first. Keep `SYMBOL_IMPLEMENTS_SYMBOL` direct only when verified; store candidates separately. |
| P1 | Make search health cheap | Replace full drift scans on request path with persisted counters/high-water marks updated during ingestion/rebuild; run deep drift audit asynchronously. |
| P1 | Improve SQLite/search performance | Add sidecar relational tables or external-content FTS with indexed `project_id`, `file_id`, symbol/call/reference ids; avoid filtering FTS `UNINDEXED` columns as primary selectors. |
| P2 | Improve graph schema completeness | Add repo-file/document heading relation, import edges, test edges, route/tool handler edges, config consumer edges, migration/schema edges, and generated-file/source-file edges. |
| P2 | Improve watcher backpressure | Buffer fsnotify wrapper channels and record dropped/blocked forwarding metrics; expose oldest queued event age, coalesced event count, and rescan coalescing count in diagnostics. |
| P2 | Improve impact output discipline | Require callers to surface `partial` and residual unknowns prominently; consider refusing precise impact when search/index health is degraded unless an explicit degraded-mode flag is set. |

## Verification Performed

- Read `AGENTS.md`, `.ai/INDEX.md`, and applicable `.ai/rules/*` files.
- Used Mivia MCP first: `projects.list`, `projects.get`, `projects.context_health`, `projects.workspace.git_status`, `projects.diagnostics.ingestion`. `projects.ingestion_status_latest`, `projects.impact.analyze`, and indexed searches timed out; this is recorded as runtime evidence, not treated as source truth.
- Shell/source audit of graph store, extraction, impact analyzer, context health, SQLite search schema/store, scheduler/orchestrator, watcher, health readiness, and SQLite connection behavior.
- Ran `gofmt` with `/home/mac/.local/bin/gofmt`.
- Ran `/home/mac/.local/bin/go test ./internal/platform/ladybug`.
- Ran `/home/mac/.local/bin/go test ./internal/projectingestion ./internal/projectreliability ./internal/platform/ladybug ./internal/platform/health`.

## Remaining Risks

- I did not restart the server, by instruction. Runtime behavior after the graph traversal patch was not verified on the running MCP process.
- MCP indexed context was degraded and timed out during the audit, so source reads and tests were the authoritative evidence.
- I did not run full-repo tests; scoped tests passed for the changed graph code and directly audited ingestion/reliability packages.
- Cross-file symbol resolution, search schema redesign, context health count APIs, and readiness behavior still need implementation.
