---
name: mivialabs-agent-mcp
description: Use this repo-local skill when operating or documenting this repository's localhost MCP server tools and resources.
---

# MiviaLabs Agent MCP Router

This skill is local to this repository. Do not promote it to global Codex or Claude skill registries until the MCP contract and safe operating patterns are explicitly approved for promotion.

## Required Reads

Before using or documenting MCP workflows, read:

- `.ai/INDEX.md`
- `.ai/rules/00-operating-doctrine.md`
- `.ai/rules/05-external-systems.md`
- `.ai/rules/10-security-privacy.md`
- `.ai/rules/20-go-service-standards.md`
- `.ai/rules/30-docker-data.md`
- `api/mcp/agent-control.v1.md`
- `api/openapi/agent-control.v1.yaml` when REST/MCP behavior is being compared

Do not use Jira or Confluence for this repository unless the user explicitly overrides that constraint in the same request.

## Boundary

Use only the localhost `agent-server` MCP endpoint at `/mcp`. Stop before any workflow that would require public exposure, a new auth model, provider calls, embeddings, vectors, external crawling, symlink traversal, raw database queries, production deployment, or personal-data processing.

Never send or ask the MCP server to return secrets, credentials, tokens, PII, raw prompts, provider payloads, skipped sensitive content, matched sensitive text, absolute roots, datastore paths, or raw LadybugDB/SQLite query results.

## Routing

Task control:

- `tasks.create`
- `tasks.get`
- Resources: `mivialabs://tasks/{id}`

Research metadata, fixture/provider-disabled posture:

- `research_runs.create`
- `research_runs.get`
- `research_sources.create`
- `research_sources.get`
- Resources: `mivialabs://research-runs/{id}`, `mivialabs://research-sources/{id}`

Project registry and metadata-only digest:

- `projects.list`
- `projects.get`
- `projects.digest`
- Resources: `mivialabs://projects/{id}`, `mivialabs://projects/{id}/digest-runs/{run_id}`

Content graph ingestion and live-update metadata after matching approvals and local config gates:

- `projects.ingest`
- `projects.ingestion_status`
- `projects.files.list`
- `projects.file.chunks`
- `projects.symbols.list`
- Resources: `mivialabs://projects/{id}/files/{file_id}`, `mivialabs://projects/{id}/files/{file_id}/chunks/{chunk_id}`, `mivialabs://projects/{id}/symbols/{symbol_id}`

Codex may expose underscore aliases such as `projects_ingest`; both dotted and underscore names are accepted by the server.

## Stop Conditions

Stop and report the blocked condition when:

- The server is not bound to localhost or loopback.
- The request requires raw database query execution.
- A requested response would reveal absolute roots, datastore paths, skipped sensitive content, matched sensitive text, secrets, PII, raw prompts, or provider payloads.
- The project is not explicitly configured for the requested `metadata_only`, `content_graph`, or `live` behavior.
- The workflow requires external provider, embedding, vector, crawling, public exposure, auth, production, or PII approval not present in stable repo docs.

## Verification

For MCP behavior changes, verify the contract and implementation together:

- `api/mcp/agent-control.v1.md`
- `api/openapi/agent-control.v1.yaml`
- `internal/agentcontrol/mcpapi`
- `internal/projectregistry/mcpapi`
- `internal/projectregistry/httpapi`
- `internal/projectingestion`

Run the narrow verifier first, then broaden to `go test ./...` when behavior or contract surfaces changed.
