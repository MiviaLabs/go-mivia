# Codex Adapter

Codex agents should read `AGENTS.md`, then treat `.ai/INDEX.md` as the canonical repository instruction source.

This adapter exists only to document Codex-specific entry behavior. Do not duplicate policy here.

Expected flow:

1. Read `.ai/INDEX.md`.
2. Read the applicable `.ai/rules/` files.
3. Use local `.ai/skills/` for phase-scoped planning, implementation, review, and security checks.
4. Produce concise final handoffs with changed files, verification, residual risk, and required human review.
