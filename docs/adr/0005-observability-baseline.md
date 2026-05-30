# ADR-0005: Observability Baseline

Status: Accepted for bootstrap
Date: 2026-05-30

## Context

The bootstrap server is local-only and has no approved production deployment, live provider integration, external datastore, auth model, or PII processing. It still needs enough observability for local debugging without creating privacy or secret leakage.

## Decision

Use structured `log/slog` JSON logs for bootstrap.

Allowed log fields:

- service
- request ID
- task ID when available
- source ID when available
- status
- error category

Prohibited log fields:

- raw request bodies
- raw prompts
- raw source content
- provider payloads
- credentials, tokens, API keys, private keys, secrets
- personal data
- sensitive URL query values

Metrics and tracing are deferred until a production deployment and privacy review exist.

## Consequences

- Local debugging has request correlation without sensitive payload capture.
- No metrics backend, tracing backend, dashboard, alerting, or production SLO is added during bootstrap.
- Before non-localhost exposure, owners must approve authn/authz, origin policy, rate limits, audit logging, retention, monitoring, incident response, and on-call coverage.

## Verification

- `make check`
- `go test ./...`
- review logging callsites for absence of raw payload logging
- secret scanning where installed
