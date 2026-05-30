# Suggested Commands

- Repo lives in WSL at `/home/mac/mivialabs/go-mivia`. From Codex Desktop PowerShell, prefer WSL-native commands such as `wsl git -C /home/mac/mivialabs/go-mivia status --short`.
- Keep read-only checks as single commands; do not chain with `&&`, `;`, command substitution, or shell wrappers when a simple command works.
- Search: `rg <pattern>` and `rg --files`.
- Git: `wsl git -C /home/mac/mivialabs/go-mivia status --short --untracked-files=all`.
- Go bootstrap checks after Phase 2: `go version`, `go mod tidy`, `go test ./...`, `make check`.
- Docker checks after Phase 3: `docker compose config`, then start only required dependencies for the phase.
- Integration tests should be opt-in, for example `make test-integration`, and must not require live paid AI or browsing providers.
- Secret checks after Phase 7: run the configured scanner if installed and report the exact missing tool if unavailable.
