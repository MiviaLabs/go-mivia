# Privacy Baseline

Status: Bootstrap policy
Date: 2026-05-30
Classification: Internal; PII-prohibited

## Position

PII and sensitive personal data ingestion is prohibited until the Security/DPO owner approves purpose, legal basis, access model, retention, deletion path, audit trail, and data residency posture.

## Current Data Classes

Allowed:

- task IDs
- task titles after basic PII/raw-query rejection
- task status
- research-run IDs
- redacted research goal summaries
- redacted source artifact references
- source type
- redacted research metadata hashes
- project metadata fingerprints
- policy metadata
- non-secret SQLite app settings and runtime flags

Prohibited:

- real secrets
- credentials and tokens
- raw prompts
- raw source content
- raw provider payloads
- personal data
- proprietary third-party content
- sensitive URL query values

## Controls

- Default HTTP bind is localhost-only.
- REST and MCP request bodies are decoded into explicit structs with unknown fields rejected.
- No raw database query endpoint exists.
- Research metadata is redacted before storage and response.
- Unit tests cover raw query rejection, obvious PII rejection, redaction, source deduplication, REST/MCP response redaction, and task state transitions.
- Logs must not include raw request bodies or sensitive values.

## Open Decisions

- Security/DPO owner
- engineering data owner
- retention period
- deletion workflow
- access model
- audit trail
- data residency and cross-border transfer posture
- provider terms and retention guarantees
