/**
 * Shared types for the first-run setup wizard.
 *
 * The wire shapes mirror apps/api/internal/setup/handler.go. We keep
 * them in their own module (rather than hidden inside the wizard
 * component) so the page.tsx server component and the middleware can
 * import the status type without dragging the wizard's client-side
 * bundle along.
 */

/**
 * Response body returned by GET /api/v1/setup/status.
 *
 * The middleware reads this on every admin navigation to decide
 * whether to redirect to /setup. The wizard reads it once on mount to
 * short-circuit to /admin/login if the install window has closed
 * between page load and form render.
 */
export interface SetupStatus {
  installation_completed: boolean;
  user_count: number;
}

/**
 * Request body for POST /api/v1/setup/install. Field names are
 * snake_case to match the API contract; the wizard transforms its
 * internal camelCase state into this shape at submit time.
 */
export interface InstallRequest {
  admin_email: string;
  admin_password: string;
  site_name: string;
  site_url: string;
}

/**
 * Success response from POST /api/v1/setup/install. The cookie is
 * delivered through Set-Cookie (HttpOnly); the body is informational.
 */
export interface InstallResponse {
  user_id: string;
  expires_at: string;
}

/**
 * Failure response shape. `code` is the stable machine-readable
 * identifier the wizard uses to highlight the offending step; `message`
 * is a developer-friendly fallback when no localized copy exists.
 */
export interface InstallError {
  code: string;
  message?: string;
}
