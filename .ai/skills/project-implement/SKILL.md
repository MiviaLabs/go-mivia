# Project Implement Skill

Use this skill to implement one approved phase from a plan.

Workflow:

1. Read `.ai/INDEX.md`, relevant rules, and the selected plan.
2. Confirm the selected phase and no-go scope.
3. Inspect current files before editing.
4. Implement only files named or clearly required by the selected phase.
5. Run the phase verifier.
6. Write a handoff summary with changed files, verification, residual risk, and next prompt.

No-go rules:

- Do not implement later phases early.
- Do not introduce real secrets.
- Do not wire live AI providers or live web access without an approved ADR.
- Do not add production deployment resources during bootstrap.

Stop conditions:

- Missing owner decision affects privacy, security, cost, licensing, or production behavior.
- Required tool is unavailable and the phase verifier cannot be completed.
- Existing uncommitted user changes conflict with the selected phase.
