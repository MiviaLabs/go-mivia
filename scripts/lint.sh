#!/usr/bin/env bash
set -euo pipefail

if ! command -v go >/dev/null 2>&1; then
  echo "missing required tool: go" >&2
  exit 127
fi

mapfile -t go_files < <(find . -path './.git' -prune -o -name '*.go' -print)

if ((${#go_files[@]} > 0)); then
  unformatted="$(gofmt -l "${go_files[@]}")"
  if [[ -n "${unformatted}" ]]; then
    echo "gofmt required for:" >&2
    echo "${unformatted}" >&2
    exit 1
  fi
fi

go vet ./...
