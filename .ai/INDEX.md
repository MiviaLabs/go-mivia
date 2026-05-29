# Agent Workflow Index

This directory is the canonical, vendor-neutral operating surface for this repository.

Agent entrypoints:

- Read this file first.
- Then read every applicable rule in `.ai/rules/`.
- Use `.ai/skills/` for repeatable planning, implementation, review, and security workflows.
- Use `.ai/handoffs/` and `.ai/tasks/` for durable phase handoffs.
- Treat `AGENTS.md` and `CLAUDE.md` as thin adapters only.

Source-of-truth order:

1. System, developer, and tool instructions.
2. This `.ai/` tree.
3. Root adapter files such as `AGENTS.md` and `CLAUDE.md`.
4. Immediate task prompt.

Required rule set:

- `.ai/rules/00-operating-doctrine.md`
- `.ai/rules/10-security-privacy.md`
- `.ai/rules/20-go-service-standards.md`
- `.ai/rules/30-docker-data.md`

Provider adapter guidance:

- Do not hardcode one AI provider before an ADR approves the provider, model family, data handling, and retention posture.
- Keep provider-specific credentials, prompts, tokens, raw source content, and personal data out of logs, traces, metrics, fixtures, and committed files.
- Put provider-specific setup notes under `.ai/adapters/<provider>/` and keep policy text in `.ai/rules/`.

Phase discipline:

- Implement one approved phase at a time.
- Re-read the relevant rules before editing.
- Run the phase-specific verifier.
- Write a handoff summary that names changed files, verification, residual risk, and the next copy-paste prompt.
