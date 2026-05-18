// SPDX-License-Identifier: Apache-2.0
//
// homepage.js — load test for the cached public homepage.
//
// Target: the Next.js public site at `/`. With ISR + the CDN cache
// the response should be returned from cache for the overwhelming
// majority of requests. SLOs come from lib/baseline.js (cachedAnon).
//
// Run:
//   k6 run scenarios/homepage.js
//   K6_WEB_BASE_URL=https://staging.example.com k6 run scenarios/homepage.js
//
// CI smoke (small VU count, short duration):
//   k6 run --vus 5 --duration 30s scenarios/homepage.js

import http from 'k6/http';
import { check } from 'k6';
import { cachedAnon, standardStages, webBase, tagsFor } from '../lib/baseline.js';

export const options = {
  stages: standardStages,
  thresholds: cachedAnon,
  // Surface a clean "scenario" label in summaries.
  tags: tagsFor('homepage'),
};

export default function () {
  const res = http.get(`${webBase}/`, {
    tags: { endpoint: 'homepage' },
    headers: {
      // A real browser sends Accept-Encoding; emulate so we measure
      // realistic transfer sizes.
      'Accept-Encoding': 'gzip, br',
      'Accept': 'text/html,application/xhtml+xml',
    },
  });

  check(res, {
    'status is 200': (r) => r.status === 200,
    'served HTML': (r) => (r.headers['Content-Type'] || '').includes('text/html'),
    // Cache hits are how we meet the p95. Don't fail the run on miss
    // (caches warm up), but track via a tagged check.
    'cache hit (advisory)': (r) => {
      const cs = r.headers['X-Cache'] || r.headers['Cf-Cache-Status'] || '';
      return cs === '' || /HIT/i.test(cs);
    },
  });
}
