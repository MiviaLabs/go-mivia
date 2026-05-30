# Agent MCP Ingestion P1 Implementation Plan

Date: 2026-05-30

## Scope

Implement the P1 items from `docs/reports/tests/2026-05-30-agent-mcp-ingestion-performance-reliability-review.md` after P0 commit `6c21866`.

Jira: not checked by repo constraint.
Confluence: not checked by repo constraint.

## Source Evidence

- Full-scan cleanup still ignores previously present skipped states because `tombstoneMissingFiles` continues when `state.Status != eligible` in `internal/projectingestion/service.go`.
- Re-ingest cleanup is file-scoped at the graph-store call site, but `MemoryGraph.DeleteNodes` still scans by label/filter; within the current abstraction the practical improvement is to keep every changed/absent file using the file-scoped `repo_file_id` filter and extend tests around transitions.
- Watcher startup still returns watcher factory and directory registration errors from `Orchestrator.Start`, and `cmd/mivia-server` still treats that error as service startup failure.
- Watch registration still reports only a count on success and no degraded status/counts on failure.
- Run metadata still exposes only aggregate counters and one `error_category`; skip/error reason counts are not persisted or exposed.

## Implementation Plan

1. Fix cleanup semantics.
   - Tombstone all previously present states that are not seen in a full scan, including skipped states.
   - Preserve hash-only state for denied and sensitive files by reusing the stored hash and leaving path/content hash empty.
   - Keep file-scoped graph cleanup through `PutFileState` and `deleteDerivedFileNodes`.

2. Add safe ingestion diagnostics.
   - Add a SQLite run reason-count table keyed by project/run/reason.
   - Add run-level `reason_counts` metadata for skip reasons and file-local error reasons only.
   - Increment reason counters during project and path ingestion without exposing raw paths, sensitive matches, or content.

3. Degrade live watching.
   - Make watcher factory and directory registration failures mark a project as degraded and allow other projects/server startup to continue.
   - Track watched/skipped/failed directory counts per project in process.
   - Add optional watch-directory budget in `OrchestratorOptions`; default unlimited to avoid behavior change.
   - Log project ID, reason category, and counts only.

## Acceptance Tests

- Deleted skipped-sensitive file becomes absent without relative path or content hash.
- Deleted denied-path/skipped state becomes absent safely.
- Eligible to skipped transition removes stale chunks/symbols.
- Skipped to eligible transition ingests chunks/symbols cleanly.
- Watcher factory/add failures degrade live mode and do not fail orchestrator/server startup.
- Watch budget records skipped/degraded counts.
- Ingestion status exposes reason counts without roots, raw paths for unsafe/sensitive content, matched sensitive text, or source content.
- Existing P0 tests remain green.
