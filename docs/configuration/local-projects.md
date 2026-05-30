# Local Project Configuration

Status: Local registry and metadata-only digest support
Date: 2026-05-30
Classification: Internal; PII-prohibited

Local project configuration is optional. If no local config exists, `agent-server` starts with environment-only defaults and an empty project list.

## Files

- Example config: [configs/agent-server.example.toml](../../configs/agent-server.example.toml)
- Default ignored local config: `configs/agent-server.local.toml`
- Explicit config path override: `MIVIA_CONFIG_PATH`
- ADR: [ADR-0006](../adr/0006-local-project-configuration.md)

Do not commit local config files. Do not put secrets, tokens, PII, raw prompts, raw source content, provider payloads, or personal data in local config.

## Copy Workflow

```sh
cp configs/agent-server.example.toml configs/agent-server.local.toml
```

Edit only placeholder values such as `root_path`, then start the server:

```sh
MIVIA_CONFIG_PATH=configs/agent-server.local.toml go run ./cmd/agent-server
```

`MIVIA_CONFIG_PATH` is fatal when it points to a missing or invalid file. When it is unset and `configs/agent-server.local.toml` is absent, startup keeps the current environment-only defaults.

Environment variables remain final overrides over file values:

- `MIVIA_HTTP_ADDR`
- `MIVIA_LADYBUG_PATH`
- `MIVIA_SQLITE_PATH`
- `MIVIA_MAX_REQUEST_BYTES`
- `MIVIA_REQUEST_TIMEOUT`
- `MIVIA_READ_HEADER_TIMEOUT`
- `MIVIA_SHUTDOWN_TIMEOUT`

## Field Reference

| Field | Required | Notes |
| --- | --- | --- |
| `version` | Yes | Must be `1`. |
| `server.http_addr` | No | Defaults to `127.0.0.1:8080`; must stay loopback. |
| `server.max_request_bytes` | No | Positive integer; defaults to `1048576`. |
| `server.request_timeout` | No | Go duration string, for example `10s`. |
| `server.read_header_timeout` | No | Go duration string, for example `5s`. |
| `server.shutdown_timeout` | No | Go duration string, for example `10s`. |
| `storage.ladybug_path` | No | Local ignored graph datastore path; defaults to `data/mivialabs.lbug`. |
| `storage.sqlite_path` | No | Local ignored app-config datastore path; defaults to `data/mivialabs-config.sqlite`. |
| `projects.id` | Yes | Stable project slug. |
| `projects.display_name` | Yes | Human-readable name. |
| `projects.description` | No | Non-sensitive summary. |
| `projects.root_path` | Yes | Absolute local path; use placeholders in committed examples. |
| `projects.enabled` | Yes | `true` only for projects allowed by the local developer. |
| `projects.classification` | No | Use `internal` unless an approved policy says otherwise. |
| `projects.graph_namespace` | No | Stable graph namespace for metadata-only digest; defaults to project ID. |
| `projects.digest_mode` | No | Only `metadata_only` is accepted. |
| `projects.update_policy` | No | Only `manual` is accepted. |
| `projects.include` | No | Root-relative include patterns. |
| `projects.exclude` | No | Root-relative exclude patterns. |
| `projects.follow_symlinks` | No | Keep `false`; symlink traversal is not approved. |

## Safe Path Examples

Use local Linux or WSL absolute paths:

```toml
root_path = "/home/dev/projects/example-service"
```

Windows drive-letter paths and UNC paths are not approved for the WSL server. Use a WSL-mounted path only after the engineering owner confirms the support model.

Docs must never point to a real developer's local config file or machine-specific project path.

## Validation Failures

Current validation rejects:

- missing explicit `MIVIA_CONFIG_PATH`
- invalid TOML
- unknown top-level sections or fields
- unknown project fields
- unsupported `version`
- unsupported `digest_mode`
- unsupported `update_policy`
- invalid Go duration strings
- non-loopback `server.http_addr`
- non-positive timeout and request-size values
- missing enabled project roots
- non-directory enabled project roots
- duplicate project IDs
- duplicate graph namespaces
- unsafe include/exclude patterns
- path traversal
- absolute include/exclude patterns
- symlink roots and symlink directory traversal
- `follow_symlinks = true`

## Local Project APIs

The server exposes bounded project metadata on localhost only:

- `GET /api/v1/projects`
- `GET /api/v1/projects/{id}`
- `POST /api/v1/projects/{id}/digest-runs`
- MCP tools: `projects.list`, `projects.get`, `projects.digest`
- MCP resources: `mivialabs://projects/{id}`, `mivialabs://projects/{id}/digest-runs/{run_id}`

Project responses omit local root paths by default. Digest runs are manual and metadata-only: graph writes store relative path, extension/language hint, file size, mtime, and a metadata fingerprint. They do not store or return raw source content or file-content hashes.
