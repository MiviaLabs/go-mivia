# ADR-0001: Go Monorepo Architecture

Status: Accepted for bootstrap
Date: 2026-05-30

## Context

This repository is being bootstrapped as a generic Go microservices monorepo for AI-agent work. The first implementation phases need a stable structure without committing to production infrastructure, a specific AI provider, an embedding model, or independent service release boundaries.

## Decision

Use one root Go module:

```text
module github.com/MiviaLabs/go-mivia
go 1.26
toolchain go1.26.3
```

Use this repository shape:

- `cmd/<service>/` for service entrypoints.
- `internal/platform/` for shared platform packages.
- `internal/<domain>/` for domain packages.
- `api/` for public API contracts.
- `db/migrations/` for forward-only migrations.
- `docs/adr/` for architecture decisions.
- `.ai/` for canonical agent workflow rules, skills, task docs, and handoffs.

Do not add `go.work` during bootstrap. Revisit that only if services need independent module versioning or release ownership.

## Rationale

A single module keeps early changes reviewable, keeps dependency management centralized, and avoids premature release boundaries. The planned directory layout matches Go conventions while leaving service code, migrations, Docker, CI, and provider adapters to their approved phases.

## Consequences

- Cross-service shared code must stay under `internal/` until there is a clear need for public packages.
- New dependencies require justification through risk, maintenance, and security review.
- Provider-specific code must wait for an ADR covering provider choice, model family, data handling, and retention.
- Production deployment resources are out of scope for bootstrap.

## Verification

Phase 2 verification requires:

- `go version`
- `go mod tidy`
- `go test ./...`
- `make check`
