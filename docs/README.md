# Documentation

Status: Bootstrap current-state
Date: 2026-05-30
Classification: Internal; PII-prohibited

This index points only to stable repository documentation. Local task plans and research plans are working artifacts; do not commit them and do not link them from technical docs.

## Map

- [System architecture](architecture/system-architecture.md): current local-only service shape, data flows, and operational boundaries.
- [ADRs](adr/): approved and proposed architecture decisions.
- [REST OpenAPI contract](../api/openapi/agent-control.v1.yaml): localhost REST contract under `/api/v1`.
- [MCP capability contract](../api/mcp/agent-control.v1.md): Streamable HTTP MCP contract under `/mcp`.
- [Local development runbook](runbooks/local-dev.md): local verification, server startup, REST smoke, and MCP smoke.
- [Incident runbook](runbooks/incident.md): bootstrap incident response notes.
- [Privacy baseline](security/privacy-baseline.md): current PII prohibition and approval gates.
- [Research data handling](security/research-data-handling.md): allowed metadata, redaction boundary, and provider restrictions.
- [Research baseline notes](research/2026-05-30-platform-baseline.md): source-grounded platform notes, not a research plan.

## Documentation Rules

- Update README, architecture docs, ADRs, API contracts, runbooks, or security docs when source behavior changes.
- Add Mermaid flow or sequence diagrams when architecture, workflow, data flow, or user flow changes.
- Do not add secrets, credentials, tokens, raw prompts, raw fetched content, provider payloads, or personal data.
- Do not promote local task plans or research plans into stable docs by linking them.
