# Go Service Standards

Module strategy:

- Start with one root Go module.
- Add `go.work` only when independent module release boundaries become real.
- Target Go `1.26` with toolchain `go1.26.3` once the Go baseline phase creates `go.mod`.

Repository shape:

- Service entrypoints belong under `cmd/<service>/`.
- Shared platform code belongs under `internal/platform/`.
- Domain packages belong under `internal/<domain>/`.
- Public API contracts belong under `api/`.
- Database migrations belong under `db/migrations/`.

Service defaults:

- Use the standard library first.
- Use `net/http` for initial service skeletons.
- Use `log/slog` for structured logs.
- Expose `/healthz` for process liveness.
- Expose `/readyz` for dependency readiness.
- Read configuration from environment variables and explicit files, not hardcoded secrets.

Logging:

- Include service name, request ID, task ID when available, and error category.
- Exclude raw prompts, raw source content, credentials, tokens, personal data, and provider payloads.

Testing:

- Unit tests first for config, health checks, redaction, state transitions, and migration runners.
- Integration tests must be opt-in and use local Compose services.
- No live internet in unit tests.
