# MiviaLabs Agents Monorepo

Generic Go microservices monorepo for AI-agent work.

## Current Phase

Phase 3 selects simple embedded LadybugDB persistence and plans the REST plus MCP server boundary. The repo intentionally has no service entrypoints, Docker runtime, database migrations, CI, or provider-specific integrations yet.

Canonical workflow rules live in `.ai/`. Root agent files are thin adapters only.

## Baseline

- Module: `github.com/MiviaLabs/mivialabs-agents-monorepo`
- Go: `1.26`
- Toolchain: `go1.26.3`
- Module strategy: one root `go.mod`; add `go.work` only if independent module release boundaries become real.
- Root package: `doc.go` only, used to keep baseline Go verification executable before service packages exist.
- Persistence: embedded LadybugDB via `github.com/LadybugDB/go-ladybug` for graph data; SQLite via `modernc.org/sqlite` for local app configuration. PostgreSQL, pgvector, Neo4j, and database Compose runtime are out of bootstrap scope.
- Interfaces planned: REST under `/api/v1` and MCP Streamable HTTP under `/mcp`.

## Planned Layout

- `.ai/`: agent rules, skills, task docs, and handoffs.
- `docs/adr/`: architecture decision records.
- `docs/research/`: source-grounded baseline research and platform notes.
- `cmd/<service>/`: service entrypoints, starting in Phase 4.
- `internal/platform/`: shared platform packages, starting in Phase 4.
- `internal/<domain>/`: domain packages, starting in Phase 6.
- `api/`: API contracts, starting in Phase 5.
- `db/migrations/`: unused during the LadybugDB bootstrap; schema bootstrap belongs behind internal store code until an ADR changes this.
- `tools/`: build-tagged dependency anchors; not application code.

## Local Checks

```sh
go version
go mod tidy
go test ./...
make check
```

If `go` is missing, install Go 1.26.x before treating Phase 2 verification as complete.

LadybugDB is CGO-backed. Future service phases must add an explicit native library setup step before importing `github.com/LadybugDB/go-ladybug` in normal build paths. SQLite configuration must stay local, non-secret, and ignored under `data/` by default.

## Security And Privacy

Do not commit real `.env` files, secrets, credentials, raw prompts, raw fetched content, provider payloads, or personal data. PII ingestion remains prohibited until the Security/DPO owner approves purpose, legal basis, access model, retention, deletion path, and audit trail.
