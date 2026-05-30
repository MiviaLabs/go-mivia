# Tech Stack

- Planned runtime: Go 1.26.x with one root module `github.com/MiviaLabs/go-mivia`.
- Planned local data layer: PostgreSQL 18.4 with pgvector 0.8.2 and Neo4j 2026.05 Community through Docker Compose.
- Planned services: `api-gateway`, `research-worker`, and `knowledge-indexer`.
- Planned shared packages: configuration, logging, health/readiness, HTTP server, PostgreSQL, Neo4j, and migrations under `internal/platform/...`.
- Planned research packages: provider interfaces, web retrieval boundary, deep-research orchestration, redaction, and storage under `internal/research/...`.
- No production Kubernetes, Terraform, cloud deployment, UI, real secrets, paid provider wiring, or PII ingestion in the bootstrap unless explicitly approved.
- Embedding provider and vector dimension are owner/ADR decisions; do not invent them during scaffold work.
