# Handoffs

Use this directory for durable cross-agent handoffs.

Required handoff fields:

- Task or phase name.
- Scope completed.
- Files changed.
- Verification performed.
- Residual risks and unavailable tools.
- Required owner review.
- Next recommended phase.
- Copy-paste prompt for the next agent.

Template:

```text
Continue <task or phase> in /home/mac/mivialabs/mivialabs-agents-monorepo.

Read .ai/INDEX.md first, then the relevant rules and task docs.

Completed:
- <facts only>

Changed files:
- <paths>

Verification:
- <commands and results>

Residual risk:
- <unverified items>

Next scope:
- <exact next phase>

No-go scope:
- <files or phases not to touch>
```
