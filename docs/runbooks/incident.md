# Incident Runbook

Status: Bootstrap
Date: 2026-05-30
Classification: Internal; no PII

## Scope

This runbook applies only to the local bootstrap `mivia-server`. There is no approved production deployment, public API exposure, live provider integration, PII processing, or external datastore runtime.

## Immediate Actions

1. Stop the local server if sensitive data may have entered requests, logs, SQLite, LadybugDB, or terminal output.
2. Preserve command history and exact local commit SHA for engineering review.
3. Do not paste raw prompts, source content, tokens, credentials, personal data, or provider payloads into Slack, Jira, GitHub, logs, screenshots, or docs.
4. Create a private engineering/security task with redacted facts only.

## Triage Checks

- Confirm bind address remained localhost-only.
- Confirm no `.env`, secret files, local DB files, or `lib-ladybug/` artifacts were committed.
- Run `go test ./...` and `make check`.
- Run `make secret-scan` or `gitleaks detect --source . --config .gitleaks.toml --redact` where installed.
- Review recent logs for request IDs, task IDs, source IDs, and error categories only.

## Escalation

Security/DPO owner confirmation is required before:

- processing personal data
- retaining or deleting personal data
- exposing non-localhost traffic
- using external AI, embedding, browsing, crawling, or provider APIs
- approving provider retention terms or cross-border transfer posture

## Logging Constraint

Allowed in logs:

- service name
- request ID
- task ID
- source ID
- status
- error category

Prohibited in logs:

- raw request bodies
- raw prompts
- raw source content
- provider payloads
- credentials, tokens, API keys, private keys, secrets
- personal data
- sensitive URL query values
