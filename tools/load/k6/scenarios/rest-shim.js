// SPDX-License-Identifier: Apache-2.0
//
// rest-shim.js — exercise the WP-compatible REST shim with a mix of
// realistic query shapes.
//
// The shim at `/wp-json/wp/v2/*` is the migration-compat surface from
// PR #347. Real WordPress consumers (existing themes, plugins, mobile
// apps) hit it with a wide set of filters — pagination, search,
// taxonomy filters, single-resource fetches, etc. This scenario
// exercises that mix so we catch regressions on the less-common paths.
//
// SLO bucket: anonRead (p95 < 400ms across the mix).
//
// Run:
//   k6 run scenarios/rest-shim.js

import http from 'k6/http';
import { check } from 'k6';
import { anonRead, standardStages, apiBase, tagsFor } from '../lib/baseline.js';

export const options = {
  stages: standardStages,
  thresholds: anonRead,
  tags: tagsFor('rest-shim'),
};

// Weighted-ish mix of queries. Each iteration runs one request from
// the list, cycled by iteration count so coverage is even across a
// full run.
const queries = [
  // Plain pagination — the most common shape.
  { name: 'posts.page1', path: '/wp-json/wp/v2/posts?per_page=20&page=1' },
  { name: 'posts.page2', path: '/wp-json/wp/v2/posts?per_page=20&page=2' },
  // Tighter page size — sidebars, widgets, "latest posts" blocks.
  { name: 'posts.small', path: '/wp-json/wp/v2/posts?per_page=5' },
  // Order by title — exercises a different index path.
  { name: 'posts.orderby', path: '/wp-json/wp/v2/posts?per_page=10&orderby=title&order=asc' },
  // Search — full-text path. Higher latency expected.
  { name: 'posts.search', path: '/wp-json/wp/v2/posts?search=hello&per_page=10' },
  // Pages list — separate handler but same shape.
  { name: 'pages.list', path: '/wp-json/wp/v2/pages?per_page=10' },
  // Taxonomy listings.
  { name: 'categories', path: '/wp-json/wp/v2/categories?per_page=20' },
  { name: 'tags', path: '/wp-json/wp/v2/tags?per_page=20' },
  // Users list (public profile fields only).
  { name: 'users', path: '/wp-json/wp/v2/users?per_page=10' },
];

export default function () {
  const q = queries[__ITER % queries.length];
  const res = http.get(`${apiBase}${q.path}`, {
    tags: { endpoint: q.name },
    headers: { 'Accept': 'application/json' },
  });

  check(res, {
    [`${q.name} status 2xx`]: (r) => r.status >= 200 && r.status < 300,
    [`${q.name} content-type JSON`]: (r) =>
      (r.headers['Content-Type'] || '').includes('application/json'),
    [`${q.name} body parses`]: (r) => {
      try {
        r.json();
        return true;
      } catch (_e) {
        return false;
      }
    },
  });
}
