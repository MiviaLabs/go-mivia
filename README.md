# MiviaLabs Agents Monorepo

Generic Go microservices monorepo for AI-agent work.

## Current Phase

Phase 2 is the repository and Go baseline. The repo intentionally has no service entrypoints, Docker runtime, database migrations, CI, or provider-specific integrations yet.

Canonical workflow rules live in `.ai/`. Root agent files are thin adapters only.

## Baseline

- Module: `github.com/MiviaLabs/mivialabs-agents-monorepo`
- Go: `1.26`
- Toolchain: `go1.26.3`
- Module strategy: one root `go.mod`; add `go.work` only if independent module release boundaries become real.

## Planned Layout

- `.ai/`: agent rules, skills, task docs, and handoffs.
- `docs/adr/`: architecture decision records.
- `docs/research/`: source-grounded baseline research and platform notes.
- `cmd/<service>/`: service entrypoints, starting in Phase 4.
- `internal/platform/`: shared platform packages, starting in Phase 4.
- `internal/<domain>/`: domain packages, starting in Phase 6.
- `api/`: API contracts, starting in Phase 5.
- `db/migrations/`: forward-only database migrations, starting in Phase 5.

## Local Checks

```sh
go version
go mod tidy
go test ./...
make check
```

If `go` is missing, install Go 1.26.x before treating Phase 2 verification as complete.

## Security And Privacy

Do not commit real `.env` files, secrets, credentials, raw prompts, raw fetched content, provider payloads, or personal data. PII ingestion remains prohibited until the Security/DPO owner approves purpose, legal basis, access model, retention, deletion path, and audit trail.
