# System Architecture

Status: Bootstrap current-state
Date: 2026-06-01
Classification: Internal; PII-prohibited
Owners: Engineering owner TBD; Security/DPO required before PII, public exposure, provider, retention, or production decisions.

## Scope

This document describes the current local-only `mivia-server` architecture. It is grounded in `cmd/mivia-server`, `internal/agentcontrol`, `internal/projectregistry`, `internal/research`, `internal/platform`, `api/openapi`, `api/mcp`, and the ADR/security docs.

Local task plans and research plans are not stable technical documentation. Do not link them here; promote durable decisions to README, ADRs, API contracts, runbooks, security docs, or this architecture doc.

## Current Shape

- One Go module with one local service entrypoint: `cmd/mivia-server`.
- HTTP surfaces: `/healthz`, `/readyz`, REST under `/api/v1`, and MCP Streamable HTTP under `/mcp`.
- Domain services: `internal/agentcontrol` for tasks, research runs, redacted agent-run metadata, and promotion-gate decisions; `internal/research` for redacted research source metadata.
- Local project services: `internal/projectregistry` loads optional local project config from `configs/mivia-server.local.toml` or explicit `MIVIA_CONFIG_PATH`, validates local roots and patterns, exposes bounded project metadata, runs manual metadata-only digest, and routes content graph data to per-project `persistent` or `in_memory` graph storage.
- Reliability services: `internal/projectreliability` exposes context health, changed-path impact analysis, and stale-claim checking through REST and MCP without raw diff or document-content echoing.
- Context-pack services: `internal/projectcontext` composes bounded text search hits, file metadata, symbol metadata, and optional impact analysis without new storage, roots, raw diffs, provider calls, or full chunk text.
- Project ingestion services: `internal/projectingestion` handles eligible local source safety gates, chunking, promoted AST extraction, extractor cache, per-project SQLite FTS5 search indexing, bounded project-targeted graph writes, SQLite run/file state, bounded REST/MCP query views, fair scheduling, live watcher orchestration, parallel full-scan file workers, search-index repair, startup recovery for interrupted runs, and periodic running-progress persistence.
- Project workspace services: `internal/projectworkspace` handles governed git status/diff, current eligible file reads with opaque edit tokens, and token-guarded exact byte-span edits for explicitly opted-in workspaces.
- Project evidence services: `internal/projectevidence` stores project-scoped Evidence Graph metadata for claims, evidence refs, decisions, actions, outcomes, artifact links, and promotion links through REST and MCP without raw prompts, raw source dumps, provider payloads, secrets, roots, raw stderr, or PII.
- Stores: Ladybug graph abstraction for graph data; lazy-opened Pebble-backed Ladybug graphs for durable content-graph persistence; SQLite for local app configuration, ingestion state, extractor cache, and FTS-backed governed search. Content-graph projects can use persistent project-scoped graph/search stores or process-local memory; persistent stores derive from the configured Ladybug path parent under `projects/<project-id>/`, with project search filenames tied to the Pebble graph storage epoch.
- Boundary: localhost-only by default; no approved production deployment, public API exposure, auth model, live provider, external crawling, embedding provider, vector dimension, arbitrary shell endpoint, raw patch upload, git commit/push/checkout/reset/branch/merge/rebase/stash/clean/restore tool, or PII processing.

## Component And Data Flow

```mermaid
flowchart TB
  LocalClient["Local caller or Codex Desktop"]
  Server["cmd/mivia-server"]
  Health["healthz and readyz"]
  REST["REST adapter /api/v1"]
  MCP["MCP adapter /mcp"]
  AgentService["internal/agentcontrol service"]
  AgentRuns["redacted agent-run metadata"]
  Promotions["promotion-gate decisions"]
  ProjectRegistry["internal/projectregistry registry"]
  Reliability["internal/projectreliability"]
  ContextPacks["internal/projectcontext"]
  ProjectDigest["metadata-only project digest"]
  ProjectIngestion["content graph ingestion"]
  ProjectWorkspace["governed workspace status/diff/read/edit"]
  ProjectEvidence["project Evidence Graph metadata"]
  Scheduler["fair ingestion scheduler"]
  Watcher["live watcher orchestrator"]
  FullScanWorkers["bounded full-scan file workers"]
  FlushWindows["weighted prepared-file flush windows"]
  GraphRouter["project graph router"]
  SemanticGraph["symbols, references, calls, headings"]
  SearchIndex["project-scoped SQLite FTS5 search index"]
  ASTSearch["named AST structural search"]
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
  AgentService --> AgentRuns
  AgentRuns --> Promotions
  REST --> ProjectRegistry
  MCP --> ProjectRegistry
  REST --> Reliability
  MCP --> Reliability
  REST --> ContextPacks
  MCP --> ContextPacks
  ContextPacks --> Reliability
  Reliability --> ProjectRegistry
  Reliability --> ProjectWorkspace
  Reliability --> SearchIndex
  ProjectRegistry --> ProjectDigest
  ProjectRegistry --> ProjectWorkspace
  ProjectDigest --> Ladybug
  ProjectRegistry --> Scheduler
  Watcher --> Scheduler
  Scheduler --> ProjectIngestion
  ProjectIngestion --> FullScanWorkers
  FullScanWorkers --> FlushWindows
  FlushWindows --> SemanticGraph
  SemanticGraph --> GraphRouter
  GraphRouter --> Ladybug
  FlushWindows --> SearchIndex
  SearchIndex --> SQLite
  SearchIndex --> ContextPacks
  SemanticGraph --> ASTSearch
  SemanticGraph --> ContextPacks
  ASTSearch --> REST
  ASTSearch --> MCP
  ProjectIngestion --> Ladybug
  ProjectIngestion --> SQLite
  ProjectWorkspace --> ProjectIngestion
  ProjectWorkspace --> REST
  ProjectWorkspace --> MCP
  REST --> ProjectEvidence
  MCP --> ProjectEvidence
  ProjectEvidence --> GraphRouter
  ConfigFile --> ProjectRegistry
  REST --> ResearchService
  MCP --> ResearchService
  ResearchService --> Redaction
  AgentService --> Ladybug
  ResearchService --> Ladybug
  AgentRuns --> Ladybug
  AgentService --> SQLite
  AgentRuns --> SQLite
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
  participant Server as mivia-server mux
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
  participant Server as mivia-server
  participant Registry as project registry
  participant Digest as metadata-only digest
  participant Graph as Ladybug graph
  participant Client as REST or MCP client

  Engineer->>Config: Copy configs/mivia-server.example.toml and edit local placeholders
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
  participant Server as mivia-server
  participant Registry as project registry
  participant Scheduler as fair scheduler
  participant Workers as full-scan file workers
  participant Ingestion as project ingestion service
  participant Watcher as live watcher
  participant Graph as target project graph store
  participant Search as target project search store
  participant SQLite as SQLite ingestion state

  Client->>Server: Manual ingest, repair, latest status, file list, outline, chunk list, FTS search, AST search, symbol source, references, calls, or call graph
  Server->>Registry: Resolve enabled content_graph project
  Registry-->>Server: Project with graph_storage setting and derived project graph/search paths
  Server->>Scheduler: Submit manual ingestion or search-index repair asynchronously
  Server->>SQLite: Read latest run status or bounded query metadata
  Watcher->>Scheduler: Submit live path event or overflow rescan
  Scheduler->>Ingestion: Run bounded full scan or priority path task
  Ingestion->>Workers: Enumerate safely and dispatch eligible regular files
  Workers->>Workers: Apply size, binary, UTF-8, sensitive-marker gates, chunking, and promoted extraction
  Workers-->>Ingestion: Return prepared file metadata only
  Ingestion->>Ingestion: Flush prepared files by file-count cap or graph/search write weight
  Ingestion->>Graph: Store eligible file versions, chunks, symbols, references, calls, headings, and run metadata through the target project's graph batch
  Ingestion->>SQLite: Store run, file state, extractor cache metadata, and periodic running counters
  Ingestion->>Search: Store FTS rows in bounded subtransactions for the target project search store
  Ingestion->>Ingestion: Tombstone stale files only after enumeration and workers drain
  Ingestion-->>Server: Run metadata with stable run ID
  Server-->>Client: JSON without roots, skipped sensitive content, matched sensitive text, secrets, PII, raw prompts, provider payloads, content hashes, raw SQLite/FTS errors, or raw parser details
```

## Governed Workspace Sequence

```mermaid
sequenceDiagram
  participant Client as REST or MCP client
  participant Server as mivia-server
  participant Workspace as projectworkspace service
  participant Registry as project registry
  participant Git as explicit git argv
  participant Disk as current project disk
  participant Safety as ingestion safety gates
  participant Scheduler as ingestion scheduler

  Client->>Server: git status, git diff, file read, or exact edit
  Server->>Workspace: Pass strict decoded request
  Workspace->>Registry: Require global workspace gate and project workspace_mode
  Registry-->>Workspace: Enabled content_graph project without exposing root
  Workspace->>Git: Status/diff with explicit argv only, no shell
  Workspace->>Disk: Read current selected file only
  Workspace->>Safety: Apply path, include/exclude, symlink, size, binary, UTF-8, and sensitive-marker gates
  Workspace-->>Client: Bounded redacted response with no roots, command lines, stderr, hashes, skipped sensitive text, secrets, PII, prompts, provider payloads, raw parser/SQLite/FTS errors, or stack traces
  Client->>Workspace: Exact edit with opaque token and old_text byte spans
  Workspace->>Safety: Recheck full new content before write
  Workspace->>Disk: Atomic same-directory replace
  Workspace->>Scheduler: Queue path ingestion after successful non-dry-run edit
```

## Governed Search Flow

```mermaid
flowchart LR
  Agent["REST or MCP client"]
  Search["Search request"]
  Literal["Literal FTS search"]
  Metadata["File, symbol, reference, and call filters"]
  Catalog["AST query catalog"]
  NamedAST["Named Tree-sitter AST search"]
  FTS["SQLite FTS5 eligible rows"]
  Graph["Ladybug graph nodes"]
  Chunks["Eligible indexed chunks"]
  Caps["Pagination, snippet, capture, and source caps"]
  Response["Redacted response"]

  Agent --> Search
  Search --> Literal
  Search --> Metadata
  Search --> Catalog
  Search --> NamedAST
  Literal --> FTS
  Metadata --> FTS
  Metadata --> Graph
  Catalog --> Graph
  NamedAST --> Chunks
  FTS --> Caps
  Graph --> Caps
  Chunks --> Caps
  Caps --> Response
  Response --> Agent
```

## Context Pack And Promotion Flow

```mermaid
flowchart LR
  Agent["AI agent"]
  ContextPack["projects.context_pack.build"]
  Search["Indexed text/file/symbol search"]
  Impact["Optional impact analysis"]
  Pack["Bounded context pack"]
  Run["Agent run artifacts"]
  Gate["agent_runs.promote_artifact"]
  EvidenceGraph["projects.evidence_graph.*"]
  Claim["claim evidence decision action outcome"]
  Decision["candidate, validated, promoted, rejected"]

  Agent --> ContextPack
  Agent --> EvidenceGraph
  EvidenceGraph --> Claim
  Claim --> Decision
  ContextPack --> Search
  ContextPack --> Impact
  Search --> Pack
  Impact --> Pack
  Pack --> Agent
  Agent --> Run
  Run --> Gate
  Gate --> Decision
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
- Promoted AST extraction runs after safety gates. Go uses the Go stdlib parser; JS, JSX, TS, TSX, C#, Python, and Dart use mandatory Tree-sitter extractors with embedded queries and startup validation; Markdown and infrastructure/config files use metadata-only extractors. Dart generated files are indexed by default unless project config excludes them, and Flutter widget/state/build metadata is promoted through symbols, references, and calls. No regex fallback is allowed for promoted Tree-sitter languages.
- The SQLite extractor cache stores only serialized symbols, headings, references, and calls keyed by project, relative-path hash, content hash, extractor name, and extractor version. It does not store raw source, AST node text, chunks, absolute roots, skipped sensitive content, matched sensitive text, secrets, prompts, provider payloads, or PII. Skipped or absent files do not keep extractor cache rows or content hashes.
- SQLite FTS5 stores governed search rows for eligible chunks, files, symbols, references, and calls. Persistent content-graph projects use one project-scoped search database under the configured storage parent, while shared metadata remains separate. Search APIs are literal/metadata search over already-indexed content, not crawling, provider calls, embeddings, vectors, raw DB queries, or raw FTS query execution. Symbol source, text search, and AST search return text only from eligible indexed chunks and only under explicit caps.
- Context packs compose existing indexed search and reliability metadata only. They return capped snippets and metadata, not full chunk text, raw diffs, roots, provider payloads, secrets, prompts, or PII.
- Promotion gates store metadata-only decisions for existing agent-run artifact refs. They do not copy runtime payloads into the knowledge graph, and validated/promoted/rejected decisions require verifier refs and bounded decision text.
- Evidence Graph records project-scoped metadata only: claim refs, evidence refs, decisions, action refs, outcome refs, artifact refs, promotion refs, safe changed-file refs, run IDs, trace IDs, timestamps, and bounded summaries/rationales. It must not store raw prompts, raw source dumps, provider payloads, secrets, roots, raw stderr, skipped sensitive content, or PII.
- Named AST search runs against eligible indexed chunks using the server-owned query catalog for Go, Python, JavaScript, JSX, TypeScript, TSX, C#, and Dart. Raw Tree-sitter query syntax is not exposed. Coverage gaps such as oversized files are represented only as safe metadata counts.
- Full scans run through a fair scheduler, dispatch configurable file workers under a shared global cap, flush prepared graph/search writes by file-count cap and internal write weight, persist running progress counters, and tombstone stale files only after enumeration and workers drain. REST and MCP manual ingestion and search-index repair calls enqueue work and return run metadata without waiting for scan completion. Live path events have priority over full-scan continuation, and operators can cap per-project worker use below the global worker count when fairness across projects matters.
- On startup, persisted `pending` or `running` ingestion runs from a previous server process are marked failed with `error_category=server_restarted`; live startup scans or fresh manual ingestion are the repair path.

## Operational Boundaries

- Default bind must remain localhost or loopback until authn/authz, origin policy, rate limits, audit logging, monitoring, incident response, and on-call coverage are approved.
- Handlers must not expose raw LadybugDB or SQLite query execution.
- Unit tests must remain fixture-only and must not perform live internet calls.
- Provider, embedding, vector, retention, production deployment, and public API decisions require ADR and owner review.
