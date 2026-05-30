# Phases 7-9 Handoff - Persistent Project Graph, Live Updates, Docs, MCP Skill

Task or phase: Remaining content graph phases after REST/MCP ingestion surfaces.

Scope completed:
- Added per-project project graph storage selection with `graph_storage = "persistent"` or `graph_storage = "in_memory"`, defaulting to `persistent`.
- Added a local persistent graph adapter behind the existing Ladybug graph abstraction and wired project digest/ingestion graph data through a project graph router.
- Kept agent-control and research graph state on the existing process-local graph path.
- Added fsnotify `v1.10.1` live watcher orchestration behind global and per-project live gates.
- Added fake watcher tests for disabled global live mode, directory watch registration, new directory watching, debounce, create/write/remove/rename events, overflow rescan, queue-full rescan, initial scan, disabled project filtering, and shutdown.
- Preserved manual ingestion as fallback for `content_graph/live` projects.
- Updated stable docs, example config, REST OpenAPI, and MCP capability docs for graph storage, manual ingestion, live mode, watcher troubleshooting, and local reset.
- Added project-local MCP router skill `mivialabs-agent-mcp` and listed it in `.ai/skills/README.md`.

Changed files:
- `.ai/skills/README.md`
- `.ai/skills/mivialabs-agent-mcp/SKILL.md`
- `README.md`
- `api/mcp/agent-control.v1.md`
- `api/openapi/agent-control.v1.yaml`
- `cmd/agent-server/main.go`
- `configs/agent-server.example.toml`
- `docs/README.md`
- `docs/architecture/system-architecture.md`
- `docs/configuration/local-projects.md`
- `docs/runbooks/local-dev.md`
- `docs/security/research-data-handling.md`
- `go.mod`
- `go.sum`
- `internal/platform/config/config.go`
- `internal/platform/config/config_test.go`
- `internal/platform/config/file.go`
- `internal/platform/config/file_test.go`
- `internal/platform/ladybug/persistent.go`
- `internal/platform/ladybug/persistent_test.go`
- `internal/platform/sqlite/schema/schema.go`
- `internal/platform/sqlite/schema/schema_test.go`
- `internal/projectingestion/orchestrator.go`
- `internal/projectingestion/orchestrator_test.go`
- `internal/projectingestion/service.go`
- `internal/projectingestion/service_test.go`
- `internal/projectingestion/watcher.go`
- `internal/projectregistry/graph_router.go`
- `internal/projectregistry/graph_router_test.go`
- `internal/projectregistry/model.go`
- `internal/projectregistry/service.go`
- `internal/projectregistry/service_test.go`
- `internal/projectregistry/store/sqlite.go`
- `internal/projectregistry/store/sqlite_test.go`

Verification:
- `go test ./internal/projectingestion ./cmd/agent-server` passed.
- `go test ./internal/platform/config ./internal/platform/ladybug ./internal/platform/sqlite/schema ./internal/projectregistry ./internal/projectregistry/store ./internal/projectregistry/httpapi ./internal/agentcontrol/mcpapi ./internal/projectingestion/... ./cmd/agent-server` passed.
- `go test ./...` passed.
- `PATH=/home/mac/.local/bin:$PATH make check` passed.

Residual risk:
- The persistent graph adapter is a local bootstrap persistence layer behind the Ladybug graph abstraction; native LadybugDB remains gated behind documented build tags and library setup.
- Live filesystem notifications can still miss events on network, mounted, or special filesystems; manual ingestion remains the fallback.
- PII ingestion remains prohibited. Legal/DPO confirmation is still required before any personal-data processing.
- No providers, embeddings, vectors, crawling, public exposure, auth model changes, symlink traversal, production deployment, or raw DB query endpoints were added.

Next recommended work:
- Run final repository gates, review the diff, and commit.
- After commit, future work should be bugfix-only unless a new approved plan or owner request expands scope.
