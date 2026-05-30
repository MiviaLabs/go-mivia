# Project Implement Skill

Use this skill to implement one approved phase from a plan.

Workflow:

1. Read `.ai/INDEX.md`, relevant rules, and the selected plan.
2. Confirm the selected phase and no-go scope.
3. Inspect current files before editing.
4. Revalidate the plan's documentation impact against current source.
5. Implement only files named or clearly required by the selected phase.
6. Update stable docs where feasible, or record `None - reason` in the handoff.
7. Run the phase verifier.
8. Write a handoff summary with changed files, docs changed, docs intentionally not changed, verification, residual risk, and next prompt.

No-go rules:

- Do not implement later phases early.
- Do not introduce real secrets.
- Do not commit task plans or research plans.
- Do not link task plans or research plans from stable technical docs.
- Do not wire live AI providers or live web access without an approved ADR.
- Do not add production deployment resources during bootstrap.

Stop conditions:

- Missing owner decision affects privacy, security, cost, licensing, or production behavior.
- Required tool is unavailable and the phase verifier cannot be completed.
- Existing uncommitted user changes conflict with the selected phase.
