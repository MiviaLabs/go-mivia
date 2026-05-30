# Phase 6 Handoff - REST And MCP Ingestion Surfaces

Task or phase: Phase 6 - REST/MCP Control And Query Surfaces.

Scope completed:
- Added localhost server wiring for manual `content_graph` ingestion control and bounded query surfaces.
- Added REST routes for ingestion runs, run status, files, chunks, and symbols under `/api/v1`.
- Added MCP tools for `projects.ingest`, `projects.ingestion_status`, `projects.files.list`, `projects.file.chunks`, and `projects.symbols.list`.
- Added MCP resources for project file, chunk, and symbol metadata.
- Added bounded pagination, max page size, max chunk-byte truncation, stable opaque IDs, and sensitive-skip redaction.
- Preserved existing metadata-only digest routes and MCP compatibility for `_meta`, JSON-string arguments, resources, and underscore aliases.
- Updated REST OpenAPI and MCP contracts for the new additive surfaces.

Changed files:
- `api/mcp/agent-control.v1.md`
- `api/openapi/agent-control.v1.yaml`
- `cmd/mivia-server/main.go`
- `internal/agentcontrol/mcpapi/mcpapi.go`
- `internal/agentcontrol/mcpapi/mcpapi_test.go`
- `internal/platform/ladybug/ladybug.go`
- `internal/projectingestion/graph_store.go`
- `internal/projectingestion/query.go`
- `internal/projectingestion/service.go`
- `internal/projectregistry/httpapi/httpapi.go`
- `internal/projectregistry/httpapi/httpapi_test.go`
- `internal/projectregistry/mcpapi/mcpapi.go`

Verification:
- `/home/mac/.local/bin/go test ./internal/projectingestion/... ./internal/agentcontrol/mcpapi ./internal/projectregistry/httpapi` passed.
- `/home/mac/.local/bin/go test ./...` passed.
- `git diff --check` passed.
- OpenAPI YAML parsed with Python `yaml.safe_load`.
- Stable-doc `.ai/tasks` grep found only the existing README prohibition against linking task files.
- Secret/PII marker search over changed files found only policy language, pagination token field names, existing synthetic MCP `token-1`, and synthetic `access_token` leak-test fixtures.

Residual risk:
- The content graph currently uses in-memory Ladybug graph state in the server process; SQLite file-state metadata survives, but chunk/symbol graph query data is process-local until native persistence is wired beyond bootstrap scope.
- Legal/DPO interpretation is still required before any personal-data processing. Phase 6 preserves the PII-prohibited posture.
- Phase 6 does not add watcher/live behavior, providers, embeddings, vectors, crawling, auth model changes, public exposure, symlink traversal, or production deployment.

Next recommended phase:
- Phase 7 only after dependency approval for the watcher dependency and exact pinned version.

Copy-paste prompt for next agent:

```text
Continue in /home/mac/mivialabs/go-mivia.

Use WSL-native tooling. Do not use Jira or Confluence.

Current state:
- Phase 6 added bounded REST/MCP ingestion control and query surfaces.
- Existing metadata-only/manual digest behavior and MCP compatibility were preserved.
- Verified: go test ./internal/projectingestion/... ./internal/agentcontrol/mcpapi ./internal/projectregistry/httpapi, go test ./..., git diff --check, OpenAPI YAML parse, stable-doc .ai/tasks grep, and secret/PII marker scan.

Read first:
- AGENTS.md
- CLAUDE.md
- .ai/INDEX.md
- .ai/rules/00-operating-doctrine.md
- .ai/rules/05-external-systems.md
- .ai/rules/10-security-privacy.md
- .ai/rules/20-go-service-standards.md
- .ai/rules/30-docker-data.md
- docs/adr/0007-content-graph-ingestion-and-live-updates.md
- docs/security/research-data-handling.md
- .ai/handoffs/2026-05-30-phase-6-rest-mcp-ingestion-surfaces.md
- .ai/tasks/active/full-project-graph-ingestion-live-updates.md

Next implementation:
- Implement Phase 7 only after dependency approval from the owner.
- Add live update orchestrator behavior behind disabled-by-default global and per-project live flags.

No-go scope:
- No providers, embeddings, vectors, crawling, public exposure, auth model changes, symlink traversal, production deployment, raw DB query endpoints, source content in logs, skipped sensitive content, matched sensitive text, secrets, PII, raw prompts, provider payloads, or absolute roots.

Required first verifier:
- go test ./internal/projectingestion ./cmd/mivia-server
```
