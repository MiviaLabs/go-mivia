# Docker And Data

Local runtime:

- Use Docker Compose for local development only.
- No production Kubernetes, Terraform, or cloud deployment in the bootstrap phases.
- Pin image versions.
- Use healthchecks for database readiness.
- Services must depend on healthchecks, not container start alone.

PostgreSQL and pgvector:

- Use `pgvector/pgvector:0.8.2-pg18-trixie` for the local PostgreSQL 18 baseline.
- Mount the persistent PostgreSQL volume at `/var/lib/postgresql`.
- Create the `vector` extension through forward-only migrations or init SQL.
- Do not hardcode vector dimension until an ADR approves the embedding provider and dimension.

Neo4j:

- Use `neo4j:2026.05.0` Community by default.
- Enterprise edition requires explicit license approval before use.
- Store graph constraints in forward-only Cypher migrations.

Secrets:

- Commit `.env.example` and secret example files only.
- Do not commit real `.env` files or real secret material.
- Prefer Compose secrets files for local credentials.

Migrations:

- Forward-only.
- Idempotent for empty local developer databases when possible.
- No destructive drops, resets, or truncation in bootstrap migrations.
- Production-impacting migration strategy requires a separate ADR.
