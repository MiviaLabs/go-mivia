# Project Plan Skill

Use this skill to create or update a durable implementation plan for this repository.

Workflow:

1. Read `.ai/INDEX.md` and all relevant rules.
2. Inspect current repository files and committed state.
3. Separate verified facts from assumptions.
4. Identify affected security, privacy, data, and operational domains.
5. Break work into phases that can be implemented independently.
6. Add acceptance criteria, verification commands, residual risks, and owner decisions.
7. Include a copy-paste handoff prompt for each phase.

Output location:

- Use `.ai/tasks/active/` for active plans unless the user names another location.
- Move completed task summaries to `.ai/tasks/done/`.

Do not:

- Treat earlier plan text as source of truth without revalidating it.
- Select AI providers, embedding dimensions, production infrastructure, or PII policy without owner approval.
