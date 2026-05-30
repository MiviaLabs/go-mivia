# Research Data Handling

Status: Bootstrap policy
Date: 2026-05-30
Classification: Internal; PII-prohibited

## Current Boundary

Research and deep-research code may store metadata only:

- artifact reference or URL after sensitive query parameters are redacted
- retrieval timestamp
- metadata hash over redacted metadata
- source type
- redacted summary
- policy metadata

Raw provider payloads, raw fetched content, raw prompts, credentials, tokens, secrets, personal data, and proprietary third-party content must not be stored in LadybugDB, SQLite, fixtures, logs, REST responses, MCP responses, traces, or metrics.

Local project configuration is optional, local-only, and intended only for engineer local computers. Use `MIVIA_CONFIG_PATH` to point at an ignored local copy of `configs/mivia-server.example.toml`. Local config must not contain secrets, tokens, PII, raw prompts, raw source content, provider payloads, or personal data.

REST and MCP project APIs return bounded project metadata only. They expose `GET /api/v1/projects`, `GET /api/v1/projects/{id}`, `POST /api/v1/projects/{id}/digest-runs`, `projects.list`, `projects.get`, and `projects.digest` on the localhost server; public exposure and auth models remain out of scope until separately approved.

Project digests follow the same metadata-only posture. They are manual and store project/file metadata plus metadata fingerprints only. The metadata fingerprint is derived from relative path, extension/language hint, file size, and mtime; it is not a file-content hash. Digest storage and REST/MCP responses must not store or return raw source content or file-content hashes.

## Content Graph Approval Gate

[ADR-0007](../adr/0007-content-graph-ingestion-and-live-updates.md) is accepted as the approval gate for future content graph ingestion and live updates. It approves a narrow local-source exception for explicitly opted-in `content_graph` projects only.

Within the accepted ADR-0007 boundary:

- local eligible project source content may be stored only for projects explicitly configured as `content_graph`
- storage is allowed only after path safety, symlink rejection, include/exclude matching, default denylist checks, size limits, binary/NUL rejection, UTF-8 validation, and sensitive-marker gates pass
- skipped sensitive files may be represented only by non-sensitive reason codes and, where needed, hash-only state that does not reveal skipped content
- source-content hashes may be stored only for content that is also stored after all gates pass
- the exception applies only to the localhost `mivia-server` on the developer machine

No provider, embedding, vector, crawling, public exposure, auth model change, symlink traversal, skipped sensitive content storage, matched sensitive-marker storage, or PII ingestion is approved by the content graph policy. PII ingestion remains prohibited unless separately approved by Security/DPO with purpose, legal basis, access model, retention, deletion path, audit trail, and data residency posture.

## Project Integration Exception

[Project Integrations Security Policy](project-integrations.md) records the approved local exception for configured Jira Cloud and Confluence Cloud rich-content and PII ingestion. That exception is not a research-data exception and does not approve raw provider payload storage, credential persistence, public exposure, embeddings, vectors, external crawling, or broader provider use.

REST and MCP content graph surfaces are localhost-only and bounded. They expose manual ingestion control, run status, file metadata, chunk metadata, and symbol metadata for opted-in projects. Responses use stable opaque IDs and must not expose absolute roots, datastore paths, skipped sensitive content, matched sensitive text, secrets, PII, raw prompts, provider payloads, or raw database query results.

Project graph storage may be configured per project as `persistent` or `in_memory`. Persistent graph data must stay in ignored local datastore files. Live watcher state is local-only, disabled by default, and allowed only when both global live updates and the project `content_graph/live` settings are enabled.

## Provider Policy

- Only fixture providers are implemented during bootstrap.
- Unit tests must not perform live network calls.
- Live browsing, crawling, paid AI providers, embedding providers, and external provider retention behavior require a new ADR plus Security/DPO and engineering-owner approval.

## Redaction Policy

Redaction must run before metadata is stored or returned. Current bootstrap redaction covers:

- emails
- phone-like values
- private-key blocks
- token, secret, password, and API-key assignments
- sensitive URL query parameters such as `token`, `api_key`, `signature`, `password`, and `secret`

This is not legal approval for PII processing. It is a defensive control for accidental input.

## Retention And Deletion Open Questions

Owner decisions still required before production or external-provider use:

- data owner
- purpose and legal basis
- retention period
- deletion path
- access model
- audit trail
- data residency and cross-border transfer posture
- provider terms and retention guarantees

The project-integrations phase-1 retention and deletion decision is recorded separately in [Project Integrations Security Policy](project-integrations.md): keep approved local Jira/Confluence content indefinitely in ignored local stores, with deletion by manual removal of ignored local datastore files for this local phase.

## Logging Rules

Logs may include service name, request ID, task ID, source ID, status, and error category.

Logs must not include raw request bodies, raw prompts, raw source content, provider payloads, secrets, tokens, credentials, personal data, or sensitive URL query values.
