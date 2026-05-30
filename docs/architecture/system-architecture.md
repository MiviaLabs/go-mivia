# System Architecture

Status: Bootstrap current-state
Date: 2026-05-30
Classification: Internal; PII-prohibited
Owners: Engineering owner TBD; Security/DPO required before PII, public exposure, provider, retention, or production decisions.

## Scope

This document describes the current local-only `agent-server` architecture. It is grounded in `cmd/agent-server`, `internal/agentcontrol`, `internal/projectregistry`, `internal/research`, `internal/platform`, `api/openapi`, `api/mcp`, and the ADR/security docs.

Local task plans and research plans are not stable technical documentation. Do not link them here; promote durable decisions to README, ADRs, API contracts, runbooks, security docs, or this architecture doc.

## Current Shape

- One Go module with one local service entrypoint: `cmd/agent-server`.
- HTTP surfaces: `/healthz`, `/readyz`, REST under `/api/v1`, and MCP Streamable HTTP under `/mcp`.
- Domain services: `internal/agentcontrol` for tasks and research runs; `internal/research` for redacted research source metadata.
- Local project services: `internal/projectregistry` loads optional local project config from `configs/agent-server.local.toml` or explicit `MIVIA_CONFIG_PATH`, validates local roots and patterns, exposes bounded project metadata, runs manual metadata-only digest, and routes content graph data to per-project `persistent` or `in_memory` graph storage.
- Project ingestion services: `internal/projectingestion` handles eligible local source safety gates, chunking, promoted AST extraction, extractor cache, bounded graph writes, SQLite run/file state, bounded REST/MCP query views, fair scheduling, and live watcher orchestration.
- Stores: Ladybug graph abstraction for graph data; SQLite for local app configuration and ingestion state. Project graph storage can be persistent or process-local per project.
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
  ProjectRegistry["internal/projectregistry registry"]
  ProjectDigest["metadata-only project digest"]
  ProjectIngestion["content graph ingestion"]
  Scheduler["fair ingestion scheduler"]
  Watcher["live watcher orchestrator"]
  ResearchService["internal/research service"]
  Redaction["research redaction boundary"]
  Ladybug["Ladybug graph abstraction"]
  SQLite["SQLite app-config store"]
  ConfigFile["MIVIA_CONFIG_PATH or ignored local TOML"]
  Contracts["api/openapi and api/mcp contracts"]
  SecurityDocs["docs/security policy docs"]

  LocalClient --> Server
  Server --> Health
  Server --> REST
  Server --> MCP
  REST --> AgentService
  MCP --> AgentService
  REST --> ProjectRegistry
  MCP --> ProjectRegistry
  ProjectRegistry --> ProjectDigest
  ProjectDigest --> Ladybug
  ProjectRegistry --> Scheduler
  Watcher --> Scheduler
  Scheduler --> ProjectIngestion
  ProjectIngestion --> Ladybug
  ProjectIngestion --> SQLite
  ConfigFile --> ProjectRegistry
  REST --> ResearchService
  MCP --> ResearchService
  ResearchService --> Redaction
  AgentService --> Ladybug
  ResearchService --> Ladybug
  AgentService --> SQLite
  ProjectRegistry --> SQLite
  Contracts --> REST
  Contracts --> MCP
  SecurityDocs --> AgentService
  SecurityDocs --> ProjectRegistry
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

## Local Project Config And Digest Sequence

```mermaid
sequenceDiagram
  participant Engineer as Local engineer
  participant Config as MIVIA_CONFIG_PATH or local TOML
  participant Server as agent-server
  participant Registry as project registry
  participant Digest as metadata-only digest
  participant Graph as Ladybug graph
  participant Client as REST or MCP client

  Engineer->>Config: Copy configs/agent-server.example.toml and edit local placeholders
  Server->>Config: Load optional local config
  Config->>Registry: Provide validated project entries
  Registry-->>Server: Project metadata registry ready
  Client->>Server: List/get projects or manually request digest
  Server->>Registry: Resolve project by ID
  Registry->>Digest: Run manual metadata-only digest
  Digest->>Graph: Store project, repo-file, and digest-run metadata
  Graph-->>Digest: Idempotent metadata write complete
  Server-->>Client: Redacted metadata response
```

## Content Graph And Live Update Sequence

```mermaid
sequenceDiagram
  participant Client as REST or MCP client
  participant Server as agent-server
  participant Registry as project registry
  participant Scheduler as fair scheduler
  participant Ingestion as project ingestion service
  participant Watcher as live watcher
  participant Graph as project graph store
  participant SQLite as SQLite ingestion state

  Client->>Server: Manual ingest, latest status, file list, outline, chunk list, or symbol list
  Server->>Registry: Resolve enabled content_graph project
  Registry-->>Server: Project with graph_storage setting
  Server->>Scheduler: Submit manual ingestion asynchronously
  Server->>SQLite: Read latest run status or bounded query metadata
  Watcher->>Scheduler: Submit live path event or overflow rescan
  Scheduler->>Ingestion: Run bounded full scan or priority path task
  Ingestion->>Ingestion: Apply path, symlink, include/exclude, size, binary, UTF-8, and sensitive-marker gates
  Ingestion->>Ingestion: Extract metadata with promoted parser registry
  Ingestion->>Graph: Store eligible file versions, chunks, symbols, headings, and run metadata
  Ingestion->>SQLite: Store run, file state, and extractor cache metadata
  Ingestion-->>Server: Run metadata with stable run ID
  Server-->>Client: JSON without roots, skipped sensitive content, matched sensitive text, secrets, PII, raw prompts, or provider payloads
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
- Local project configuration is for engineer local computers only. SQLite may store configured local root paths as ignored local app-configuration state, but REST/MCP project metadata responses omit root paths, include/exclude patterns, datastore paths, and raw database query surfaces.
- Project digest stores metadata only: relative path, extension/language hint, size, mtime, and `metadata_sha256` derived from those metadata fields. It must not store raw source content or file-content hashes.
- Content graph ingestion is approved only for explicitly opted-in `content_graph` projects. It may store eligible local source chunks after all gates pass. Skipped sensitive content, matched sensitive-marker text, secrets, PII, raw prompts, provider payloads, and absolute roots must not be stored or returned.
- Promoted AST extraction runs after safety gates. Go uses the Go stdlib parser; JS, JSX, TS, TSX, and C# use mandatory Tree-sitter extractors with embedded queries and startup validation; Markdown and infrastructure/config files use metadata-only extractors. No regex fallback is allowed for promoted Tree-sitter languages.
- The SQLite extractor cache stores only serialized symbols and headings keyed by project, relative-path hash, content hash, extractor name, and extractor version. It does not store raw source, AST node text, chunks, absolute roots, skipped sensitive content, matched sensitive text, secrets, prompts, provider payloads, or PII.
- Full scans commit graph writes in bounded windows and run through a fair scheduler. REST and MCP manual ingestion calls enqueue work and return run metadata without waiting for scan completion. Live path events have priority over full-scan continuation, and global plus per-project limits prevent one project from monopolizing ingestion workers.

## Operational Boundaries

- Default bind must remain localhost or loopback until authn/authz, origin policy, rate limits, audit logging, monitoring, incident response, and on-call coverage are approved.
- Handlers must not expose raw LadybugDB or SQLite query execution.
- Unit tests must remain fixture-only and must not perform live internet calls.
- Provider, embedding, vector, retention, production deployment, and public API decisions require ADR and owner review.
