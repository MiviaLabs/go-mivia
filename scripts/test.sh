#!/usr/bin/env bash
set -euo pipefail

if ! command -v go >/dev/null 2>&1; then
  echo "missing required tool: go" >&2
  exit 127
fi

go test ./...
