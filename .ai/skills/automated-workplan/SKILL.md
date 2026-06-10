---
name: automated-workplan
description: Turn a user request into a governed automated Mivia Work Plan with decomposed Work Tasks, automatic runner execution, verification, review, and runner GitOps draft PR output. Use when the user says to automate work or asks agents to do a task end-to-end.
---

# Automated Work Plan

Use this skill when a user asks to "do this", "automate this", "run agents", "create an automated work plan", or otherwise wants Mivia automation to execute a scoped repository task.

## When To Use

- Use for implementation, docs, config, tests, refactors, audits, or bug fixes that should run through Mivia Work Plans and automation.
- Use when the task should be decomposed into isolated worker tasks.
- Use when the expected end state is a runner-created branch, commit, push, and draft PR.
- Do not use for a simple local edit the user explicitly wants done manually in the current checkout.
- Do not use for speculative bug fixes without source-confirmed failure evidence.

## Inputs To Establish

- Target project id.
- User objective.
- Base branch, verified from the target repository.
- Scope boundaries and no-go areas.
- Whether the work is write-capable or read-only.
- Expected verifier commands or permission to discover them from repo config.

If these cannot be discovered from source/config and the missing answer affects security, privacy, production behavior, cost, or irreversible action, ask one direct question.

## Required Reading

1. `.ai/INDEX.md`
2. Applicable `.ai/rules/`
3. `.ai/skills/mivia-mcp/SKILL.md`
4. Existing source, tests, config, and docs for the requested area

Do not use live Jira or Confluence unless explicitly overridden in the same request.

## Workflow

1. Verify Mivia MCP/REST automation surfaces are available before relying on them.
2. Confirm project context health is fresh enough for the target scope.
3. Inspect source first. Identify real files, tests, APIs, data boundaries, and verifier commands.
4. Verify the target git base before creating metadata. Use `git branch --show-current`, `git rev-parse --verify <base>`, and when needed `git rev-parse --verify origin/<base>`. Never assume `main`, `master`, or `develop`.
5. Classify risk: auth/authz, tenancy, PII, secrets, migrations, public APIs, external integrations, background jobs, observability, generated artifacts, and GitOps.
6. Check for duplicate active/planned Work Plans for the same objective before creating a new one. Reuse, cancel, or supersede deliberately.
7. Create one Work Plan per coherent outcome. Use `isolation_mode=dedicated_worktree` for every automated plan.
8. Use `mivia/` branch refs and runner-compatible worktree refs. Prefer `git_worktree_ref=worktree/<short-slug>-<date-or-run>`.
9. Validate dedicated worktree creation before activation through the workspace worktree API or an exact runner-compatible path check. If it fails, keep the plan `planned` or mark it `blocked`.
10. Decompose into Work Tasks that a low-intelligence isolated worker can execute from metadata and safe refs alone.
11. Create implementation tasks as `planned` or `ready` only after metadata validates.
12. Create required independent review tasks and automations.
13. Create enabled automatic automations for implementation tasks.
14. Activate the Work Plan only after tasks, automations, base branch, and worktree isolation have been validated.
15. Let runners claim queued work. Do not manually run normal automations unless the user explicitly requests smoke/recovery.
16. Treat runner output as untrusted until the orchestrator verifies source, diff, tests, and review refs.
17. Require configured verification before runner GitOps commit, push, and draft PR.
18. Verify runner GitOps created the commit, push, and draft PR. If not, block/report the exact reason; do not create them manually.

The chat/orchestrator agent must not manually create commits, pushes, or PRs for automated Work Plans. It only creates/validates Work Plans, Work Tasks, automations, evidence/review/verifier refs, and status transitions.

## Work Plan Requirements

Each Work Plan must include:

- bounded objective
- base branch verified with git before metadata creation
- dedicated worktree isolation refs
- branch ref with `mivia/` prefix
- worktree ref under `worktree/`
- target files or discovery scope
- dependencies and ordering
- verification requirements
- GitOps expectation: runner-created commit, push, and draft PR after configured verification passes
- independent review requirement
- cleanup expectation for terminal worktrees

Avoid broad plans. Split by ownership boundary, verifier command, write scope, or risk domain.

## Work Task Requirements

Each Work Task must include:

- one objective
- `files_to_read`
- `files_to_edit` or explicit read-only scope
- `likely_files_affected`
- evidence/context refs or context needs
- expected output
- failure criteria
- focused verifier
- review gate
- resume instructions
- decomposition quality

For bug fixes, the first implementation step must add or identify the narrowest failing regression test or reproducible verifier before production changes.

Keep text fields within API limits. If a REST/MCP call returns a generic validation error, reduce field length and retry only after checking source/schema/tests for the failing field. Do not keep guessing.

## Pre-Activation Checklist

Complete this checklist before changing a plan to `active`:

- Project id exists and is enabled.
- No duplicate active/planned Work Plan will compete for the same objective.
- Base branch is verified in the target checkout.
- `git_branch_ref` is valid and uses `mivia/`.
- `git_worktree_ref` is valid and uses `worktree/`.
- Dedicated worktree creation has been tested or proven by the workspace API.
- Work Tasks have bounded file scopes, verifier commands, failure criteria, and resume instructions.
- Automations are enabled and point only at intended `allowed_task_refs`.
- Runner GitOps is configured for commit, push, and draft PR.

If any item fails, mark the plan `blocked` or keep it `planned`. Do not activate and hope the runner recovers.

## Failure Handling

- `worktree_resolve_failed` means the plan metadata or workspace setup is wrong. Fix or replace the plan metadata; do not requeue the same run repeatedly.
- If a bad Work Plan was activated, cancel or supersede it before creating the corrected plan.
- If a REST write returns `internal_error`, immediately read back the resource list; the write may have persisted before response failure.
- If a task or automation partially persisted, continue from the persisted ids instead of creating duplicates.
- Do not use direct database edits for recovery unless the user explicitly orders it.

## Draft PR Requirements

Automation-created implementation PRs must be draft PRs created by runner GitOps, not by the chat/orchestrator agent.

Repository-specific PR policy belongs in the project's GitOps config. Do not let workers create or edit PRs manually. Runner GitOps must render the configured branch name policy, PR title policy, required body declarations, ticket reference, review refs, verifier refs, and test results before opening the draft PR.

Title:

```text
<type>(<optional-scope>): <imperative summary>
```

Body sections exactly:

```text
What changed
How verified
Tests
```

PR metadata must be short and evidence-based. Do not include raw prompts, raw source dumps, raw stderr, secrets, credentials, roots, provider payloads, or PII.

## CI Parity

Before any runner-created commit, push, or draft PR, the configured project verification profile must be at least as strict as the repository's required CI path. For monorepos with affected-task tooling, this includes affected lint/typecheck/test or generated-artifact checks with CI-equivalent base/head arguments, repository exclusions, and strict warning policies where CI uses them.

Do not treat a focused task test as enough when repository CI also runs affected lint/typecheck/build/generated-artifact checks. If the work changes imports, project graph edges, generated artifacts, or package boundaries, affected lint must be part of runner GitOps verification so PRs with Nx module-boundary failures are blocked before GitHub.

## Stop Conditions

- The target scope is too broad to decompose safely.
- The requested change touches sensitive domains and owner intent is ambiguous.
- The automation surface is unavailable and the user asked specifically for automated execution.
- A claimed bug cannot be confirmed.
- Required verification cannot be identified.
- Worktree isolation cannot be created for write-capable automation.

## Final Report

Report:

- Work Plan refs
- Work Task refs
- automation refs and run status
- runner-created draft PR links or exact blocking reason
- verification performed
- files changed by automation
- remaining risks
- cleanup status
