# Agent MCP Promoted AST Plan Validation

Date: 2026-05-30

## Result

The promoted AST/live-rescan plan was validated against current source and hardened in place.

## Evidence Checked

- Repo rules and constraints from `AGENTS.md`, `.ai/INDEX.md`, and required `.ai/rules/*`.
- Current P0/P1/P2 reports under `docs/reports/tests/`.
- Ingestion source: `internal/projectingestion/service.go`, `parser_javascript.go`, `orchestrator.go`, `sqlite_store.go`, `graph_store.go`, `query.go`, `model.go`.
- Config/schema source: `internal/platform/config/config.go`, `internal/platform/config/file.go`, `internal/platform/sqlite/schema/schema.go`.
- Docs to update later: `README.md`, `docs/agent-context-guide.md`, `docs/configuration/local-projects.md`, `docs/runbooks/local-dev.md`, `docs/architecture/system-architecture.md`, `.ai/skills/mivia-mcp/SKILL.md`.
- Dependency feasibility via `go list -m` outside the repo and primary package docs for Tree-sitter Go bindings and grammar packages.

Jira: not checked by repo constraint.
Confluence: not checked by repo constraint.

## Key Corrections Made

- Split the original broad plan into Phases A-G with exact files, tests, commands, acceptance criteria, and rollback/stop rules.
- Defined exact config keys and defaults.
- Defined exact extractor names and versions.
- Defined exact SQLite cache schema and cache privacy rules.
- Clarified that grammar modules are required by root module path, while imports use `/bindings/go`.
- Clarified that C# can use `github.com/tree-sitter/tree-sitter-c-sharp v0.23.5` with import `github.com/tree-sitter/tree-sitter-c-sharp/bindings/go`.
- Defined scheduler fairness behavior precisely enough to implement.
- Added diagrams and mandatory documentation updates.

## Remaining Risks

- Tree-sitter introduces native/CGO lifecycle risk and requires dependency review.
- Query quality will determine extraction usefulness; fixture coverage must be broad before trusting enterprise monorepos.
- Fair scheduler implementation is coupled to bounded graph batches; implementing scheduler before graph batch yields would leave starvation unresolved.

## Human Decisions Needed

- Approve mandatory Tree-sitter native dependencies.
- Confirm scheduler and batch defaults.
- Confirm cache location in the existing SQLite app DB.
- Confirm whether `.jsx` should share the TSX extractor or get a separate extractor name.
