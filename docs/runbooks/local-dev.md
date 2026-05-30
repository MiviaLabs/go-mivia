# Local Development Runbook

Status: Bootstrap
Date: 2026-05-30
Classification: Internal; no PII

## Prerequisites

- Go `1.26.3`
- `make`
- `curl`
- Optional: `gitleaks`
- Optional gated native path: LadybugDB native libraries through `scripts/ladybug-libs.sh`

## Verify

```sh
go version
go mod tidy
go test ./...
make check
make secret-scan
```

`make secret-scan` skips locally when `gitleaks` is not installed. CI runs secret scanning through the configured GitHub Action.

## Run Agent Server

```sh
MIVIA_HTTP_ADDR=127.0.0.1:8080 \
MIVIA_SQLITE_PATH=:memory: \
go run ./cmd/agent-server
```

Default bind is localhost-only. Do not bind to `0.0.0.0` or a public interface until authn/authz, origin policy, rate limits, and audit logging are approved.

## REST Smoke

```sh
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS -H 'Content-Type: application/json' \
  -d '{"title":"local smoke"}' \
  http://127.0.0.1:8080/api/v1/tasks
```

## MCP Smoke

```sh
curl -fsS \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-06-18' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize"}' \
  http://127.0.0.1:8080/mcp
```

## Ladybug Native Gate

Normal builds use the in-memory Ladybug graph abstraction and do not import `go-ladybug` in normal build paths.

Native import verification is gated:

```sh
./scripts/ladybug-libs.sh
export CGO_LDFLAGS="-L$(pwd)/lib-ladybug -llbug -Wl,-rpath,$(pwd)/lib-ladybug"
go test -tags 'ladybug_native system_ladybug' ./internal/platform/ladybug/...
```

Do not commit `lib-ladybug/` or local database files.

## Troubleshooting

- `MIVIA_HTTP_ADDR` rejected: use `127.0.0.1` or `localhost`.
- SQLite open failure: check the configured directory is writable or use `MIVIA_SQLITE_PATH=:memory:`.
- MCP 406: include both `application/json` and `text/event-stream` in `Accept`.
- MCP 403: use a localhost or loopback `Origin`.
