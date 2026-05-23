// Package setup implements the first-run install surface — the
// in-browser equivalent of `gonext init`.
//
// On a fresh deployment with no users in the database, an operator can
// open /api/v1/setup/status, see {"installation_completed": false},
// and POST /api/v1/setup/install to create the bootstrap super_admin,
// stamp the `core.site.installation_completed_at` option, and receive
// the session cookie that logs them in immediately.
//
// Once the install option is present every endpoint in this package
// returns 423 Locked + {"code":"already_installed"}. There is no
// "reinstall" path — that would defeat the entire point of locking
// the surface. Operators who need to wipe and start over drop the
// option row out-of-band (psql) and restart.
//
// The package is hardened against three abuse patterns:
//
//  1. Brute-force probing of the install endpoint. A per-IP token bucket
//     (5 attempts / hour by default) caps the rate at which an attacker
//     can guess they hit the window between deploy and first-install.
//
//  2. Post-install hijack. The lock check runs BEFORE any payload work,
//     so a second installer cannot replace the first one's credentials.
//
//  3. Weak credentials. The handler rejects passwords shorter than 12
//     characters before any DB write — the strength meter in the
//     wizard is a UX convenience, not the gate.
//
// The package depends on a tiny set of seams (UserCreator, OptionStore,
// SessionCreator, Limiter, Now) so tests can drive the install logic
// without spinning up Postgres / Redis. Production wiring lives in
// apps/api/cmd/server/main.go.
package setup
