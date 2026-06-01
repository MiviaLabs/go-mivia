# Docker And Data

Local runtime:

- Use Docker Compose for local development only.
- No production Kubernetes, Terraform, or cloud deployment in the bootstrap phases.
- Pin image versions.
- Use healthchecks for runtime dependencies when they are approved.
- Services must depend on healthchecks, not container start alone.

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
