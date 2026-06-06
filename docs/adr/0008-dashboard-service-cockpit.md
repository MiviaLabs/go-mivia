# ADR-0008: Dashboard Service Cockpit

Status: Proposed
Date: 2026-06-06
Classification: Internal; PII-prohibited

## Context

`mivia-server` already exposes a local dashboard shell under `/dashboard` and a broad REST surface under `/api/v1`. The current dashboard assets are served by the same server process, use same-origin requests, and set a restrictive content security policy through `internal/dashboard/httpapi`.

The current API surface includes both read-only status and metadata routes and mutating workflow, automation, evidence, confidence, knowledge, ingestion, workspace, task, and research routes. The dashboard needs a stable product boundary before it is treated as a first-release service cockpit.

Existing ADRs require the server to remain localhost-only by default. No production deployment, public API exposure, remote auth model, PII processing, provider-backed execution, embeddings, vectors, raw database query surface, or raw source dump surface is approved for the dashboard.

## Decision

Define the first-release dashboard as a local, read-only service cockpit for `mivia-server`.

- Serve the cockpit from the existing `mivia-server` process under `/dashboard`.
- Keep the dashboard same-origin with REST under `/api/v1` and MCP under `/mcp`.
- Keep the default binding localhost or loopback only.
- Use read-only REST `GET` routes and the project-scoped agent activity stream for first-release UI state.
- Prefer bounded aggregate and metadata routes for primary views, especially project list, project details, dashboard summary, context health, latest ingestion status, work plan/task metadata, automation run metadata, evidence graph metadata, confidence metadata, knowledge metadata, workflow metadata, workspace git summary, and locally indexed integration metadata.
- Keep raw roots, datastore paths, raw diffs, raw prompts, raw source dumps, provider payloads, secrets, credentials, tokens, skipped sensitive content, and PII out of dashboard responses and persisted dashboard state.
- Keep local Jira and Confluence content read-only and already-ingested only. The dashboard must not call live Jira or Confluence connectors or trigger provider polling unless a later ADR and repo-rule override approve that behavior.

Mutating API routes are not first-release dashboard scope. This includes workflow compile/import/status changes, confidence scoring, knowledge reuse recording or promotion, evidence graph writes, Work Plan and Work Task lifecycle transitions, automation submission or runner control, ingestion and search-index triggers, workspace file edits or worktree creation, task/research creation, and agent-run writes.

If a future cockpit release needs controlled actions, it requires a follow-up decision covering UX safeguards, authentication and authorization, origin policy, rate limits, audit logging, replay/idempotency behavior, and explicit route-by-route scope.

## Read-Only UI Scope

The first-release cockpit may present:

- Project inventory and validation metadata.
- Context health, latest ingestion run metadata, and dashboard summary aggregates.
- Work Plan, Work Task, automation run, workflow, permission snapshot, evidence graph, confidence, and knowledge metadata.
- Workspace git status and bounded diff summary metadata where the project workspace is opted in.
- Locally indexed integration item metadata, search results, and bounded indexed previews.
- Project-scoped agent activity stream events after dashboard-side redaction and truncation safeguards.

The first-release cockpit must not present:

- Buttons or forms that mutate workflow, task, automation, evidence, confidence, knowledge, ingestion, workspace, task, research, or agent-run state.
- Raw database query, raw patch upload, arbitrary shell, git commit/push/checkout/reset/branch/merge/rebase/stash/clean/restore, provider polling, crawling, embedding, vector, or public exposure controls.
- Raw source dumps, raw prompts, raw provider payloads, secrets, credentials, tokens, skipped sensitive content, or personal data.

## Security Boundary

The dashboard inherits the server boundary from ADR-0003 and ADR-0007:

- Localhost or loopback binding remains mandatory until authn/authz, origin policy, rate limits, audit logging, monitoring, incident response, and on-call coverage are approved.
- The dashboard is internal and PII-prohibited.
- Same-origin `connect-src 'self'` remains the expected browser connection boundary.
- Server handlers remain responsible for method checks, content-type checks, request-size limits, origin validation where browser-capable clients are accepted, and response redaction.
- Dashboard code must treat agent activity payload details as sensitive local-debug material and render only bounded safe summaries by default.

## Consequences

- The dashboard can become the default local operator cockpit without adding a second service or a new public surface.
- Existing mutating dashboard affordances, if present in assets, are experimental and must be removed, hidden, or gated before this ADR is accepted as first-release scope.
- Route and OpenAPI updates that add or remove dashboard-consumed routes must keep read-only and mutating capabilities clearly separated.
- Public hosting, remote team access, production deployment, and authenticated multi-user operation remain out of scope.

## Verification

Before accepting this ADR or treating the dashboard as first-release scope:

- Confirm stable docs contain no links to ignored local task-planning files.
- Confirm the current dashboard route inventory separates read-only and mutating routes.
- Confirm dashboard UI code does not expose first-release mutating actions.
- Run focused server and dashboard package tests for the changed surface.
- Confirm the localhost-only boundary has not been weakened.

## References

- `docs/adr/0003-mivia-server-rest-and-mcp-boundary.md`
- `docs/adr/0007-content-graph-ingestion-and-live-updates.md`
- `docs/architecture/system-architecture.md`
- `api/openapi/agent-control.v1.yaml`
- `internal/dashboard/httpapi`
- `cmd/mivia-server`
