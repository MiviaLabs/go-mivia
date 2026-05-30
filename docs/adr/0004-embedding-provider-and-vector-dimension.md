# ADR-0004: Embedding Provider And Vector Dimension

Status: Proposed; provider and dimension intentionally unselected
Date: 2026-05-30

## Context

The bootstrap graph store is LadybugDB and local app configuration is SQLite. Research and deep-research metadata are planned, but no AI provider, embedding provider, model family, retention behavior, external crawling policy, vector storage model, or vector dimension has been approved.

Selecting a provider or vector dimension now would create data-handling, cost, retention, and migration commitments before Security/DPO and engineering-owner review.

## Decision

Do not select an embedding provider or vector dimension in bootstrap.

- Provider: unselected.
- Embedding model: unselected.
- Vector dimension: unselected.
- Vector index/storage model: unselected.
- External provider retention posture: unapproved.
- Live provider integration: out of scope.

Task and research-run APIs may store only local metadata and redacted summaries. They must not store raw prompts, raw fetched content, provider payloads, credentials, tokens, secrets, or PII.

## Consequences

- No vector schema, vector index, pgvector, Neo4j vector feature, or embedding-specific field is added in Phase 5.
- Any future vector work requires an ADR covering provider choice, model family, dimension, cost, retention, deletion, access controls, auditability, and migration/backfill strategy.
- Tests must stay fixture-only and must not perform live provider calls.

## Required Owner Review

- Engineering owner approval for provider/model/dimension.
- Security/DPO approval for any personal-data processing, retention, deletion, access model, cross-border transfer, and audit trail.
- Legal or procurement review for provider terms, data retention, and enterprise licensing where applicable.
