# Documentation

Status: Bootstrap current-state
Date: 2026-06-01
Classification: Internal; PII-prohibited

This index points only to stable repository documentation. Local task plans and research plans are working artifacts; do not commit them and do not link them from technical docs.

## Map

- [System architecture](architecture/system-architecture.md): current local-only service shape, data flows, context packs, promotion gates, and operational boundaries.
- [Business overview](business-overview.md): non-technical value, current capability set, context-pack value, promotion-gate vocabulary, and local-only business boundary.
- [Agent context server guide](agent-context-guide.md): short guide for business stakeholders, engineers, and agents using Mivia REST/MCP, context packs, promotion gates, shell, and Serena fallback routing.
- [Automation runner operations](automation-runner.md): Docker and devcontainer runner user mapping, GitOps safety, and local git ownership cleanup.
- [ADRs](adr/): approved and proposed architecture decisions.
- [REST OpenAPI contract](../api/openapi/agent-control.v1.yaml): localhost REST contract under `/api/v1`.
- [MCP capability contract](../api/mcp/agent-control.v1.md): Streamable HTTP MCP contract under `/mcp`.
- [Local project configuration](configuration/local-projects.md): `MIVIA_CONFIG_PATH`, example config, graph storage selection, local project metadata APIs, manual digest, content graph ingestion, and live update boundaries.
- [Local development runbook](runbooks/local-dev.md): local verification, server startup, REST smoke, MCP smoke, manual ingestion, live mode, watcher troubleshooting, and local reset.
- [Release notes](releases/v0.2.0.md): current `v0.2.0` release notes for governed workflows and external automation runner support.
- [Test reports](reports/tests/README.md): convention for concise local verification and agent A/B experiment reports.
- [Incident runbook](runbooks/incident.md): bootstrap incident response notes.
- [Privacy baseline](security/privacy-baseline.md): default PII prohibition, local exceptions, and approval gates.
- [Project integrations security policy](security/project-integrations.md): approved local Jira/Confluence rich-content and PII handling boundary.
- [Research data handling](security/research-data-handling.md): allowed metadata, redaction boundary, and provider restrictions.
- [Research baseline notes](research/2026-05-30-platform-baseline.md): source-grounded platform notes, not a research plan.

## Documentation Rules

- Update README, architecture docs, ADRs, API contracts, runbooks, or security docs when source behavior changes.
- Add Mermaid flow or sequence diagrams when architecture, workflow, data flow, or user flow changes.
- Do not add secrets, credentials, tokens, raw prompts, raw fetched content, provider payloads, or personal data.
- Do not promote local task plans or research plans into stable docs by linking them.
