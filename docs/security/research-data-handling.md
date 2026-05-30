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

Local project configuration is optional, local-only, and intended only for engineer local computers. Use `MIVIA_CONFIG_PATH` to point at an ignored local copy of `configs/agent-server.example.toml`. Local config must not contain secrets, tokens, PII, raw prompts, raw source content, provider payloads, or personal data.

REST and MCP project APIs return bounded project metadata only. They expose `GET /api/v1/projects`, `GET /api/v1/projects/{id}`, `POST /api/v1/projects/{id}/digest-runs`, `projects.list`, `projects.get`, and `projects.digest` on the localhost server; public exposure and auth models remain out of scope until separately approved.

Project digests follow the same metadata-only posture. They are manual and store project/file metadata plus metadata fingerprints only. The metadata fingerprint is derived from relative path, extension/language hint, file size, and mtime; it is not a file-content hash. Digest storage and REST/MCP responses must not store or return raw source content or file-content hashes.

## Content Graph Approval Gate

[ADR-0007](../adr/0007-content-graph-ingestion-and-live-updates.md) is proposed as an approval gate for future content graph ingestion and live updates. It does not relax the current source-content prohibition.

Until ADR-0007 is accepted with named engineering owner and Security/DPO owner approvals:

- no source content, chunk text, symbol text extracted from source, source-content hashes, file-version content state, or live watcher state may be stored in LadybugDB, SQLite, fixtures, logs, REST responses, MCP responses, traces, or metrics
- no provider, embedding, vector, crawling, public exposure, auth model, or symlink traversal work is approved
- only metadata-only/manual project digest behavior remains approved

If ADR-0007 is accepted later, this policy must be updated before any implementation stores source content or source-content hashes. PII ingestion remains prohibited unless separately approved by Security/DPO with purpose, legal basis, access model, retention, deletion path, audit trail, and data residency posture.

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

## Logging Rules

Logs may include service name, request ID, task ID, source ID, status, and error category.

Logs must not include raw request bodies, raw prompts, raw source content, provider payloads, secrets, tokens, credentials, personal data, or sensitive URL query values.
