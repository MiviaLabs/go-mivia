---
name: project-plan
description: Create or update repo-local implementation plans after reading .ai rules, verifying current source, scoping phases, documenting risks, and writing local handoff prompts.
---

# Project Plan Skill

Use this skill to create or update a workspace-local implementation plan for this repository.

Plans are working artifacts, not technical documentation. Do not commit task plans or research plans, and do not link them from stable tech docs. Durable decisions belong in README, docs, ADRs, API contracts, runbooks, security docs, or architecture docs.

Workflow:

1. Read `.ai/INDEX.md` and all relevant rules.
2. Inspect current repository files and committed state.
3. Separate verified facts from assumptions.
4. Identify affected security, privacy, data, and operational domains.
5. Add a `Documentation impact` decision: stable docs to update, new diagrams/docs needed, or `None - reason`.
6. Add Mermaid flow, sequence, or architecture diagrams when the task changes architecture, workflow, data flow, or user flow.
7. Break work into phases that can be implemented independently.
8. Add acceptance criteria, verification commands, residual risks, and owner decisions.
9. Include a copy-paste handoff prompt for each phase.

Output location:

- Use ignored local files under `.ai/tasks/active/` for active plans unless the user names another local location.
- Move completed task summaries to `.ai/tasks/done/` only when they remain useful locally.
- Do not include task plans or research plans in commits.

Do not:

- Treat earlier plan text as source of truth without revalidating it.
- Link task plans or research plans from README, architecture docs, ADRs, API docs, runbooks, or security docs.
- Select AI providers, embedding dimensions, production infrastructure, or PII policy without owner approval.
