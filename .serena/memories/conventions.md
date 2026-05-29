# Conventions

- Default language is Go. Use one root `go.mod` until independent module release boundaries are real.
- Keep Go code idiomatic: small packages, explicit errors, context-aware I/O, no package-level mutable state unless justified.
- HTTP services expose `/healthz` for process liveness and `/readyz` for dependency readiness.
- Logs are structured JSON-oriented `log/slog` records with service/request/task identifiers only; no raw content, prompts, secrets, tokens, or PII.
- Keep provider integrations behind interfaces; do not hardcode OpenAI, Anthropic, browser, or embedding-provider choices without an ADR.
- Docker/Compose config must pin images, use healthchecks for dependencies, use named volumes, and read secrets from files or environment examples only.
- Avoid copying MASS, Nominait, CrookedCircuits, Unity, or TypeScript-specific conventions into this repo unless the repo explicitly documents that decision.
- Documentation should record decisions and handoffs, not replace executable checks.
