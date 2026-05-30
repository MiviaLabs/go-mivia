# Local Active Tasks

Store active task plans and in-progress phase notes here as local-only working files.

Per-task files in this directory are ignored work artifacts. Do not commit them and do not link them from technical docs. If a decision must survive, promote it to a stable README, ADR, API contract, runbook, security doc, or architecture doc.

File guidance:

- One task per Markdown file.
- Include status, owner assumptions, last verified date, phase scope, documentation impact, verification commands, and next-agent prompt.
- For documentation impact, list stable docs to update or write `None - reason`.
- Keep claims grounded in current repository state.
- Move completed local summaries to `.ai/tasks/done/` only when they remain useful locally.

Do not store:

- Secrets.
- Raw prompts.
- Raw fetched source content.
- PII or personal data.
- Provider tokens or credentials.
