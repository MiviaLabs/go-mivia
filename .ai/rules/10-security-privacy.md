# Security And Privacy

Classification:

- Internal by default.
- PII ingestion is prohibited until the Security/DPO owner approves purpose, legal basis, access model, retention, deletion path, and audit trail.

Sensitive data rules:

- Do not commit real secrets, `.env` files, credentials, tokens, private keys, raw prompts, raw fetched research content, personal data, or proprietary third-party content.
- Do not log raw source content, prompts, tokens, credentials, personal data, or provider request/response bodies.
- Use redacted references, hashes, summaries, and artifact IDs where possible.
- Test fixtures must not contain real PII or real credentials.

Review checklist:

- Authentication and authorization.
- IDOR/BOLA and privilege escalation.
- Injection, SSRF, XSS, CSRF, unsafe deserialization, and path traversal.
- Secrets exposure and insecure defaults.
- Dependency and supply-chain risk.
- Logging, tracing, metrics, analytics, and snapshot leaks.
- Retention, deletion, auditability, and access controls.

AI provider posture:

- No provider, embedding model, vector dimension, retention behavior, or external crawling policy is approved until captured in an ADR.
- Provider adapters must expose explicit data handling boundaries.
- Unit tests must not perform live network calls.

Human review required:

- PII or personal-data processing.
- External provider selection.
- Broad crawling or scraping.
- Public API exposure.
- Production deployment or cloud infrastructure.
- Enterprise-licensed features.
