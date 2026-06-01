SHELL := /usr/bin/env bash

.PHONY: check lint secret-scan test test-integration tidy

check: lint test

lint:
	bash scripts/lint.sh

test:
	bash scripts/test.sh

tidy:
	go mod tidy

secret-scan:
	@if command -v gitleaks >/dev/null 2>&1; then \
		gitleaks detect --source . --config .gitleaks.toml --redact; \
	else \
		echo "gitleaks not installed; skipping local secret scan"; \
	fi

test-integration:
	@echo "No integration tests yet. Database runtime is deferred pending ADR approval."
