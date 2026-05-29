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

Verification:

- Run the narrowest meaningful check first.
- Broaden verification when the change touches shared behavior or operational controls.
- If a tool is unavailable, report the exact missing tool and residual risk.

Final handoff:

- Changed files.
- Verification performed.
- Risks remaining.
- Required human review or owner decisions.
