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
- Expose REST endpoints under `/api/v1`.
- Expose MCP Streamable HTTP under `/mcp` when MCP is implemented.
- Keep database access behind internal store interfaces; handlers must not execute raw LadybugDB or SQLite query strings from clients.

Logging:

- Include service name, request ID, task ID when available, and error category.
- Exclude raw prompts, raw source content, credentials, tokens, personal data, and provider payloads.

Testing:

- Unit tests first for config, health checks, redaction, state transitions, and migration runners.
- Bug fixes must add the narrowest focused regression test first when feasible, naming the failing behavior instead of only covering the patched helper.
- Regression tests must exercise the public boundary or smallest stable internal contract that proves the bug, not a mocked path that can pass while the bug remains.
- Automation, workflow, GitOps, verifier, runner, and closeout packages require broad contract tests for any changed behavior. Cover valid flow, invalid input, retry/recovery, terminal failure, stale state, dirty worktree, generated-artifact drift, concurrent or out-of-order completion, and downstream handoff shape before accepting the implementation.
- Prompt-rendering tests are supporting evidence only. They do not prove pipeline correctness unless paired with state-machine, runner, GitOps, or verifier tests that exercise the artifact consumed by the next stage.
- When adding a new failure category, safe ref, verifier, recovery path, PR rule, branch rule, or configured command, add tests for both the accepting path and the blocking path, including the exact category/ref or command shape that downstream code receives.
- If a bug is confirmed but cannot be covered by an automated test in scope, document the concrete reason and run the smallest reproducible manual or package verifier.
- Integration tests must be opt-in; local Compose services may be used only after the runtime dependency is approved.
- No live internet in unit tests.
