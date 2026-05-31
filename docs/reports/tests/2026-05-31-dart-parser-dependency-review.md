# Dart Parser Dependency Review

Date: 2026-05-31
Scope: Dart and Flutter content graph extraction for `mivia-server`.

## Decision

Pin `github.com/UserNobody14/tree-sitter-dart` at `v0.0.0-20260520003023-a9bdfa3db2fb`.

## Evidence

- The Tree-sitter parser list points Dart support at `github.com/UserNobody14/tree-sitter-dart`.
- `pkg.go.dev` exposes Go-compatible `bindings/go` for that repository.
- No official `github.com/tree-sitter/tree-sitter-dart` repository was available during implementation.
- The separately addressable `github.com/UserNobody14/tree-sitter-dart/bindings/go@latest` path has a stale module-path mismatch, so the root module is pinned and the in-module `bindings/go` package is imported.

## Risk

The parser is community-maintained, untagged, and pre-v1. Startup grammar/query validation and synthetic fixture tests are required to catch breakage. No live network calls are used in unit tests.
