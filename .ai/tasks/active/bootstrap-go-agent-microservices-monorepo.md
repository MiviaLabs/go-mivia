# Bootstrap Go Agent Microservices Monorepo

Status: Active
Last verified: 2026-05-30
Classification: Internal; PII-prohibited until policy owner approves
Owners: Engineering owner TBD; Security/DPO TBD

## Scope

Bootstrap this repository as a generic Go microservices monorepo for AI-agent work.

Goals:

- `.ai/` is the canonical vendor-neutral source of truth.
- `AGENTS.md` and `CLAUDE.md` are thin wrappers.
- Go baseline uses one root module until independent release boundaries exist.
- Local data services use pinned PostgreSQL/pgvector and Neo4j images.
- Migrations are forward-only and non-destructive.
- Research boundaries avoid hardcoding one AI provider.
- CI and verification gates are added after service/data layers exist.

Non-goals:

- No production Kubernetes, Terraform, or cloud deployment in bootstrap.
- No UI.
- No real secrets.
- No paid Neo4j Enterprise features without license approval.
- No PII ingestion until Security/DPO approves purpose, retention, access, and deletion.
- No embedding model or vector dimension until ADR approval.

## Acceptance Criteria

- AC-1: `.ai/` contains canonical agent rules, skills, task/handoff templates, and provider adapter guidance.
- AC-2: `AGENTS.md` and `CLAUDE.md` are thin wrappers that load `.ai/`.
- AC-3: Go monorepo baseline builds and tests with Go 1.26.x.
- AC-4: Compose defines PostgreSQL plus pgvector and Neo4j with pinned versions, healthchecks, volumes, and secret files.
- AC-5: Service skeletons expose `/healthz` and `/readyz`.
- AC-6: Migrations create pgvector extension, base tables, and Neo4j constraints.
- AC-7: Every phase has a copy-paste handoff prompt.
- AC-8: CI runs format, vet, unit tests, Compose config validation, and secret scanning.

## Phase 1 - Agent Workflow Foundation

Status: Completed
Verified: 2026-05-30

Files:

- `.ai/INDEX.md`
- `.ai/rules/00-operating-doctrine.md`
- `.ai/rules/10-security-privacy.md`
- `.ai/rules/20-go-service-standards.md`
- `.ai/rules/30-docker-data.md`
- `.ai/skills/README.md`
- `.ai/skills/project-plan/SKILL.md`
- `.ai/skills/project-implement/SKILL.md`
- `.ai/skills/project-review/SKILL.md`
- `.ai/skills/security-review/SKILL.md`
- `.ai/adapters/codex/README.md`
- `.ai/adapters/claude/README.md`
- `AGENTS.md`
- `CLAUDE.md`
- `.ai/handoffs/README.md`
- `.ai/tasks/active/README.md`
- `.ai/tasks/done/README.md`

Verifier:

- Open `AGENTS.md` and `CLAUDE.md`; both must point to `.ai/INDEX.md` and avoid duplicated policy.
- Check expected files exist.

Verification performed:

- `sed -n '1,80p' AGENTS.md`
- `sed -n '1,80p' CLAUDE.md`
- `find .ai -type f`
- `wc -l AGENTS.md CLAUDE.md`
- `grep -R -n -E '[[:blank:]]$' .ai AGENTS.md CLAUDE.md`

Prompt:

```text
Implement Phase 1 only in /home/mac/mivialabs/mivialabs-agents-monorepo. Create the vendor-neutral .ai rules, skills, adapter docs, AGENTS.md, CLAUDE.md, and handoff/task folders listed in the active bootstrap task. Do not create Go code, Docker files, databases, CI, or service scaffolding. Keep wrappers thin and make .ai the canonical source. Run file existence checks and report changed files plus residual gaps.
```

## Phase 2 - Repo And Go Baseline

Files:

- `README.md`
- `docs/adr/0001-go-monorepo-architecture.md`
- `docs/research/2026-05-30-platform-baseline.md`
- `.gitignore`
- `.editorconfig`
- `.gitattributes`
- `go.mod`
- `Makefile`
- `scripts/check.sh`
- `scripts/test.sh`
- `scripts/lint.sh`

Verifier:

- `go version`
- `go mod tidy`
- `go test ./...`
- `make check`

Prompt:

```text
Implement Phase 2 only. Read .ai first. Add root Go monorepo baseline files, ADR-0001, research baseline, scripts, and Makefile. Use one root go.mod with Go 1.26/toolchain go1.26.3. Do not add Docker, database migrations, or service code yet. If Go is not installed, stop after file creation and report the exact missing tool.
```

## Phase 3 - Local Docker And Databases

Files:

- `compose.yaml`
- `.env.example`
- `secrets/.gitignore`
- `secrets/postgres_password.example`
- `secrets/neo4j_auth.example`
- `infra/postgres/init/001-create-extensions.sql`
- `infra/neo4j/migrations/001-constraints.cypher`
- `docker/Dockerfile.service`
- `infra/README.md`

Verifier:

- `docker compose config`
- `docker compose up -d postgres neo4j`
- PostgreSQL vector extension query.
- Neo4j auth check.

Prompt:

```text
Implement Phase 3 only. Add Compose, env examples, secret examples, PostgreSQL pgvector init SQL, Neo4j constraint migration, and shared service Dockerfile. Pin PostgreSQL/pgvector and Neo4j versions from .ai rules and the active bootstrap task. Do not add Go services beyond Dockerfile support. Validate with docker compose config and, if Compose is available, start only postgres and neo4j and prove vector extension plus Neo4j auth.
```

## Phase 4 - Service Skeletons

Files:

- `cmd/api-gateway/main.go`
- `cmd/research-worker/main.go`
- `cmd/knowledge-indexer/main.go`
- `internal/platform/config`
- `internal/platform/logging`
- `internal/platform/health`
- `internal/platform/postgres`
- `internal/platform/neo4j`
- `internal/platform/httpserver`

Verifier:

- `go test ./...`
- `go run ./cmd/api-gateway`
- Local `/healthz` and `/readyz` smoke test.

Prompt:

```text
Implement Phase 4 only. Add api-gateway, research-worker, knowledge-indexer, and shared platform packages. Keep handlers minimal: health, readiness, config loading, structured logging, PostgreSQL and Neo4j connection probes. Do not implement research logic or schemas beyond what the skeleton needs. Run gofmt, go test ./..., and a local api-gateway smoke test.
```

## Phase 5 - Data Contracts And Migrations

Files:

- `db/migrations/postgres/000001_init.sql`
- `db/migrations/postgres/000002_pgvector.sql`
- `db/migrations/neo4j/000001_constraints.cypher`
- `internal/platform/migrate`
- `api/openapi/agent-control.v1.yaml`
- `docs/adr/0002-embedding-provider-and-vector-dimension.md`

Verifier:

- Migrations apply on empty DB.
- Migrations rerun safely where idempotent.
- Tests assert PostgreSQL and Neo4j constraints.

Prompt:

```text
Implement Phase 5 only. Add forward-only PostgreSQL and Neo4j migrations, migration runner, and initial OpenAPI contract. Create ADR-0002 for embedding provider and vector dimension; do not invent the provider. Migrations must be idempotent on empty local dev databases and must not drop data. Run migration tests and dependency readiness checks.
```

## Phase 6 - Research And Deep-Research Boundaries

Files:

- `internal/research/provider`
- `internal/research/web`
- `internal/research/deep`
- `internal/research/redaction`
- `internal/research/store`
- `docs/security/research-data-handling.md`

Verifier:

- Unit tests for redaction.
- Unit tests for source hashing and duplicate detection.
- Unit tests for task state transitions.
- No live network in unit tests.

Prompt:

```text
Implement Phase 6 only. Add research/deep-research package boundaries, provider interfaces, storage adapters, redaction, and fixture tests. Do not wire a real paid AI or browsing provider unless an ADR already approves it. No live network in unit tests. Prove raw content is not logged and sensitive fields are redacted.
```

## Phase 7 - CI, Security, Observability, Runbooks

Files:

- `.github/workflows/ci.yml`
- `.github/dependabot.yml`
- `.gitleaks.toml`
- `docs/runbooks/local-dev.md`
- `docs/runbooks/incident.md`
- `docs/security/privacy-baseline.md`
- `docs/adr/0003-observability-baseline.md`

Verifier:

- CI workflow syntax validates.
- `make check`
- `docker compose config`
- `go test ./...`
- Secret scanning where installed.

Prompt:

```text
Implement Phase 7 only. Add CI, dependabot, secret scanning config, local-dev runbook, incident runbook, privacy baseline, and ADR-0003. Keep observability minimal unless dependencies are already approved. Run make check, docker compose config, go test ./..., and secret scanning if installed. Report any unavailable tools as residual risk.
```
