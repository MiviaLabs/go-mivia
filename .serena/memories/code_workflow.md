# Code Workflow

## Default code posture

- Keep phase scope strict. If a copied phase prompt says "Phase N only", do not add later-phase files or infrastructure.
- Before implementation, read `.ai/INDEX.md` and relevant `.ai/rules/*` when present; if `.ai/` does not exist yet, follow root `AGENTS.md` plus this Serena memory set.
- Prefer test-first for behavior changes: smallest focused failing test, smallest fix, then rerun before broadening.
- If red-first is not practical during bootstrap, state why and use the narrowest equivalent proof: config validation, compile check, smoke test, migration dry run, or static inspection.
- Use Serena semantic tools for Go symbol discovery and references. Use `rg` for text, config, migration, and documentation searches.

## Domain routing

- Go services: keep entrypoints under `cmd/<service>` and shared code under `internal/...`; avoid exporting packages without a real cross-service contract.
- Configuration: load from environment or explicit files; never commit real `.env` or secret material.
- Databases: migrations are forward-only, idempotent for local empty DBs, and must not drop data without an approved ADR.
- Research/deep-research: provider interfaces first; no paid provider, live crawling, or embedding dimension hardcoded without an ADR/owner decision.
- Privacy: do not log raw prompts, raw fetched content, credentials, tokens, personal data, or source payloads.
- Observability: start with structured `slog` fields that exclude sensitive data; add OpenTelemetry only after an approved observability ADR.

Read this alongside `mem:task_completion`, `mem:conventions`, and `mem:core`.
