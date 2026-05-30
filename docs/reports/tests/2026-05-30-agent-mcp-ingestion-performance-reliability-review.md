# Agent MCP Ingestion Performance And Reliability Review

Date: 2026-05-30

## Scope and Evidence Checked

- Repo rules and boundaries: `AGENTS.md`, `.ai/INDEX.md`, `.ai/rules/00-operating-doctrine.md`, `.ai/rules/05-external-systems.md`, `.ai/rules/10-security-privacy.md`, `.ai/rules/20-go-service-standards.md`, `.ai/rules/30-docker-data.md`, `.ai/skills/mivia-mcp/SKILL.md`.
- User-facing docs: `docs/agent-context-guide.md`, `docs/configuration/local-projects.md`.
- Implementation: `cmd/mivia-server/main.go`, `internal/projectingestion/*`, `internal/projectregistry/{httpapi,mcpapi,patterns,service}.go`, `internal/agentcontrol/mcpapi/mcpapi.go`, `internal/platform/ladybug/{ladybug,persistent}.go`, `configs/mivia-server.local.toml`, `configs/mivia-server.example.toml`.
- Tests checked: `internal/projectingestion/*_test.go`, `internal/projectregistry/**/*_test.go`, `internal/agentcontrol/mcpapi/mcpapi_test.go`.
- Jira: not checked by repo constraint.
- Confluence: not checked by repo constraint.

## Current Architecture Summary

- `mivia-server` loads local project config, opens SQLite for project/run/file state, opens a project graph router backed by persistent or memory Ladybug graphs, then exposes REST under `/api/v1` and MCP under `/mcp` (`cmd/mivia-server/main.go:52-131`).
- Manual ingestion walks the project root, applies include/exclude and safety gates, chunks eligible UTF-8 files, parses Go symbols and Markdown headings, writes SQLite file state, writes graph nodes, then tombstones missing eligible files (`internal/projectingestion/service.go:71-168`, `internal/projectingestion/service.go:466-543`).
- Live ingestion starts one fsnotify watcher per live project, recursively watches included directories, debounces file events, and enqueues per-path ingestion or full rescans on overflow/queue pressure (`internal/projectingestion/orchestrator.go:78-130`, `internal/projectingestion/orchestrator.go:185-365`).
- MCP discovery exposes project metadata, ingestion runs, file metadata, bounded chunks, and symbol lists. File/chunk/symbol responses are paginated with max page size 100 (`internal/projectregistry/mcpapi/mcpapi.go:120-213`, `internal/projectingestion/query.go:14-18`).

## Confirmed Performance/Reliability Issues

1. Full-repo ingestion can fail on one bad file.
   - `filepath.WalkDir` returns on the first walk/stat/read/chunk/parse error (`internal/projectingestion/service.go:90-154`).
   - `parseEligible` currently returns Go parser errors as fatal scan errors (`internal/projectingestion/service.go:492-495`, `internal/projectingestion/parser_go.go:19-22`).
   - Impact: a single syntactically broken Go file, transient read error, or permission issue can fail an enterprise monorepo scan after partial work.

2. Persistent graph writes are not durable at monorepo scale.
   - The persistent graph is an in-memory graph serialized as a full pretty-printed JSON snapshot on persist (`internal/platform/ladybug/persistent.go:144-188`).
   - Batch mode reduces per-node fsyncs, but still marshals and rewrites the entire graph once per batch (`internal/platform/ladybug/persistent.go:85-99`).
   - Chunks store raw eligible chunk text inside graph node properties (`internal/projectingestion/graph_store.go:75-96`), so graph snapshot size grows with indexed source text.
   - Impact: large repositories will hit high memory, CPU, disk-write amplification, and long graph mutex holds.

3. SQLite state writes and query paths are unbatched and unindexed at the service layer.
   - Each file state is saved with one `ExecContext` call (`internal/projectingestion/sqlite_store.go:96-145`).
   - `ListFileStates` loads every matching row into memory, then service-level pagination slices the in-memory result (`internal/projectingestion/sqlite_store.go:147-191`, `internal/projectingestion/service.go:276-300`).
   - `GetFile` scans all file states to find one opaque ID (`internal/projectingestion/service.go:332-350`).
   - Impact: list/get operations become O(project file count), and ingestion pays one SQLite upsert per file without transaction batching.

4. Graph query APIs paginate after loading and sorting full result sets.
   - `ListChunks` loads all chunk nodes for a file and sorts before pagination (`internal/projectingestion/graph_store.go:261-291`).
   - `ListSymbols` loads all project symbols and sorts before pagination (`internal/projectingestion/graph_store.go:312-338`).
   - Impact: symbol discovery becomes expensive exactly where agents need it most.

5. Re-ingest cleanup is correct for changed/deleted eligible files but expensive and incomplete.
   - Changed-file cleanup deletes `CodeSymbol`, `DocumentHeading`, `ContentChunk`, and `FileVersion` nodes before rewriting (`internal/projectingestion/graph_store.go:39-143`, `internal/projectingestion/graph_store.go:423-430`).
   - Tests confirm stale symbol/chunk replacement and deleted eligible-file tombstones (`internal/projectingestion/service_test.go:237-313`).
   - Tombstoning only processes prior `eligible` states, so deleted skipped files can remain `present=true` forever (`internal/projectingestion/service.go:519-543`).
   - Each derived-node delete is label/filter based and scans graph nodes in the memory backend (`internal/platform/ladybug/ladybug.go:127-150`).

6. Watcher startup is not durable for thousands of directories.
   - Startup recursively calls `watcher.Add` for every included directory and returns the first error (`internal/projectingestion/orchestrator.go:324-365`).
   - `mivia-server` treats orchestrator start failure as service startup failure (`cmd/mivia-server/main.go:94-104`).
   - The local MASS config enables live updates, initial startup scan, queue depth 128, worker count 2, and include `**/*` (`configs/mivia-server.local.toml:18-27`, `configs/mivia-server.local.toml:76-78`).
   - Impact: OS watch limits, mounted filesystem behavior, or one inaccessible directory can prevent the local server from starting instead of degrading to manual ingestion.

7. Error diagnostics are too coarse for large scans.
   - Run metadata exposes only one `error_category` plus aggregate counts (`internal/projectingestion/query.go:25-39`).
   - Full-scan failures return sanitized but non-actionable messages such as `walk_failed`, `stat failed`, `read failed`, or `parse failed` without per-file failure counts or categories (`internal/projectingestion/service.go:90-154`, `internal/projectingestion/service.go:479-495`).
   - Watch walk errors collapse to `watch walk failed` (`internal/projectingestion/orchestrator.go:338-340`).
   - Impact: operators cannot tell whether skipped/failed counts are expected safety behavior, parser weakness, filesystem trouble, or a config mistake.

8. MCP discovery is safe but too weak for programming-agent navigation.
   - `projects.files.list` filters by status and extension only (`internal/projectregistry/mcpapi/mcpapi.go:147-167`).
   - `projects.symbols.list` has no file, language, kind, name, prefix, or package filters (`internal/projectregistry/mcpapi/mcpapi.go:199-213`).
   - Symbol extraction covers Go package/import/type/function/method symbols and Markdown headings only; headings are written but not exposed through MCP list tools, and TS/JS/infra/config files have no semantic extraction (`internal/projectingestion/service.go:607-618`, `internal/projectingestion/parser_go.go:14-89`, `internal/projectingestion/parser_markdown.go:9-51`).
   - Impact: agents must fall back to broad file/chunk scans for non-Go repos and mixed monorepos.

## Confirmed Safety/Privacy Posture

- Server docs and config keep the service loopback-local and reject public exposure without separate review (`docs/configuration/local-projects.md`, `configs/mivia-server.example.toml:7-12`).
- MCP/REST project responses are designed to omit roots and datastore paths; tests assert no root leakage in ingestion responses (`internal/projectregistry/httpapi/httpapi_test.go:78-135`, `internal/agentcontrol/mcpapi/mcpapi_test.go:144-189`).
- Sensitive-content skips are hash-only: no relative path, no content hash, and no chunk text (`internal/projectingestion/service.go:553-604`, `internal/projectingestion/service_test.go:159-198`).
- Safety gates skip denied paths, secrets-like content, binary content, NUL bytes, invalid UTF-8, oversized files, and symlinks (`internal/projectingestion/safety.go:20-55`, `internal/projectingestion/safety.go:79-113`, `internal/projectingestion/service.go:105-135`).
- Chunk responses are bounded by project/request max chunk bytes and UTF-8 truncation (`internal/projectingestion/query.go:131-140`, `internal/projectingestion/query.go:186-201`).
- There is no raw DB query endpoint in the reviewed REST/MCP project ingestion surfaces.

## Prioritized Improvement Plan

P0 - Make scans fault-tolerant.
- Convert per-file read/stat/parse/chunk errors into file-level failed/skipped state with non-sensitive reason codes and relative path hash.
- Keep full-run status `completed_with_errors` or `completed` plus `files_failed`, not failed, when only file-local errors occur.
- Reserve run-level failure for root traversal, store corruption, context cancellation, or graph/state write failure.
- Add tests for invalid Go syntax, unreadable file, transient stat/read failure, and mixed good/bad files proving useful files still ingest.

P0 - Replace JSON-snapshot persistence for content graph scale.
- Move persistent graph storage to indexed/incremental writes or the native Ladybug backend before treating enterprise monorepo ingestion as durable.
- Store chunk text outside monolithic graph snapshots or in an indexed chunk table keyed by project/file/version/chunk.
- Add bulk `PutNodes`, `PutRelationships`, `DeleteByProjectFile`, and commit every bounded batch of files.
- Ensure batch failures do not persist partial graph mutation unless the run is intentionally marked partial and diagnosable.

P0 - Push pagination and lookups into storage.
- Change `ListFileStates` to accept pagination and execute SQL `LIMIT` plus cursor filtering.
- Add direct file lookup by `(project_id, relative_path_hash)` instead of scanning all states.
- Add SQLite indexes for project/status/extension/hash and persist extension as a first-class state column.
- Add graph indexes or storage APIs for project/file/chunk index and project/symbol name/kind/file filters.

P1 - Fix cleanup semantics and cost.
- Tombstone all previously present states not seen in a full scan, including skipped states, while keeping sensitive/denied paths hash-only.
- Replace label-wide derived-node deletes with indexed file-scoped deletes.
- Add tests for deleted skipped-sensitive, deleted denied-path, changed Markdown heading, and file changing from eligible to skipped and back.

P1 - Make live watching degrade instead of killing startup.
- If watcher creation or directory registration fails, start the server with that project marked `live_degraded` and require manual ingestion fallback.
- Add a configurable watch-directory budget and report watched, skipped, failed, and degraded counts.
- Avoid absolute paths in startup and watcher errors; log project ID, relative path hash where available, error category, and count.
- Default `initial_scan_on_start=false` for very large live projects unless explicitly accepted by the local owner.

P1 - Improve diagnosability without leaking content.
- Persist per-run skip/error counters by reason: denied path, too large, binary, invalid UTF-8, sensitive content, parse error, read error, stat error, watch degraded.
- Expose bounded diagnostics through `projects.ingestion_status`; no raw paths for unsafe/sensitive cases and no matched sensitive text.
- Add run duration, max RSS if practical, graph write time, SQLite write time, and files/sec metrics.

P2 - Strengthen MCP discovery for agents.
- Add filters to `projects.symbols.list`: `kind`, `name_prefix`, `file_id`, `extension`, `package`.
- Add `projects.headings.list` or include document headings in a separate document-symbol list.
- Add `projects.files.list` filters for path prefix, skipped reason, modified-since, and presence.
- Add a bounded `projects.file.outline` flow returning metadata, headings/symbols, and chunk IDs without full chunk text.

P2 - Expand semantic extraction carefully.
- Add Markdown headings to MCP output first, since parsing already exists.
- Add lightweight extractors for TS/JS exports/classes/functions, Dockerfile stages, Makefile targets, OpenAPI paths, SQL migration names, and YAML/TOML/JSON top-level keys.
- Require dependency/security review before adding tree-sitter or other parser dependencies.

## Recommended Tests/Benchmarks

- Unit: full scan continues when one file has invalid Go syntax.
- Unit: unreadable/stat-failing file records a non-sensitive error state and does not fail unrelated files.
- Unit: deleted skipped-sensitive state becomes absent without exposing relative path or content hash.
- Unit: `GetFile` uses direct storage lookup and returns the same metadata as list results.
- Unit: `projects.symbols.list` filters by kind/name/file without scanning unrelated symbols.
- Unit: watcher `Add` failure degrades live mode and does not fail server startup.
- Unit: watcher queue overflow emits one bounded rescan and deduplicates repeated rescans.
- Benchmark: synthetic repo with 10k, 50k, and 100k files across code/docs/infra/config, with bad files and sensitive-marker fixtures.
- Benchmark metrics: scan duration, files/sec, SQLite write time, graph write time, graph store size, max memory, query latency for first and deep pages, live startup directory count, watch registration time.
- Local smoke: representative large monorepo config with include `**/*`, generated/dependency/cache/private exclusions only, manual ingestion first, then live mode with watch budget.

## Implementation Risks Or Human Decisions Needed

- Storage decision: JSON snapshot persistence is the main scale blocker; owner approval is needed for native Ladybug persistence or a chunk/index store split.
- Parser decision: TS/JS and infra semantic extraction may need new parser dependencies; security/maintenance review is required before adding them.
- Privacy decision: any richer diagnostics must stay reason/count/hash-only for sensitive or denied content.
- Operations decision: choose default behavior for live watcher degradation, watch budgets, and startup scan policy for very large local projects.
- Acceptance decision: define target scale numbers before implementation, for example file count, max graph size, initial ingest time, live startup time, and p95 MCP query latency.
