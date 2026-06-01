# ADR-0002: Ladybug Graph And SQLite Persistence Baseline

Status: Accepted for bootstrap; amended 2026-06-02 for Pebble-backed content graph persistence
Date: 2026-05-30

## Context

The bootstrap plan originally considered PostgreSQL with pgvector and Neo4j. That direction is now removed from bootstrap scope. The selected baseline is the internal Ladybug graph abstraction for graph persistence plus SQLite for local app configuration.

The repository currently has only the root Go module, dependency anchor, docs, and agent workflow files. There is no service code, schema, migration runner, Docker runtime, REST API, MCP endpoint, or approved PII processing.

## Decision

Use the internal Ladybug graph abstraction as the bootstrap graph persistence boundary and SQLite as the local app-configuration store. For durable content-graph projects, `graph_storage = "persistent"` uses the Pebble-backed Ladybug graph implementation.

- Keep `github.com/LadybugDB/go-ladybug` only as the native dependency anchor until native usage is explicitly re-approved.
- Use `github.com/cockroachdb/pebble/v2` for durable content-graph storage under the Ladybug abstraction.
- Use `modernc.org/sqlite` as the bootstrap SQLite driver.
- Keep PostgreSQL, pgvector, Neo4j, and database Docker Compose out of bootstrap.
- Start with one local embedded database path configured by `MIVIA_LADYBUG_PATH`.
- Start with one local SQLite path configured by `MIVIA_SQLITE_PATH`.
- Default local runtime data to an ignored `data/` directory.
- Use in-memory Ladybug graphs for tests where possible.
- Use in-memory SQLite for tests where possible.
- Keep schema initialization idempotent and forward-only.
- Do not expose raw LadybugDB or SQLite query execution over REST or MCP.

`go-ladybug` is CGO-backed. Before any normal build path imports native LadybugDB, add a controlled native library setup path for local development and CI. The native library directory must remain ignored and uncommitted. Pebble is pure Go and is now the durable content-graph persistence implementation.

## Initial Data Shape

Use a graph-first shape for the bootstrap:

- `Agent`: service or operator identity metadata.
- `Task`: agent task lifecycle and status.
- `ResearchRun`: research or deep-research run metadata.
- `Source`: source URL or artifact reference metadata.
- `Document`: canonical artifact metadata.
- `Chunk`: redacted excerpt or chunk metadata.

Initial relationships:

- `AGENT_RAN_TASK`
- `TASK_CREATED_RESEARCH_RUN`
- `TASK_USED_SOURCE`
- `DOCUMENT_HAS_CHUNK`
- `DOCUMENT_LINKS_TO_DOCUMENT`
- `TASK_TOUCHED_REPO_FILE`

Do not store raw prompts, raw fetched content, credentials, tokens, or personal data.

Initial SQLite app-configuration tables:

- `app_settings`: key, value, value_type, updated_at.
- `runtime_flags`: key, enabled, description, updated_at.
- `schema_versions`: component, version, applied_at.

SQLite must not store real secrets, credentials, tokens, raw prompts, raw fetched content, or personal data.

## Consequences

- The first service can run without external database containers.
- Content-graph writes avoid JSONL snapshot/replay scaling limits.
- App configuration is local and lightweight without mixing settings into graph data.
- CI does not need native LadybugDB libraries for the Pebble-backed content graph.
- Vector search is not part of the bootstrap until a later ADR approves embedding provider, vector dimension, and storage model.
- Backups, compaction, concurrency limits, and production-readiness remain open decisions before any production use.
- No migration is supported from legacy JSONL graph data to Pebble. Stop the server, delete ignored local graph files, and run full reingestion.

## Verification

Phase 3 verification:

- `go list -m github.com/LadybugDB/go-ladybug`
- `go list -m github.com/cockroachdb/pebble/v2`
- `go mod tidy`
- `go test ./...`
- Confirm no PostgreSQL, pgvector, Neo4j, Compose database runtime, database secret files, or database migrations exist.

## References

- `github.com/LadybugDB/go-ladybug` module README in the local Go module cache: CGO-backed official Go binding with native library setup requirements.
- `modernc.org/sqlite` module version list checked with `go list -m -versions modernc.org/sqlite`; bootstrap pins `v1.51.0`.
- `.ai/rules/30-docker-data.md`
