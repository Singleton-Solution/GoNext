// SPDX-License-Identifier: Apache-2.0
//
// posts-list.js — anonymous GET of the WP-compatible posts list.
//
// This exercises the wprest shim at `/wp-json/wp/v2/posts` which is
// the most-requested REST surface for read-heavy WordPress traffic.
// SLO bucket: anonRead (p95 < 400ms).
//
// Run:
//   k6 run scenarios/posts-list.js

import http from 'k6/http';
import { check } from 'k6';
import { anonRead, standardStages, apiBase, tagsFor } from '../lib/baseline.js';

export const options = {
  stages: standardStages,
  thresholds: anonRead,
  tags: tagsFor('posts-list'),
};

export default function () {
  const url = `${apiBase}/wp-json/wp/v2/posts?per_page=20`;
  const res = http.get(url, {
    tags: { endpoint: 'wprest.posts.list' },
    headers: { 'Accept': 'application/json' },
  });

  check(res, {
    'status is 200': (r) => r.status === 200,
    'JSON content type': (r) =>
      (r.headers['Content-Type'] || '').includes('application/json'),
    'body parses as array': (r) => {
      try {
        return Array.isArray(r.json());
      } catch (_e) {
        return false;
      }
    },
    'X-WP-Total header present': (r) => r.headers['X-Wp-Total'] !== undefined,
  });
}
