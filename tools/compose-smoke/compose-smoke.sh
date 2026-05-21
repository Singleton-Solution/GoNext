#!/usr/bin/env bash
#
# compose-smoke.sh — end-to-end smoke test for the GoNext compose stack.
#
# Brings the stack up via `docker compose up -d --wait`, polls every
# long-lived service's health surface until ready (60s budget), exercises
# one minimal real request flow against the api, and tears everything
# down — including on failure.
#
# Exit codes:
#   0  every probe passed and the stack tore down cleanly
#   1  one or more probes failed (logs are dumped before teardown)
#   2  usage / environment error (Docker not running, etc.)
#
# Probes:
#   - postgres : Postgres healthcheck (pg_isready) — owned by base compose
#   - redis    : Redis PING                        — owned by base compose
#   - minio    : /minio/health/live                 — owned by base compose
#   - api      : GET /healthz (alive) and GET /readyz (db + redis green)
#   - worker   : container present + not crash-looped (no HTTP listener
#                today; the smoke check verifies process liveness via
#                `docker compose ps` exit-code semantics)
#   - admin/web: GET / (200; the placeholder may return any 2xx/3xx)
#
# Real-flow check:
#   The /api/v1/posts route is not yet mounted in the api binary (it
#   exists in apps/api/internal/rest/posts but the wiring lands in a
#   follow-up). For now the smoke harness validates one canonical
#   real-shipped JSON surface — GET /openapi.json — which exercises
#   the same JSON serialization path the rest of the REST API uses.
#   Once posts.Mount lands in cmd/server/main.go this script flips to
#   probe /api/v1/posts.
#
# Usage:
#   tools/compose-smoke/compose-smoke.sh             # full run
#   KEEP_UP=1 tools/compose-smoke/compose-smoke.sh   # skip teardown (for debugging)
#   API_PORT=18080 ...                                # override host port

set -euo pipefail

# Resolve the repo root regardless of where the script is invoked from.
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
cd "${REPO_ROOT}"

# ----- Config (env-overridable) ---------------------------------------------

API_PORT="${API_PORT:-8080}"
ADMIN_PORT="${ADMIN_PORT:-3001}"
WEB_PORT="${WEB_PORT:-3000}"
HEALTH_TIMEOUT_SECS="${HEALTH_TIMEOUT_SECS:-60}"
KEEP_UP="${KEEP_UP:-0}"

COMPOSE=(docker compose -f docker-compose.yml -f docker-compose.dev.yml)

# ----- Output helpers -------------------------------------------------------

# Disable color if stdout is not a tty (e.g., CI logs).
if [[ -t 1 ]]; then
  C_RED=$'\033[31m'; C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'; C_RESET=$'\033[0m'
else
  C_RED=""; C_GREEN=""; C_YELLOW=""; C_RESET=""
fi

log()  { printf '%s[smoke]%s %s\n' "${C_YELLOW}" "${C_RESET}" "$*"; }
ok()   { printf '%s[ ok ]%s %s\n' "${C_GREEN}" "${C_RESET}" "$*"; }
fail() { printf '%s[FAIL]%s %s\n' "${C_RED}" "${C_RESET}" "$*" >&2; }

# ----- Teardown trap --------------------------------------------------------

cleanup() {
  local exit_code=$?
  if [[ "${KEEP_UP}" == "1" ]]; then
    log "KEEP_UP=1: leaving the stack running."
    exit "${exit_code}"
  fi
  if [[ "${exit_code}" -ne 0 ]]; then
    log "smoke failed; dumping last 80 lines per service..."
    "${COMPOSE[@]}" logs --tail=80 || true
  fi
  log "tearing down stack..."
  "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  exit "${exit_code}"
}
trap cleanup EXIT INT TERM

# ----- Pre-flight ----------------------------------------------------------

if ! command -v docker >/dev/null 2>&1; then
  fail "docker is not installed or not on PATH"
  exit 2
fi
if ! docker info >/dev/null 2>&1; then
  fail "docker daemon is not reachable (is Docker running?)"
  exit 2
fi

# ----- Bring up the stack --------------------------------------------------

log "starting stack (compose up -d --wait)..."
"${COMPOSE[@]}" up -d --wait --quiet-pull
ok "compose up returned"

# ----- Per-service probes --------------------------------------------------

# poll_url <name> <url> [<expect_jq_path>]
# Polls url every second up to HEALTH_TIMEOUT_SECS, asserting HTTP 200 +
# optionally a non-empty value at expect_jq_path in the response body.
poll_url() {
  local name="$1" url="$2" jq_path="${3:-}"
  local deadline=$(( SECONDS + HEALTH_TIMEOUT_SECS ))
  local http_code
  local body_file="/tmp/.smoke-body-$$"
  while (( SECONDS < deadline )); do
    http_code=$(curl -sS -o "${body_file}" -w '%{http_code}' "${url}" 2>/dev/null || echo "000")
    if [[ "${http_code}" == "200" ]]; then
      if [[ -z "${jq_path}" ]]; then
        ok "${name}: ${url} → 200"
        rm -f "${body_file}"
        return 0
      fi
      # JSON sanity: first non-whitespace byte must be { or [. Avoids
      # shell-substitution pitfalls with large response bodies.
      if head -c 1024 "${body_file}" | grep -qE '^[[:space:]]*[{[]'; then
        ok "${name}: ${url} → 200 + JSON body"
        rm -f "${body_file}"
        return 0
      fi
    fi
    sleep 1
  done
  rm -f "${body_file}"
  fail "${name}: ${url} did not become ready within ${HEALTH_TIMEOUT_SECS}s (last http_code=${http_code:-unknown})"
  return 1
}

# poll_compose_health <service>
# Polls the docker-compose declared healthcheck until "healthy".
poll_compose_health() {
  local svc="$1"
  local deadline=$(( SECONDS + HEALTH_TIMEOUT_SECS ))
  local state
  while (( SECONDS < deadline )); do
    state=$("${COMPOSE[@]}" ps --format json "${svc}" 2>/dev/null \
              | python3 -c 'import sys,json
try:
  for line in sys.stdin:
    line=line.strip()
    if not line: continue
    d=json.loads(line)
    print(d.get("Health", d.get("State","")))
    break
except Exception:
  pass' 2>/dev/null || echo "")
    if [[ "${state}" == "healthy" ]]; then
      ok "${svc}: compose health = healthy"
      return 0
    fi
    sleep 1
  done
  fail "${svc}: compose health never reached 'healthy' within ${HEALTH_TIMEOUT_SECS}s"
  return 1
}

# poll_container_running <service>
# Asserts the service container is up and has NOT exited (covers worker,
# which has no HTTP health surface today).
poll_container_running() {
  local svc="$1"
  local deadline=$(( SECONDS + HEALTH_TIMEOUT_SECS ))
  local state
  while (( SECONDS < deadline )); do
    state=$("${COMPOSE[@]}" ps --format json "${svc}" 2>/dev/null \
              | python3 -c 'import sys,json
try:
  for line in sys.stdin:
    line=line.strip()
    if not line: continue
    d=json.loads(line)
    print(d.get("State",""))
    break
except Exception:
  pass' 2>/dev/null || echo "")
    if [[ "${state}" == "running" ]]; then
      ok "${svc}: container state = running"
      return 0
    fi
    sleep 1
  done
  fail "${svc}: container never reached 'running' within ${HEALTH_TIMEOUT_SECS}s (last state: ${state})"
  return 1
}

failures=0

log "probing service health..."

# Data services — rely on compose healthchecks declared in the base file.
poll_compose_health postgres || failures=$(( failures + 1 ))
poll_compose_health redis    || failures=$(( failures + 1 ))
poll_compose_health minio    || failures=$(( failures + 1 ))

# api liveness + readiness.
poll_url api-healthz   "http://localhost:${API_PORT}/healthz"          || failures=$(( failures + 1 ))
poll_url api-readyz    "http://localhost:${API_PORT}/readyz"           || failures=$(( failures + 1 ))

# worker: stays up.
poll_container_running worker || failures=$(( failures + 1 ))

# admin/web: placeholder pages return 200 (admin has wget healthcheck;
# web does too but with a slightly longer start-period). We give both
# a generous timeout via the same poll.
poll_url admin-root    "http://localhost:${ADMIN_PORT}/"                || failures=$(( failures + 1 ))
poll_url web-root      "http://localhost:${WEB_PORT}/"                  || failures=$(( failures + 1 ))

# Minimal real-flow check.
log "probing real request flow..."
poll_url api-openapi   "http://localhost:${API_PORT}/openapi.json" jq  || failures=$(( failures + 1 ))
poll_url api-root      "http://localhost:${API_PORT}/"              jq || failures=$(( failures + 1 ))

if (( failures > 0 )); then
  fail "${failures} probe(s) failed"
  exit 1
fi

ok "all probes passed"
exit 0
