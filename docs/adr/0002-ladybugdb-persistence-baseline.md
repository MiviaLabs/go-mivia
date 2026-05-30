# ADR-0002: LadybugDB And SQLite Persistence Baseline

Status: Accepted for bootstrap
Date: 2026-05-30

## Context

The bootstrap plan originally considered PostgreSQL with pgvector and Neo4j. That direction is now removed from bootstrap scope. The selected baseline is embedded LadybugDB for graph persistence plus SQLite for local app configuration.

The repository currently has only the root Go module, dependency anchor, docs, and agent workflow files. There is no service code, schema, migration runner, Docker runtime, REST API, MCP endpoint, or approved PII processing.

## Decision

Use LadybugDB as the bootstrap graph persistence store and SQLite as the local app-configuration store.

- Use `github.com/LadybugDB/go-ladybug` from the root module.
- Use `modernc.org/sqlite` as the bootstrap SQLite driver.
- Keep PostgreSQL, pgvector, Neo4j, and database Docker Compose out of bootstrap.
- Start with one local embedded database path configured by `MIVIA_LADYBUG_PATH`.
- Start with one local SQLite path configured by `MIVIA_SQLITE_PATH`.
- Default local runtime data to an ignored `data/` directory.
- Use in-memory LadybugDB for tests where possible.
- Use in-memory SQLite for tests where possible.
- Keep schema initialization idempotent and forward-only.
- Do not expose raw LadybugDB or SQLite query execution over REST or MCP.

`go-ladybug` is CGO-backed. Before any normal build path imports it, add a controlled native library setup path for local development and CI. The native library directory must remain ignored and uncommitted.

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
- App configuration is local and lightweight without mixing settings into graph data.
- CI must account for native LadybugDB libraries before service packages import `go-ladybug`.
- Vector search is not part of the bootstrap until a later ADR approves embedding provider, vector dimension, and storage model.
- Backups, compaction, concurrency limits, and production-readiness remain open decisions before any production use.

## Verification

Phase 3 verification:

- `go list -m github.com/LadybugDB/go-ladybug`
- `go mod tidy`
- `go test ./...`
- Confirm no PostgreSQL, pgvector, Neo4j, Compose database runtime, database secret files, or database migrations exist.

## References

- `github.com/LadybugDB/go-ladybug` module README in the local Go module cache: CGO-backed official Go binding with native library setup requirements.
- `modernc.org/sqlite` module version list checked with `go list -m -versions modernc.org/sqlite`; bootstrap pins `v1.51.0`.
- `.ai/rules/30-docker-data.md`
