# Research Data Handling

Status: Bootstrap policy
Date: 2026-05-30
Classification: Internal; PII-prohibited

## Current Boundary

Research and deep-research code may store metadata only:

- artifact reference or URL after sensitive query parameters are redacted
- retrieval timestamp
- content hash
- source type
- redacted summary
- policy metadata

Raw provider payloads, raw fetched content, raw prompts, credentials, tokens, secrets, personal data, and proprietary third-party content must not be stored in LadybugDB, SQLite, fixtures, logs, REST responses, MCP responses, traces, or metrics.

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
