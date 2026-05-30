# Agent MCP Ingestion P0 Implementation Plan

Date: 2026-05-30

## Scope

Implement only the P0 items from `docs/reports/tests/2026-05-30-agent-mcp-ingestion-performance-reliability-review.md`.

Jira: not checked by repo constraint.
Confluence: not checked by repo constraint.

## Source Evidence

- Full scans currently return from `filepath.WalkDir` on file-local walk/stat/read/chunk/parse errors in `internal/projectingestion/service.go`.
- Go parse failures currently propagate from `parseEligible` and fail the run instead of becoming file-local skipped/error state.
- `ListFiles` calls `ListFileStates` and paginates the full in-memory result in `internal/projectingestion/service.go`.
- `GetFile` scans all file states to match the opaque file ID in `internal/projectingestion/service.go`.
- `SQLiteStore.ListFileStates` selects all matching rows without storage-level `LIMIT` in `internal/projectingestion/sqlite_store.go`.
- `PersistentGraph.persist` marshals a full pretty JSON graph snapshot and rewrites the whole graph store after each non-batched operation and each batch in `internal/platform/ladybug/persistent.go`.

## Implementation Plan

1. Add file-local failure states.
   - Add non-sensitive skip reasons for read/stat/chunk/parse errors.
   - Convert full-scan file-local errors into skipped states and continue scanning.
   - Keep run failure reserved for root traversal, context cancellation, tombstone, state write, and graph write failures.

2. Push file lookup and pagination into SQLite.
   - Add `GetFileStateByHash`.
   - Add paginated `ListFileStatesPage` using validated page size/token and SQL `LIMIT`.
   - Keep the existing unpaginated list for internal cleanup/tests.
   - Add SQLite indexes that support project/status and project/hash lookup.

3. Replace full-snapshot persistent graph writes with an incremental journal.
   - Preserve loading of existing snapshot files for compatibility.
   - Write new mutations as bounded JSONL operation records.
   - In batch mode, record and append only the batch operations, not a full graph snapshot.
   - Keep in-memory graph semantics and existing query behavior unchanged.

## Acceptance Tests

- Invalid Go syntax does not fail full-repo ingestion and does not block valid files.
- Read/stat/chunk/parse file-local errors produce skipped/error state without raw unsafe content and without blocking useful files.
- Direct file lookup uses the hashed file ID path and returns expected metadata.
- Storage-level pagination returns filtered pages and rejects invalid pagination.
- Persistent graph writes append operation records and do not rewrite the full graph snapshot per node or per batch.
- Existing sensitive skip behavior remains hash-only with no content hash.
- Existing chunk response bounds remain covered by current tests.
