# System Architecture

Status: Bootstrap current-state
Date: 2026-05-30
Classification: Internal; PII-prohibited
Owners: Engineering owner TBD; Security/DPO required before PII, public exposure, provider, retention, or production decisions.

## Scope

This document describes the current local-only `agent-server` architecture. It is grounded in `cmd/agent-server`, `internal/agentcontrol`, `internal/research`, `internal/platform`, `api/openapi`, `api/mcp`, and the ADR/security docs.

Local task plans and research plans are not stable technical documentation. Do not link them here; promote durable decisions to README, ADRs, API contracts, runbooks, security docs, or this architecture doc.

## Current Shape

- One Go module with one local service entrypoint: `cmd/agent-server`.
- HTTP surfaces: `/healthz`, `/readyz`, REST under `/api/v1`, and MCP Streamable HTTP under `/mcp`.
- Domain services: `internal/agentcontrol` for tasks and research runs; `internal/research` for redacted research source metadata.
- Stores: Ladybug graph abstraction for graph data; SQLite for local app configuration. Normal builds use the in-memory Ladybug graph unless native tags are enabled.
- Boundary: localhost-only by default; no approved production deployment, public API exposure, auth model, live provider, external crawling, embedding provider, vector dimension, or PII processing.

## Component And Data Flow

```mermaid
flowchart TB
  LocalClient["Local caller or Codex Desktop"]
  Server["cmd/agent-server"]
  Health["healthz and readyz"]
  REST["REST adapter /api/v1"]
  MCP["MCP adapter /mcp"]
  AgentService["internal/agentcontrol service"]
  ResearchService["internal/research service"]
  Redaction["research redaction boundary"]
  Ladybug["Ladybug graph abstraction"]
  SQLite["SQLite app-config store"]
  Contracts["api/openapi and api/mcp contracts"]
  SecurityDocs["docs/security policy docs"]

  LocalClient --> Server
  Server --> Health
  Server --> REST
  Server --> MCP
  REST --> AgentService
  MCP --> AgentService
  REST --> ResearchService
  MCP --> ResearchService
  ResearchService --> Redaction
  AgentService --> Ladybug
  ResearchService --> Ladybug
  AgentService --> SQLite
  Contracts --> REST
  Contracts --> MCP
  SecurityDocs --> AgentService
  SecurityDocs --> ResearchService
```

## REST And MCP Request Sequence

```mermaid
sequenceDiagram
  participant Client as Local caller
  participant Server as agent-server mux
  participant Adapter as REST or MCP adapter
  participant Service as Domain service
  participant Guard as Validation and redaction
  participant Store as Ladybug or SQLite store

  Client->>Server: Localhost HTTP request
  Server->>Adapter: Route by /api/v1 or /mcp
  Adapter->>Guard: Check method, content type, origin, and schema
  Guard->>Service: Pass validated metadata only
  Service->>Guard: Reject raw queries, prompts, secrets, tokens, and personal data
  Service->>Store: Persist local metadata
  Store-->>Service: Return stored metadata
  Service-->>Adapter: Return redacted response model
  Adapter-->>Client: JSON response
```

## Documentation Update Sequence

```mermaid
sequenceDiagram
  participant Change as Future change
  participant Rules as .ai rules and skills
  participant Source as Code, contracts, ADRs
  participant Docs as Stable docs
  participant Checks as Verification

  Change->>Rules: Check documentation impact
  Rules->>Source: Revalidate current behavior
  Source->>Docs: Update README, docs, ADRs, API contracts, runbooks, or security docs
  Docs->>Checks: Run link, Mermaid, secret, test, and make checks
  Checks-->>Change: Record evidence and residual risk
```

## Data Classification

- Internal by default.
- PII ingestion is prohibited until Security/DPO approval covers purpose, legal basis, access model, retention, deletion path, and audit trail.
- REST, MCP, stores, logs, fixtures, docs, traces, and metrics must not contain raw prompts, raw fetched content, provider payloads, credentials, tokens, secrets, or personal data.
- Research source handling stores redacted metadata only; live provider execution and broad crawling remain out of scope.

## Operational Boundaries

- Default bind must remain localhost or loopback until authn/authz, origin policy, rate limits, audit logging, monitoring, incident response, and on-call coverage are approved.
- Handlers must not expose raw LadybugDB or SQLite query execution.
- Unit tests must remain fixture-only and must not perform live internet calls.
- Provider, embedding, vector, retention, production deployment, and public API decisions require ADR and owner review.
