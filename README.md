# MiviaLabs Agents Monorepo

Generic Go microservices monorepo for AI-agent work.

## Overview

This repository contains the local MiviaLabs agent service platform. The current service is `agent-server`, a Go HTTP server that exposes REST APIs under `/api/v1` and MCP Streamable HTTP under `/mcp` for local agent-control, research metadata, and project metadata workflows.

The platform is local-first and localhost-only by default. It stores local metadata through the Ladybug graph abstraction and SQLite app-configuration store, supports optional local project configuration, and can run manual metadata-only project digests plus explicitly opted-in local content graph ingestion. It does not ingest PII, call live AI or browsing providers, expose public APIs, run embeddings/vector storage, or use production database infrastructure.

Canonical workflow rules live in `.ai/`. Root agent files are thin adapters only.

## Baseline

- Module: `github.com/MiviaLabs/mivialabs-agents-monorepo`
- Go: `1.26`
- Toolchain: `go1.26.3`
- Module strategy: one root `go.mod`; add `go.work` only if independent module release boundaries become real.
- Server: `cmd/agent-server`
- Local project config: optional, local-only TOML loaded from `configs/agent-server.local.toml` or explicit `MIVIA_CONFIG_PATH`; committed example is `configs/agent-server.example.toml`.
- Persistence: LadybugDB graph abstraction for graph data; SQLite via `modernc.org/sqlite` for local app configuration. Project graph storage is selectable per project with `graph_storage = "persistent"` or `graph_storage = "in_memory"`.
- Interfaces: REST under `/api/v1`; MCP Streamable HTTP under `/mcp`.

## Layout

- `.ai/`: canonical agent workflow rules, skills, and handoffs. Local task and research plans are ignored working artifacts, not technical docs.
- `api/openapi/`: REST OpenAPI contracts.
- `api/mcp/`: MCP capability docs.
- `cmd/agent-server/`: local agent server entrypoint.
- `configs/`: committed local config examples only; developer-local configs stay ignored.
- `internal/agentcontrol/`: task and research-run domain, stores, REST adapter, MCP adapter.
- `internal/projectregistry/`: local project config registry, validation, REST/MCP metadata APIs, and manual metadata-only digest.
- `internal/research/`: fixture-only research boundaries, redaction, metadata storage, REST/MCP hooks.
- `internal/platform/`: config, logging, health, HTTP, Ladybug, SQLite platform packages.
- `docs/`: stable technical documentation index.
- `docs/architecture/`: system architecture and data-flow docs.
- `docs/adr/`: architecture decision records.
- `docs/configuration/`: local configuration guides.
- `docs/research/`: source-grounded baseline notes only; do not store or link research plans.
- `docs/runbooks/`: local development and incident runbooks.
- `docs/security/`: privacy and research-data handling baselines.
- `db/migrations/`: unused during the LadybugDB bootstrap; schema bootstrap belongs behind internal store code until an ADR changes this.
- `tools/`: build-tagged dependency anchors; not application code.

## Documentation

- [Documentation index](docs/README.md)
- [System architecture](docs/architecture/system-architecture.md)
- [REST OpenAPI contract](api/openapi/agent-control.v1.yaml)
- [MCP capability contract](api/mcp/agent-control.v1.md)
- [Local project configuration](docs/configuration/local-projects.md)
- [Local development runbook](docs/runbooks/local-dev.md)
- [Privacy baseline](docs/security/privacy-baseline.md)
- [Research data handling](docs/security/research-data-handling.md)

Do not link `.ai/tasks/*` files or research-plan files from technical docs. They are local, stale-prone working artifacts.

## Local Checks

```sh
go version
go mod tidy
go test ./...
make check
```

If `go` is missing, install Go 1.26.x before treating verification as complete.

## Run Locally

Foreground server:

```sh
MIVIA_HTTP_ADDR=127.0.0.1:8080 \
MIVIA_SQLITE_PATH=:memory: \
go run ./cmd/agent-server
```

Optional local project config:

```sh
cp configs/agent-server.example.toml configs/agent-server.local.toml
MIVIA_CONFIG_PATH=configs/agent-server.local.toml go run ./cmd/agent-server
```

Use placeholder paths only in committed docs and examples. Local configs are ignored and must not contain secrets, tokens, PII, raw prompts, raw source content, or provider payloads.

Smoke:

```sh
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS -H 'Content-Type: application/json' \
  -d '{"title":"local smoke"}' \
  http://127.0.0.1:8080/api/v1/tasks
curl -fsS http://127.0.0.1:8080/api/v1/projects
curl -fsS \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-06-18' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  http://127.0.0.1:8080/mcp
```

## Codex Desktop MCP

Codex Desktop can register the server directly as a Streamable HTTP MCP server:

```powershell
codex mcp add mivialabs-agent-server --url http://127.0.0.1:8080/mcp
codex mcp get mivialabs-agent-server
```

For a long-running WSL process from Windows, build once and run the binary:

```powershell
wsl -d Ubuntu --cd /home/mac/mivialabs/mivialabs-agents-monorepo env PATH=/home/mac/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin go build -o /tmp/mivialabs-agent-server ./cmd/agent-server
wsl -d Ubuntu --cd /home/mac/mivialabs/mivialabs-agents-monorepo env MIVIA_HTTP_ADDR=127.0.0.1:8080 MIVIA_SQLITE_PATH=:memory: /tmp/mivialabs-agent-server
```

The currently exposed MCP tools are `tasks.create`, `tasks.get`, `research_runs.create`, `research_runs.get`, `research_sources.create`, `research_sources.get`, `projects.list`, `projects.get`, `projects.digest`, `projects.ingest`, `projects.ingestion_status`, `projects.files.list`, `projects.file.chunks`, and `projects.symbols.list`. Codex Desktop may show underscore-normalized callable names such as `tasks_create` or `projects_digest`; the server accepts both forms.

## Local Project APIs

Project APIs are for engineer local computers only. REST exposes project list/get, manual digest, manual ingestion, ingestion status, file, chunk, and symbol metadata endpoints under `/api/v1`; MCP exposes matching project tools and resources.

Project config is local-only and loaded through `MIVIA_CONFIG_PATH` or the ignored default `configs/agent-server.local.toml`. The committed schema example is [configs/agent-server.example.toml](configs/agent-server.example.toml).

Project digest is manual and metadata-only. Content graph ingestion is opt-in with `digest_mode = "content_graph"` and uses the same local path, denylist, binary, UTF-8, size, and sensitive-marker gates before storing eligible source chunks. REST/MCP responses omit local root paths, datastore paths, skipped sensitive content, matched sensitive text, secrets, raw prompts, provider payloads, and PII.

Live project updates require both global live enablement and per-project `update_policy = "live"`. The watcher is directory-based, non-recursive at the OS API level, and registers each eligible directory; overflow or full queues trigger a bounded project rescan. Manual ingestion remains available as fallback.

LadybugDB native imports remain gated behind `scripts/ladybug-libs.sh` and the `ladybug_native system_ladybug` tags. SQLite configuration and persistent graph files must stay local, non-secret, and ignored under `data/` by default.

## Security And Privacy

Do not commit real `.env` files, secrets, credentials, raw prompts, raw fetched content, provider payloads, or personal data. PII ingestion remains prohibited until the Security/DPO owner approves purpose, legal basis, access model, retention, deletion path, and audit trail.
