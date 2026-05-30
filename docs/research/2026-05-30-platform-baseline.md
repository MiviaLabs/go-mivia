# Platform Baseline Research

Date: 2026-05-30
Classification: Internal; PII-prohibited until policy owner approves

## Scope

This note records the bootstrap baseline from the active repository plan and `.ai/` rules. It is not approval for production deployment, provider selection, PII processing, broad crawling, or licensed enterprise features.

## Current Baseline

- Language: Go `1.26`
- Toolchain: `go1.26.3`
- Module strategy: one root `go.mod`
- Local PostgreSQL/pgvector runtime: scrapped from bootstrap on 2026-05-30.
- Local Neo4j runtime: scrapped from bootstrap on 2026-05-30.
- LadybugDB: `github.com/LadybugDB/go-ladybug` is selected for simple embedded bootstrap persistence.
- SQLite: `modernc.org/sqlite` selected for local app configuration and non-secret runtime metadata.
- Local orchestration: no database Compose runtime is approved.
- Server interfaces planned: REST under `/api/v1`; MCP Streamable HTTP under `/mcp`.

## Owner Decisions Still Required

- Security/DPO approval before collecting, storing, processing, logging, or deleting PII.
- Engineering approval before selecting an AI provider, embedding model, vector dimension, retention behavior, or provider-specific adapter.
- Engineering approval and ADR before using PostgreSQL, pgvector, Neo4j, or another external datastore for application behavior.
- Native library setup before importing LadybugDB in normal build paths.
- SQLite schema/config review before storing anything beyond non-secret app settings and runtime metadata.
- API/MCP auth model before exposing non-localhost traffic.
- License approval before using Neo4j Enterprise features.
- Architecture review before adding production Kubernetes, Terraform, or cloud deployment resources.

## Phase Boundaries

- Phase 2 adds repository metadata, Go module baseline, scripts, ADR-0001, and this research note.
- Phase 3 selects LadybugDB for simple embedded persistence and plans REST/MCP boundaries.
- Phase 4 adds the single `agent-server` skeleton with health, readiness, REST, MCP, and controlled LadybugDB setup.
- Phase 5 adds OpenAPI, MCP capability docs, and idempotent LadybugDB schema bootstrap.
- Phase 6 adds research package boundaries.
- Phase 7 adds CI, security, observability notes, and runbooks.

## Verification Notes

The Go baseline must be verified locally with `go version`, `go mod tidy`, `go test ./...`, and `make check` after Go 1.26.x is installed in the execution environment.
