/**
 * Performance route entry.
 *
 * Server component shell around the client `PerformancePage`. The
 * page is rendered dynamically because every refresh hits the API
 * for a fresh percentile aggregate.
 */
import type { ReactElement } from 'react';
import { PerformancePage } from './PerformancePage';

export const dynamic = 'force-dynamic';

export default function PerformanceRoute(): ReactElement {
  return <PerformancePage />;
}
