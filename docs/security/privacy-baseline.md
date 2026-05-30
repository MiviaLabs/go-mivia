# Privacy Baseline

Status: Bootstrap policy
Date: 2026-05-30
Classification: Internal; PII-prohibited by default with approved local exceptions

## Position

PII and sensitive personal data ingestion is prohibited until the Security/DPO owner approves purpose, legal basis, access model, retention, deletion path, audit trail, and data residency posture.

Approved local exceptions are documented in stable security artifacts. The current approved exception is [Project Integrations Security Policy](project-integrations.md), covering configured local Jira Cloud and Confluence Cloud rich-content ingestion, local graph storage, local search, and bounded local MCP reads under the localhost-only server boundary.

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
- owner-approved Jira/Confluence integration metadata and rich content under [Project Integrations Security Policy](project-integrations.md)

Prohibited:

- real secrets
- credentials and tokens
- raw prompts
- raw source content
- raw provider payloads
- personal data
- proprietary third-party content
- sensitive URL query values

The personal-data prohibition remains the default. Jira/Confluence personal data is allowed only inside the explicitly approved local project-integrations boundary.

## Controls

- Default HTTP bind is localhost-only.
- REST and MCP request bodies are decoded into explicit structs with unknown fields rejected.
- No raw database query endpoint exists.
- Research metadata is redacted before storage and response.
- Unit tests cover raw query rejection, obvious PII rejection, redaction, source deduplication, REST/MCP response redaction, and task state transitions.
- Logs must not include raw request bodies or sensitive values.
- Local Jira/Confluence integration content is bounded to configured allowlists, ignored local stores, and local MCP responses; credentials and raw provider payload blobs remain prohibited.

## Open Decisions

- Security/DPO owner
- engineering data owner
- retention period outside the approved local project-integrations exception
- deletion workflow outside the approved local project-integrations exception
- access model outside the approved local project-integrations exception
- audit trail outside the approved local project-integrations exception
- data residency and cross-border transfer posture
- provider terms and retention guarantees
