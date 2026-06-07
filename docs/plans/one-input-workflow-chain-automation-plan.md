# One Input Workflow Chain Automation Plan

## Status

Planned.

## Objective

Add a Mivia MCP capability where an operator can submit one bounded input string, such as `MASS-1044`, and the server creates a governed automation chain for the configured project:

1. resolve input context from local project integrations and indexed repo context,
2. compile the configured decomposition workflow into a Work Plan,
3. create automatic decomposition automation,
4. after decomposition review gates pass, automatically trigger implementation workflow automation,
5. after implementation review and verifier gates pass, automatically trigger post-implementation validation workflow automation,
6. allow configured GitOps to create a draft PR only after post-implementation validation passes.

The chain must be project-configurable in TOML and must not store raw prompts, raw source, raw stderr, provider payloads, secrets, roots, external URLs, or PII.

## Current Source Findings

- Workflow metadata is already modeled and exposed by `internal/projectworkflow`.
- Existing MCP workflow tools include validate/import/list/get/update/compile plus agent definition and permission snapshot tools.
- Work Plans and Work Tasks are already modeled and exposed by `internal/projectworkplan`.
- Automation metadata and run queues are already modeled and exposed by `internal/projectautomation`.
- GitOps is already isolated in `internal/projectgitops` and must remain downstream of verifier/review evidence.
- Local Jira/Confluence content is allowed only through Mivia local integrations, not live connector calls.
- Repo rules require automation to be lifecycle-triggered: create tasks as `planned`, create/enable automation, then transition tasks to `ready`.
- Existing MASS local config already defines enabled workflow TOML paths for decomposition, implementation, code-review bug planning, and post-implementation validation.

## Proposed User Contract

Add one MCP tool:

`projects.workflow_chains.start`

Input:

```json
{
  "id": "mass-monorepo",
  "chain_ref": "mass-governed-ticket-delivery",
  "input_text": "MASS-1044",
  "created_by_run_id": "optional-safe-ref",
  "trace_id": "optional-safe-ref",
  "dry_run": false
}
```

Output:

```json
{
  "project_id": "mass-monorepo",
  "chain_ref": "mass-governed-ticket-delivery",
  "input_ref": "jira:MASS-1044",
  "status": "queued",
  "work_plan_ids": ["..."],
  "automation_ids": ["..."],
  "next_action": "decomposition automation will run when planned tasks transition to ready"
}
```

The tool is metadata-only. It creates Work Plans, Work Tasks, Automations, safe evidence refs, and chain state. It does not directly run shell commands or call live Atlassian connectors.

## TOML Configuration

Add project-scoped workflow chain config:

```toml
[[projects.workflow_chains]]
chain_ref = "mass-governed-ticket-delivery"
enabled = true
input_kind = "jira_issue_key"
input_pattern = "^MASS-[0-9]+$"
context_provider = "jira"
context_mode = "local_ingested"
default_title_template = "{{input_ref}} governed delivery"
gitops_mode = "draft_pr_after_post_validation"

[[projects.workflow_chains.stages]]
stage_ref = "decomposition"
workflow_ref = "governed-decomposition-planning"
trigger = "on_chain_start"
automation_ref_template = "{{chain_ref}}.{{input_ref}}.decomposition"
required_status_before_next = "completed"

[[projects.workflow_chains.stages]]
stage_ref = "implementation"
workflow_ref = "governed-workplan-implementation"
trigger = "after_stage_review_passed"
depends_on = ["decomposition"]
automation_ref_template = "{{chain_ref}}.{{input_ref}}.implementation"
required_status_before_next = "completed"

[[projects.workflow_chains.stages]]
stage_ref = "post-validation"
workflow_ref = "governed-post-implementation-validation"
trigger = "after_stage_review_passed"
depends_on = ["implementation"]
automation_ref_template = "{{chain_ref}}.{{input_ref}}.post-validation"
required_status_before_next = "completed"
```

Validation rules:

- `chain_ref`, `stage_ref`, and `workflow_ref` must be safe refs.
- `input_pattern` must compile and must be bounded.
- every configured workflow ref must resolve to exactly one enabled workflow for the project.
- `gitops_mode = "draft_pr_after_post_validation"` requires project GitOps config and post-validation stage.
- chain config must reject unknown triggers, duplicate stage refs, dependency cycles, and missing review-gate requirements.

## Implementation Design

Add package:

`internal/projectworkflowchain`

Responsibilities:

- parse and validate `WorkflowChainConfig`,
- normalize one input string into a safe `InputRef`,
- resolve local context refs from configured providers,
- compile each stage workflow into Work Plans in dependency order,
- create automatic automation metadata bound to stage Work Plans and allowed task refs,
- create planned chain state and transition the first stage to ready only after its automation exists,
- expose chain metadata and start/list/get MCP tools.

Initial MCP tools:

- `projects.workflow_chains.start`
- `projects.workflow_chains.get`
- `projects.workflow_chains.list`

Optional later tools:

- `projects.workflow_chains.cancel`
- `projects.workflow_chains.resume`

Service dependencies:

- project registry for project config,
- workflow service for list/get/compile,
- workplan service for status transitions and task listing,
- automation service for create/list/run queue metadata,
- integration service for local Jira/Confluence context refs,
- gitops config only as a downstream readiness policy, not direct PR creation from the start tool.

## Chain State Model

Persist safe metadata only:

- `chain_run_id`
- `project_id`
- `chain_ref`
- `input_ref`
- `status`
- `stage_runs[]`
- `work_plan_ids[]`
- `automation_ids[]`
- `created_by_run_id`
- `trace_id`
- timestamps

Do not persist raw Jira descriptions, raw Confluence content, source dumps, prompts, stderr, credentials, roots, or personal data.

## Stage Execution Rules

- Chain start compiles the decomposition workflow and creates its automation first.
- Downstream stages are created as `planned` chain stages.
- Stage advancement is lifecycle-driven by Work Plan/Work Task completion and review/verifier refs.
- Implementation starts only after decomposition review tasks are completed successfully.
- Post-validation starts only after implementation review and verifier refs exist.
- Draft PR GitOps is allowed only after post-validation completes and configured GitOps checks pass.
- Manual `projects.automations.run` is allowed only for smoke tests and explicit recovery, not normal chain flow.

## MASS Local Config Update

Update ignored local config only after code is implemented and validated:

- add `[[projects.workflow_chains]]` under `mass-monorepo`,
- set `input_kind = "jira_issue_key"`,
- set `input_pattern = "^MASS-[0-9]+$"`,
- chain stages:
  - `governed-decomposition-planning`,
  - `governed-workplan-implementation`,
  - `governed-post-implementation-validation`,
- set GitOps mode to draft PR after post-validation.

Committed examples should use placeholder projects and no real credentials.

## Tests

Narrow tests first:

- config parsing and validation rejects duplicate stages, dependency cycles, unknown workflow refs, unsafe refs, unsafe input patterns, missing post-validation when GitOps draft PR is configured.
- start tool rejects raw/unmatched input text and accepts `MASS-1044`.
- dry-run start returns planned stage order without creating automation.
- real start creates decomposition Work Plan, automation metadata, permission refs, and planned downstream stages without executing shell.
- lifecycle advancement starts implementation only after decomposition review/verifier requirements are satisfied.
- lifecycle advancement starts post-validation only after implementation review/verifier requirements are satisfied.
- GitOps readiness is not marked until post-validation completion.
- MCP adapter rejects unknown fields and redacts outputs.

Package checks:

- `go test ./internal/projectworkflowchain/...`
- `go test ./internal/projectworkflow ./internal/projectworkplan ./internal/projectautomation ./internal/projectgitops`
- `go test ./cmd/mivia-server ./internal/...` if blast radius warrants.

Runtime checks:

- rebuild image,
- restart compose stack with local config,
- `GET /readyz`,
- native MCP `projects.list`,
- MCP `projects.workflow_chains.start` dry-run for `MASS-1044`,
- MCP real start for `MASS-1044` only after confirming local Jira context is ingested and no unsafe data will be stored.

## Review Plan

First review after implementation:

- config safety and validation,
- MCP argument validation,
- no raw Jira/Confluence/source/prompt persistence,
- lifecycle ordering,
- GitOps cannot fire before post-validation.

Second review after fixes:

- rerun focused tests,
- inspect final diff,
- rebuild/restart,
- run MCP smoke test.

## Risks

- Chain advancement may need a new lifecycle hook in automation/workplan services if no existing completion callback exists.
- Local Jira data may be unavailable or stale; the start tool must fail with a clear local-context error rather than live-fetching.
- Draft PR creation depends on existing GitOps config and runner credentials; chain start must report blocked if those are unavailable.
- Full end-to-end implementation plus live MASS PR creation is high blast radius; keep first implementation behind explicit project TOML chain config and dry-run support.
