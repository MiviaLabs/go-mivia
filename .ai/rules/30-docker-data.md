# Docker And Data

Local runtime:

- Use Docker Compose for local development only.
- No production Kubernetes, Terraform, or cloud deployment in the bootstrap phases.
- Pin image versions.
- Use healthchecks for runtime dependencies when they are approved.
- Services must depend on healthchecks, not container start alone.

Local container startup:

- Start local containers through `./scripts/mivia-compose-up`, not raw `docker compose up`, so the ignored `.docker-compose.local.yml` override is included and `MIVIA_AUTOMATION_UID` / `MIVIA_AUTOMATION_GID` are inferred from the WSL user.
- For project automation runs, `.docker-compose.local.yml` must mount `./configs/mivia-server.local.toml` to `/app/configs/mivia-server.local.toml` and `./configs/workflows` to `/app/configs/workflows:ro`; `configs/mivia-server.compose.toml` intentionally has no project roots and will make runners report no configured projects.
- Mount every enabled project root declared by `configs/mivia-server.local.toml` into both `mivia-server` and `mivia-automation-runner`; startup validation fails if any configured project root is missing inside the container.
- Mount the target project repository to `/workspace` for runner Codex execution. The `/workspace` bind must point at the same checkout as the configured target project root.
- Mount `CODEX_HOME` writable for runners. Do not mount `/home/mac/.codex` read-only: Codex CLI needs local runtime writes and fails before task execution with read-only filesystem errors.
- Set runner environment `MIVIA_CONFIG_PATH=/app/configs/mivia-server.local.toml`, `MIVIA_RUNTIME_HOME=<runtime-home>`, `CODEX_HOME=<runtime-home>/.codex`, and `MIVIA_AUTOMATION_PROJECT_ID=<target-project-id>` when the runner pool is intended to execute one project. An empty project id makes the runner discover all projects and may poll a different configured project than the intended target.
- Rebuild and recreate the server plus the required runner pool with:

```bash
./scripts/mivia-compose-up -d --force-recreate --scale mivia-automation-runner=6 mivia-server mivia-automation-runner
```

- Verify startup with `curl -fsS http://127.0.0.1:8080/healthz`, `curl -fsS http://127.0.0.1:8080/readyz`, `docker ps --filter name=mivia --format '{{.Names}} {{.Status}}'`, and a minimal in-runner Codex smoke command before starting a governed pipeline.
- Treat runner log lines as hard failures until fixed: `codex_config_missing`, `project discovery returned no configured projects`, `load workflow definition failed`, and Codex `Read-only file system` initialization errors.

Database posture:

- Use the internal Ladybug graph abstraction for graph persistence. Durable content-graph storage uses the lazy-opened Pebble-backed Ladybug implementation behind the existing `graph_storage = "persistent"` setting.
- Use SQLite as the local app-configuration store.
- Do not configure PostgreSQL, pgvector, Neo4j, Docker Compose database services, database secret files, or database volumes during bootstrap.
- Store local graph database files under a configurable path such as `MIVIA_LADYBUG_PATH`, defaulting to an ignored local `data/` directory.
- Store SQLite configuration data under a configurable path such as `MIVIA_SQLITE_PATH`, defaulting to an ignored local `data/` directory.
- Use in-memory LadybugDB for unit tests where possible.
- Use in-memory SQLite for unit tests where possible.
- Model schema changes as idempotent bootstrap queries; no destructive resets, truncation, or drop-and-recreate flows.
- Do not hardcode vector dimension until an ADR approves the embedding provider, storage model, and dimension.

Ladybug runtime:

- Do not expose Pebble, JSONL, native LadybugDB, or SQLite raw query execution over REST or MCP.
- Do not commit `lib-ladybug/`, Pebble data directories, local database files, generated runtime artifacts, or seed data.
- Do not store PII, raw prompts, raw fetched content, credentials, tokens, or personal data in database fixtures.
- No migration from legacy JSONL graph files or legacy project search SQLite files is supported for the Pebble-backed content graph epoch. Stop the server, delete ignored local graph/search files if desired, and run full reingestion.

SQLite runtime:

- SQLite is for app settings, runtime metadata, feature/config flags, and non-secret local configuration only.
- Do not store real secrets, credentials, tokens, raw prompts, raw fetched content, or personal data in SQLite.
- Keep SQLite schema bootstrap idempotent and forward-only.

HTTP interfaces:

- Expose REST APIs under `/api/v1`.
- Expose MCP Streamable HTTP under `/mcp`.
- Do not expose a raw database query endpoint over REST or MCP.
- Validate request size, content type, origin, and authentication before accepting remote MCP or REST traffic.

Secrets:

- Commit `.env.example` and secret example files only.
- Do not commit real `.env` files or real secret material.
- Prefer explicit local secret files for approved local runtime credentials.

Migrations:

- Forward-only.
- Idempotent for empty local developer databases when possible.
- No destructive drops, resets, or truncation in bootstrap migrations.
- Production-impacting migration strategy requires a separate ADR.
