---
name: jira-governed-ticket-delivery
description: Run a configured Mivia workflow-chain delivery pipeline for a Jira ticket such as MASS-1044. Use when the user asks to run governed ticket delivery, Jira ticket automation, or the whole pipeline for one ticket through Mivia MCP; do not use live Jira/Confluence connectors unless explicitly overridden.
---

# Jira Governed Ticket Delivery

Use this skill when a user asks to run the configured Mivia governed delivery pipeline for one Jira-style ticket key, for example `MASS-1044`.

## When To Use

- Use for a configured project workflow chain such as `<PROJECT>-governed-ticket-delivery`.
- Use when the ticket context should come from local Mivia project integration context and workflow-chain metadata.
- Use when GitOps must remain the sole owner of commit, push, and PR creation.
- Do not use for ad hoc manual implementation in the current checkout.
- Do not use live Jira or Confluence connectors for this repository unless the user explicitly overrides that constraint in the same request.

## Required Inputs

- Target Mivia project id or alias, for example `mass-monorepo`.
- Ticket key, for example `MASS-1044`.
- Configured chain ref, normally `<PROJECT>-governed-ticket-delivery`.

If project id or chain ref is missing, discover it from Mivia project config and workflow-chain list. Ask only if more than one plausible configured project/chain remains.

## Preconditions

1. Read `.ai/INDEX.md` and applicable `.ai/rules/`.
2. Use `.ai/skills/mivia-mcp/SKILL.md` for MCP-first routing and safety rules.
3. Verify server health with `/healthz` and `/readyz` when using local Docker.
4. Verify the project is valid with `projects.get`.
5. Verify workflow chains with `projects.workflow_chains.list`.
6. Verify the ticket input matches the configured chain input pattern.
7. Confirm runner and GitOps config are project-specific, especially:
   - branch template and allowed change types
   - ticket ref pattern and ticket URL template
   - PR title/body templates
   - configured verification commands for lint, typecheck, tests, Semgrep, generated artifacts, and diff checks

## Workflow

1. Confirm current state before starting anything:
   - current branch/worktree of the Mivia repo
   - server, dashboard, and automation-runner container state if Docker is used
   - `/readyz` result
   - project id and validation status
   - chain ref and existing recent chain runs for the same ticket
2. If server or runner binaries changed, rebuild and restart only the needed services:
   - `docker compose -f docker-compose.yml -f .docker-compose.local.yml build mivia-server mivia-automation-runner`
   - `docker compose -f docker-compose.yml -f .docker-compose.local.yml up -d --force-recreate mivia-server mivia-dashboard mivia-automation-runner`
3. Run a dry-run chain start first:
   - call `projects.workflow_chains.start`
   - set `dry_run=true`
   - use `input_text=<TICKET-KEY>`
   - set a unique `created_by_run_id` and `trace_id`
4. Inspect the dry-run result:
   - input ref must be the real ticket, for example `jira:MASS-1044`
   - context refs must include local ticket context refs
   - planned stages must include decomposition, implementation, and post-validation
   - no persisted run should exist for dry-run only
5. Start exactly one real chain run:
   - call `projects.workflow_chains.start`
   - set `dry_run=false`
   - reuse the same project id, chain ref, and ticket key
   - use a unique trace id that includes the ticket key
6. Record safe metadata only:
   - chain run id
   - work plan ids
   - work task ids
   - automation ids
   - trace id
   - stage status
7. Monitor only the current chain/trace:
   - use `projects.workflow_chains.get` for chain lifecycle
   - use `projects.automation_runs.list` for `running` and `queued`
   - use `projects.automation_runs.get` for specific current run ids
   - do not treat unfiltered historical failed runs as current failures
8. Stop on the first current blocker:
   - inspect current run id, task id, stage, status, failure category, safe summary, heartbeat, and lease
   - inspect runner/server logs only enough to identify the current blocker
   - do not start duplicate chain runs or blind reruns
9. If the current blocker is Mivia code/config, fix Mivia first:
   - add the narrowest regression test first
   - patch the smallest source/config surface
   - run focused tests, then affected tests
   - rebuild/restart server and runner
   - verify the exact blocked route/tool/command now works
   - allow the runner to recover or requeue according to Mivia lifecycle rules
10. Let GitOps own repository delivery:
   - do not manually commit/push/open PR in the target project
   - verify GitOps state and retry only through configured recovery tools when the chain is already eligible

## Current Failure Triage

Use this checklist before declaring failure:

- Is the failure from the current `trace_id` or current chain stage?
- Is the run still heartbeating with an unexpired lease?
- Is it queued because dependencies/review gates are not complete?
- Did a server restart mark an interrupted run as timeout and create a replacement run?
- Is the runner blocked on a local Mivia endpoint, verifier command, worktree cleanup, credential issue, or target repo CI rule?
- Is the failure terminal after retry limit, or recoverable through configured post-task/GitOps recovery?

## Output Format

Report briefly:

- project id, chain ref, ticket key, and chain run id
- dry-run result
- real run current stage and current run id
- current blocker or confirmation that it is still running normally
- changed Mivia files and commit id if a Mivia fix was needed
- verification performed
- remaining risk and any required human review

Never include raw prompts, raw source dumps, provider payloads, secrets, roots, private URLs, or PII in the report.
