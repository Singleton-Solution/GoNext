// SPDX-License-Identifier: Apache-2.0
//
// login.js — load test for POST /api/v1/auth/login.
//
// We hit the endpoint with two request shapes — one that should
// succeed (200) and one that must always fail (401). Both branches
// have explicit thresholds so a regression that flips the 401 path to
// 200 fails the run loudly.
//
// SLO bucket: authedAdmin (the cost is dominated by the Argon2id
// verify on every attempt). For the invalid branch the service
// performs a constant-time dummy verify, so latency on both shapes
// should be comparable.
//
// Run:
//   k6 run scenarios/login.js
//   K6_VALID_EMAIL=admin@example.com K6_VALID_PASSWORD=... k6 run scenarios/login.js
//
// IMPORTANT: the 401 rate threshold below asserts that EVERY invalid
// attempt returned 401. Anything else fails the run.

import http from 'k6/http';
import { check } from 'k6';
import { Rate } from 'k6/metrics';
import { authedAdmin, standardStages, apiBase, tagsFor } from '../lib/baseline.js';

// Per-branch metrics. k6 surfaces them in the summary and lets us
// assert on them in `thresholds`.
const invalid401Rate = new Rate('login_invalid_401_rate');
const valid200Rate = new Rate('login_valid_200_rate');

export const options = {
  stages: standardStages,
  thresholds: {
    ...authedAdmin,
    // Every invalid attempt MUST return 401 — no false positives, no
    // 500s, no leaked 200s. This is the security-critical assertion
    // from the issue.
    'login_invalid_401_rate': ['rate==1.0'],
    // Valid-creds branch should land on 2xx the vast majority of the
    // time. We allow a 1% slop for retry-window races.
    'login_valid_200_rate': ['rate>0.99'],
  },
  tags: tagsFor('login'),
};

const validEmail = __ENV.K6_VALID_EMAIL || 'admin@example.com';
const validPassword = __ENV.K6_VALID_PASSWORD || 'changeme-dev-only';

const invalidEmail = __ENV.K6_INVALID_EMAIL || 'nobody@example.invalid';
const invalidPassword = __ENV.K6_INVALID_PASSWORD || 'wrong-password';

export default function () {
  // Hit the invalid branch first so a misconfigured run fails fast
  // rather than burning a real account through rate limits.
  postLogin(invalidEmail, invalidPassword, /*expect=*/ 401);
  postLogin(validEmail, validPassword, /*expect=*/ 200);
}

function postLogin(email, password, expect) {
  const url = `${apiBase}/api/v1/auth/login`;
  const payload = JSON.stringify({ email, password });
  const res = http.post(url, payload, {
    tags: { endpoint: 'auth.login', branch: expect === 200 ? 'valid' : 'invalid' },
    headers: { 'Content-Type': 'application/json' },
  });

  if (expect === 401) {
    invalid401Rate.add(res.status === 401);
    check(res, {
      'invalid creds -> 401': (r) => r.status === 401,
      'invalid creds -> JSON error body': (r) => {
        try {
          return typeof r.json('error') === 'string';
        } catch (_e) {
          return false;
        }
      },
    });
  } else {
    valid200Rate.add(res.status === 200);
    check(res, {
      'valid creds -> 200 or 401': (r) => r.status === 200 || r.status === 401,
      // We don't fail on 401 here because dev fixtures vary — the
      // metric above does the strict assertion.
    });
  }
}
