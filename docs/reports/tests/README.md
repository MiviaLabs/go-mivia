# Test Reports

Status: Current reporting convention
Date: 2026-05-30
Classification: Internal; PII-prohibited

Use this directory for short, evidence-backed reports from local agent experiments and verification runs.

## A/B Agent Comparison Reports

Save one Markdown report per experiment:

```text
docs/reports/tests/YYYY-MM-DD-agent-mcp-abtest-<task-slug>.md
```

Required sections:

- Objective and exact base commit.
- Task prompt summary, not raw prompt dumps.
- Worktree paths or branch names.
- Agent constraints: MCP-assisted vs non-MCP.
- Metrics: elapsed time, tool-call counts when available, files changed, diff stats, tests run, failures.
- Correctness check against acceptance criteria.
- Safety check: no absolute roots, raw DB queries, skipped sensitive content, matched sensitive text, secrets, PII, raw prompts, or provider payloads.
- First-pass quality assessment.
- Recommendation and follow-up changes.

Do not store secrets, raw prompts, provider payloads, personal data, or copied source chunks in reports.
