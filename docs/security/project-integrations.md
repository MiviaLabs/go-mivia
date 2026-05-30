# Project Integrations Security Policy

Status: Approved local phase-1 policy
Date: 2026-05-31
Classification: Internal; local Atlassian rich-content and PII exception

## Scope

This policy covers project-scoped Jira Cloud and Confluence Cloud integrations for the localhost `agent-server`.

Approved providers:

- Jira Cloud
- Confluence Cloud

Out of scope:

- Jira Data Center and Confluence Data Center
- attachment download
- writes back to Atlassian
- generic Atlassian endpoint proxying
- public or non-loopback server exposure
- external AI provider, embedding, vector, crawling, or cloud storage use

Freshness is polling-only through manual or configured local scheduler runs.

## Owner Approval

The engineering owner approves local-only ingestion, local graph storage, local search, and bounded local MCP read access for configured Jira and Confluence project context.

Approved purpose:

- provide local agent project context
- support local planning, implementation, review, and search workflows

Approved data owner:

- engineering owner for this local project-integration phase

Approved legal and operating basis:

- owner-approved internal local processing on an engineer-controlled workstation
- configured project/provider allowlists only
- existing localhost-only REST/MCP boundary only

## Approved Data Classes

Jira data may include, when configured:

- issue ID and key
- project key
- summary
- description
- comments
- assignee and reporter account IDs and display names
- issue type, status, priority, labels, components, timestamps
- configured rich/custom fields from `allowed_fields`

Confluence data may include, when configured:

- page ID
- space key
- title
- configured page body representation
- comments
- labels
- configured properties
- author and owner account IDs and display names when available
- status, version, and timestamps

These fields may contain PII. This policy approves them only inside ignored local stores and bounded local MCP responses.

## Prohibited Data

Do not store or return:

- credentials
- API tokens
- raw auth headers
- raw credential file contents
- credential file paths in MCP/status/errors
- raw provider response payload blobs
- raw provider request/response bodies in logs
- local roots or datastore paths
- arbitrary unallowlisted Jira or Confluence fields
- content outside configured Jira `project_keys` or Confluence `space_keys`

Committed examples and fixtures must use placeholders only and must not contain real emails, tokens, credentials, Atlassian content, or provider payloads.

## Credential Handling

Credentials must be env/file references only.

Allowed local credential references:

- `email_env`
- `email_file`
- `api_token_env`
- `api_token_file`
- `credentials_file`

Credentials are resolved at call time. They must not be persisted in TOML examples, SQLite, LadybugDB, logs, MCP responses, fixtures, or docs.

## Access Model

Access is local-only:

- server bind address must remain loopback
- REST and MCP remain on the existing localhost boundary
- configured project IDs scope access
- Jira reads must stay inside configured `project_keys`
- Confluence reads must stay inside configured `space_keys`

No authentication model change is approved by this policy.

## Retention And Deletion

Phase 1 retention decision:

- keep locally ingested Jira and Confluence data indefinitely in ignored local stores
- no purge tool is required for this phase
- deletion path is manual removal of ignored local datastore files by the engineer/operator

Before any non-localhost exposure, shared deployment, or broader organizational use, retention and deletion must be re-reviewed and a first-class purge workflow must be designed.

## Audit Trail

The local audit trail consists of:

- integration source metadata
- sync run metadata
- sync state metadata
- item metadata
- graph artifact/chunk metadata
- MCP/tool logs that omit raw credentials, auth headers, provider payload bodies, local roots, and raw credential refs

Logs may include provider, project ID, operation, run ID, mode, HTTP status class, duration, item counts, and error category.

Logs must not include rich Jira/Confluence content, comments, page bodies, raw provider payloads, credentials, tokens, auth headers, emails, local roots, or file paths.

## Storage Controls

SQLite may store integration metadata, sync state, item identifiers, keys, cursors, hashes, status, timestamps, and run counters.

LadybugDB may store approved rich-content artifacts and bounded chunks for configured project/provider allowlists.

Storage must remain in ignored local datastore files. Do not commit local datastore files.

Provider payloads must be field-filtered and chunked before graph storage. Do not persist arbitrary provider JSON blobs.

## Response Controls

MCP search/read responses must be bounded and field-shaped.

Responses must enforce:

- project ID scope
- provider allowlists
- max result counts
- max body bytes
- max comment counts and comment body bytes when comments are enabled
- no credentials, auth headers, raw provider payloads, local roots, or credential refs

Status responses remain redacted and must not expose raw credential values, credential file paths, env var names, site URLs, or raw cursor values.

## Verification Requirements

Implementation phases that enable ingestion, graph writes, MCP search, or MCP reads must include focused tests for:

- allowlist enforcement
- env/file credential resolution without value/path leaks
- no live network calls in unit tests
- no raw credentials or auth headers in errors/log-shaped outputs
- no raw provider payload blob persistence
- bounded chunking and response shaping
- Jira/Confluence rich fields included only when configured
- disabled provider and disabled ingestion behavior

## Re-Review Triggers

Security/privacy re-review is required before:

- public or non-loopback exposure
- new auth model
- cloud deployment
- attachment ingestion
- writes to Jira or Confluence
- OAuth flow
- external AI provider, embedding, vector, crawling, or hosted search use
- broader retention/deletion requirements
- additional providers
