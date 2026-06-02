---
name: security-review
description: Review repo changes touching PII, secrets, auth, providers, public APIs, migrations, logs, retention, deletion, auditability, or access control.
---

# Security Review Skill

Use this skill for changes touching PII, secrets, auth, authorization, external providers, public APIs, migrations, logs, or data retention.

Workflow:

1. Read `.ai/rules/10-security-privacy.md`.
2. Identify data entering, leaving, stored by, or logged by the changed system.
3. Check authn/authz, tenancy boundaries, injection risk, SSRF, secrets handling, logging leaks, dependency risk, retention, deletion, auditability, and access control.
4. Verify tests or controls for negative cases.
5. Name required Security/DPO, legal, licensing, or engineering-owner decisions.

Hard stops:

- Real secrets or PII in committed files.
- Raw prompts, provider payloads, tokens, credentials, or source content in logs.
- PII processing without purpose, legal basis, retention, deletion path, access model, and audit trail.
- Enterprise Neo4j or paid provider use without owner approval.

Output:

- Confirmed issues.
- Risk level.
- Evidence.
- Fix direction.
- Required owner review.
