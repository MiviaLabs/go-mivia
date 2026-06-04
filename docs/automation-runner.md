# Automation Runner Operations

Status: Current
Date: 2026-06-05
Classification: Internal; PII-prohibited

## Runner User

Containerized automation that writes to a bind-mounted repository must run as the same host user that owns the checkout. In shared examples this is configured with:

```yaml
user: "${MIVIA_AUTOMATION_CONTAINER_USER:-10001:10001}"
```

The devcontainer example uses:

```yaml
user: "${MIVIA_CONTAINER_USER:-1000:1000}"
```

Set `MIVIA_AUTOMATION_CONTAINER_USER="$(id -u):$(id -g)"` when the host checkout is not owned by that default UID and GID. Do this before enabling GitOps commit, push, or draft PR automation. Configure `MIVIA_CONTAINER_USER` separately when the server needs different permissions for its data volume or local workspace mounts.

For local Docker Compose runs, prefer the helper script so the automation sidecar user is inferred before Compose starts:

```bash
scripts/mivia-compose-up -d
```

The helper exports `MIVIA_AUTOMATION_CONTAINER_USER="$(id -u):$(id -g)"` unless you already set it, includes `.docker-compose.local.yml` when present, and then runs `docker compose up` with the repository compose files. It does not override `MIVIA_CONTAINER_USER`; the server may need a different user for its data volume or local workspace mounts. Pass normal `docker compose up` flags after the script name.

When the runner mounts a host Codex home, the mounted `config.toml` must be readable by that same UID/GID. If a run reports `codex_config_unreadable`, fix host ownership or permissions for the mounted Codex config, then restart the runner. Do not run the runner as root to work around this; root-owned worktree metadata and commits can break later local automation.

For ignored local overrides, use a runner-specific variable if the server still needs different permissions:

```yaml
user: "${MIVIA_AUTOMATION_CONTAINER_USER:-1000:1000}"
```

Set `MIVIA_AUTOMATION_CONTAINER_USER="$(id -u):$(id -g)"` for the automation sidecar. Avoid `0:0`; root-run sidecars create root-owned commits, refs, and worktree metadata on Linux and macOS bind mounts.

## GitOps Conventions

Runner GitOps is controlled by `[git_operations]` and optional `[git_operations.conventions]` in the server config. The convention fields are generic and project-safe: they use fixed placeholders, not arbitrary shell or template evaluation. Commit subjects and PR titles must render as Conventional Commits.

Supported placeholders:

- `{{project_id}}`
- `{{work_plan_id}}`
- `{{work_task_id}}`
- `{{work_task_ref}}`
- `{{work_task_title}}`
- `{{automation_id}}`
- `{{automation_run_id}}`
- `{{operator_id}}`
- `{{review_refs}}`
- `{{verifier_refs}}`
- `{{test_results}}`
- `{{commit_subject}}`

Draft PR bodies always render exactly these sections: `What changed`, `How verified`, and `Tests`. The default `How verified` text includes project ID, Work Plan ID, Work Task ID, automation ID, automation run ID, operator ID, review refs, and verifier refs. Test results are included when a caller supplies safe summaries; otherwise the runner states that tests were not reported and orchestrator verification is pending.

Example:

```toml
[git_operations.conventions]
commit_type = "feat"
commit_scope = "gitops"
commit_summary_template = "complete {{work_task_id}}"
pull_request_title_template = "{{commit_subject}}"
what_changed_template = "Completed {{work_task_title}} for {{project_id}}."
how_verified_template = "Project ID: {{project_id}}\nWork Plan ID: {{work_plan_id}}\nWork Task ID: {{work_task_id}}\nAutomation ID: {{automation_id}}\nAutomation Run ID: {{automation_run_id}}\nOperator ID: {{operator_id}}\nReview refs: {{review_refs}}\nVerifier refs: {{verifier_refs}}"
tests_template = "{{test_results}}"
```

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
find .git/worktrees -maxdepth 1 -printf '%u:%g %p\n'
```

Do not run normal automation as root to work around this. Root-owned refs can block later local commits and branch updates.
