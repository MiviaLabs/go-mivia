# Automation Runner Operations

Status: Current
Date: 2026-06-05
Classification: Internal; PII-prohibited

## Runner User

Containerized automation that writes to a bind-mounted repository must run as the same host user that owns the checkout. In shared examples this is configured with:

```yaml
user: "${MIVIA_CONTAINER_USER:-10001:10001}"
```

The devcontainer example uses:

```yaml
user: "${MIVIA_CONTAINER_USER:-1000:1000}"
```

Set `MIVIA_CONTAINER_USER="$(id -u):$(id -g)"` when the host checkout is not owned by that default UID and GID. Do this before enabling GitOps commit, push, or draft PR automation.

For local Docker Compose runs, prefer the helper script so the automation sidecar user is inferred before Compose starts:

```bash
scripts/mivia-compose-up -d
```

The helper exports `MIVIA_AUTOMATION_CONTAINER_USER="$(id -u):$(id -g)"` unless you already set it, includes `.docker-compose.local.yml` when present, and then runs `docker compose up` with the repository compose files. Pass normal `docker compose up` flags after the script name.

For ignored local overrides, use a runner-specific variable if the server still needs different permissions:

```yaml
user: "${MIVIA_AUTOMATION_CONTAINER_USER:-1000:1000}"
```

Set `MIVIA_AUTOMATION_CONTAINER_USER="$(id -u):$(id -g)"` for the automation sidecar. Avoid `0:0`; root-run sidecars create root-owned commits, refs, and worktree metadata on Linux and macOS bind mounts.

## Ownership Cleanup

If an older root-run container already wrote Git metadata, repair only the affected repository metadata before creating more worktrees or commits:

```bash
docker run --rm -u 0 -v "$PWD:/repo" --entrypoint sh mivia-server:local -c 'chown -R "$(stat -c %u /repo):$(stat -c %g /repo)" /repo/.git/refs/heads/mivia /repo/.git/logs/refs/heads/mivia /repo/.git/worktrees 2>/dev/null || true'
```

Then verify:

```bash
git status --short --branch
find .git/refs/heads/mivia -maxdepth 1 -printf '%u:%g %p\n'
find .git/logs/refs/heads/mivia -maxdepth 1 -printf '%u:%g %p\n'
```

Do not run normal automation as root to work around this. Root-owned refs can block later local commits and branch updates.
