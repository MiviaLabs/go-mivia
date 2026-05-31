# Operating Doctrine

Default posture:

- Be evidence-driven. Verify claims against files, commands, tests, or runtime output.
- Keep changes small, reviewable, and aligned with the current phase.
- Do not import conventions from unrelated repositories unless this repo explicitly adopts them.
- Preserve `.ai/` as the canonical workflow source.
- Keep root and vendor adapter files as pointers to `.ai/`.

Before editing:

- Read the current task, relevant `.ai/` rules, and affected files.
- Identify whether the change touches auth, authorization, tenancy, PII, secrets, migrations, public APIs, background jobs, observability, or external integrations.
- Stop and ask for owner confirmation when missing information changes security, privacy, cost, production behavior, or irreversible data impact.

Implementation boundaries:

- Follow the approved phase scope.
- Do not create later-phase files early.
- Do not mix infrastructure, service logic, and data-model work before the phase requires it.
- Use durable files for decisions, handoffs, and task status.

Commit policy:

- Use Conventional Commits for all new commits in this repository.
- Format: `<type>(<optional-scope>): <imperative summary>`.
- Allowed common types: `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `build`, `ci`, `chore`, `revert`.
- Keep the subject concise and aligned with the actual change; do not hide behavioral changes under `chore`.
- Use `BREAKING CHANGE:` in the commit body when compatibility is intentionally broken.
- Preserve existing commit messages during history-rewrite or identity-only repair unless the user explicitly asks to rewrite messages.

Verification:

- Run the narrowest meaningful check first.
- Broaden verification when the change touches shared behavior or operational controls.
- If a tool is unavailable, report the exact missing tool and residual risk.

Final handoff:

- Changed files.
- Verification performed.
- Risks remaining.
- Required human review or owner decisions.
