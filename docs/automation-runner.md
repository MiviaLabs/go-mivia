# Automation Runner Operations

Status: Current
Date: 2026-06-05
Classification: Internal; PII-prohibited

## Runner User

Containerized automation that writes to a bind-mounted repository must run as the same host user that owns the checkout. The default local Docker Compose runner uses:

```yaml
user: "${MIVIA_CONTAINER_USER:-10001:10001}"
```

The devcontainer example uses:

```yaml
user: "${MIVIA_CONTAINER_USER:-1000:1000}"
```

Set `MIVIA_CONTAINER_USER="$(id -u):$(id -g)"` when the host checkout is not owned by that default UID and GID. Do this before enabling GitOps commit, push, or draft PR automation.

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
