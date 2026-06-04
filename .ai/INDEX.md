# Agent Workflow Index

This directory is the canonical, vendor-neutral operating surface for this repository.

Repository identity: `github.com/MiviaLabs/go-mivia`.
Product name: Mivia (`mivia` in command, path, and service names).
Server binary/service name: `mivia-server`.

Agent entrypoints:

- Read this file first.
- Then read every applicable rule in `.ai/rules/`.
- Use `.ai/skills/` for repeatable planning, implementation, review, and security workflows.
- Use `.ai/skills/mivia-mcp/SKILL.md` for the local Mivia MCP workflow and keep global Codex/Claude copies synchronized from that source.
- Use `.ai/handoffs/` for durable phase handoffs.
- Use `.ai/tasks/` only as an ignored local planning workspace. Do not commit task plans or research plans, and do not link them from technical docs.
- Treat `AGENTS.md` and `CLAUDE.md` as thin adapters only.

Context-tool routing:

- Use the local Mivia MCP server first for indexed repo context and opted-in workspace operations: project discovery, ingestion status, file discovery, file metadata, bounded chunks, outlines, text/file/symbol/reference/call search, symbol source, references, callers, callees, call graph, named AST query catalog/search, search-index repair status, redacted ingestion diagnostics, governed git status/diff, current eligible file reads, token-guarded exact edits, local Jira/Confluence integration metadata/status/counts/search/read under `.ai/rules/05-external-systems.md`, and planning context exposed by the localhost content graph.
- Do not use Serena for indexed project discovery, repo-instruction discovery, symbol overview/listing, references, call sites, search, bounded source chunks, or planning context when Mivia MCP is available and current.
- Use Serena only when Mivia MCP is unavailable, stale, missing this project, or lacks the needed semantic operation; state that fallback explicitly. Do not initialize or read Serena instructions unless actually taking that fallback.
- Use MCP workspace tools first for governed git status/diff and eligible current file reads when the project is opted in with `workspace_mode = "read_only"` or `"edit"`. For existing eligible text files in `edit` mode, use `projects.workspace.file_read` then `projects.workspace.file_edit` or `projects.workspace.file_delete` before shell, `apply_patch`, or manual edits. For new eligible text files, use `projects.workspace.file_create`. These tools are not recursive delete, arbitrary patch, arbitrary shell, or shell-replacement surfaces. Treat read maxes as caps that may truncate returned text; truncation is a reason to page, narrow, or re-read through MCP, not to bypass it. Use shell for tests, builds, generated files, logs, process control, arbitrary commands, non-opted-in repos, and files not yet indexed or allowed by MCP.
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

Adapter guidance:

- `.ai/adapters/codex/` and `.ai/adapters/claude/` are agent-client entrypoint notes only. Do not duplicate policy there.
- Do not hardcode one AI provider before an ADR approves the provider, model family, data handling, and retention posture.
- Keep provider-specific credentials, prompts, tokens, raw source content, and personal data out of logs, traces, metrics, fixtures, and committed files.
- If provider-specific setup is approved, put it under a provider-named path such as `.ai/adapters/providers/<provider>/` and keep policy text in `.ai/rules/`.

Phase discipline:

- Implement one approved phase at a time.
- Re-read the relevant rules before editing.
- For any write-capable Work Plan, use dedicated worktree isolation by default. `isolation_mode=shared` is only for read-only planning or inspection; implementation, docs, config, generated-file, automation, and test writes require `dedicated_worktree` when MCP workspace git support is available.
- Run the phase-specific verifier.
- Write a handoff summary that names changed files, verification, residual risk, and the next copy-paste prompt.
