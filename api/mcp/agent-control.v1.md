# Agent Control MCP Capability Contract

Version: 0.1.0
Protocol target: MCP 2025-06-18 Streamable HTTP
Endpoint: `/mcp`
Classification: Internal; local project-integration rich-content exception only

## Transport

- HTTP `POST /mcp` accepts one JSON-RPC request, notification, or response per request.
- `Accept` must include `application/json` and `text/event-stream`.
- `Content-Type` must be `application/json`.
- `MCP-Protocol-Version` must be `2025-06-18` when present.
- `Origin`, when present, must be localhost or loopback.
- HTTP `GET /mcp` returns 405 until SSE streams are implemented.
- MCP `_meta` fields are accepted in tool-call params and arguments for client compatibility.
- Tool-call `arguments` may be an object or a JSON-encoded object string.
- The server accepts both dotted tool names, for example `tasks.create`, and Codex-style underscore aliases, for example `tasks_create`.

## Local Dashboard Activity

- The REST dashboard exposes `GET /api/v1/projects/{id}/agent-activity/stream` as a project-scoped SSE stream for MCP activity, agent-run lifecycle events, verifier metadata, promotion decisions, policy guard events, and workspace/ingestion correlation metadata.
- The stream replays recent persisted redacted events and then sends live `mcp_activity`, `policy_event`, `agent_run_started`, `agent_step`, `agent_promotion`, and `agent_run_completed` events for the selected project.
- Reconnecting clients may send `Last-Event-ID` or `after_id` to replay events with IDs greater than the last seen event.
- Events include method/tool, status, duration, failure category, client class, request metadata, `trace_id`, `run_id`, `parent_id`, `correlation_kind`, and input/output summary classes.
- Live in-memory events may include raw request/params/arguments/result payloads for localhost debugging. Persistent storage omits raw payloads and payload-derived hashes by default; raw payload/hash retention requires explicit local debug opt-in with `MIVIA_DEBUG_ENABLED=true` and `MIVIA_AGENT_ACTIVITY_RETAIN_RAW_PAYLOADS=true`.
- Raw payloads may include source snippets, prompts, secrets, provider payloads, or personal data if a caller sent them. They should not be copied into durable artifacts without explicit intent.

## Tools

### Work Plan And Work Task Contract

Work Plans and Work Tasks are the governed workflow for multi-step implementation when the running server exposes them through `tools/list`. Agents must verify the callable surface before use and must report any surface gap instead of pretending tools exist.

When exposed, agents MUST use Work Plans and Work Tasks for governed multi-step work:

1. `projects.work_plans.create` or `projects.work_plans.resume` creates/selects the plan before editing.
2. `projects.work_tasks.create` decomposes broad work into small low-intelligence-agent-ready tasks with one objective, bounded scope, dependencies, evidence needs, context pack refs or context needs, verification requirements, likely affected files, and resume instructions. Each task must be executable by an isolated low-intelligence worker from task metadata and attached refs alone, without prior chat memory, hidden orchestrator context, or broad repo intuition. Verification must be written so the orchestrator can run it independently; scoped workers may write tests or artifacts but must not run verifier commands unless explicitly allowed.
3. `projects.work_tasks.claim` and `projects.work_tasks.start` are required before execution.
4. Context-dependent tasks must attach context pack refs with `projects.work_tasks.attach_context_pack`.
5. Evidence-backed tasks must attach Evidence Graph or claim refs with `projects.work_tasks.attach_evidence` and `projects.work_tasks.attach_claim`.
6. Tasks with verification requirements must attach verifier result refs with `projects.work_tasks.attach_verifier_result` before completion.
7. Lifecycle order is mandatory. Work Plans normally move `planned -> active -> done`; do not jump `planned -> done`. Work Tasks normally move `planned -> ready -> claimed -> in_progress -> verifying/needs_review -> done`; use `blocked`, `failed`, `cancelled`, or `superseded` only when that state is true.
8. `projects.work_tasks.complete`, `projects.work_tasks.block`, `projects.work_tasks.fail`, or `projects.work_tasks.update_status` must keep status current; blocked tasks require a blocked reason and resume instructions.
9. Before completing implementation, review, research, or automation tasks, agents must make a reusable-knowledge decision. If a durable conclusion exists, attach Evidence Graph claim/evidence refs, score confidence when reusable, and create/link a Knowledge Promotion candidate. If no reusable knowledge exists, record a short no-reusable-knowledge reason in the task outcome or attachment note.
10. `projects.work_tasks.get_next` is required after resume, completion, block, or uncertainty.
11. `projects.work_tasks.promote_knowledge_candidate` may create or link only a Knowledge Promotion candidate. It must not bypass Evidence Graph, Confidence Engine, verifier, project, or org promotion gates.

Dotted tools and underscore aliases:

```text
projects.work_plans.create
projects.work_plans.get
projects.work_plans.list
projects.work_plans.update_status
projects.work_plans.resume
projects.work_tasks.create
projects.work_tasks.update_status
projects.work_tasks.claim
projects.work_tasks.release
projects.work_tasks.start
projects.work_tasks.complete
projects.work_tasks.fail
projects.work_tasks.block
projects.work_tasks.list_open
projects.work_tasks.list_mine
projects.work_tasks.list_blocked
projects.work_tasks.get_next
projects.work_tasks.attach_evidence
projects.work_tasks.attach_context_pack
projects.work_tasks.attach_claim
projects.work_tasks.attach_verifier_result
projects.work_tasks.promote_knowledge_candidate
```

All Work Plan and Work Task metadata must remain metadata-only. Raw prompts, completions, source dumps, raw stderr, provider payloads, secrets, roots, external URLs, skipped sensitive content, and PII are prohibited.

Subagents are not exempt from Evidence Graph or Knowledge Promotion. Worker/subagents must return safe candidate claim refs, evidence refs, verifier refs, confidence refs when available, and knowledge candidate refs, or explicitly state `no_reusable_knowledge` with a bounded reason. The orchestrator is responsible for verifier execution, confidence scoring, and project/org promotion gates.

### Parallel Work Plan Isolation

When MCP workspace git support is available, each parallel Work Plan MUST declare its own metadata-only isolation refs on `projects.work_plans.create`:

- `isolation_mode`: use `dedicated_worktree` for executable parallel plans, `shared` only for read-only planning, and `unavailable` when git isolation cannot be provided.
- `parallel_group_ref`: common ref shared by Work Plans that are orchestrated together.
- `workspace_ref`, `git_base_ref`, `git_branch_ref`, and `git_worktree_ref`: opaque refs only, never filesystem locations.

Agents MUST NOT run two write-capable Work Plans in the same worktree ref when their likely affected files, artifacts, verifier scope, or promotion scope overlap. The orchestrator owns parallel scheduling and final verification.

When exposed, `projects.workspace.git_worktree_create` MUST be used by the orchestrator before assigning executable parallel Work Tasks for a new Work Plan. It accepts `worktree_ref`, `branch_ref`, optional `base_ref`, and `dry_run`, creates the dedicated git worktree through the governed workspace boundary, and returns metadata refs only. Agents must use the returned `isolation_ref`/`worktree_ref` in Work Plan metadata and automation refs. Do not create worktrees with raw shell commands when the MCP tool is available.

### Project Automation Contract

Project automation is an executor over Work Plans and Work Tasks. It is not an alternate task system and must not execute raw prompts directly.

When exposed, automation tools MUST follow these rules:

1. Automation runs operate only on existing project Work Plans and ready Work Tasks. Agents MUST NOT call `projects.automations.create`, `projects.automations.run`, or `projects.automations.run_parallel_batch` as a substitute for `projects.work_plans.*` or `projects.work_tasks.*`.
2. If automation uses in-process runner execution and Codex CLI is available in the server runtime, executable automation MUST use `codex_cli`.
3. If automation uses external runner execution, `projects.automations.run` queues `codex_cli` work and the local `mivia-automation-runner` MUST run from the user's logged-in Codex environment.
4. Automation MUST NOT silently fall back from Codex CLI to manual/dry-run execution.
5. Required pre-automation state: selected Work Task has bounded scope, dependency state, context/evidence/claim refs where needed, likely affected files, verification requirement, and resume instructions.
6. Parallel work MUST be orchestrator-owned through a safe parallel batch.
7. Parallel batches may include only independent ready Work Tasks with satisfied dependencies and disjoint write/verifier/artifact scope.
8. Worker/subagent prompts must be generated from Work Task metadata and safe refs only.
9. Automation output is untrusted until verifier refs and Evidence Graph outcomes exist.
10. Any reusable conclusion from automation MUST be represented as Evidence Graph metadata, scored by Confidence Engine when knowledge may be reused, and promoted only through `projects.work_tasks.promote_knowledge_candidate` plus `projects.knowledge.*` gates.

Dotted tools and underscore aliases:

```text
projects.automations.create
projects.automations.get
projects.automations.list
projects.automations.run
projects.automations.run_parallel_batch
projects.automation_runs.get
projects.automation_runs.list
projects.automation_runs.claim_next
projects.automation_runs.complete_attempt
```

Automation records must remain metadata-only. Raw prompts, completions, source dumps, raw stderr, provider payloads, secrets, roots, external URLs, skipped sensitive content, and PII are prohibited.

Project-scoped REST routes:

```text
POST /api/v1/projects/{id}/automations
GET /api/v1/projects/{id}/automations
GET /api/v1/projects/{id}/automations/{automation_id}
POST /api/v1/projects/{id}/automations/{automation_id}/runs
POST /api/v1/projects/{id}/automations/{automation_id}/parallel-batches
GET /api/v1/projects/{id}/automation-runs
POST /api/v1/projects/{id}/automation-runs/claim-next
GET /api/v1/projects/{id}/automation-runs/{run_id}
POST /api/v1/projects/{id}/automation-runs/{run_id}/attempt-result
```

`POST /api/v1/projects/{id}/automations/{automation_id}/runs` executes one automation run in in-process mode and queues one automation run in external mode. External runners call `POST /api/v1/projects/{id}/automation-runs/claim-next`, execute `codex exec --json --input-file <metadata-only-file>` locally, then call `POST /api/v1/projects/{id}/automation-runs/{run_id}/attempt-result`. A successful Codex CLI exit leaves the run in verifier-required state; it does not prove verification, task completion, claim validation, confidence, or knowledge promotion.

Strict end-to-end automation sequence:

```text
projects.work_plans.resume/list/create
-> projects.work_tasks.create for isolated-worker-ready tasks
-> projects.work_tasks.attach_context_pack / attach_evidence / attach_claim as needed
-> projects.automations.create over the existing plan/tasks
-> projects.automations.run or run_parallel_batch
-> external runner claim/complete when runner_execution=external
-> orchestrator verifier runs outside workers
-> projects.work_tasks.attach_verifier_result
-> Evidence Graph decisions/actions/outcomes for reusable conclusions
-> projects.confidence.claims.score when knowledge may be reused
-> projects.work_tasks.promote_knowledge_candidate
-> projects.knowledge.validate / promote_project / optional org review/promote
-> projects.work_tasks.complete only after verification requirements are satisfied
```

### `tasks.create`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["title"],
  "properties": {
    "title": { "type": "string", "minLength": 1, "maxLength": 200 }
  }
}
```

Output: structured `Task` JSON plus a JSON text content block.

### `tasks.get`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: structured `Task` JSON plus a JSON text content block.

### `research_runs.create`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["task_id", "goal_summary"],
  "properties": {
    "task_id": { "type": "string", "minLength": 1 },
    "goal_summary": { "type": "string", "minLength": 1, "maxLength": 500 }
  }
}
```

Output: structured `ResearchRun` metadata plus a JSON text content block.

### `research_runs.get`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: structured `ResearchRun` metadata plus a JSON text content block.

### `research_sources.create`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["research_run_id", "artifact_ref", "source_type", "summary"],
  "properties": {
    "research_run_id": { "type": "string", "minLength": 1 },
    "artifact_ref": { "type": "string", "minLength": 1 },
    "source_type": { "type": "string", "minLength": 1 },
    "summary": { "type": "string", "minLength": 1 }
  }
}
```

Output: structured redacted `ResearchSource` metadata plus a JSON text content block.

### `research_sources.get`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: structured redacted `ResearchSource` metadata plus a JSON text content block.

### `agent_runs.create`

Input schema: `project_id` plus optional `trace_id`, `task_id`, redacted `summary`, safe project-relative `changed_files`, verifier metadata, and artifact refs. If omitted, `trace_id` defaults to the generated run id.

Output: structured redacted `AgentRun` metadata plus a JSON text content block. The store rejects raw prompts, completions, source dumps, raw stderr, secrets, credentials, provider payloads, absolute roots, and PII.

### `agent_runs.step_append`

Input schema: `run_id`, `status` (`running`, `completed`, or `failed`), and optional `trace_id`, `tool_name`, `tool_category`, `failure_category`, redacted `notes`, safe `changed_files`, verifier metadata, and artifact refs. If omitted, `trace_id` inherits the parent run trace id. For verifier metadata, prefer `command` as the executable and `args` as flags/paths; simple space-separated words in `command` are normalized into args.

Output: updated structured redacted `AgentRun` metadata plus a JSON text content block.

### `agent_runs.promote_artifact`

Input schema: `run_id`, existing `artifact_ref`, optional `artifact_kind`, `state` (`candidate`, `validated`, `promoted`, or `rejected`), `source_ref`, optional `verifier_ref`, and optional bounded `decision`.

Output: updated structured redacted `AgentRun` metadata plus a JSON text content block.

Rules:

- `artifact_ref` must match an artifact already present on the run or one of its steps.
- `validated`, `promoted`, and `rejected` require `verifier_ref` and `decision`.
- `candidate` records evidence before validation and does not require a verifier.
- Refs must be safe local refs, not absolute roots or traversal paths.
- Raw prompts, completions, source dumps, raw stderr, secrets, credentials, provider payloads, roots, and PII are rejected.

### `agent_runs.complete`

Input schema: `run_id`, final `status` (`completed` or `failed`), and optional `failure_category`, redacted `summary`, safe `changed_files`, verifier metadata, and artifact refs.

Output: completed structured redacted `AgentRun` metadata plus a JSON text content block.

### `agent_runs.get`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: structured redacted `AgentRun` metadata plus a JSON text content block.

### `projects.list`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": [],
  "properties": {}
}
```

Output: configured local project metadata without root paths, include/exclude patterns, raw source content, or file-content hashes. Metadata may include safe configured aliases and auto-discovered aliases such as a Go module path; callers may pass either `id` or an alias to project-scoped tools.

### `projects.get`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: one configured local project metadata object without local root path exposure. The `id` field is the canonical configured project ID; `aliases` are accepted alternate IDs for agent convenience. Configure aliases explicitly when the runtime may not be able to read `go.mod` from a mounted project root.

### `projects.digest`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": [],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "project_id": { "type": "string", "minLength": 1 }
  }
}
```

Output: metadata-only digest run counts and status. Pass either `id` or `project_id`; `id` remains the preferred contract. The digest stores file metadata and metadata fingerprints only; raw source content and file-content hashes are not stored or returned.

### `projects.context_health`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: deterministic context readiness and freshness metadata for one project, including enabled/config state, latest run summary, safe counters, search-index health, and workspace git availability. `latest_run.chunks_stored` and `latest_run.symbols_stored` keep the same per-run delta semantics as `projects.ingestion_status_latest`; existing graph inventory is exposed through top-level `indexed_chunk_count` and `indexed_symbol_count`. Status `syncing` means ingestion is active or a bounded probe timed out under local load; `degraded` is reserved for explicit failed ingestion or degraded search-index state. It omits roots, source text, skipped sensitive content, raw errors, prompts, provider payloads, secrets, credentials, and PII.

### `projects.impact.analyze`

Input schema: `id`, optional safe project-relative `changed_paths`, optional `diff_scope` (`working_tree`, `staged`, or `head`), and optional `max_diff_bytes`.

Output: deterministic impact metadata with graph-backed source anchors from defining symbols, references, callers, and implementer edges; affected domains, REST routes, MCP tools, security flags, residual unknowns, and `partial`/`partial_reason` when index health prevents a complete answer. During active ingestion, graph fanout is skipped and the result is partial with `index_syncing`. When no paths are supplied, the tool may use governed workspace diff file metadata; it does not return raw diff content.

### `projects.context_pack.build`

Input schema: `id`, optional `query`, optional `path_prefix`, optional safe project-relative `changed_paths`, optional `diff_scope` (`working_tree`, `staged`, or `head`), optional `max_diff_bytes`, optional `max_items` capped at 25, optional `max_snippet_bytes`, and optional `include_impact`.

Output: bounded context pack with project ID, limits, text search hits, file metadata, symbol metadata, optional impact analysis, a manifest-only reproducibility record, warnings, partial flag, and explicit v1 limitations.

The manifest records normalized query/options, graph/search-index status, selected file/symbol/chunk IDs, file timestamps, warnings, limitations, and truncated redacted SHA-256 values over manifest metadata identifiers only. The tool composes existing local indexed context only. It does not create storage, call providers, return roots, return raw workspace diffs, include full chunk text, or include full source by default. Text hits include capped snippets and chunk metadata with empty `chunk.text`.

### `projects.claims.check`

Input schema: `id`, optional inline `documents`, optional `selected_paths`, optional known REST/MCP name overrides, and optional `include_verified`. Selected paths are limited to stable docs/contracts (`README.md`, `docs/`, `api/`, and `.ai/skills/mivia-mcp/SKILL.md`).

Output: summary counts plus actionable line-level claim findings for registered MCP tool names, registered REST route patterns, and forbidden `.ai/tasks/` links in stable docs. Verified findings are omitted by default and counted in `verified_omitted`; pass `include_verified: true` only for audit/debug output. The checker is deterministic and does not use LLM judgment, broad crawling, or document-content echoing.

### Evidence Graph Tools

Evidence Graph tools are project-scoped and metadata-only. They reject raw prompts, raw source dumps, provider payloads, secrets, roots, raw stderr, and PII. Use dotted tool names when available; underscore aliases are accepted for each tool listed below.

#### `projects.evidence_graph.claims.create`

Alias: `projects_evidence_graph_claims_create`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "claim_ref", "summary"],
  "properties": {
    "id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "run_id": { "type": "string", "maxLength": 200 },
    "trace_id": { "type": "string", "maxLength": 200 },
    "claim_ref": { "type": "string", "minLength": 1, "maxLength": 200 },
    "summary": { "type": "string", "minLength": 1, "maxLength": 500 },
    "status": { "type": "string", "enum": ["candidate", "validated", "promoted", "rejected"] }
  }
}
```

Output: structured `Claim` metadata plus a JSON text content block.

#### `projects.evidence_graph.claims.get`

Alias: `projects_evidence_graph_claims_get`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "claim_id"],
  "properties": {
    "id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "claim_id": { "type": "string", "minLength": 1, "maxLength": 200 }
  }
}
```

Output: structured `ClaimRecord` metadata containing the claim plus evidence, decisions, actions, outcomes, artifact links, and promotion links.

#### `projects.evidence_graph.claims.list`

Alias: `projects_evidence_graph_claims_list`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "artifact_ref": { "type": "string", "maxLength": 200 },
    "promotion_state": { "type": "string", "enum": ["candidate", "validated", "promoted", "rejected"] },
    "outcome_status": { "type": "string", "enum": ["passed", "failed", "blocked", "unknown"] },
    "run_id": { "type": "string", "maxLength": 200 },
    "trace_id": { "type": "string", "maxLength": 200 },
    "page_size": { "type": "integer", "minimum": 1, "maximum": 100 },
    "page_token": { "type": "string", "maxLength": 20 }
  }
}
```

Output: structured `ClaimList` metadata with `claims` and optional `next_page_token`. The default page size is 50 and the maximum is 100.

#### `projects.evidence_graph.evidence.append`

Alias: `projects_evidence_graph_evidence_append`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "claim_id", "evidence_ref", "evidence_kind"],
  "properties": {
    "id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "claim_id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "evidence_ref": { "type": "string", "minLength": 1, "maxLength": 200 },
    "evidence_kind": { "type": "string", "enum": ["context_pack", "file", "chunk", "symbol", "verifier", "claim_check", "artifact", "other"] },
    "source_ref": { "type": "string", "maxLength": 200 },
    "summary": { "type": "string", "maxLength": 500 }
  }
}
```

Output: structured `Evidence` metadata plus a JSON text content block.

#### `projects.evidence_graph.decisions.create`

Alias: `projects_evidence_graph_decisions_create`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "claim_id", "decision_ref", "state", "verifier_ref", "rationale"],
  "properties": {
    "id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "claim_id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "decision_ref": { "type": "string", "minLength": 1, "maxLength": 200 },
    "state": { "type": "string", "enum": ["validated", "promoted", "rejected"] },
    "verifier_ref": { "type": "string", "minLength": 1, "maxLength": 200 },
    "rationale": { "type": "string", "minLength": 1, "maxLength": 500 }
  }
}
```

Output: structured `Decision` metadata plus a JSON text content block.

#### `projects.evidence_graph.actions.create`

Alias: `projects_evidence_graph_actions_create`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "claim_id", "decision_id", "action_ref", "action_kind"],
  "properties": {
    "id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "claim_id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "decision_id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "action_ref": { "type": "string", "minLength": 1, "maxLength": 200 },
    "action_kind": { "type": "string", "enum": ["code_change", "doc_change", "verifier_run", "config_change", "review_comment", "other"] },
    "summary": { "type": "string", "maxLength": 500 },
    "changed_files": { "type": "array", "items": { "type": "string", "maxLength": 300 }, "maxItems": 100 },
    "run_id": { "type": "string", "maxLength": 200 }
  }
}
```

Output: structured `Action` metadata plus a JSON text content block.

#### `projects.evidence_graph.outcomes.create`

Alias: `projects_evidence_graph_outcomes_create`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "claim_id", "action_id", "outcome_ref", "outcome_kind", "status"],
  "properties": {
    "id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "claim_id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "action_id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "outcome_ref": { "type": "string", "minLength": 1, "maxLength": 200 },
    "outcome_kind": { "type": "string", "enum": ["test", "build", "claim_check", "manual_review", "promotion", "failure", "other"] },
    "status": { "type": "string", "enum": ["passed", "failed", "blocked", "unknown"] },
    "verifier_ref": { "type": "string", "maxLength": 200 },
    "summary": { "type": "string", "maxLength": 500 }
  }
}
```

Output: structured `Outcome` metadata plus a JSON text content block.

#### `projects.evidence_graph.artifacts.link`

Alias: `projects_evidence_graph_artifacts_link`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "claim_id", "artifact_ref"],
  "properties": {
    "id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "claim_id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "artifact_ref": { "type": "string", "minLength": 1, "maxLength": 200 },
    "artifact_kind": { "type": "string", "maxLength": 200 },
    "run_id": { "type": "string", "maxLength": 200 }
  }
}
```

Output: structured `ArtifactLink` metadata plus a JSON text content block.

#### `projects.evidence_graph.promotions.link`

Alias: `projects_evidence_graph_promotions_link`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "claim_id", "artifact_ref", "promotion_state", "source_ref"],
  "properties": {
    "id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "claim_id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "run_id": { "type": "string", "maxLength": 200 },
    "artifact_ref": { "type": "string", "minLength": 1, "maxLength": 200 },
    "promotion_state": { "type": "string", "enum": ["candidate", "validated", "promoted", "rejected"] },
    "source_ref": { "type": "string", "minLength": 1, "maxLength": 200 },
    "verifier_ref": { "type": "string", "maxLength": 200 },
    "decision_ref": { "type": "string", "maxLength": 200 },
    "action_ref": { "type": "string", "maxLength": 200 },
    "outcome_ref": { "type": "string", "maxLength": 200 }
  }
}
```

Output: structured `PromotionLink` metadata plus a JSON text content block. Non-candidate links require `verifier_ref` and `decision_ref`; promoted links require `outcome_ref` for a passed outcome.

### Knowledge Promotion Tools

Knowledge Promotion tools are metadata-only. They convert verified Evidence Graph and Confidence Engine conclusions into reusable project or org knowledge. Project-level promotion is the default. Org-level promotion is optional, stricter, explicit, and never automatic. Agents MUST treat promoted knowledge as guidance, not proof; they MUST revalidate current source/context before acting and MUST record reuse events. Stale or contradicted knowledge MUST be superseded, not deleted.

All Knowledge Promotion results return `structuredContent` plus a JSON text content block. Inputs and outputs must not contain raw prompts, raw completions, raw source dumps, raw stderr, provider payloads, secrets, roots, external URLs, or PII. Use dotted tool names when available; underscore aliases are accepted for every tool below.

Exact reuse sequence for agents:

1. MUST query project knowledge with `projects.knowledge.list` before planning in the current project.
2. MUST query org knowledge with `orgs.knowledge.list` before making cross-project claims.
3. Verify current source, context health, tests or other relevant runtime context before acting.
4. Record Evidence Graph metadata for any new conclusion.
5. Score confidence with `projects.confidence.claims.score`.
6. Promote only after the Evidence Graph, Confidence Engine, passed-outcome, verifier, project, and optional org gates pass.
7. Record a reuse event with `projects.knowledge.reuse_events.record`.

#### `projects.knowledge.candidates.create`

Alias: `projects_knowledge_candidates_create`

Input schema: `id`, `knowledge_ref`, `claim_id`, `claim_ref`, required `summary`, required `reuse_guidance`, and optional `confidence_assessment_id`, `confidence_score`, `confidence_band`, `evidence_refs`, `verifier_refs`, `outcome_refs`, `promotion_refs`, and `supersedes_ref`.

Output: structured `KnowledgeRecord` metadata plus a JSON text content block.

#### `projects.knowledge.validate`

Alias: `projects_knowledge_validate`

Input schema: `id`, `knowledge_id`, `decision_ref`, `verifier_ref`, and bounded 1000-character `rationale`.

Output: structured `KnowledgeRecord` metadata plus a JSON text content block.

#### `projects.knowledge.promote_project`

Alias: `projects_knowledge_promote_project`

Input schema: `id`, `knowledge_id`, `decision_ref`, `verifier_ref`, and bounded 1000-character `rationale`.

Output: structured `KnowledgeRecord` metadata plus a JSON text content block with `state=project_promoted` when the project promotion gate passes.

#### `projects.knowledge.submit_org_review`

Alias: `projects_knowledge_submit_org_review`

Input schema: `id`, `knowledge_id`, `org_ref` (`default`), `decision_ref`, `verifier_ref`, bounded 1000-character `rationale`, and safe `decided_by`.

Output: structured `KnowledgeRecord` metadata plus a JSON text content block with `state=org_review`. This is not org promotion.

#### `projects.knowledge.promote_org`

Alias: `projects_knowledge_promote_org`

Input schema: `id`, `knowledge_id`, explicit `scope=org`, `org_ref=default`, `decision_ref`, `verifier_ref`, bounded 1000-character `rationale`, and safe `decided_by`.

Output: structured `KnowledgeRecord` metadata plus a JSON text content block with `state=org_promoted` only after stricter org gates pass.

#### `projects.knowledge.reject`

Alias: `projects_knowledge_reject`

Input schema: `id`, `knowledge_id`, `decision_ref`, `verifier_ref`, bounded 1000-character `rationale`, and optional safe `decided_by`.

Output: structured rejected `KnowledgeRecord` metadata plus a JSON text content block.

#### `projects.knowledge.supersede`

Alias: `projects_knowledge_supersede`

Input schema: `id`, `knowledge_id`, `superseded_by_ref`, `decision_ref`, `verifier_ref`, bounded 1000-character `rationale`, and optional safe `decided_by`.

Output: structured superseded `KnowledgeRecord` metadata plus a JSON text content block. Use this for stale or contradicted promoted knowledge instead of deletion.

#### `projects.knowledge.reuse_events.record`

Alias: `projects_knowledge_reuse_events_record`

Input schema: `id`, `knowledge_id`, `reuse_ref`, `revalidated`, `outcome` (`used`, `skipped`, `stale`, or `contradicted`), and optional `agent_run_id`, `trace_id`, `revalidation_ref`, and bounded `summary`.

Output: structured `KnowledgeReuseEvent` metadata plus a JSON text content block.

#### `projects.knowledge.get`

Alias: `projects_knowledge_get`

Input schema: `id` and `knowledge_id`.

Output: one structured `KnowledgeRecord` metadata object plus a JSON text content block.

#### `projects.knowledge.list`

Alias: `projects_knowledge_list`

Input schema: `id` plus optional filters `scope`, `state`, `claim_id`, `knowledge_ref`, `confidence_band`, `min_confidence`, `max_confidence`, `page_size`, and `page_token`.

Output: structured metadata page with `knowledge` and optional `next_page_token` plus a JSON text content block.

#### `orgs.knowledge.list`

Alias: `orgs_knowledge_list`

Input schema: `org_ref=default` plus optional filters `state=org_promoted`, `claim_id`, `knowledge_ref`, `confidence_band`, `min_confidence`, `max_confidence`, `page_size`, and `page_token`.

Output: structured metadata page with org-promoted `knowledge` and optional `next_page_token` plus a JSON text content block. Org-wide non-promoted records are not exposed.

### Confidence Tools

Confidence tools are project-scoped and metadata-only. They score and retrieve deterministic confidence assessments for Evidence Graph claims. They do not store or return raw prompts, raw completions, raw source dumps, raw stderr, provider payloads, secrets, roots, external URLs, PII, raw graph traversal, raw request payloads, raw scoring internals, AI/provider scoring, embedding scoring, or vector scoring. Use dotted tool names when available; underscore aliases are accepted for each tool listed below.

#### `projects.confidence.claims.score`

Alias: `projects_confidence_claims_score`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "claim_id"],
  "properties": {
    "id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "claim_id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "changed_paths": { "type": "array", "items": { "type": "string", "maxLength": 300 }, "maxItems": 100 },
    "claim_check_paths": { "type": "array", "items": { "type": "string", "maxLength": 300 }, "maxItems": 20 },
    "include_verified": { "type": "boolean" }
  }
}
```

Output: structured `ScoreClaimResponse` metadata with an `assessment` object. The assessment includes `id`, `project_id`, `claim_id`, `claim_ref`, optional `run_id` and `trace_id`, `score` from 0 to 100, `band` (`high`, `medium`, `low`, or `unknown`), `recommendation` (`promote`, `verify`, `review`, `reject`, or `insufficient_evidence`), bounded `factors`, safe input counters, and timestamps.

#### `projects.confidence.claims.get`

Alias: `projects_confidence_claims_get`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "claim_id"],
  "properties": {
    "id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "claim_id": { "type": "string", "minLength": 1, "maxLength": 200 }
  }
}
```

Output: one stored metadata-only `ConfidenceAssessment` for the claim. It returns the same assessment shape as `projects.confidence.claims.score` and no raw evidence bodies or raw source.

#### `projects.confidence.claims.list`

Alias: `projects_confidence_claims_list`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1, "maxLength": 200 },
    "band": { "type": "string", "enum": ["high", "medium", "low", "unknown"] },
    "min_score": { "type": "integer", "minimum": 0, "maximum": 100 },
    "max_score": { "type": "integer", "minimum": 0, "maximum": 100 },
    "recommendation": { "type": "string", "enum": ["promote", "verify", "review", "reject", "insufficient_evidence"] },
    "run_id": { "type": "string", "maxLength": 200 },
    "trace_id": { "type": "string", "maxLength": 200 },
    "page_size": { "type": "integer", "minimum": 1, "maximum": 100 },
    "page_token": { "type": "string", "maxLength": 20 }
  }
}
```

Output: structured metadata with `assessments` and optional `next_page_token`. The default page size is 50 and the maximum is 100.

### Workspace Tools

Workspace tools are available only when `[workspace].enabled = true` and the target project has `workspace_mode = "read_only"` or `"edit"` with `digest_mode = "content_graph"`. Their `id` input is always a project id or safe alias returned by `projects.list` / `projects.get`; callers must not pass cwd, root, UNC, WSL, local filesystem paths, or workspace URIs as `id`. They never expose roots, datastore paths, raw command lines, raw stderr, content hashes, skipped sensitive content, secrets, PII, raw prompts, provider payloads, raw parser/SQLite/FTS errors, or stack traces.

- `projects.workspace.git_status` / `projects_workspace_git_status`: parsed git status with project `id`, optional `include_untracked`, project-relative `path_prefix`, `page_size`, and `page_token`. If Git is unavailable in the runtime, the tool fails explicitly with `git is not available in the mivia-server runtime`.
- `projects.workspace.git_diff` / `projects_workspace_git_diff`: capped safe diff with project `id`, optional `scope` (`working_tree`, `staged`, `head`), one optional file selector, project-relative `path_prefix`, `context_lines`, `max_diff_bytes`, and `page_token`. If Git is unavailable in the runtime, the tool fails explicitly with `git is not available in the mivia-server runtime`.
- `projects.workspace.file_read` / `projects_workspace_file_read`: current eligible file text by `file_id` or project-relative `relative_path`, with an opaque edit token. Requested `max_bytes` values above the server limit are accepted and clamped; check `text_truncated` for partial reads.
- `projects.workspace.file_edit` / `projects_workspace_file_edit`: `workspace_mode = "edit"` only; applies ordered exact byte-span edits with `edit_token`, `old_text`, and `new_text`. Successful non-dry-run edits queue path ingestion.
- `projects.workspace.file_create` / `projects_workspace_file_create`: `workspace_mode = "edit"` only; creates one eligible workspace text file by project-relative `relative_path`, `text`, optional `create_parent_dirs`, and optional `dry_run`. Successful non-dry-run creates queue path ingestion.
- `projects.workspace.file_delete` / `projects_workspace_file_delete`: `workspace_mode = "edit"` only; deletes one eligible workspace file by `file_id` or project-relative `relative_path` after validating `edit_token`, with optional `dry_run`. Successful non-dry-run deletes queue path ingestion.

No workspace tool executes arbitrary shell commands, accepts raw patches, or performs git commit, push, checkout, reset, branch, merge, rebase, stash, clean, or restore operations. Shell remains required for tests, builds, logs, process control, arbitrary commands, generated-file verification, and non-opted-in repositories.

### Project Integration Tools

Project integration tools are available for configured Jira Cloud and Confluence Cloud providers only. They are local, polling-backed, and use local SQLite/LadybugDB state. Status responses are redacted. Search/read responses return only locally ingested, bounded graph content and never call Atlassian or resolve credentials.

Integration status responses omit raw site URLs, raw allowlists, env var names, file paths, credentials, auth headers, local roots, raw provider payloads, and raw cursor values. Local rich-content search/read responses may include approved Jira/Confluence content and PII under [Project Integrations Security Policy](../../docs/security/project-integrations.md), but still omit credentials, auth headers, raw provider payload blobs, local roots, datastore paths, and credential refs.

### `projects.integrations.list`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: configured provider summaries for one local project, including provider name, enabled flag, auth mode, credential source type, allowlist kind/count, ingestion flag, and polling interval. Raw allowlist values and credential refs are not returned.

### `projects.integrations.status`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "provider"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "provider": { "type": "string", "enum": ["jira", "confluence"] }
  }
}
```

Output: redacted config-derived provider status plus local source/sync metadata when available. The response separates `coverage`, `sync_state`, `last_run`, and `active_run` so an empty incremental run does not imply zero local corpus coverage. Cursor presence may be reported as a boolean, but raw cursors are not returned.

### `projects.integrations.counts`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: local item counts for configured integration providers only. Counts are read-only, do not call Jira or Confluence, and do not prove remote provider totals.

### `projects.integrations.poll`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "provider"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "provider": { "type": "string", "enum": ["jira", "confluence"] },
    "kind": { "type": "string", "enum": ["initial_full", "incremental"] }
  }
}
```

Output: queued manual provider poll metadata with a `run_id`. The tool submits work through the local integration scheduler and returns quickly; poll `projects.integrations.poll_status` or `projects.integrations.status` before relying on newly indexed integration data. The background run uses configured env/file credential refs at execution time, but the response does not return credentials, credential refs, raw provider payloads, raw cursors, raw roots, or datastore paths.

### `projects.integrations.poll_status`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "provider", "run_id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "provider": { "type": "string", "enum": ["jira", "confluence"] },
    "run_id": { "type": "string", "minLength": 1 }
  }
}
```

Output: redacted local integration poll run metadata by run ID, plus redacted sync state when available. The response omits credentials, credential refs, raw site URLs, raw allowlists, raw provider payloads, raw cursors, raw roots, and datastore paths.

### `projects.integrations.search`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "query"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "provider": { "type": "string", "enum": ["jira", "confluence"] },
    "query": { "type": "string", "minLength": 1 },
    "max_results": { "type": "integer", "minimum": 1, "maximum": 50 },
    "max_snippet_bytes": { "type": "integer", "minimum": 1, "maximum": 4096 },
    "case_sensitive": { "type": "boolean" }
  }
}
```

Output: bounded local graph matches across locally ingested integration chunks. The response includes artifact/chunk metadata and capped snippets only. No remote provider call is made.

### `projects.jira.issue.get`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "key"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "key": { "type": "string", "minLength": 1 },
    "max_chunk_bytes": { "type": "integer", "minimum": 1, "maximum": 16384 },
    "max_chunks": { "type": "integer", "minimum": 1, "maximum": 200 },
    "chunk_offset": { "type": "integer", "minimum": 0 }
  }
}
```

Output: one locally ingested Jira issue artifact and bounded chunks by issue key. Chunk text is capped by `max_chunk_bytes`; returned chunk count defaults to 3 and is capped by `max_chunks`. If more chunks are available, `chunks_truncated` is true and `next_chunk_offset` can be passed as `chunk_offset` for the next local page. The tool reads local graph state only.

Recoverable local misses return an MCP tool result with `isError: true` and structured `reason` values: `bad_project_id`, `not_indexed`, `provider_unavailable`, or `bad_argument`. `id` is the Mivia project slug, not a Jira numeric issue ID. A `not_indexed` miss does not prove upstream absence; inspect `projects.integrations.status`, queue an explicit `projects.integrations.poll` only when approved, then check `projects.integrations.poll_status`.

### `projects.confluence.page.get`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "page_id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "page_id": { "type": "string", "minLength": 1 },
    "max_chunk_bytes": { "type": "integer", "minimum": 1, "maximum": 16384 },
    "max_chunks": { "type": "integer", "minimum": 1, "maximum": 200 },
    "chunk_offset": { "type": "integer", "minimum": 0 }
  }
}
```

Output: one locally ingested Confluence page artifact and bounded chunks by page ID. Chunk text is capped by `max_chunk_bytes`; returned chunk count defaults to 3 and is capped by `max_chunks`. If more chunks are available, `chunks_truncated` is true and `next_chunk_offset` can be passed as `chunk_offset` for the next local page. The tool reads local graph state only. Recoverable local misses use the same `isError: true` typed local-only error shape as Jira issue reads.

### `projects.ingest`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: queued manual `content_graph` ingestion run metadata for an opted-in local project. The tool submits work through the scheduler and returns quickly with a `run_id`; poll `projects.ingestion_status` or call `projects.ingestion_status_latest` before relying on indexed data. The response does not include absolute roots, source-content hashes, skipped sensitive content, matched sensitive text, secrets, PII, raw prompts, or provider payloads.

### `projects.search_index.rebuild`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: queued repair `content_graph` ingestion run metadata for the opted-in project. The tool submits local SQLite FTS repair through the scheduler, returns quickly with a `run_id`, and callers must poll `projects.ingestion_status` or call `projects.ingestion_status_latest` before relying on search results. The tool does not expose raw database queries, absolute roots, content hashes, skipped sensitive content, matched sensitive text, secrets, PII, raw prompts, provider payloads, or raw SQLite/FTS errors.

### `projects.ingestion_status`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "run_id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "run_id": { "type": "string", "minLength": 1 }
  }
}
```

Output: non-sensitive ingestion run metadata, including `run_kind` (`full_scan`, `delta`, or `search_index_rebuild`).

### `projects.ingestion_status_latest`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 }
  }
}
```

Output: latest meaningful non-sensitive ingestion run metadata. Zero-delta live heartbeat runs are skipped so they do not mask the last full scan; direct `projects.ingestion_status` still returns a heartbeat when called by `run_id`.

Output: latest non-sensitive ingestion run metadata for the project: run ID, status, trigger, counts, reason counts, timestamps, and error category only.

### `projects.files.list`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "status": { "type": "string", "enum": ["eligible", "skipped", "absent"] },
    "extension": { "type": "string", "minLength": 1 },
    "page_size": { "type": "integer", "minimum": 1, "maximum": 100 },
    "page_token": { "type": "string" }
  }
}
```

Output: bounded file metadata using stable opaque `file_id` values. `extension` accepts values with or without a leading dot, matches case-insensitively, and rejects whitespace or path separators. Sensitive skips return reason codes only and omit relative paths.

### `projects.files.get`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "file_id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "file_id": { "type": "string", "minLength": 1 }
  }
}
```

Output: bounded file metadata for one opaque file id. Safe relative paths include a normalized lowercase `extension` field. Sensitive skips omit relative paths, extensions, content hashes, skipped sensitive content, and matched sensitive text.

### `projects.file.chunks`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "file_id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "file_id": { "type": "string", "minLength": 1 },
    "page_size": { "type": "integer", "minimum": 1, "maximum": 100 },
    "page_token": { "type": "string" },
    "max_chunk_bytes": { "type": "integer", "minimum": 1 }
  }
}
```

Output: bounded eligible chunk text for one opaque file id. Skipped sensitive files have no chunks.

### `projects.symbols.list`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "page_size": { "type": "integer", "minimum": 1, "maximum": 100 },
    "page_token": { "type": "string" }
  }
}
```

Output: bounded symbol metadata for an opted-in content graph project. Supports `name_prefix`, `name_contains`, `extension`, `package`, `receiver`, and `case_sensitive`.

### `projects.search.text`

Input schema: `id`, required `query`, optional `mode` (`literal` only), `case_sensitive`, `extension`, `path_prefix`, `page_size`, `page_token`, `max_snippet_bytes`, and `max_matches`.

Output: deterministic paginated matches from eligible indexed `ContentChunk` nodes only. Each result includes safe file metadata, chunk location metadata with no full chunk text, match span, and a capped snippet. Results may be stale until ingestion catches up. Skipped, denied, sensitive, absent, and unindexed files are excluded.

### `projects.search.files`

Input schema: `id`, optional `extension`, `path_prefix`, `path_contains`, `case_sensitive`, `page_size`, and `page_token`.

Output: eligible indexed file metadata only. Absolute roots, skipped sensitive paths, content hashes, absent files, and denied files are not returned.

### `projects.search.symbols`

Input schema: `id`, optional `kind`, `name_prefix`, `name_contains`, `file_id`, `extension`, `package`, `receiver`, `case_sensitive`, `page_size`, and `page_token`.

Output: bounded symbol metadata from eligible indexed files. Use this before broad text pattern searches when symbol names are enough.

### `projects.search.references`

Input schema: `id`, optional `name_contains`, `target_name_contains`, `enclosing_contains`, `extension`, `path_prefix`, `resolution_status`, `confidence`, `case_sensitive`, `page_size`, and `page_token`.

Output: bounded indexed reference metadata. No source text, roots, content hashes, skipped sensitive content, raw parser errors, or raw datastore details are returned.

### `projects.search.calls`

Input schema: `id`, optional `name_contains`, `caller_name_contains`, `callee_name_contains`, `extension`, `path_prefix`, `resolution_status`, `confidence`, `case_sensitive`, `page_size`, and `page_token`.

Output: bounded indexed call metadata, including unresolved call nodes where available. No source text, roots, content hashes, skipped sensitive content, raw parser errors, or raw datastore details are returned.

### `projects.search.ast.queries`

Input schema: `id`.

Output: supported named AST query catalog entries for the project surface. Each entry includes query ID, language, supported capture names, query version, and matching file extensions. The response also includes safe per-language coverage counters scoped to `file_too_large`. Raw Tree-sitter query text is not returned, and raw Tree-sitter query syntax is not accepted by the search surface. Sensitive, denied, absent, parse-error, and other skipped files are not catalog inputs. Oversized files are represented only as safe coverage gaps through file/ingestion metadata such as `skipped_reason=file_too_large`, size, and reason counts; source text, chunks, snippets, content hashes, roots, skipped sensitive text, raw parser/SQLite/FTS/Tree-sitter errors, raw prompts, provider payloads, secrets, and PII are not returned.

### `projects.search.ast`

Input schema: `id`, required `language` (`go`, `python`, `javascript`, `jsx`, `typescript`, `tsx`, `csharp`, `dart`), required `query` named catalog id, optional `captures`, `extension`, `path_prefix`, `page_size`, `page_token`, `max_matches`, and `max_snippet_bytes`.

Named query ids: `function_declarations`, `class_declarations`, `type_declarations`, `call_expressions`, `imports`, `test_functions`, `assignments`, and `error_handling` where supported by the language. Dart also supports Flutter-specific `flutter_widgets` and `flutter_build_methods`.

Output: bounded capture results from eligible indexed chunks only, including safe file metadata, chunk location metadata without full chunk text, capture name/text, span, capped snippet, `query_language`, `query_version`, `result_truncated`, `coverage`, and search index metadata. Raw Tree-sitter query syntax is not accepted. Sensitive, denied, absent, parse-error, oversized, and other skipped files are unreachable from AST search. Oversized files may appear only as `file_too_large` coverage counts, file metadata with `skipped_reason=file_too_large`, and ingestion reason counts. Roots, content hashes, skipped sensitive text, raw parser/Tree-sitter errors, raw SQLite/FTS errors, raw prompts, provider payloads, secrets, and PII are not returned.

### `projects.symbol.source`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "symbol_id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "symbol_id": { "type": "string", "minLength": 1 },
    "max_source_bytes": { "type": "integer", "minimum": 1 }
  }
}
```

Output: bounded source text for one eligible indexed symbol. Text is derived only from eligible stored chunks and capped by `max_source_bytes` and project limits.

### `projects.symbol.references`

Input schema: `id`, `symbol_id`, optional `page_size`, and optional `page_token`.

Output: bounded reference metadata resolved to the requested symbol. Source text, roots, content hashes, skipped sensitive content, and matched sensitive text are not returned.

### `projects.symbol.callers`

Input schema: `id`, `symbol_id`, optional `page_size`, and optional `page_token`.

Output: bounded direct caller edges for the requested symbol.

### `projects.symbol.callees`

Input schema: `id`, `symbol_id`, optional `page_size`, and optional `page_token`.

Output: bounded direct callee edges for the requested symbol.

### `projects.symbol.call_graph`

Input schema: `id`, `symbol_id`, optional `direction` (`callers`, `callees`, `both`), optional `max_depth` (`1..5`), and optional `max_nodes` (`1..100`).

Output: bounded call graph nodes and edges with `resolution_status` and confidence metadata. Unresolved dynamic-language cases are represented as metadata, not guessed edges.

### `projects.headings.list`

Input schema: `id`, optional `file_id`, `page_size`, and `page_token`.

Output: bounded Markdown/document heading metadata without chunk text.

### `projects.file.outline`

Input schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "file_id"],
  "properties": {
    "id": { "type": "string", "minLength": 1 },
    "file_id": { "type": "string", "minLength": 1 },
    "kind": { "type": "string" },
    "name_prefix": { "type": "string" },
    "symbol_page_size": { "type": "integer", "minimum": 1, "maximum": 100 },
    "symbol_page_token": { "type": "string" },
    "include_chunk_text": { "type": "boolean" },
    "max_chunk_bytes": { "type": "integer", "minimum": 1 }
  }
}
```

Output: bounded file metadata, headings, symbols, symbol pagination token, and chunk IDs/line ranges. By default, outline chunks omit text. When `include_chunk_text` is true, outline chunks may include eligible stored chunk text truncated by `max_chunk_bytes` and project caps. Skipped sensitive files, matched sensitive text, raw AST node text, absolute roots, and raw local config values are never returned.

## Resources

Resource templates:

- `mivialabs://tasks/{id}`
- `mivialabs://research-runs/{id}`
- `mivialabs://research-sources/{id}`
- `mivialabs://agent-runs/{id}`
- `mivialabs://projects/{id}`
- `mivialabs://projects/{id}/digest-runs/{run_id}`
- `mivialabs://projects/{id}/files/{file_id}`
- `mivialabs://projects/{id}/files/{file_id}/chunks/{chunk_id}`
- `mivialabs://projects/{id}/files/{file_id}/outline`
- `mivialabs://projects/{id}/symbols/{symbol_id}`

`resources/read` returns `application/json` text content for the requested task, research-run, research-source, agent-run, project, project-digest-run, project-file, project-file-chunk, or project-symbol metadata.

## Codex Desktop Registration

Run the server locally, then register:

```powershell
codex mcp add mivia-server --url http://127.0.0.1:8080/mcp
codex mcp get mivia-server
```

Codex Desktop exposes the tools through generated callable names. In this environment, `tasks.create` appeared as `tasks_create` and was verified through native Codex MCP invocation.

## Security And Privacy Constraints

- No raw LadybugDB or SQLite query execution is exposed.
- Raw prompts, skipped sensitive source content, fetched provider payload blobs, secrets, tokens, and credentials are prohibited in requests, responses, fixtures, logs, and stores.
- Approved local Jira/Confluence rich content, including possible PII, is allowed only under [Project Integrations Security Policy](../../docs/security/project-integrations.md), in ignored local stores, through bounded local MCP search/read responses.
- Research-run create accepts only a redacted `goal_summary`; live provider execution and broad crawling are out of scope.
- Agent-run metadata stores only redacted execution metadata and rejects raw prompts, completions, source dumps, raw stderr, secrets, credentials, provider payloads, absolute roots, and PII.
- Project responses omit local root paths by default.
- Project responses may include safe aliases such as Go module paths; aliases are lookup IDs, not roots.
- Project responses include `graph_storage` as `persistent` or `in_memory`; they do not expose datastore paths.
- Project digest is manual and metadata-only; it does not store or return raw source content or file-content hashes.
- Content graph ingestion and query tools are localhost-only, manually triggered, bounded by pagination and chunk-byte caps, and use stable opaque IDs instead of absolute roots.
- Ingestion query responses must not return skipped sensitive content, matched sensitive-marker text, secrets, PII, raw prompts, provider payloads, raw database query results, or absolute roots.
