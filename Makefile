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

# GO_MODULES is the list of workspace member directories. Earlier we used
# `go list -m -f '{{.Dir}}/...' all | xargs go build`, but `all` in workspace
# mode expands to the union of every module's full transitive dep graph,
# which makes `go build` try to compile half of $GOPATH/pkg/mod. The clean
# fix is to iterate the workspace's own members, not their dep closures.
GO_MODULES := packages/go apps/api apps/worker cli/gonext

build-go: ## Build all Go binaries.
	@echo "==> Building Go workspace"
	@for dir in $(GO_MODULES); do \
		echo "  → $$dir"; \
		(cd $$dir && go build ./...) || exit 1; \
	done

build-js: ## Build all JS/TS workspace packages.
	@echo "==> Building pnpm workspace"
	@pnpm -r --parallel run build

# ---------------------------------------------------------------------------
# Test

.PHONY: test test-go test-js
test: test-go test-js ## Run all tests.

test-go: ## Run Go tests (per-module).
	@echo "==> Running Go tests"
	@for dir in $(GO_MODULES); do \
		echo "  → $$dir"; \
		(cd $$dir && go test -race -count=1 ./...) || exit 1; \
	done

test-js: ## Run JS/TS tests.
	@echo "==> Running JS/TS tests"
	@pnpm -r run test

# ---------------------------------------------------------------------------
# Accessibility (issue #250 — WCAG 2.1 AA gate)

.PHONY: a11y
a11y: ## Run only the axe-core a11y subset of the e2e suite (all 3 browsers).
	@echo "==> Running a11y e2e suite (tools/e2e/tests/a11y)"
	@# tools/e2e is intentionally outside the pnpm workspace (see #241);
	@# invoke its scripts directly via its own package manifest. Locally
	@# you may not have firefox/webkit binaries installed — use
	@# `make a11y-chromium` for a fast chromium-only loop.
	@cd tools/e2e && pnpm run test:a11y

.PHONY: a11y-chromium
a11y-chromium: ## Run the a11y subset on chromium only (fast local loop).
	@echo "==> Running a11y e2e suite (chromium only)"
	@cd tools/e2e && pnpm run test:a11y:chromium

# ---------------------------------------------------------------------------
# End-to-end smoke (fresh install -> publish -> public-site render)

.PHONY: e2e-smoke
e2e-smoke: ## Run the fresh-install happy-path smoke against a running stack.
	@echo "==> Running e2e smoke (tools/e2e/tests/install-and-publish.spec.ts)"
	@# Wipes the e2e database; gated behind E2E_ALLOW_DESTRUCTIVE so a
	@# stray invocation can't nuke a real one.
	@cd tools/e2e && E2E_ALLOW_DESTRUCTIVE=1 pnpm run e2e:smoke

.PHONY: e2e-blog-loop
e2e-blog-loop: ## Run the full "write a blog post" canary against a running stack.
	@echo "==> Running e2e blog loop (tools/e2e/tests/full-blog-loop.spec.ts)"
	@# Same destructive guard as e2e-smoke: the spec depends on
	@# globalSetup running gonext init, which TRUNCATEs the e2e
	@# database. The E2E_ALLOW_DESTRUCTIVE flag is the failsafe.
	@cd tools/e2e && E2E_FRESH_INSTALL=1 E2E_ALLOW_DESTRUCTIVE=1 \
		pnpm exec playwright test tests/full-blog-loop.spec.ts --project=chromium

# ---------------------------------------------------------------------------
# Lint

.PHONY: lint lint-go lint-js lint-md
lint: lint-go lint-js lint-md ## Run all linters.

lint-go: ## Run go vet + golangci-lint (if installed) per module.
	@echo "==> Linting Go"
	@for dir in $(GO_MODULES); do \
		echo "  → $$dir"; \
		(cd $$dir && go vet ./...) || exit 1; \
	done
	@if command -v golangci-lint >/dev/null 2>&1; then \
		for dir in $(GO_MODULES); do \
			(cd $$dir && golangci-lint run ./...) || exit 1; \
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
#
# `make up` brings up the FULL dev stack (data + app services) by composing
# the base docker-compose.yml with docker-compose.dev.yml. The override
# adds api, worker, admin, web, and a one-shot migrate runner; the base
# keeps Postgres, Redis, and MinIO. We export COMPOSE_FILE so subsequent
# `docker compose ...` calls in this shell pick up both files without the
# operator typing them out — this is the "docker compose run --rm api psql"
# escape hatch that doesn't go through the Makefile.

COMPOSE_FILES := -f docker-compose.yml -f docker-compose.dev.yml
export COMPOSE_FILE := docker-compose.yml:docker-compose.dev.yml

.PHONY: up up-data down logs ps restart psql redis-cli smoke
up: ## Start the full local dev stack (data + apps).
	@docker compose $(COMPOSE_FILES) up -d --wait

up-data: ## Start only the data services (Postgres, Redis, MinIO).
	@docker compose -f docker-compose.yml up -d --wait

down: ## Stop the local dev stack (preserves volumes).
	@docker compose $(COMPOSE_FILES) down

logs: ## Tail logs from the dev stack.
	@docker compose $(COMPOSE_FILES) logs -f

ps: ## List dev stack container status.
	@docker compose $(COMPOSE_FILES) ps

restart: down up ## Restart the dev stack.

psql: ## Open psql against the dev database.
	@docker compose -f docker-compose.yml exec postgres psql -U gonext -d gonext_dev

redis-cli: ## Open redis-cli against the dev redis.
	@docker compose -f docker-compose.yml exec redis redis-cli

smoke: ## Bring up the stack, probe every service's /healthz, and tear it down.
	@./tools/compose-smoke/compose-smoke.sh

# ---------------------------------------------------------------------------
# Bench

.PHONY: bench
bench: ## Run the in-tree `gonext bench` synthetic load runner (short smoke).
	@cd cli/gonext && go run . bench --vus 5 --duration 30s --ramp 5s --no-slo

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
