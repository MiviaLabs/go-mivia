# ADR-0003: Mivia Server REST And MCP Boundary

Status: Accepted for bootstrap
Date: 2026-05-30

## Context

The bootstrap needs a simple server surface that can be used by humans, scripts, and MCP-capable agents. The selected persistence baseline is embedded LadybugDB for graph data plus SQLite for local app configuration. There is no approved AI provider, embedding model, public deployment, PII processing, or production auth model yet.

## Decision

Build one Go `mivia-server` first.

- Service entrypoint: `cmd/mivia-server/main.go`.
- REST API base path: `/api/v1`.
- MCP Streamable HTTP endpoint: `/mcp`.
- Liveness endpoint: `/healthz`.
- Readiness endpoint: `/readyz`.
- Shared packages: `internal/platform/config`, `internal/platform/logging`, `internal/platform/health`, `internal/platform/httpserver`, `internal/platform/ladybug`, `internal/platform/sqlite`.
- Domain packages: `internal/agentcontrol` and later `internal/research`.

The REST and MCP surfaces must call the same internal service/store interfaces. They must not duplicate business logic and must not expose raw Ladybug query execution.

## REST Surface

Initial REST contract:

- `GET /healthz`
- `GET /readyz`
- `POST /api/v1/tasks`
- `GET /api/v1/tasks/{id}`
- `POST /api/v1/research-runs`
- `GET /api/v1/research-runs/{id}`

All request and response bodies must be explicit JSON structs with validation. Do not log raw request bodies.

## MCP Surface

Use the MCP Streamable HTTP model at a single `/mcp` endpoint. The endpoint must support initialization and tool/resource discovery before any mutating tools are added.

Initial MCP capabilities:

- Tool: `tasks.create`
- Tool: `tasks.get`
- Tool: `research_runs.create`
- Tool: `research_runs.get`
- Resource: `mivialabs://tasks/{id}`
- Resource: `mivialabs://research-runs/{id}`

MCP requests must enforce request-size limits, content-type checks, origin validation for browser-capable clients, and localhost-only default binding until an auth model is approved.

## Security

- Default bind address: `127.0.0.1`.
- No raw prompt, raw source content, credentials, tokens, or personal data in logs.
- No PII ingestion until the Security/DPO owner approves purpose, legal basis, access model, retention, deletion path, and audit trail.
- No public or non-localhost exposure until authn/authz, rate limits, CORS/origin policy, and audit logging are approved.

## Consequences

- Agents can connect through MCP without a separate process from the REST server.
- Humans and scripts can use the REST API for the same task and research-run lifecycle.
- Transport compatibility and MCP session behavior must be tested explicitly.
- The first implementation stays simple: one server, one embedded datastore, no background workers until later phases need them.

## References

- Model Context Protocol 2025-06-18 transport spec: `https://modelcontextprotocol.io/specification/2025-06-18/basic/transports`
- `docs/adr/0002-ladybugdb-persistence-baseline.md`
- `.ai/rules/20-go-service-standards.md`
- `.ai/rules/30-docker-data.md`
