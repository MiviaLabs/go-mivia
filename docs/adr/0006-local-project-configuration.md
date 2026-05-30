# ADR-0006: Local Project Configuration

Status: Proposed
Date: 2026-05-30

## Context

The local `mivia-server` previously loaded runtime settings only from environment variables. Future project ingestion needs a typed local configuration surface before any registry, REST/MCP project routes, graph digest, provider, embedding, vector storage, public exposure, auth model, live crawling, or file watcher is added.

Local config remains internal, localhost-only, and PII-prohibited. Config files must not contain credentials, tokens, raw prompts, raw source content, provider payloads, or personal data.

## Decision

Use TOML for the local server config file.

- Default local path: `configs/mivia-server.local.toml`.
- Explicit override: `MIVIA_CONFIG_PATH`.
- Committed example: [configs/mivia-server.example.toml](../../configs/mivia-server.example.toml).
- User guide: [docs/configuration/local-projects.md](../configuration/local-projects.md).

Use `github.com/pelletier/go-toml/v2` as the TOML parser. It is a small, Go-native dependency and supports strict decoding with unknown-field rejection. Do not vendor a parser and do not add a custom partial TOML parser.

JSON remains the no-new-dependency fallback if the TOML dependency is later rejected by the engineering owner. YAML is rejected for this bootstrap path because implicit typing, anchors, and broader parser behavior are unnecessary for local path configuration.

## Consequences

- `MIVIA_CONFIG_PATH` set to a missing or invalid config is fatal.
- If `MIVIA_CONFIG_PATH` is unset and `configs/mivia-server.local.toml` is absent, the server keeps current environment-only defaults and an empty project list.
- Environment variables remain final overrides over file values.
- The parser rejects unknown config sections and project fields.
- Durations use Go duration strings such as `10s`.
- Shell variables are not expanded inside config values.
- Phase 1 only loads config; it does not add project routes, graph digest, providers, embeddings, vector storage, public exposure, auth model, live crawling, or file watching.

## Required Owner Review

- Engineering owner review if the TOML parser dependency should be replaced with JSON-only config.
- Security/DPO review before any future project ingestion can process personal data.
- Engineering owner and Security/DPO review before exposing project config beyond localhost or adding provider-backed digest behavior.
