SHELL := /usr/bin/env bash

.PHONY: check lint test test-integration tidy

check: lint test

lint:
	./scripts/lint.sh

test:
	./scripts/test.sh

tidy:
	go mod tidy

test-integration:
	@echo "No integration tests yet. Database runtime is deferred pending ADR approval."
