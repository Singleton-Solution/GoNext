// Package initcmd implements the `gonext init` subcommand: the
// one-command first-run bootstrap that turns a freshly-cloned (or
// freshly-deployed) GoNext install into a usable site.
//
// # Why this exists
//
// Before this command landed, the path from `git clone` to "a site
// you can sign into" required four manual steps:
//
//  1. point DATABASE_URL at a running Postgres
//  2. `gonext migrate up` to lay down the schema
//  3. hand-craft an INSERT into users + user_passwords with an
//     argon2id PHC string the operator had to produce themselves
//  4. write a row into options for the active theme
//
// Step 3 in particular blocked every real-world adoption attempt:
// nobody wants to read packages/go/auth/password to figure out the
// salt/pepper/PHC encoding by hand. `gonext init` collapses every
// step into one verb.
//
// # Contract
//
// `gonext init` is idempotent. Running it twice on the same install
// is a no-op the second time — the installation_completed_at option
// row (written at the end of a successful first run) is the gate.
// The active-theme row is consulted as a fallback so older databases
// that were initialized by `gonext migrate up` are still recognized
// as "set up".
//
// # Flags
//
//	--admin-email STRING          Email address of the initial admin.
//	--admin-password STRING       Initial admin password. Mutually
//	                              exclusive with --admin-password-stdin.
//	--admin-password-stdin        Read the password from stdin instead.
//	--site-name STRING            Human-facing site name.
//	--site-url STRING             Canonical site URL (no trailing slash).
//	--skip-migrations             Don't run `migrate up`. Useful when
//	                              the schema was already applied by a
//	                              prior step (kube initContainer).
//	--skip-theme-seed             Don't install the bundled default
//	                              theme. Useful when the operator
//	                              wants to install a custom theme
//	                              before any user-visible request.
//	--non-interactive             Don't prompt for missing fields.
//	                              Fail fast instead.
//
// In interactive mode (the default), any field not supplied via a
// flag is prompted on stdin. Password prompts are echo-suppressed via
// golang.org/x/term.
//
// # Exit codes
//
//	0  success (including idempotent re-run)
//	1  setup failed (DB, migrations, seed, admin creation, write)
//	2  usage error (bad flags, missing required field in non-interactive
//	    mode, password too short, invalid email)
//
// # Architectural seam
//
// init.go owns argument parsing and flag/prompt orchestration.
// setup.go is the pure orchestrator that runs the steps in order
// against an injected pool — it has no dependency on os.Stdin or
// the flag package, so tests can drive it directly. admin.go
// produces the argon2id PHC hash and writes users + user_passwords
// in a single transaction. prompt.go is the stdin helper with
// password hiding and basic validation.
package initcmd
