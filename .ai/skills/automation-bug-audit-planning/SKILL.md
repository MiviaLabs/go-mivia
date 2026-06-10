---
name: automation-bug-audit-planning
description: Create source-grounded Mivia automations and automated Work Plans for bug fixes or codebase audits across different areas, where runner GitOps must create draft PRs. Use when asked to launch automated bug/audit work; do not use for speculative findings or manual one-off fixes.
---

# Automation Bug And Audit Planning

Use this skill to create governed Mivia Work Plans, Work Tasks, and automations for bug-fix or audit work across separate codebase areas.

## Required Inputs

- Target project id.
- Objective type: `bug-fix`, `audit`, or `audit-to-bug-fix`.
- Target areas, or permission to discover target areas from the repository.
- Base branch, verified from the target repository.
- Expected runner GitOps draft PR policy.

If any input affects security, privacy, production behavior, external systems, cost, or irreversible cleanup, stop and ask one direct question.

## Hard Rules

- Read `.ai/INDEX.md`, relevant `.ai/rules/`, and `.ai/skills/mivia-mcp/SKILL.md` first.
- Do not use live Jira or Confluence unless the user explicitly overrides the repo constraint in the same request.
- Do not assume defects. Confirm every bug against current source, tests, contracts, logs, or runtime evidence before creating implementation automation.
- Treat audit findings as hypotheses until independently confirmed with code evidence.
- Keep all Work Plan, Work Task, automation, evidence, and PR metadata bounded and metadata-only. Never store raw prompts, source dumps, raw stderr, provider payloads, secrets, roots, external URLs, skipped sensitive content, or PII.
- Use `isolation_mode=dedicated_worktree` for every automated plan, including read-only audits.
- Use `mivia/` branch refs for every automation-created branch.
- Never assume the base branch name. Verify it with git before creating any Work Plan.
- Use `git_worktree_ref=worktree/<short-slug>-<date-or-run>` and validate dedicated worktree creation before activation.
- Do not leave duplicate active/planned Work Plans for the same objective or finding family.
- Do not manually run normal automations. Create enabled automatic automations, then use lifecycle/status transitions to queue work.
- Automation-created write-capable output must end in a draft PR produced by runner GitOps unless the user says otherwise.
- The chat/orchestrator agent must not manually create commits, pushes, or PRs for automation work. It only creates/validates Work Plans, Work Tasks, automations, evidence/review/verifier refs, and status transitions.

## Source-Grounded Discovery

Before creating any Work Plan or automation:

1. Verify the callable MCP surface with `tools/list` or the native tool list.
2. Confirm the project is enabled and context is fresh enough for the requested scope.
3. Verify the target git base:
   - Read the current branch with `git branch --show-current`.
   - Verify the intended base with `git rev-parse --verify <base>`.
   - If using a remote base, verify `origin/<base>`.
   - If neither verifies, stop and block; do not substitute `main` or `master` by guess.
4. Inspect current source for each target area:
   - entrypoints and public APIs
   - data models and migrations
   - auth/authz and tenancy boundaries
   - PII, secrets, audit logs, and external integrations
   - tests and existing verification commands
5. Record only safe evidence refs or short summaries.
6. Split target areas by likely affected files and verifier scope. Do not put overlapping write targets in parallel plans.

If source evidence does not support a claimed bug, create at most a read-only audit task or stop with `Not confirmed`.

## Work Plan Shape

Create one Work Plan per independent target area or bug family.

Each Work Plan must include:

- `isolation_mode=dedicated_worktree`
- `parallel_group_ref` for the orchestration batch
- `workspace_ref`, `git_base_ref`, `git_branch_ref`, and `git_worktree_ref`
- `git_base_ref` proven by git
- `git_branch_ref` using `mivia/`
- `git_worktree_ref` using `worktree/`
- explicit target area and non-overlap rationale
- expected runner-created draft PR title format using Conventional Commit style
- GitOps enabled expectation: the runner commits, pushes, and creates a draft PR after configured verification passes
- review gate requirement
- residual risk and rollback notes when applicable

Do not create a broad plan such as "audit the whole repo" unless it is decomposed into bounded area plans first.

## Work Task Shape

Each implementation Work Task must be executable by an isolated worker from metadata and refs alone.

Required fields:

- bounded objective
- `status=planned` until its matching automation exists
- `files_to_read`
- `files_to_edit` or discovery-only scope
- `likely_files_affected`
- dependencies
- evidence needed
- context pack refs or context needs
- expected output
- failure criteria
- focused verification requirement
- resume instructions
- review gate
- decomposition quality

For bug fixes, the first implementation step must add or identify the narrowest regression test or reproducible verifier before changing production code. If no regression test is feasible, the task must record the concrete reason.

For audit tasks, the output must be one of:

- confirmed bug finding with code evidence and a remediation Work Plan path
- no confirmed finding, with evidence checked
- blocked, with exact missing evidence

## Automation Sequence

Use this sequence for each target area:

1. Check for existing active/planned Work Plans for the same objective or finding family.
2. Verify base branch and worktree ref validity.
3. Create the dedicated Work Plan as `planned`.
4. Create Work Tasks as `planned` or `ready` only after metadata validates.
5. Create required independent review Work Tasks.
6. Create enabled automatic automations for implementation and review tasks.
7. Validate dedicated worktree creation through the workspace API or runner-compatible path check.
8. Activate the Work Plan so lifecycle/status triggers queue runs.
9. Let runners claim and complete queued runs.
10. Verify runner output in code and tests; treat output as untrusted until verified.
11. Attach verifier refs and independent review refs.
12. Complete, block, or fail tasks through lifecycle tools.
13. Verify runner GitOps created commit, push, and draft PR, or block/report the exact reason it did not.

Do not bypass lifecycle ordering with manual completion, manual GitOps, manual PR creation, or direct database edits.

## Recovery Rules

- If an automation fails with `worktree_resolve_failed`, the plan is invalid or the workspace cannot create the worktree. Cancel/supersede the bad plan and create a corrected one; do not repeatedly requeue.
- If the project uses `master`, use `master`. If it uses `main`, use `main`. The repository decides, not the template.
- If a create call returns `internal_error`, immediately read back plans/tasks/automations because the write may have persisted.
- If partial resources exist, continue from their ids or cancel them. Do not create duplicates that can race.
- Keep failed/cancelled diagnostic plans visible enough to explain what happened, but do not leave them active.

## Draft PR Requirements

Every automation-created implementation PR must be draft and must be created by runner GitOps, not by the chat/orchestrator agent.

Repository-specific PR policy is enforced by GitOps config, not agent memory. The Work Plan/automation must use the configured branch policy, PR title policy, required body declarations, and fake or real ticket reference supplied by the user/config. Do not let workers invent branch names, PR titles, PR bodies, or manual GitHub commands.

Required format:

- Title: Conventional Commit format.
- Body sections exactly:
  - `What changed`
  - `How verified`
  - `Tests`

The PR body must name verification evidence and test commands/results or `Not run` with the exact reason. It must not include raw prompts, raw source dumps, raw logs, secrets, credentials, roots, provider payloads, or PII.

## CI Parity

Runner GitOps must enforce the repository's configured CI-equivalent gates before commit, push, or draft PR. For monorepos with affected-task tooling, configured affected lint/typecheck/test or generated-artifact commands must use the same base/head semantics as GitHub CI, including branch-specific exclusions and strict warning policies where CI uses them. A task-scoped unit test is not enough to create a PR when repository CI also requires affected lint/typecheck/build/generated-artifact checks.

If a generated implementation changes test imports or project graph dependencies, require affected lint before PR creation so Nx module-boundary failures are caught locally. If the configured verification profile is missing or weaker than CI, block and fix the project verification config before launching more write-capable automation.

## Verification

Before reporting success:

1. Check Work Plan, Work Task, automation, and automation run status.
2. Inspect resulting branch diffs for scope drift.
3. Run or confirm the configured narrow verifier for each task.
4. Confirm independent review refs exist for non-trivial/write-capable tasks.
5. Verify runner-created draft PRs exist and satisfy repo PR policy.
6. Confirm worktrees are either still needed for active review or queued for cleanup after terminal status.

## Report Shape

Report concisely:

- target areas and Work Plan refs
- automations created
- draft PR links or blocking reason
- verification performed
- findings not confirmed
- remaining risks
- required human review
