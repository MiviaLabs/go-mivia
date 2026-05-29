# Task Completion

- Final response must include changed files, verification performed, remaining risk, and required human review if any.
- Run the narrowest meaningful verifier first, then broaden only when the touched boundary warrants it.
- If a tool is missing or cannot run, report the exact command/tool and residual risk.
- Do not commit, push, create PRs, transition Jira, or send Slack messages unless explicitly asked.
- For auth, tenancy, PII/PDPL, secrets, migrations, public APIs, external providers, background jobs, or production-impacting paths, include explicit risk review and negative-case tests where applicable.
- Update `.ai/tasks` or handoff artifacts when the phase requires durable handoff state.
