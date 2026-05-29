# Core

- Source of truth order: current code/config/tests/migrations/container definitions/logs/runtime artifacts > ADRs under `docs/adr/` > `.ai/` rules/skills/tasks/handoffs > older docs or comments.
- Startup entrypoint is `.ai/INDEX.md` once Phase 1 exists. `AGENTS.md` and `CLAUDE.md` should stay thin wrappers pointing there, not duplicate policy.
- Project identity: Go microservices monorepo for AI-agent work with PostgreSQL/pgvector, Neo4j, Docker Compose, provider-neutral research boundaries, and repeatable agent handoffs.
- Serena is required when available: activate this repo, confirm active project, and prefer Serena semantic/symbol tools for Go discovery before broad file reads or edits.
- Skill routing before non-trivial work: use `.ai/skills/README.md` when present; domain memories are commands in `mem:suggested_commands`, stack in `mem:tech_stack`, style in `mem:conventions`, code workflow in `mem:code_workflow`, and done gates in `mem:task_completion`.
- Memory maintenance is deliberate: read `mem:memory_maintenance` before creating, splitting, renaming, or promoting Serena memories; update this core routing memory when a new memory should be discovered early.
- High-risk surfaces: auth/authz, tenancy, PII/PDPL, secrets, migrations, public APIs, background jobs, external AI/browsing providers, audit logs, observability, and rollback.
- Preserve user changes/deletions. Do not revert unrelated dirty work; inspect local state before editing and keep diffs scoped.
