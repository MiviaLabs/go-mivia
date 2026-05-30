# Agent MCP Ingestion P2 Implementation Plan

Date: 2026-05-30

## Scope

Implement the P2 items from `docs/reports/tests/2026-05-30-agent-mcp-ingestion-performance-reliability-review.md` after P0 commit `6c21866`, P1 commit `f76d3fa`, and startup hotfix `f415f00`.

Jira: not checked by repo constraint.
Confluence: not checked by repo constraint.

## Source Evidence

- `projects.symbols.list` still accepted only pagination and called `Service.ListSymbols` without filters.
- `DocumentHeading` nodes were written during Markdown ingestion, but no MCP/REST list or file-outline flow exposed them.
- `projects.files.list` supported only status and extension filters, despite SQLite-backed state having enough safe metadata for path-prefix, present, skipped-reason, and modified-since filters.
- `parseEligible` extracted Go symbols and Markdown headings only; TS/JS, Dockerfile, Makefile, OpenAPI, SQL, YAML/TOML/JSON files had no semantic metadata.
- Live path-event workers logged `started` and then only logged completion after `IngestPath` returned, with no elapsed time or slow-task diagnostic while a large ingestion was still running.

## Implementation Plan

1. Strengthen safe discovery APIs.
   - Add file filters for safe path prefix, skipped reason, present status, and modified-since.
   - Add symbol filters for kind, name prefix, file id, extension, and package where practical.
   - Expose headings with `projects.headings.list`.
   - Add `projects.file.outline` returning file metadata, headings, symbols, and chunk IDs without chunk text.

2. Expand dependency-free semantic extraction.
   - Keep Markdown headings as first-class output.
   - Add lightweight TS/JS function/class/export extraction.
   - Add Dockerfile stages, Makefile targets, OpenAPI paths, SQL migration names, and YAML/TOML/JSON top-level keys.
   - Avoid new parser dependencies.

3. Improve live path-event diagnosability.
   - Add elapsed duration to path-event completion/failure logs.
   - Add bounded slow-task diagnostic for started path events that have not completed yet.
   - Keep logs hash-only for paths and do not emit roots or content.

## Acceptance Tests

- MCP symbols list filters by kind, prefix, file id, extension, and package.
- MCP headings list returns Markdown headings without chunk text.
- MCP file outline returns metadata, headings, symbols, and chunk IDs without chunk text.
- File list filters by path prefix, skipped reason, present status, and modified-since.
- TS/JS and infra/config extractors emit bounded symbols without new dependencies.
- Live path-event slow diagnostics are emitted without raw path/root/content leakage.
- P0/P1 behavior and required focused test gate remain green.
