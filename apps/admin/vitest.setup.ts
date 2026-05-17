/**
 * Vitest setup for @gonext/admin.
 *
 * Re-exports the shared setup module from @gonext/test-config, which:
 *  - registers @testing-library/jest-dom matchers
 *  - installs a loud fetch stub (unmocked network calls throw)
 *  - runs RTL `cleanup()` after every test
 *
 * Admin-specific globals (e.g. MSW server registration once `src/test/msw.ts`
 * lands per issue #240 acceptance criteria) should be added below this import.
 */
import '@gonext/test-config/setup';
