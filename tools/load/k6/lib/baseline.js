// SPDX-License-Identifier: Apache-2.0
//
// baseline.js — shared SLO thresholds for k6 scenarios.
//
// All scenarios import from here so the SLO numbers are defined in one
// place. The values come from the targets agreed in issue #248:
//
//   - Cached homepage:        p95 < 250ms, p99 < 500ms
//   - Logged-in admin list:   p95 < 800ms, p99 < 1500ms
//   - WP shim (anon GET):     p95 < 400ms, p99 < 800ms
//   - Sustained throughput:   200 RPS on a 2-vCPU runner
//
// Each scenario picks the threshold set that matches its endpoint
// class. The fields are plain k6 threshold expressions so they slot
// directly into `options.thresholds` without further translation.

// Cached, anonymous, page-cache-eligible responses. Tightest budget.
export const cachedAnon = {
  // 95% of requests under 250ms, 99% under 500ms.
  http_req_duration: ['p(95)<250', 'p(99)<500'],
  // Error budget — fewer than 1% of requests may fail.
  http_req_failed: ['rate<0.01'],
  // Connection setup should not dominate.
  http_req_connecting: ['p(95)<50'],
};

// Authenticated admin endpoints (DB-backed, no full-page cache).
export const authedAdmin = {
  http_req_duration: ['p(95)<800', 'p(99)<1500'],
  http_req_failed: ['rate<0.01'],
};

// Anonymous REST reads (DB-backed but cacheable at the edge).
export const anonRead = {
  http_req_duration: ['p(95)<400', 'p(99)<800'],
  http_req_failed: ['rate<0.01'],
};

// Default base URLs. Overridable per-run via env vars so the same
// scripts work against local Compose, staging, and prod-like rigs.
//   K6_BASE_URL       — API origin (e.g. http://localhost:8080)
//   K6_WEB_BASE_URL   — Web (Next.js) origin (e.g. http://localhost:3000)
export const apiBase = __ENV.K6_BASE_URL || 'http://localhost:8080';
export const webBase = __ENV.K6_WEB_BASE_URL || 'http://localhost:3000';

// Tagging — k6 surfaces these in the summary so multi-scenario runs
// can be filtered per endpoint class.
export function tagsFor(name) {
  return { scenario: name };
}

// Ramp profile shared by the four main scenarios: 30s warm-up to 50 VUs,
// then 2 minutes sustained, then 15s ramp-down. CI smoke runs override
// this via CLI flags (`--vus 5 --duration 30s`).
export const standardStages = [
  { duration: '30s', target: 50 },
  { duration: '2m', target: 50 },
  { duration: '15s', target: 0 },
];
