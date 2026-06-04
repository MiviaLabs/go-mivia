---
name: mivia-mcp
description: Use with the Mivia localhost MCP server for any indexed project when an agent needs project discovery, ingestion state, context health, impact analysis, context packs, stale-claim checks, Work Plan/Work Task governance when exposed, project automation/run metadata when exposed, search, bounded chunks, symbol navigation, call graph, named AST discovery, governed git status/diff, current eligible file reads, eligible file create/delete, exact token-guarded edits, redacted agent-run metadata, promotion-gate decisions, project-scoped Evidence Graph metadata, project-scoped confidence scoring metadata, Knowledge Promotion metadata, or locally ingested Jira/Confluence context.
---

# Mivia Agent MCP

Portable skill. It can be copied into any repository indexed by a running Mivia `mivia-server`.

## Maintenance And Verification Gate

- Treat this skill as a claim-bearing contract, not a wishlist. Before adding, removing, renaming, or copying any MCP tool, resource, REST route, config, or release/publication claim, verify it against the client's exposed tool list or current callable surface plus source evidence. Use raw HTTP `tools/list` only for surface verification or when native clients cannot list tools. Use `projects.claims.check` for MCP tool, REST route, and `.ai/tasks/*` link claims; use git tags, registry checks, release metadata, or source-controlled docs for version/publication claims.
- If the current callable surface cannot be established, mark tool availability as unverified and do not present documented names as available.
- After changing MCP tool, REST route, resource, or `.ai/tasks/*` link claims, run `projects.claims.check` against this file or the changed snippet before syncing global copies.
- Keep maintenance edits small, source-grounded, and additive unless source evidence proves a documented claim is stale.

## MCP-First Routing

When a Mivia MCP server is available for the target project, use it as the first choice for indexed project discovery and bounded context. Keep the MCP call set proportional to the task; do not run reliability or handoff tools by default when a smaller read/search/status call answers the question.

Review and implementation guidance:

- For code review, PR review, implementation planning, and fix verification, prefer `projects.list` -> `projects.get` -> `projects.graph_status` or `projects.context_health` when freshness affects the answer. Do not use `projects.ingestion_status_latest` alone to decide whether indexed MCP context is usable; it is one run record, not the authoritative graph inventory.
- If `projects.graph_status.status` / `projects.context_health.status` is not `ready`, state the status and freshness gap only when the answer relies on indexed freshness. Treat `syncing` as normal active indexing, not corruption. If `indexed_content_available=true`, indexed MCP tools remain usable while ingestion catches up.
- For changed-path review, use `projects.impact.analyze` when blast radius is unclear, the change is security/privacy/API-sensitive, or the user asks for review/audit confidence. If the result is partial with `index_syncing`, treat it as active indexing and fall back to focused source inspection for the current task rather than treating the index as degraded.
- For source evidence, prefer indexed MCP search/navigation when available: `projects.context_pack.build`, `projects.search.*`, `projects.symbols.list`, `projects.symbol.source`, `projects.symbol.references`, callers/callees, call graph, headings, outlines, and bounded chunks.
- For workspace tools, `id` always means the canonical project id or a safe alias returned by `projects.list` / `projects.get`. Never pass the current working directory, repository root, UNC path, WSL path, local filesystem path, or workspace URI as `id`. When workspace mode is `read_only` or `edit`, prefer `projects.workspace.file_read` for current eligible file content. When workspace mode is `edit`, use `projects.workspace.file_read` then `projects.workspace.file_edit` for exact/token-guarded edits, `projects.workspace.file_delete` for existing eligible single-file deletes, and `projects.workspace.file_create` for new eligible text files before shell, `apply_patch`, or manual file operations. These tools are not recursive delete, arbitrary patch, arbitrary shell, or shell-replacement surfaces.
- For actual runtime proof, use shell: tests, builds, logs, process control, generated files, and exact runtime facts. Use shell or manual edits only when MCP workspace edit is unavailable, the file is ineligible, the repository is not opted in, or the change is a broad/generated rewrite outside the exact-edit contract.
- For stable docs/contracts that changed or are cited in a review, use `projects.claims.check` when the task depends on MCP tool names, REST route names, or `.ai/tasks/*` link claims being current.
- Before commit, use the smallest verification set appropriate to the changed files and risk. Add `projects.context_health`, `projects.impact.analyze`, `projects.claims.check`, or `agent_runs.*` only when they materially improve confidence, support a review/handoff, or are explicitly requested.
- For multi-step reviews, fix loops, implementation handoffs, or resumable work, agents should use `agent_runs.*` to record redacted breadcrumbs and `agent_runs.promote_artifact` to record promotion-gate decisions for existing artifact refs. Store only safe metadata; never store raw prompts, completions, source dumps, raw stderr, roots, secrets, provider payloads, skipped sensitive content, or PII.

## Project Workflow TOML

Project Workflow TOML is a compile-only metadata surface. It validates and imports bounded workflow definitions, agent definitions, review gates, step dependencies, permission snapshots, and safe refs. It does not execute TOML, raw prompts, shell commands, Codex CLI, source, stderr, provider payloads, secrets, roots, external URLs, skipped sensitive content, or PII.

Use workflow tools only when the running server exposes them through `tools/list`:

```text
projects.workflows.validate_toml
projects.workflows.import_toml
projects.workflows.get
projects.workflows.list
projects.workflows.update_status
projects.workflows.compile_to_work_plan
projects.agent_definitions.list
projects.agent_definitions.get
projects.permission_snapshots.get
projects.permission_snapshots.list
```

Rules:

1. Validate TOML before import. Validation returns metadata and issues only.
2. Import stores workflow metadata only. Import does not create execution runs and does not bypass Work Plans or Work Tasks.
3. Enable workflow metadata only after review gates, verifier requirements, evidence/claim refs, resume instructions, safe affected-file refs, and permission metadata are present.
4. Compile with `projects.workflows.compile_to_work_plan` before automation. Compile creates or returns Work Plan, Work Task, reviewer task, automation, and permission snapshot refs; it does not run automation.
5. Required review gates must be independent when `independent_from_owner=true`; a reviewer agent cannot review the same agent's implementation or automation step.
6. Automation steps must depend on at least one Work Task step and have a required review gate. Automation remains an executor over ready Work Tasks only.
7. Permission snapshots are immutable metadata for allowed skills/tools/commands, denied commands, workspace mode, network policy, secret policy, log policy, runtime, retry policy, content hash, and run/trace refs. They are not OS sandbox proof or execution approval.
8. Reusable conclusions must follow the order: Evidence Graph refs and outcomes, verifier refs, independent review refs, confidence score when reusable, Knowledge Promotion candidate, validation, project promotion, optional org review/promotion. Knowledge never auto-promotes from TOML, automation, or confidence alone.

Checked-in workflow definitions:

- `workflow-decomposition-planning`: turns a user objective into a governed Work Plan and isolated Work Tasks.
- `workflow-workplan-implementation`: executes reviewed ready Work Tasks through bounded workers, independent review, and orchestrator verification.
- `workflow-code-review-bug-planning`: automatic code review workflow that can queue bounded scanner, independent-review, and bug Work Plan creation tasks. It creates bug Work Plans only for independently confirmed, evidence-backed bugs and must not auto-implement speculative or unreviewed findings.

REST mirrors the same metadata surface at:

```text
POST /api/v1/projects/{id}/workflows/validate-toml
POST /api/v1/projects/{id}/workflows/import-toml
GET /api/v1/projects/{id}/workflows
GET /api/v1/projects/{id}/workflows/{workflow_id}
POST /api/v1/projects/{id}/workflows/{workflow_id}/status
POST /api/v1/projects/{id}/workflows/{workflow_id}/compile
GET /api/v1/projects/{id}/workflows/{workflow_id}/agent-definitions
GET /api/v1/projects/{id}/workflows/{workflow_id}/agent-definitions/{agent_id}
GET /api/v1/projects/{id}/permission-snapshots
GET /api/v1/projects/{id}/permission-snapshots/{snapshot_id}
```

Workflow records, agent definitions, review gates, compile results, and permission snapshots must stay metadata-only. Never store raw prompts, completions, source dumps, raw stderr, provider payloads, secrets, roots, external URLs, skipped sensitive content, or PII.

## Governed Work Plans And Work Tasks

Work Plans and Work Tasks are the governed workflow for multi-step implementation once the running MCP server exposes `projects.work_plans.*` and `projects.work_tasks.*` through `tools/list`. Before relying on them, verify the callable surface. If the tools are absent, report the surface gap explicitly; do not claim they are available and do not store Work Plan/Task records in ad hoc stable docs.

When exposed, agents MUST use this workflow for governed multi-step work:

1. Create or select a Work Plan before editing. Use `projects.work_plans.resume` when resuming, entering an existing project, or answering "what was I doing?" Use `projects.work_plans.create` only when no active plan exists.
2. Decompose large work into Work Tasks before editing. Each task must have one objective, bounded scope, dependencies, evidence needs, context pack refs or context needs, likely affected files or discovery scope, verification requirements, and resume instructions.
   - Each Work Task must be executable by an isolated low-intelligence worker from task metadata and attached refs alone. It must not depend on prior chat memory, hidden orchestrator context, or broad repo intuition.
   - Verification requirements must be written for orchestrator-run verification. Worker agents may write tests or artifacts when scoped, but must not run verifier commands unless the human/orchestrator explicitly allows it.
   - If an orchestrator cannot hand the task to a low-intelligence worker without adding private context, the task is too broad or under-specified; split or block it before execution.
   - Use first-class task packet fields when exposed: `files_to_read`, `files_to_edit`, `likely_files_affected`, `review_gate`, create-time non-terminal `status`, and `decomposition_quality`. Do not hide read/write scope or review gates in prose when those fields are available.
   - `projects.work_tasks.list` is a supported alias for `projects.work_tasks.list_open`; use it when a client expects a generic list tool.
   - Safe project-relative paths are allowed in Work Task descriptions and path fields. Roots, traversal, UNC paths, home-directory shortcuts, drive prefixes, secrets, raw prompts, raw source dumps, provider payloads, and PII remain prohibited.
3. Reject broad or vague tasks, or keep them in `planned`, when evidence, context, verification, dependencies, or resume instructions are missing.
4. Attach context pack refs before starting tasks that depend on indexed context. Store refs only, not context pack contents.
5. Claim a task with `projects.work_tasks.claim` before editing files or executing the task, then mark execution start with `projects.work_tasks.start`.
6. Attach Evidence Graph refs and claim refs before evidence-backed decisions. Use `projects.work_tasks.attach_evidence` and `projects.work_tasks.attach_claim` for refs only.
7. Keep Work Task status current. Use `projects.work_tasks.get` to inspect a known task before lifecycle changes, `projects.work_tasks.update_status` to cancel or supersede stale planned metadata, and `projects.work_tasks.block` with a clear blocked reason and resume instructions instead of silently stopping.
8. Follow lifecycle order. Do not jump a Work Plan directly from `planned` to `done`; move `planned -> active -> done` after all required tasks are complete. Do not jump a Work Task directly from `planned` to `done`; normal execution is `planned -> ready -> claimed -> in_progress -> verifying/needs_review -> done`, with `blocked`, `failed`, `cancelled`, or `superseded` used only when true.
9. Attach verifier result refs with `projects.work_tasks.attach_verifier_result` before completing tasks. Verifier refs must be short safe identifiers such as `test-workplan-mcp`, not commands, raw logs, raw stderr, paths, or source text. The orchestrator runs verifier commands unless the task explicitly allows a worker to run one narrow verifier. Do not mark a task complete when verification requirements are unmet.
10. Attach independent review result refs with `projects.work_tasks.attach_review_result` before completing any non-trivial or write-capable task. Review refs must be short safe identifiers such as `review-workplan-mcp`. The reviewer must be a different agent/run from the implementing `claimed_by_run_id` when known. Use `review_exempt_reason` only for tiny mechanical tasks with no code, config, data, auth, tenancy, privacy, API, migration, automation, or promotion risk.
11. Before completing any implementation, review, research, or automation task, make an explicit reusable-knowledge decision:
    - If the task created or relied on a durable conclusion, create/link Evidence Graph claim/evidence/decision/action/outcome refs, attach the relevant claim/evidence refs to the Work Task, score confidence when the conclusion may be reused, then create/link a Knowledge Promotion candidate.
    - If there is no reusable knowledge, say so in the Work Task `outcome` or attachment note with a short reason such as "no reusable project knowledge; code-only mechanical change".
    - Worker/subagents should produce candidate claim/evidence/knowledge refs or a clear "no reusable knowledge" note. The orchestrator verifies, scores confidence, and performs promotion gates.
12. Use `projects.work_tasks.get_next` after resume, after completion/block/failure, and whenever the next safe task is unclear.
13. Create or link Knowledge Promotion candidates through `projects.work_tasks.promote_knowledge_candidate` only when Evidence Graph, Confidence Engine, verifier, project, and optional org gates are respected. This tool must not bypass `projects.knowledge.*` validation or promotion.

Compact required flow:

```text
resume/create plan
-> create isolated-worker-ready tasks
-> attach context/evidence/claims
-> claim task
-> start task
-> execute bounded scope
-> independent review or explicit tiny-task review exemption
-> attach verifier result
-> record Evidence Graph outcome and confidence when reusable knowledge exists
-> create/link knowledge candidate or record no-reusable-knowledge reason
-> complete/block/fail
-> get next task
```

Work Plan and Work Task metadata must stay metadata-only. Never store raw prompts, completions, source dumps, raw stderr, provider payloads, secrets, roots, external URLs, skipped sensitive content, or PII.

### Parallel Work Plan Isolation

When `projects.workspace.git_status` or equivalent MCP workspace git support is available, every write-capable Work Plan MUST carry a dedicated worktree binding on `projects.work_plans.create`, even when only one worker or one Work Plan is expected. Parallel execution makes this mandatory, but single write-capable implementation plans must use the same isolation by default.

- `isolation_mode=dedicated_worktree`
- `parallel_group_ref=<shared orchestration ref>`
- `workspace_ref=<opaque workspace ref>`
- `git_base_ref=<base ref>`
- `git_branch_ref=<per-plan branch ref>`
- `git_worktree_ref=<opaque per-plan worktree ref>`

Use `isolation_mode=shared` only for read-only planning or inspection Work Plans. Do not use `shared` for implementation, generated-file writes, config changes, docs changes, test changes, automation writes, or any task that may modify the workspace. Use `isolation_mode=unavailable` only when git isolation is genuinely unavailable and report the risk before execution. These are refs, not filesystem locations. Do not run two write-capable Work Plans in the same worktree ref when likely affected files, artifacts, verifier scope, or promotion scope overlap. The orchestrator owns parallel scheduling and final verification.

When `projects.workspace.git_worktree_create` is exposed, the orchestrator MUST call it before assigning executable write-capable Work Tasks for a new Work Plan. The tool creates the dedicated worktree from `worktree_ref`, `branch_ref`, and optional `base_ref`, and returns metadata refs only. Agents must use the returned `isolation_ref`/`worktree_ref` in Work Plan metadata and automation refs. Do not create worktrees with raw shell commands when the MCP tool is available.

Dedicated worktrees are lifecycle resources. When a write-capable Work Plan reaches a terminal status (`done`, `failed`, `cancelled`, or `superseded`), the orchestrator or automation cleanup path must remove the dedicated worktree after verifier/review evidence is preserved and only when no active runs, unpreserved changes, or dependent review gates still need it. If cleanup tooling is missing, record the cleanup gap in the Work Plan/final report instead of leaving it implicit.

## Project Automation

Project Automation is an executor layer over Work Plans and Work Tasks. It is not an alternate planning system. When `projects.automations.*` and `projects.automation_runs.*` are exposed by `tools/list`, agents MUST use them only after a Work Plan exists and tasks are isolated-worker-ready. `projects.automations.run` executes the selected run in in-process or managed mode and queues the selected run in external mode. When `automation.work_plan_status_trigger.enabled = true`, moving a Work Plan into a configured status such as `active` queues each enabled automatic automation for that plan once. Managed mode lets `mivia-server` own automatic execution in native runtimes; Docker Compose and devcontainer configs use external mode with a `mivia-automation-runner` sidecar. A successful Codex CLI exit still requires independent review refs and orchestrator verifier refs before task completion or knowledge promotion.

Mandatory rules:

1. Automation runs must target existing project Work Plans and ready Work Tasks. Do not submit raw prompts or broad goals as automation work.
2. In-process and managed execution use Codex CLI from the server runtime. Managed mode is the normal native Linux, macOS, and WSL path when automation is enabled. Docker Compose and devcontainer configs use external execution and start `mivia-automation-runner` as a sidecar for containerized execution. External execution queues `codex_cli` work and MUST be claimed only by an explicitly supervised `mivia-automation-runner`.
3. Do not silently fall back from Codex CLI to manual/dry-run execution. A missing or denied runner is a policy/result state, not permission to improvise.
4. External runners MUST call `projects.automation_runs.claim_next`, execute only the returned metadata-only Codex input, then call `projects.automation_runs.complete_attempt` with status metadata only.
5. Do not call `projects.automations.create`, `projects.automations.run`, or `projects.automations.run_parallel_batch` until `projects.work_plans.*` and `projects.work_tasks.*` have created the execution structure.
6. Required pre-automation task metadata: bounded objective, dependencies, evidence/context/claim refs where needed, likely affected files or discovery scope, verification requirement, and resume instructions.
7. Parallel work must be orchestrator-owned. Use `projects.automations.run_parallel_batch` before running workers in parallel.
8. A parallel batch may include only independent ready tasks with done dependencies, disjoint likely affected files, explicit verification requirements, and no overlapping artifact or promotion scope.
9. Worker/subagent prompts must be generated from Work Task metadata and safe refs only. They must not depend on hidden chat context.
10. Only the orchestrator runs verifier commands unless the task explicitly allows a worker to run a narrow verifier.
11. Every automation-produced write-capable task needs an independent review result attached with `projects.work_tasks.attach_review_result` before completion. Do not let an implementation worker review its own task.
12. Automation output is untrusted until independent review refs, verifier refs, and Evidence Graph outcomes exist.
13. Any reusable conclusion from automation must be represented as Evidence Graph metadata, scored by Confidence Engine when knowledge may be reused, and promoted only through `projects.work_tasks.promote_knowledge_candidate` plus `projects.knowledge.*` gates.

Strict automation sequence:

```text
work plan
-> isolated-worker-ready work tasks
-> context/evidence/claim refs
-> automation definition
-> run or safe parallel batch
-> external claim/complete if configured
-> independent review result attached to task
-> orchestrator verification
-> verifier refs attached to task
-> Evidence Graph outcome and Confidence score where reusable
-> Knowledge Promotion candidate and project/org gates
-> Work Task completion
```

Use these tools:

```text
projects.automations.create
projects.automations.get
projects.automations.list
projects.automations.update_status
projects.automations.run
projects.automations.run_parallel_batch
projects.automation_runs.get
projects.automation_runs.list
projects.automation_runs.claim_next
projects.automation_runs.complete_attempt
```

REST mirrors the same metadata surface at:

```text
POST /api/v1/projects/{id}/automations
GET /api/v1/projects/{id}/automations
GET /api/v1/projects/{id}/automations/{automation_id}
POST /api/v1/projects/{id}/automations/{automation_id}/status
POST /api/v1/projects/{id}/automations/{automation_id}/runs
POST /api/v1/projects/{id}/automations/{automation_id}/parallel-batches
GET /api/v1/projects/{id}/automation-runs
POST /api/v1/projects/{id}/automation-runs/claim-next
GET /api/v1/projects/{id}/automation-runs/{run_id}
POST /api/v1/projects/{id}/automation-runs/{run_id}/attempt-result
```

Automation records must stay metadata-only. Never store raw prompts, completions, source dumps, raw stderr, provider payloads, secrets, roots, external URLs, skipped sensitive content, or PII.

MCP-first surfaces:

Host repository external-system rules override the integration guidance below. In this repository, live Jira/Confluence connectors remain prohibited unless the user explicitly overrides in the same request; local ingested Jira/Confluence MCP graph search/read is allowed only under `.ai/rules/05-external-systems.md` and must not call Atlassian or prove upstream absence.

- Project discovery, enabled state, digest mode, update policy, workspace mode, and graph storage.
- Ingestion run state, live/manual freshness, skipped reason counts, search-index degradation, repair status, and redacted ingestion diagnostics, including project-scoped storage keys but not raw datastore paths.
- Indexed file discovery, opaque file IDs, file metadata, outlines, headings, symbols, references, call sites, and bounded chunks.
- Governed workspace git status/diff, current eligible file reads, token-guarded exact edits, eligible single-file deletes, and new eligible text-file creates when `[workspace].enabled = true` and the project is opted in. Prefer `projects.workspace.file_read` before shell reads for `read_only` or `edit` workspaces. In `edit` mode, use `projects.workspace.file_read` before `projects.workspace.file_edit` or `projects.workspace.file_delete`, and use `projects.workspace.file_create` for new eligible text files before shell, `apply_patch`, or manual file operations.
- Context health, deterministic changed-path impact analysis, and selected stable-doc stale-claim checks.
- Project-scoped Evidence Graph metadata for claims, evidence refs, decisions, actions, outcomes, artifact links, and promotion links. Store only safe refs and short summaries; never store raw prompts, raw source dumps, provider payloads, secrets, roots, raw stderr, skipped sensitive content, or PII.
- Project-scoped confidence scoring metadata for Evidence Graph claims through `projects.confidence.claims.score`, `projects.confidence.claims.get`, and `projects.confidence.claims.list` plus underscore aliases. Confidence tools return deterministic score, band, recommendation, bounded factors, and safe input counters only; they must not store or expose raw prompts, raw completions, raw source dumps, raw stderr, provider payloads, secrets, roots, external URLs, PII, raw graph traversal, raw request payloads, raw scoring internals, AI/provider scoring, embedding scoring, or vector scoring.
- Knowledge Promotion metadata for project-level and org-level promoted knowledge. Query project-level knowledge before planning in the current project. Query org-level knowledge before cross-project claims. Treat promoted knowledge as guidance, never proof, until current source/context has been revalidated.
- Context packs that combine bounded search snippets, indexed file metadata, symbol metadata, optional impact analysis, and manifest-only reproducibility metadata.
- Redacted agent-run metadata for run status, steps, changed safe paths, verifier metadata, artifact refs, promotion-gate decisions, and optional `trace_id` correlation. When callers omit `trace_id`, the generated run id is the trace anchor.
- Configured Jira/Confluence integration provider listing/status/counts, async manual poll submission/status, and local integration graph search/read.
- Any task asking what the indexed project graph knows or whether local content graph ingestion is current.
- Planning and review context that can be answered from indexed files, symbols, references, calls, headings, or chunks.

Do not bypass MCP with raw database queries, absolute root inspection, broad shell scans, ad hoc `git status`/`git diff`, direct file reads, `apply_patch`, manual file edits, or shell file operations when an MCP workspace tool can answer or perform the operation. For opted-in projects, use `projects.workspace.file_read` before shell reads; in `workspace_mode = "edit"`, use `projects.workspace.file_edit` for exact/token-guarded edits, `projects.workspace.file_delete` for existing eligible single-file deletes, and `projects.workspace.file_create` for new eligible text files before shell or manual file operations. Omit `max_bytes` for full eligible file text; set it only when an explicit capped read is intended. Use shell only for tests, build output, logs, process control, generated-file verification, arbitrary commands outside the MCP contract, non-opted-in repositories, and files not yet eligible or allowed by MCP.

Do not use Serena for indexed project discovery, symbol overview/listing, references, call sites, search, bounded source chunks, or planning context when Mivia MCP is available and current.

If MCP is unavailable, stale, missing the project, or lacks the needed semantic operation, state that explicitly, then fall back to Serena plus shell for the minimum evidence needed.

## Inputs

Know or discover:

- MCP endpoint, default `http://127.0.0.1:8080/mcp`.
- Project ID, from the user or `projects.list`. Project-scoped tools also accept safe aliases returned by `projects.list` / `projects.get`, including configured repo/module aliases and auto-discovered Go module paths.
- Host repository rules, tests, and privacy/security boundaries.
- Operator config validation reports are available with `mivia-server config check --config <path> --redacted-json`; use this before hand-inspecting config for support triage when a redacted machine-readable validity report is enough. The report must not expose roots, URLs, Cloud IDs, credential references, or config paths.
- Before editing release examples in docs, Docker Compose, or devcontainer snippets, verify the current Go module tag and container image tag from git tags, registry checks, release metadata, or source-controlled release docs. Do not hardcode a release pair in this skill.

Do not assume the current repository is the server repo. Do not assume any specific language or directory layout.

## Tool Choice

| Need | First choice | Avoid |
| --- | --- | --- |
| Code symbols, references, call sites, edit targets | Mivia MCP when indexed and current | Serena as first resort in indexed Mivia projects |
| Indexed project map, ingestion state, file IDs, chunks, symbols | Mivia MCP | Raw DB queries, absolute paths, broad shell scans |
| Routine indexed text, path, symbol, reference, call, named AST discovery, or AST query-catalog discovery | `projects.search.*` | Serena `search_for_pattern`, raw DB queries, broad shell scans |
| Governed git status/diff/read, eligible file create/delete, and exact token-guarded edits for opted-in projects | MCP workspace tools | Broad shell scans, `apply_patch`, manual edits, recursive delete, or shell file operations as first resort |
| Context freshness/readiness, changed-path impact, stale docs/contracts | Mivia MCP reliability tools | LLM judgment, broad crawling, raw diff echoing |
| Bounded task context package | `projects.context_pack.build` | Manual broad scans, raw diffs, provider calls, full chunk dumps |
| Redacted agent-run metadata and promotion decisions | `agent_runs.*` | Raw prompts, completions, source dumps, raw stderr, roots, secrets, provider payloads, or PII |
| Governed multi-step Work Plans and Work Tasks when exposed by the running server | `projects.work_plans.*`, `projects.work_tasks.*` after verifying `tools/list` | Calling tools without surface verification, unmanaged parallel work, raw prompts, source dumps, raw stderr, provider payloads, secrets, roots, or PII |
| Project automation metadata and orchestrator-owned parallel batches when exposed by the running server | `projects.automations.*`, `projects.automation_runs.*` after verifying `tools/list`; external runners use `projects.automation_runs.claim_next` and `projects.automation_runs.complete_attempt` only | Raw prompt execution, silent manual fallback, worker-run verifiers without explicit permission, unmanaged parallel work |
| Promoted reusable knowledge | `projects.knowledge.*`, `orgs.knowledge.list` | Treating promoted knowledge as proof, automatic org promotion, deletion instead of supersession |
| Configured Jira/Confluence status, poll, search, or read | Mivia MCP integration tools | Jira/Confluence connectors, provider dashboards, live Atlassian reads during local search/read |
| Live project agent activity inspection | Local dashboard `Agent activity` drawer or `GET /api/v1/projects/{id}/agent-activity/stream` | Persistent logs or external telemetry by default |
| Current tests/runtime state, builds, logs, generated files, process control, arbitrary commands, non-opted-in repos | Shell or host tooling | MCP as proof of those runtime facts |

If unclear:

1. Indexed code structure -> Mivia MCP.
2. Indexed project discovery -> MCP.
3. Governed git status/diff/read for `read_only` or `edit` workspaces, plus exact token-guarded edits, eligible file create, and eligible file delete for `edit` workspaces -> MCP workspace tools.
4. Context health, impact analysis, stale-claim checks, or agent-run metadata -> MCP reliability/control tools.
5. Bounded multi-source project context -> `projects.context_pack.build`.
6. Local Jira/Confluence context -> MCP integration tools.
7. Tests, builds, logs, process control, generated files, arbitrary commands, or non-opted-in repos -> shell.
8. Non-indexed semantic gap -> Serena or host semantic tool, with the fallback stated.

## Safe Sequence

Use the smallest sequence that answers the task. Do not call every tool by default; call the smallest MCP set that proves the answer.

1. Confirm the MCP endpoint is localhost or loopback.
2. Use the client's exposed tool list when available. Use raw HTTP `tools/list` only for surface verification or when native clients cannot list tools.
3. Call `projects.list` to discover visible project IDs and aliases. If the user supplies a repo identity such as a Go module path, try it as a project ID/alias, then call `projects.get` and use the returned canonical `id` for follow-up calls. If the expected alias is missing, report that the server config should set the project's `aliases` list. Confirm `enabled`, `digest_mode`, `update_policy`, `workspace_mode`, `graph_storage`, and `validation_status`.
   - `graph_storage=persistent` means durable local Ladybug graph persistence backed by lazy-opened per-project Pebble stores for content-graph projects; the open-DB limit is derived from enabled persistent content-graph projects and capped at 16. Project search SQLite files are versioned with the Pebble graph storage epoch, so old search rows must not be treated as current graph coverage. Diagnostics may report `persistent_pebble_project`. It does not mean a remote graph database, Cypher, SQLite graph storage, or Jira/Confluence read-through.
4. Call `projects.graph_status` or `projects.context_health` before relying on indexed code/content if the answer depends on freshness. Use the returned status, `indexed_content_available`, indexed file/symbol/chunk counts, search-index state, latest run, and active run metadata as the authoritative graph inventory. If the status is not `ready`, state the status and either use MCP with the active-sync caveat when `indexed_content_available=true`, wait/poll, run ingestion when appropriate, or fall back with the freshness gap explicit. Status `syncing` means normal active indexing or a bounded probe under load, not a degraded index. Use `projects.ingestion_status_latest` only when you need the latest run record specifically.
5. Call `projects.search.text`, `projects.search.files`, `projects.search.symbols`, `projects.search.references`, `projects.search.calls`, `projects.search.ast.queries`, or `projects.search.ast` for routine indexed discovery before broad text scans.
6. Use `projects.impact.analyze` before reviewing or explaining a changed path set when the blast radius is not obvious. Prefer explicit `changed_paths`; use governed workspace diff mode only when the workspace is opted in and you need metadata from current changes. If it returns partial `index_syncing`, state that graph fanout is temporarily skipped under active ingestion and inspect the changed source directly.
7. Use `projects.context_pack.build` when one bounded response should combine search snippets, indexed file metadata, symbol metadata, optional impact analysis, and a manifest-only reproducibility record. The manifest records normalized query/options, graph/search-index status, selected file/symbol/chunk IDs, file timestamps, warnings, limitations, and truncated redacted hashes over manifest metadata identifiers only. It does not persist context packs, call providers, return raw diffs, include full chunk text, or include full source by default.
8. Use `projects.claims.check` before trusting selected stable docs/contracts that name MCP tools or REST routes. It is for selected files or pasted snippets, not broad crawling or LLM judgment.
9. Use `agent_runs.create`, `agent_runs.step_append`, `agent_runs.promote_artifact`, `agent_runs.complete`, and `agent_runs.get` to leave redacted execution breadcrumbs and promotion-gate decisions when a workflow benefits from resumability or handoff. Use a safe `trace_id` when correlating several agent runs, MCP calls, workspace edits, ingestion runs, verifier attempts, failures, and promotion decisions; otherwise the created run id becomes the trace anchor. Store only project/task IDs, statuses, changed project-relative paths, verifier command metadata, artifact refs, promotion states, and short safe summaries/notes.
10. Call `projects.files.list`, `projects.symbols.list`, or `projects.headings.list` with small `page_size` to confirm indexed content exists and narrow to stable opaque IDs.
11. Treat `projects.ingest` as asynchronous. It returns quickly with queued run metadata and a `run_id`; poll `projects.ingestion_status` with that `run_id` until `completed` or `failed`.
   - A `pending` or `running` run from before the current server process is an interrupted local queue entry, not active work. Current server builds fail interrupted runs on startup with `error_category=server_restarted`; restart onto a current build before trusting a long-pending zero-file run.
12. If search metadata reports `degraded: true`, call `projects.search_index.rebuild` only when the user or task explicitly asks to repair the local search index. Treat the rebuild as asynchronous: it returns queued run metadata and a `run_id`; poll `projects.ingestion_status` with that `run_id` until `completed` or `failed` before relying on search again.
13. Call `projects.diagnostics.ingestion` when ingestion, watcher, scheduler, or search-index behavior looks inconsistent. It is diagnostics-only and redacted; do not use it as a substitute for tests or logs.
14. Call `projects.files.get` when you need one file's bounded metadata by opaque `file_id`.
15. Call `projects.file.outline` first when file structure is enough. Use `kind`, `name_prefix`, `symbol_page_size`, and `symbol_page_token` to keep large symbol maps bounded. Use `projects.symbol.references`, `projects.symbol.callers`, `projects.symbol.callees`, and `projects.symbol.call_graph` for common indexed navigation. Use `projects.symbol.source` only when bounded eligible source text for one symbol is needed. Set `include_chunk_text=true` with a small `max_chunk_bytes` when eligible file source context is needed directly in the outline. Call `projects.file.chunks` when separate chunk paging is needed.
16. For configured Jira/Confluence context, call `projects.integrations.list` first. Use `projects.integrations.status` for provider config/sync state, `projects.integrations.counts` for total locally ingested items by provider, `projects.integrations.poll` to queue a manual provider run, and `projects.integrations.poll_status` with the returned `run_id` to watch that run. Use `projects.integrations.search`, `projects.jira.issue.get`, and `projects.confluence.page.get` only for already-ingested local graph content. Search/read/count tools do not call Atlassian or resolve credentials.
17. For opted-in workspaces, use `projects.workspace.git_status`, `projects.workspace.git_diff`, and `projects.workspace.file_read` before shell for status, diff, and eligible current file reads when `workspace_mode` is `read_only` or `edit`. In `workspace_mode = "edit"`, use `projects.workspace.file_read` then `projects.workspace.file_edit` before shell, `apply_patch`, or manual file edits when the edit is exact/token-guarded; use `projects.workspace.file_delete` after `file_read` for existing eligible single-file deletes; use `projects.workspace.file_create` for new eligible text files. First resolve the project through `projects.list` or `projects.get`, then pass the returned canonical `id` or listed alias. Do not pass cwd/root/UNC/WSL/filesystem paths in `id`; use `relative_path` or `path_prefix` for project-relative file selectors. `file_edit` and `file_delete` require the opaque token from a current file read and queue path ingestion after successful non-dry-run writes. `file_create` is for new eligible text files only. None of these tools provide recursive delete, arbitrary patch upload, arbitrary shell, or a shell replacement. Omit `max_bytes` for full eligible file text; pass a positive `max_bytes` only when intentionally requesting a capped read. If workspace git tools report `git is not available in the mivia-server runtime`, state that MCP git status/diff is unavailable and fall back to shell for exact git facts.
18. Switch to Serena or another semantic tool only if MCP cannot answer the required symbol body, reference, call, or edit-planning question.
19. Switch to shell for tests, builds, logs, generated files, process control, arbitrary commands, and non-opted-in repos. For edited indexed files, rely on live ingestion as the normal freshness path and poll latest ingestion status when search results look unexpected.

If MCP is down, the project is not listed, or live ingestion cannot provide current indexed context, say so and fall back to Serena or another semantic tool plus shell. Do not invent MCP facts.

## Tools

Use dotted names when available. Codex-style underscore aliases are accepted by the server for tool calls. If a tool is absent from `tools/list`, treat it as unavailable in that running server build even if this skill documents it.

| Purpose | Tools |
| --- | --- |
| Tasks | `tasks.create`, `tasks.get` |
| Research metadata only | `research_runs.create`, `research_runs.get`, `research_sources.create`, `research_sources.get` |
| Agent run metadata only | `agent_runs.create`, `agent_runs.step_append`, `agent_runs.promote_artifact`, `agent_runs.complete`, `agent_runs.get` |
| Project registry | `projects.list`, `projects.get` |
| Metadata digest and reliability | `projects.digest`, `projects.graph_status`, `projects.context_health`, `projects.impact.analyze`, `projects.context_pack.build`, `projects.claims.check` |
| Content graph | `projects.ingest`, `projects.search_index.rebuild`, `projects.ingestion_status`, `projects.ingestion_status_latest`, `projects.files.list`, `projects.files.get`, `projects.file.chunks`, `projects.symbols.list`, `projects.search.text`, `projects.search.files`, `projects.search.symbols`, `projects.search.references`, `projects.search.calls`, `projects.search.ast.queries`, `projects.search.ast`, `projects.symbol.source`, `projects.symbol.references`, `projects.symbol.callers`, `projects.symbol.callees`, `projects.symbol.call_graph`, `projects.headings.list`, `projects.file.outline` |
| Governed workspace | `projects.workspace.git_status`, `projects.workspace.git_diff`, `projects.workspace.file_read`, `projects.workspace.file_edit`, `projects.workspace.file_create`, `projects.workspace.file_delete` plus underscore aliases |
| Evidence Graph metadata only | `projects.evidence_graph.claims.create`, `projects.evidence_graph.claims.get`, `projects.evidence_graph.claims.list`, `projects.evidence_graph.evidence.append`, `projects.evidence_graph.decisions.create`, `projects.evidence_graph.actions.create`, `projects.evidence_graph.outcomes.create`, `projects.evidence_graph.artifacts.link`, `projects.evidence_graph.promotions.link` plus underscore aliases |
| Knowledge Promotion metadata only | `projects.knowledge.candidates.create`, `projects.knowledge.validate`, `projects.knowledge.promote_project`, `projects.knowledge.submit_org_review`, `projects.knowledge.promote_org`, `projects.knowledge.reject`, `projects.knowledge.supersede`, `projects.knowledge.reuse_events.record`, `projects.knowledge.get`, `projects.knowledge.list`, `orgs.knowledge.list` plus underscore aliases |
| Work Plans and Work Tasks metadata only | `projects.work_plans.create`, `projects.work_plans.get`, `projects.work_plans.list`, `projects.work_plans.update_status`, `projects.work_plans.resume`, `projects.work_tasks.create`, `projects.work_tasks.get`, `projects.work_tasks.update_status`, `projects.work_tasks.claim`, `projects.work_tasks.release`, `projects.work_tasks.start`, `projects.work_tasks.complete`, `projects.work_tasks.fail`, `projects.work_tasks.block`, `projects.work_tasks.list_open`, `projects.work_tasks.list_mine`, `projects.work_tasks.list_blocked`, `projects.work_tasks.get_next`, `projects.work_tasks.attach_evidence`, `projects.work_tasks.attach_context_pack`, `projects.work_tasks.attach_claim`, `projects.work_tasks.attach_verifier_result`, `projects.work_tasks.attach_review_result`, `projects.work_tasks.promote_knowledge_candidate` plus underscore aliases |
| Project Automation metadata only | `projects.automations.create`, `projects.automations.get`, `projects.automations.list`, `projects.automations.update_status`, `projects.automations.run`, `projects.automations.run_parallel_batch`, `projects.automation_runs.get`, `projects.automation_runs.list`, `projects.automation_runs.claim_next`, `projects.automation_runs.complete_attempt` plus underscore aliases |
| Diagnostics | `projects.diagnostics.ingestion` |
| Project integrations | `projects.integrations.list`, `projects.integrations.status`, `projects.integrations.counts`, `projects.integrations.poll`, `projects.integrations.poll_status`, `projects.integrations.search`, `projects.jira.issue.get`, `projects.confluence.page.get` |

### Tool Use Notes

- `tasks.create` / `tasks.get`: local agent task metadata only. Do not use for project implementation plans unless the repository asks for MCP task records.
- `research_runs.create` / `research_runs.get` and `research_sources.create` / `research_sources.get`: redacted research metadata only. They do not fetch providers and must not contain raw source content, prompts, secrets, or personal data.
- `agent_runs.create` / `agent_runs.step_append` / `agent_runs.complete` / `agent_runs.get`: redacted agent-run metadata only. Use for resumability, review/fix loops, handoffs, and trace correlation. Keep the returned `id` and pass it as `run_id` to step, promotion, and completion calls; `agent_runs.get` uses `id`. `trace_id` is optional and must be a safe identifier; if omitted on create, the generated run id becomes the trace id, and steps inherit it. They must not contain raw prompts, completions, source dumps, raw stderr, roots, secrets, credentials, provider payloads, or PII. For verifier metadata, prefer `command` as the executable and put flags/paths in `args`; simple space-separated words in `command` are normalized into args. Verifier args may include loopback URLs without credentials, query strings, or fragments; external URLs remain out of bounds.
- `agent_runs.promote_artifact`: redacted promotion-gate metadata only. Use for `candidate`, `validated`, `promoted`, and `rejected` decisions on existing artifact refs. Validated, promoted, and rejected decisions require a verifier ref and bounded decision text; raw payloads, roots, secrets, and PII remain out of bounds.
- `projects.list`: first project-discovery call. Returns configured project metadata without root paths, including safe lookup aliases when available.
- `projects.get`: use before project-specific work to confirm the selected project is enabled and validate content/workspace modes. The returned `id` is canonical; use it for follow-up calls even when you started from an alias.
- `projects.digest`: metadata-only digest for projects that support digest mode. Content-graph projects may reject this as unsupported; use ingestion/search tools instead.
- `projects.graph_status`: authoritative graph inventory and sync-state summary for one configured project. Prefer this over `projects.ingestion_status_latest` when deciding if indexed MCP tools are usable.
- `projects.context_health`: readiness/freshness summary for one configured project using safe config, ingestion, search-index, indexed file/symbol/chunk counts, active/latest run metadata, and workspace-git metadata. A `syncing` response with `indexed_content_available=true` means MCP indexed tools can still be used with the active-sync caveat.
- `projects.impact.analyze`: deterministic changed-path impact analysis. It may use governed workspace diff file metadata but must not return raw diff content. During active ingestion it may return partial `index_syncing` metadata instead of waiting behind busy graph/search stores.
- `projects.context_pack.build`: bounded context package from existing indexed search, file metadata, symbol metadata, optional impact analysis, and manifest-only reproducibility metadata. It does not create storage, call providers, return roots, return raw diffs, include full chunk text, or include full source by default.
- `projects.claims.check`: deterministic stale-claim check for selected stable docs/contracts. Default output is concise: summary counts plus actionable findings only; pass `include_verified: true` only when a full audit/debug list is needed. It does not use LLM judgment, broad crawling, document-content echoing, or release/publication validation. Use git tags, registry checks, or release metadata for version/publication claims.
- `projects.evidence_graph.claims.create` / `projects.evidence_graph.claims.get` / `projects.evidence_graph.claims.list`: project-scoped Evidence Graph claim metadata. `claims.list` supports safe filters by `artifact_ref`, `promotion_state`, `outcome_status`, `run_id`, and `trace_id`, plus `page_size` and `page_token`; the default page size is 50 and the maximum is 100.
- `projects.evidence_graph.evidence.append`: add one safe evidence ref to a claim. `evidence_kind` is one of `context_pack`, `file`, `chunk`, `symbol`, `verifier`, `claim_check`, `artifact`, or `other`.
- `projects.evidence_graph.decisions.create`: add one decision with `decision_ref`, `state`, `verifier_ref`, and bounded `rationale`; states are `validated`, `promoted`, or `rejected`.
- `projects.evidence_graph.actions.create`: add one action linked to a decision with `decision_id`, `action_ref`, `action_kind`, optional safe `changed_files`, and optional `run_id`.
- `projects.evidence_graph.outcomes.create`: add one outcome linked to an action with `action_id`, `outcome_ref`, `outcome_kind`, `status`, optional `verifier_ref`, and optional summary.
- `projects.evidence_graph.artifacts.link`: link a safe `artifact_ref` to a claim with optional `artifact_kind` and `run_id`.
- `projects.evidence_graph.promotions.link`: link a safe promotion decision to a claim with `artifact_ref`, `promotion_state`, `source_ref`, optional `run_id`, and decision/action/outcome refs. Non-candidate links require `verifier_ref` and `decision_ref`; promoted links require an `outcome_ref` for a passed outcome.
- `projects.confidence.claims.score` / `projects_confidence_claims_score`: calculate and store one deterministic metadata-only confidence assessment for a project-scoped Evidence Graph claim. Inputs are `id`, `claim_id`, optional safe project-relative `changed_paths`, optional stable-doc `claim_check_paths`, and optional `include_verified`.
- `projects.confidence.claims.get` / `projects_confidence_claims_get`: fetch the stored metadata-only confidence assessment for one claim by `id` and `claim_id`.
- `projects.confidence.claims.list` / `projects_confidence_claims_list`: list metadata-only confidence assessments by optional `band`, `min_score`, `max_score`, `recommendation`, `run_id`, `trace_id`, `page_size`, and `page_token`; the default page size is 50 and the maximum is 100.
- `projects.knowledge.candidates.create` / `projects_knowledge_candidates_create`: create one metadata-only project knowledge candidate from safe Evidence Graph and confidence refs.
- `projects.knowledge.validate` / `projects_knowledge_validate`: validate candidate knowledge with current Evidence Graph and Confidence Engine metadata.
- `projects.knowledge.promote_project` / `projects_knowledge_promote_project`: promote validated knowledge at project scope after the project gate passes. Project-level promotion is the default.
- `projects.knowledge.submit_org_review` / `projects_knowledge_submit_org_review`: explicitly submit project-promoted knowledge for default org review. This does not promote org knowledge.
- `projects.knowledge.promote_org` / `projects_knowledge_promote_org`: explicitly promote org-reviewed knowledge to org scope after stricter gates pass. Org-level promotion is optional, stricter, explicit, and never automatic.
- `projects.knowledge.reject` / `projects_knowledge_reject`: reject a knowledge record without deleting metadata.
- `projects.knowledge.supersede` / `projects_knowledge_supersede`: supersede stale or contradicted knowledge without destructive deletion.
- `projects.knowledge.reuse_events.record` / `projects_knowledge_reuse_events_record`: record safe metadata when promoted knowledge is used, skipped, stale, or contradicted.
- `projects.knowledge.get` / `projects_knowledge_get`: fetch one project knowledge record as metadata only.
- `projects.knowledge.list` / `projects_knowledge_list`: list project knowledge by safe metadata filters. Use this before planning in the current project.
- `orgs.knowledge.list` / `orgs_knowledge_list`: list default org-promoted knowledge only. Use this before making cross-project claims.
- `projects.ingest`: queue bounded content-graph ingestion. Always poll with `projects.ingestion_status`.
- `projects.search_index.rebuild`: repair degraded local search index only when asked or when degradation blocks the task. Always poll with `projects.ingestion_status`.
- `projects.ingestion_status`: read one ingestion/rebuild run by `run_id`.
- `projects.ingestion_status_latest`: latest run metadata only. Do not use it alone as a graph-readiness or MCP-usability decision.
- `projects.files.list`: discover eligible indexed files with filters such as path/status/extension and a small `page_size`.
- `projects.files.get`: fetch one file metadata record by opaque `file_id`.
- `projects.file.chunks`: page bounded chunk text for one eligible file. Keep `max_chunk_bytes` small.
- `projects.file.outline`: preferred first read for one file's structure; use it before chunk text when symbols/headings are enough.
- `projects.symbols.list`: list bounded symbol metadata; filter by `kind`, `package`, `name_prefix`, `name_contains`, `receiver`, `file_id`, and page tokens.
- `projects.search.text`: literal indexed text search. Use for known strings, error names, config keys, or prose.
- `projects.search.files`: indexed file metadata search by safe project-relative path. Use before file list when you know part of a path.
- `projects.search.symbols`: symbol search by prefix/substr. Use before references/call graph when you need stable symbol IDs.
- `projects.search.references`: indexed reference metadata search by name/target/enclosing symbol.
- `projects.search.calls`: indexed call edge search by caller/callee names.
- `projects.search.ast.queries`: list available named AST query IDs and safe coverage before AST search.
- `projects.search.ast`: run only named AST queries from the catalog; never send raw Tree-sitter query text.
- `projects.symbol.source`: bounded source for one eligible symbol. Use only after selecting a stable symbol ID.
- `projects.symbol.references`: references resolving to one symbol ID.
- `projects.symbol.callers`: direct callers for one symbol ID.
- `projects.symbol.callees`: direct callees for one symbol ID.
- `projects.symbol.call_graph`: bounded traversal around one symbol ID; set depth/limits conservatively.
- `projects.headings.list`: Markdown/document heading metadata. Use for docs discovery before broad text reads.
- `projects.workspace.git_status`: governed git status for opted-in workspaces. `id` must be a project id or alias from `projects.list` / `projects.get`, not a filesystem path. Prefer before shell `git status` when available. If it reports Git unavailable or times out, fall back to shell and report the MCP gap.
- `projects.workspace.git_diff`: governed capped diff for opted-in workspaces. `id` must be a project id or alias from `projects.list` / `projects.get`, not a filesystem path; use `relative_path` or `path_prefix` for file filtering. Prefer before shell `git diff` when available. If it reports Git unavailable, fall back to shell and report the MCP gap.
- `projects.workspace.file_read`: current eligible file content plus edit token. `id` must be a project id or alias from `projects.list` / `projects.get`, not a filesystem path; select files with `file_id` or project-relative `relative_path`. Prefer it before shell reads for opted-in `read_only` or `edit` workspaces. Omit `max_bytes` for full eligible file text; pass a positive value only for intentional capped reads. Required before `projects.workspace.file_edit` and `projects.workspace.file_delete`.
- `projects.workspace.file_edit`: exact token-guarded edit only. `id` must be a project id or alias from `projects.list` / `projects.get`, not a filesystem path. Do not use for broad rewrites, generated files, or arbitrary patches.
- `projects.workspace.file_create`: create a new eligible text file in an opted-in `edit` workspace. Use it before shell or manual file creation when the target path is allowed and absent. Do not use it for generated files, binary files, arbitrary patch payloads, directory creation, or shell replacement.
- `projects.workspace.file_delete`: delete one existing eligible file in an opted-in `edit` workspace using the opaque token from `projects.workspace.file_read`. Do not use it for recursive deletes, directory deletes, globs, cleanup sweeps, generated-output removal, or shell replacement.
- `projects.diagnostics.ingestion`: redacted scheduler/watcher/runtime/storage diagnostics. Use when ingestion/search behavior is suspect; switch to logs only if runtime proof is required.
- `projects.integrations.list`: discover configured Jira/Confluence providers and redacted config metadata for one project.
- `projects.integrations.status`: provider coverage, sync state, last run, active run, polling config, and cursor presence only.
- `projects.integrations.counts`: total locally ingested item counts by configured provider. Counts are local-store counts, not live provider totals.
- `projects.integrations.poll`: queue manual local integration polling. This may call Atlassian Cloud in the background using configured credentials; response remains redacted.
- `projects.integrations.poll_status`: fetch one local poll run by `run_id`.
- `projects.integrations.search`: search already-ingested local Jira/Confluence chunks only.
- `projects.jira.issue.get`: read one locally ingested Jira issue by issue key with bounded chunks. Default page is 3 chunks; pass `chunk_offset` from `next_chunk_offset` to continue.
- `projects.confluence.page.get`: read one locally ingested Confluence page by page ID with bounded chunks. Default page is 3 chunks; pass `chunk_offset` from `next_chunk_offset` to continue.

## Knowledge Promotion And Collective Learning

Project-level promoted knowledge is the default reuse surface for agents working in one project. Org-level promoted knowledge is optional, stricter, explicit, cross-project guidance and MUST never be inferred or created automatically from confidence score alone.

Agents MUST query `projects.knowledge.list` before making an implementation plan for the current project. Agents MUST query `orgs.knowledge.list` before making a cross-project claim. When project-level and org-level knowledge conflict for project-specific behavior, agents MUST prefer current project evidence after revalidation.

Promoted knowledge is guidance, not proof. Agents MUST revalidate promoted knowledge against current source, current context health, relevant tests/runtime evidence when available, and `projects.claims.check` for stable docs, MCP tool, REST route, and route-claim surfaces before acting. If revalidation finds stale or contradicted knowledge, agents MUST create or use a superseding record through `projects.knowledge.supersede`; they MUST NOT delete or destructively edit the old record.

Agents MUST record reuse with `projects.knowledge.reuse_events.record` whenever they use, skip, find stale, or find contradicted promoted knowledge. Use `outcome=used`, `skipped`, `stale`, or `contradicted`, and include `revalidated=true` plus a safe `revalidation_ref` when the agent acted on the knowledge.

Exact agent sequence:

1. Query project knowledge with `projects.knowledge.list`.
2. Query org knowledge with `orgs.knowledge.list` only when making a cross-project claim.
3. Verify current source/context with MCP context tools, workspace reads, shell/runtime evidence when needed, and `projects.claims.check` for stable docs/tool/route claims.
4. Record Evidence Graph metadata for any new conclusion with `projects.evidence_graph.*`.
5. Score confidence with `projects.confidence.claims.score`.
6. Promote only after gates pass: `projects.knowledge.candidates.create`, `projects.knowledge.validate`, then `projects.knowledge.promote_project`; use `projects.knowledge.submit_org_review` and `projects.knowledge.promote_org` only for explicit org promotion.
7. Record the reuse event with `projects.knowledge.reuse_events.record`.

Promotion records MUST remain metadata-only. Keep raw prompts, raw completions, raw source dumps, raw stderr, provider payloads, secrets, roots, external URLs, and PII out of knowledge records, promotion decisions, reuse events, summaries, refs, verifier refs, and rationale fields.

For this repository, agents MUST avoid live Jira and Confluence connectors unless the user explicitly overrides that constraint in the same request. Local ingested Jira/Confluence MCP search/read remains subject to `.ai/rules/05-external-systems.md` and must not be treated as live provider proof.

## Indexed Metadata Contract

- Promoted AST metadata currently covers Go stdlib AST, Tree-sitter JS/JSX/TS/TSX, Tree-sitter C#, Tree-sitter Python, Markdown headings, and lightweight infrastructure/config metadata.
- `projects.search.ast.queries` returns the supported named AST query catalog: query IDs, languages, capture names, query versions, matching file extensions, and safe per-language coverage counts. It does not return raw Tree-sitter query text.
- `projects.search.ast` runs named Tree-sitter structural queries over eligible indexed chunks for Go, Python, JavaScript, JSX, TypeScript, TSX, and C#. It accepts catalog IDs such as `function_declarations`, `class_declarations`, `call_expressions`, `imports`, `test_functions`, `assignments`, and `error_handling`; it does not accept raw Tree-sitter query syntax.
- Sensitive, denied, absent, parse-error, and other skipped files are unreachable from AST search. Oversized text files can be chunk-indexed when they pass streaming safety checks, but semantic extraction is skipped and represented as `skipped_reason=semantic_too_large`; unsafe oversized files remain safe coverage gaps. Source text, chunks, snippets, content hashes, raw parser/SQLite/FTS/Tree-sitter errors, roots, secrets, PII, raw prompts, and provider payloads are not exposed for skipped or unsafe files.
- TS/JS/TSX/JSX, C#, and Python have no regex fallback. If a promoted grammar or embedded query cannot initialize, server startup fails with `extractor_initialization_failed`.
- Per-file parser failures are file-local `parse_error` skips; full scans continue.
- Extractor cache rows store only symbols, headings, references, and calls keyed by hashes, extractor name, version, and fingerprint. Legacy empty-fingerprint rows are treated as cache misses when fingerprint-aware lookup is used; agents should not interpret a one-time refresh as file content change.
- Full scans run through bounded graph write batches and the fair scheduler; live path events have priority over full-scan continuation.

Resources:

- `mivialabs://tasks/{id}`
- `mivialabs://research-runs/{id}`
- `mivialabs://research-sources/{id}`
- `mivialabs://projects/{id}`
- `mivialabs://projects/{id}/digest-runs/{run_id}`
- `mivialabs://projects/{id}/files/{file_id}`
- `mivialabs://projects/{id}/files/{file_id}/chunks/{chunk_id}`
- `mivialabs://projects/{id}/files/{file_id}/outline`
- `mivialabs://projects/{id}/symbols/{symbol_id}`

Read resources only when a resource URI is already known and a template exactly matches the target. Prefer tools for discovery, pagination, status, search, counts, and writes.

## Workspace Boundary

Workspace tools are default-disabled and require both global `[workspace].enabled = true` and per-project `workspace_mode = "read_only"` or `"edit"` with `digest_mode = "content_graph"`. `read_only` allows governed git status/diff and current eligible file reads. `edit` additionally allows exact byte-span edits and eligible single-file deletes guarded by an opaque per-process token from `projects.workspace.file_read`, plus new eligible text-file creation through `projects.workspace.file_create`; use these paths before shell, `apply_patch`, or manual file operations when the file is eligible. File reads return full eligible text unless the caller passes an explicit positive `max_bytes` cap. There is no arbitrary shell endpoint, public exposure, auth change, provider call, embedding/vector/crawling path, raw DB query endpoint, raw patch upload endpoint, recursive delete, or git commit/push/checkout/reset/branch/merge/rebase/stash/clean/restore tool.

## Raw HTTP Fallback

Use raw HTTP only when no native MCP client is available:

- `POST http://127.0.0.1:<port>/mcp`
- `Content-Type: application/json`
- `Accept: application/json, text/event-stream`
- Optional `MCP-Protocol-Version: 2025-06-18`

Start with `tools/list`, then use `tools/call`. Do not use raw HTTP to bypass MCP boundaries.

The local dashboard exposes a project-scoped `Agent activity` drawer backed by `GET /api/v1/projects/{id}/agent-activity/stream`. It streams persisted redacted recent MCP activity plus agent-run lifecycle events, verifier metadata, promotion decisions, policy events, and live in-memory activity for the selected project, including method/tool, status, duration, failure category, client class, request metadata, `trace_id`, `run_id`, `parent_id`, `correlation_kind`, and input/output summary classes. Reconnecting clients can resume with `Last-Event-ID` or `after_id`. Live in-memory events may include collapsed raw request/params/arguments/result payloads for localhost debugging; persistent storage omits raw payloads and payload-derived hashes by default unless explicit local debug retention is enabled with `MIVIA_DEBUG_ENABLED=true` and `MIVIA_AGENT_ACTIVITY_RETAIN_RAW_PAYLOADS=true`. Treat full payloads as sensitive local-debug material. Do not copy them into docs, commits, agent-run metadata, or external tools. Even when debug retention is enabled or the task asks for inspection, redact first and never copy secrets, credentials, auth headers, tokens, PII, raw prompts, raw source dumps, or provider payloads without explicit human approval and a narrower safe excerpt.

## A/B Agent Tests

When measuring MCP impact:

1. Create two clean worktrees from the same commit.
2. Give both agents the same task and acceptance criteria.
3. MCP run: require `tools/list`, `projects.get`, and small `projects.files.list` or `projects.symbols.list` before broad shell reads.
4. Non-MCP run: forbid MCP and raw `/mcp` HTTP.
5. Require each agent to save a run log with elapsed time, tool calls, files changed, diff stats, tests run, and failures.
6. Save the evaluator report in the host repo's test-report location; if none exists, use `docs/reports/tests/`.
7. Do not let either implementation agent review the other implementation.

## Hard Boundaries

Never request, store, or expose:

- Absolute roots or datastore paths.
- Raw DB queries or raw query results.
- Secrets, credentials, tokens, raw prompts, or raw provider payload blobs.
- PII, except owner-approved Jira/Confluence rich content returned through bounded local integration search/read under the project integration policy.
- Skipped sensitive content or matched sensitive text.
- Public exposure, embeddings, vectors, crawling, production deployment, symlink traversal, or auth-model changes.
- Provider calls, except configured local integration polling through `projects.integrations.poll`.

Stop and report the blocked condition if the workflow requires any of those.

## Project Integration Boundary

Project integration tools cover configured Jira Cloud and Confluence Cloud providers only. They are local, polling-backed, and configured per project. Status responses are redacted and must omit raw site URLs, raw allowlists, env var names, file paths, credentials, auth headers, local roots, raw provider payloads, and raw cursor values.

Counts:

- `projects.integrations.counts` accepts `id` only.
- It returns local item counts for configured providers only.
- A zero count means no local items currently match that project/provider in the local integration store; it does not prove the remote provider has zero items.
- Counts are read-only and do not call Jira, Confluence, or credential providers.

Polling:

- `projects.integrations.poll` accepts `id`, `provider` (`jira` or `confluence`), and optional `kind` (`initial_full` or `incremental`).
- It returns queued run metadata with a `run_id`; always use `projects.integrations.poll_status` or `projects.integrations.status` before relying on new data.
- The background run may call Atlassian Cloud using configured env/file credential refs at execution time. The response must not expose credentials, credential refs, raw provider payloads, raw cursors, roots, or datastore paths.

Local graph search/read:

- `projects.integrations.search` searches already-ingested local integration chunks only.
- `projects.jira.issue.get` reads one locally ingested Jira issue by issue key.
- `projects.confluence.page.get` reads one locally ingested Confluence page by page ID.
- These search/read tools do not call Atlassian and must return only bounded local graph content.
- A local miss returns a typed MCP tool error such as `not_indexed`; it does not prove upstream absence. For read tools, `id` is the Mivia project slug, not a Jira numeric issue ID.
