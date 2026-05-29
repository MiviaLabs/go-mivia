# Platform Baseline Research

Date: 2026-05-30
Classification: Internal; PII-prohibited until policy owner approves

## Scope

This note records the bootstrap baseline from the active repository plan and `.ai/` rules. It is not approval for production deployment, provider selection, PII processing, broad crawling, or licensed enterprise features.

## Current Baseline

- Language: Go `1.26`
- Toolchain: `go1.26.3`
- Module strategy: one root `go.mod`
- Local relational/vector store planned for Phase 3: `pgvector/pgvector:0.8.2-pg18-trixie`
- Local graph store planned for Phase 3: `neo4j:2026.05.0` Community
- Local orchestration planned for Phase 3: Docker Compose only

## Owner Decisions Still Required

- Security/DPO approval before collecting, storing, processing, logging, or deleting PII.
- Engineering approval before selecting an AI provider, embedding model, vector dimension, retention behavior, or provider-specific adapter.
- License approval before using Neo4j Enterprise features.
- Architecture review before adding production Kubernetes, Terraform, or cloud deployment resources.

## Phase Boundaries

- Phase 2 adds repository metadata, Go module baseline, scripts, ADR-0001, and this research note.
- Phase 3 adds local Docker and database runtime.
- Phase 4 adds service skeletons.
- Phase 5 adds data contracts and migrations.
- Phase 6 adds research package boundaries.
- Phase 7 adds CI, security, observability notes, and runbooks.

## Verification Notes

The Go baseline must be verified locally with `go version`, `go mod tidy`, `go test ./...`, and `make check` after Go 1.26.x is installed in the execution environment.
