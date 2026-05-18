# Makefile — convenience wrappers around k6 for the load-test suite.
#
# Run from this directory (tools/load/k6/). Each target maps to a
# scenario in scenarios/ and uses shared SLO thresholds from
# lib/baseline.js. Override the base URLs with env vars:
#
#   K6_BASE_URL=http://api.local:8080 \
#   K6_WEB_BASE_URL=http://web.local:3000 \
#   make all
#
# Pre-release flow: `make all` against a fresh Compose stack.
# CI quick check: `make ci-smoke` — small VU count, short duration.

K6 ?= k6
SCENARIOS_DIR := scenarios

.PHONY: homepage posts-list login rest-shim all ci-smoke help

help:
	@echo "k6 load-test targets:"
	@echo "  make homepage     - public homepage (cached, anon)"
	@echo "  make posts-list   - GET /wp-json/wp/v2/posts (anon)"
	@echo "  make login        - POST /api/v1/auth/login (valid + invalid)"
	@echo "  make rest-shim    - mix of WP REST shim queries"
	@echo "  make all          - run every scenario sequentially"
	@echo "  make ci-smoke     - 5 VUs / 30s smoke run of homepage"

homepage:
	$(K6) run $(SCENARIOS_DIR)/homepage.js

posts-list:
	$(K6) run $(SCENARIOS_DIR)/posts-list.js

login:
	$(K6) run $(SCENARIOS_DIR)/login.js

rest-shim:
	$(K6) run $(SCENARIOS_DIR)/rest-shim.js

# Run the full suite. Failures in any single scenario propagate (k6
# exits non-zero on threshold breach) and stop the chain.
all: homepage posts-list login rest-shim

# Small smoke run — meant for CI on PRs that touch apps/api/**. Uses
# the homepage scenario as a representative single endpoint.
ci-smoke:
	$(K6) run --vus 5 --duration 30s $(SCENARIOS_DIR)/homepage.js
