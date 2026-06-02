# External Systems

Default constraint:

- Do not use live Jira or Confluence connectors for this repository.
- Do not search, read, write, update, comment on, or link live Jira or Confluence artifacts while working in this repo.
- Use local source, tests, contracts, config, committed docs, logs, and runtime evidence instead.

Local MCP exception:

- Mivia MCP local integration tools may list provider status/counts and search/read already-ingested local Jira/Confluence graph content when `.ai/INDEX.md`, `.ai/skills/mivia-mcp/SKILL.md`, or the immediate task explicitly routes to that local MCP context.
- Local integration search/read is not a live connector override. It must not call Atlassian, resolve credentials, write remote artifacts, or prove upstream absence.
- A local miss or zero count means only that the local graph has no matching ingested item.

Override:

- The user may explicitly override live Jira/Confluence constraints in the same request.
- An older plan, task file, README, ADR, or external summary is not an override.

Planning:

- Plans must state `Jira: not checked by repo constraint` and `Confluence: not checked by repo constraint` when references are listed.
