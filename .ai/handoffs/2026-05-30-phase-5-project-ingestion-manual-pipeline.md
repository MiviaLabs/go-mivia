# Phase 5 Handoff - Manual Content Graph Ingestion

Task or phase: Phase 5 - Manual Full Ingestion Pipeline.

Scope completed:
- Recorded ADR-0007 as accepted with Mac Lisowski as named Engineering and Security/DPO owner for the local-source exception.
- Updated research data handling policy for the accepted, localhost-only `content_graph` exception.
- Added manual project ingestion service behind existing `content_graph` + manual project mode.
- Added graph persistence for eligible file versions, chunks, symbols, headings, runs, and relationships.
- Added SQLite run and file-state persistence with hash-only sensitive skips and no source-content hash for skipped files.
- Preserved `metadata_only/manual` digest behavior.

Changed files:
- `cmd/agent-server/main.go`
- `docs/adr/0007-content-graph-ingestion-and-live-updates.md`
- `docs/security/research-data-handling.md`
- `internal/projectingestion/graph_store.go`
- `internal/projectingestion/service.go`
- `internal/projectingestion/service_test.go`
- `internal/projectingestion/sqlite_store.go`
- `internal/projectingestion/safety.go`
- `internal/projectregistry/model.go`
- `internal/projectregistry/store/sqlite.go`

Verification:
- `/home/mac/.local/bin/go test ./internal/projectregistry ./internal/projectingestion` passed.
- `/home/mac/.local/bin/go test ./...` passed.
- `git diff --check` passed.
- `grep -RIn "\.ai/tasks" docs api configs` returned no stable-doc links.
- Secret/PII marker grep over changed files returned only policy text, detector regexes, and synthetic test marker text.

Residual risk:
- Security/DPO approval is recorded as Mac Lisowski per owner instruction in this session; separate legal/DPO interpretation is still required before any personal-data processing.
- Phase 5 does not expose REST/MCP ingestion APIs and does not implement live watcher behavior.

Next recommended phase:
- Phase 6 only after reviewing Phase 5 commit and keeping REST/MCP query surfaces bounded and localhost-only.

Copy-paste prompt for next agent:

```text
Continue in /home/mac/mivialabs/mivialabs-agents-monorepo.

Use WSL-native tooling. Do not use Jira or Confluence.

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
- .ai/handoffs/2026-05-30-phase-5-project-ingestion-manual-pipeline.md
- .ai/tasks/active/full-project-graph-ingestion-live-updates.md

Implement Phase 6 only if Phase 5 is committed and tests are green.

No-go scope:
- No watcher/live behavior.
- No providers, embeddings, vectors, crawling, public exposure, auth model changes, symlink traversal, raw DB query endpoints, or source content in logs.
- No skipped sensitive content, matched sensitive text, source-content hashes for skipped files, secrets, PII, raw prompts, or provider payloads.

First verifier:
- go test ./internal/projectingestion/... ./internal/agentcontrol/mcpapi ./internal/projectregistry/httpapi
```
