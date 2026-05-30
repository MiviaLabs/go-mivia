# Agent Workflow Index

This directory is the canonical, vendor-neutral operating surface for this repository.

Agent entrypoints:

- Read this file first.
- Then read every applicable rule in `.ai/rules/`.
- Use `.ai/skills/` for repeatable planning, implementation, review, and security workflows.
- Use `.ai/handoffs/` for durable phase handoffs.
- Use `.ai/tasks/` only as an ignored local planning workspace. Do not commit task plans or research plans, and do not link them from technical docs.
- Treat `AGENTS.md` and `CLAUDE.md` as thin adapters only.

Context-tool routing:

- Use the local MiviaLabs MCP server first for indexed repo context and opted-in workspace operations: project discovery, ingestion status, file discovery, file metadata, bounded chunks, outlines, text/file/symbol/reference/call search, symbol source, references, callers, callees, call graph, named AST query catalog/search, search-index repair status, governed git status/diff, current eligible file reads, token-guarded exact edits, and planning context exposed by the localhost content graph.
- Do not use Serena for indexed project discovery, symbol overview/listing, references, call sites, search, bounded source chunks, or planning context when MiviaLabs MCP is available and current.
- Use Serena only when MiviaLabs MCP is unavailable, stale, missing this project, or lacks the needed semantic operation; state that fallback explicitly.
- Use MCP workspace tools first for governed git status/diff, eligible current file reads, and exact edits when the project is opted in. Use shell commands for tests, builds, generated files, logs, process control, arbitrary commands, non-opted-in repos, and files not yet indexed or allowed by MCP.
- Do not guess between tools: if the question is about indexed code structure, search, source snippets, AST discovery, project graph state, or opted-in workspace status/diff/read/edit, start with MCP; if it is about tests, builds, logs, generated files, process control, arbitrary commands, or non-opted-in repos, use shell.
- If the MCP server is unavailable, state the gap and fall back to Serena plus shell only for the minimum evidence needed.

Source-of-truth order:

1. System, developer, and tool instructions.
2. This `.ai/` tree.
3. Root adapter files such as `AGENTS.md` and `CLAUDE.md`.
4. Immediate task prompt.

Required rule set:

- `.ai/rules/00-operating-doctrine.md`
- `.ai/rules/05-external-systems.md`
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
