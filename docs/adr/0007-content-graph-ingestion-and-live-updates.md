# ADR-0007: Content Graph Ingestion And Live Updates

Status: Proposed; approval gate only
Date: 2026-05-30
Classification: Internal; PII-prohibited

## Context

Current local project digest is manual and metadata-only. It stores relative path metadata, file size, mtime, language hint, and metadata fingerprints. It does not store raw source content or file-content hashes.

Future content graph work would require a narrow exception to the current source-content prohibition so local agents can answer codebase questions from eligible project files. That exception has security, privacy, retention, deletion, and operational consequences, so it must be explicitly approved before implementation stores source content, source-derived text, source-content hashes, or live watcher state.

This ADR does not approve source-content storage. It defines the approval gate and the exact exception requested.

## Decision

Create a hard approval gate for any future `content_graph` ingestion or `live` update behavior.

No implementation may store source content, chunk text, symbol text extracted from source, source-content hashes, file-version content state, or live watcher state until all of these are true:

- This ADR status is changed to `Accepted`.
- A named engineering owner approves the local-source exception.
- A named Security/DPO owner approves the security and privacy posture.
- `docs/security/research-data-handling.md` is updated in the same or earlier approved change to reflect the accepted exception.
- Verification proves the implementation preserves the permanent prohibitions in this ADR.

The requested exception, if accepted, is narrow:

- Local eligible project source content may be stored only for projects explicitly configured as `content_graph`.
- Storage is allowed only after path safety, symlink rejection, include/exclude matching, default denylist checks, size limits, binary/NUL rejection, UTF-8 validation, and sensitive-marker gates pass.
- Skipped sensitive files may be represented only by non-sensitive reason codes and, where needed, hash-only state that does not reveal the skipped path or content.
- The exception applies only to the localhost `agent-server` on the developer machine.

Permanent prohibitions:

- No PII or personal-data ingestion without separate Security/DPO approval covering purpose, legal basis, access model, retention, deletion path, audit trail, and data residency.
- No secrets, credentials, tokens, raw prompts, provider payloads, skipped sensitive content, or matched sensitive-marker text in stores, logs, traces, metrics, fixtures, REST responses, or MCP responses.
- No AI providers, browsing providers, embedding providers, vector indexes, vector dimensions, pgvector, Neo4j vector features, or provider retention behavior.
- No external crawling, public exposure, production deployment, or new remote auth model.
- No symlink traversal.
- No raw LadybugDB or SQLite query endpoint.

## Data Handling Boundary

Data owner: the named engineering owner recorded when this ADR is accepted.

Purpose: local codebase navigation and retrieval for explicitly opted-in `content_graph` projects on a developer workstation.

Access model: localhost-only REST and MCP surfaces under the existing server boundary. Public or non-localhost exposure remains prohibited until a separate authn/authz, rate-limit, origin, audit, and operational review is accepted.

Retention: local ignored datastore files only. No cloud, production, provider, analytics, telemetry, or shared retention is approved by this ADR.

Deletion/reset path: local reset by disabling content graph settings, stopping the server, and deleting ignored local datastore files. Any finer-grained project deletion or tombstone behavior must be implemented and tested before it is documented as supported.

Audit trail expectation: ingestion runs must record non-sensitive run metadata such as project ID, run ID, trigger, mode, counts, status, duration, and error category. Audit records must not include raw source content, absolute roots, skipped sensitive content, matched sensitive text, secrets, raw prompts, provider payloads, or personal data.

Local-only boundary: all approved behavior remains on the developer workstation and must preserve loopback binding.

## Consequences

- Phase 1 may add only this ADR and policy gate language.
- Later disabled config scaffolding may be implemented only when explicitly approved for that phase.
- Source-content storage, source-content hashes, ingestion storage schemas, parsers, REST/MCP ingestion APIs, watcher code, provider work, embeddings, vectors, public exposure, auth changes, crawling, and symlink traversal remain out of scope until their phase and approval gates are satisfied.
- If either required owner rejects the exception, future work must stop before source-content storage. Metadata-only/manual digest may continue unchanged.

## Required Owner Review

- Engineering owner approval for the local-source exception, data owner assignment, purpose, access model, retention, deletion/reset path, audit expectations, and rollout phase boundaries.
- Security/DPO approval for the source-content exception, PII-prohibited posture, sensitive-marker handling, skipped-file representation, logging rules, retention, deletion/reset path, and audit trail.
- Legal/DPO confirmation is required before any interpretation that personal data may be processed.

## Verification Gate

Before any later phase stores source content or source-content hashes, reviewers must confirm:

- This ADR is accepted with named owner approvals.
- `docs/security/research-data-handling.md` contains the accepted exception and still prohibits PII unless separately approved.
- Stable docs contain no links to local task-plan paths.
- Tests cover sensitive-marker skips, no skipped-content storage, no matched-marker logging, no absolute root leakage, and localhost-only boundaries.
- `git diff --check`, stable-doc task-link grep, and secret/PII marker search pass for the phase.
