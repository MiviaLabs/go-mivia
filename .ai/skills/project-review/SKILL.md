# Project Review Skill

Use this skill to review repository changes for correctness, maintainability, and phase compliance.

Workflow:

1. Read `.ai/INDEX.md` and relevant rules.
2. Identify the exact diff or commit range under review.
3. Compare changes against the selected plan phase and acceptance criteria.
4. Prioritize confirmed bugs, regressions, security/privacy issues, migration hazards, and missing verification.
5. Report findings with file and line references.

Report shape:

- Findings first, ordered by severity.
- Open questions only when they block validation.
- Summary second.
- Verification gaps last.

Do not:

- Report speculative issues as confirmed.
- Review unrelated files unless they affect the change.
- Accept policy duplication in root adapter files when `.ai/` should be canonical.
