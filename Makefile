# GoNext top-level Makefile.
#
# This is the canonical entry point for local development tasks. Underlying
# work is delegated to go.work for Go modules and pnpm for TypeScript/JS.

.DEFAULT_GOAL := help
.PHONY: help

# Use bash with strict mode for any non-trivial recipe.
SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c

# ---------------------------------------------------------------------------
# Help

help: ## Show this help.
	@awk 'BEGIN {FS = ":.*?## "; printf "Usage: make <target>\n\nTargets:\n"} \
		/^[a-zA-Z0-9_.-]+:.*?## / { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' \
		$(MAKEFILE_LIST)

# ---------------------------------------------------------------------------
# Setup

.PHONY: setup
setup: ## Install Go and JS dependencies, sync workspaces.
	@go work sync
	@pnpm install --frozen-lockfile || pnpm install

# ---------------------------------------------------------------------------
# Build

.PHONY: build build-go build-js
build: build-go build-js ## Build everything.

build-go: ## Build all Go binaries.
	@echo "==> Building Go workspace"
	@go list -m -f '{{.Dir}}/...' all | xargs go build

build-js: ## Build all JS/TS workspace packages.
	@echo "==> Building pnpm workspace"
	@pnpm -r --parallel run build

# ---------------------------------------------------------------------------
# Test

.PHONY: test test-go test-js
test: test-go test-js ## Run all tests.

test-go: ## Run Go tests.
	@echo "==> Running Go tests"
	@go list -m -f '{{.Dir}}/...' all | xargs go test -race -count=1

test-js: ## Run JS/TS tests.
	@echo "==> Running JS/TS tests"
	@pnpm -r run test

# ---------------------------------------------------------------------------
# Lint

.PHONY: lint lint-go lint-js lint-md
lint: lint-go lint-js lint-md ## Run all linters.

lint-go: ## Run go vet + golangci-lint (if installed).
	@echo "==> Linting Go"
	@go list -m -f '{{.Dir}}/...' all | xargs go vet
	@if command -v golangci-lint >/dev/null 2>&1; then \
		for dir in $$(go list -m -f '{{.Dir}}' all); do \
			(cd $$dir && golangci-lint run ./...); \
		done; \
	else \
		echo "(golangci-lint not installed, skipping)"; \
	fi

lint-js: ## Run JS/TS linters.
	@echo "==> Linting JS/TS"
	@pnpm -r --parallel run lint

lint-md: ## Run markdown lint.
	@echo "==> Linting Markdown"
	@command -v markdownlint-cli2 >/dev/null && markdownlint-cli2 "**/*.md" || \
		pnpm dlx markdownlint-cli2 "**/*.md" || true

# ---------------------------------------------------------------------------
# Format

.PHONY: fmt fmt-go fmt-js
fmt: fmt-go fmt-js ## Format all code.

fmt-go: ## Format Go code.
	@gofmt -w .

fmt-js: ## Format JS/TS code.
	@pnpm -r --parallel run format 2>/dev/null || true

# ---------------------------------------------------------------------------
# Dev stack (docker-compose)

.PHONY: up down logs restart psql redis-cli
up: ## Start the local dev stack (Postgres, Redis, MinIO).
	@docker compose up -d

down: ## Stop the local dev stack.
	@docker compose down

logs: ## Tail logs from the dev stack.
	@docker compose logs -f

restart: down up ## Restart the dev stack.

psql: ## Open psql against the dev database.
	@docker compose exec postgres psql -U gonext -d gonext_dev

redis-cli: ## Open redis-cli against the dev redis.
	@docker compose exec redis redis-cli

# ---------------------------------------------------------------------------
# Maintenance

.PHONY: tidy clean
tidy: ## Run go mod tidy in every module.
	@for dir in $$(go list -m -f '{{.Dir}}' all); do \
		echo "==> tidying $$dir"; \
		(cd $$dir && go mod tidy); \
	done

clean: ## Remove build artifacts.
	@rm -rf bin/ dist/ build/ .next/ out/ node_modules/.cache/
	@find . -type f -name '*.test' -delete 2>/dev/null || true
