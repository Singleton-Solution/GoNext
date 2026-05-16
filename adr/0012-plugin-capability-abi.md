# ADR 0012: Plugin capabilities are declared in the manifest and gated at every host ABI call

- **Status**: proposed
- **Date**: 2026-05-17
- **Deciders**: @tayebmokni
- **Consulted**: doc 02 §6 (host ABI capabilities)
- **Informed**: plugin SDK authors, security reviewers, admin reviewers

## Context

ADR 0005 picks WebAssembly via wazero as the plugin runtime. The runtime is one half of the plugin-security story; the other half is **what plugins can do once they are running**. WebAssembly by itself gives memory isolation and a closed-world default — a WASM module has no syscalls, no network, no filesystem, no DB. The host has to import functions for any of those, and once imported, every plugin module can call them. Without an additional permission layer, every plugin gets every imported capability.

WordPress's failure mode is the opposite: every plugin runs with full host-process privileges by default. There is no manifest-declared "this plugin needs DB write" gate. A plugin that wants to read all user emails just does it; a plugin that wants to send mail to anywhere just does it. This is the documented attack class our positioning (proposal S1) targets.

The design (doc 02 §6) specifies a capability-based authorization model:

- Plugins declare the capabilities they need in their **manifest** at publish time.
- At install, the admin sees the requested capabilities and either approves them or rejects the install.
- At activation, the host issues a **signed, scoped, time-bounded capability token** for the plugin instance.
- Every host ABI call checks the token before executing.
- Tokens auto-expire and rotate transparently on the guest's next call.

The capability vocabulary is **fixed and finite**. Adding a new capability requires a core change. Plugins cannot invent new capabilities or escalate.

The v1 capability list (doc 02 §6, abridged):

| Capability | What it gates |
|---|---|
| `db.read` | Scoped read access to core tables via host-defined views and to plugin-owned tables. |
| `db.write` | Write access to plugin-owned tables (and rarely, explicitly granted, core tables). |
| `kv` | Per-plugin namespaced Redis KV store. |
| `queue` | Asynq job enqueue, with per-plugin partitioning (ADR 0010). |
| `cron` | Manifest-declared cron schedules dispatched via the hook bus. |
| `http.fetch` | Outbound HTTP, allowlisted hosts, SSRF-blocked private IP ranges, rate-limited. |
| `http.serve` | Mount HTTP routes under `/api/plugins/{slug}/...`. |
| `email` | Send mail via the host's mailer with rate limits and template scoping. |
| `media.read`, `media.write` | Scoped media access. |
| `users.read` | Scoped user reads, field-allowlisted (email requires explicit grant). |
| `secrets` | Implicit grant from declaring `secrets.keys` in the manifest. |
| `cache.invalidate` | Tag-based cache invalidation via the outbox (ADR 0011). |
| `audit.emit` | Emit rows into the audit log with `actor_kind='plugin'`. |
| `i18n`, `clock`, `log`, `observability` | Always available; not gated. |

Each capability has scoping (a list pattern like `db.read:core.posts:read`), rate limits, and per-invocation budgets. The detailed contract per capability lives in doc 02 §6.2–§6.8.

The alternative is to give plugins blanket permissions and audit downstream. That is the WordPress model. It defeats the whole point of the sandbox.

A more dynamic alternative is **capability negotiation at runtime** — the plugin asks for caps as it runs, the user approves them on the fly. That sounds nice but is unreviewable: a plugin gets approved once and then asks for arbitrary new capabilities later. It also makes static review (doc 02 §10.4) impossible.

A finer-grained alternative is **per-call ACLs** — every host call checks a separate per-call permission. That blows the performance budget; the host ABI is on the hot path of every hook dispatch.

## Decision

Plugin capabilities are declared in `manifest.json`'s `capabilities` block at publish time. The admin reviews and approves capabilities at install. The host issues a signed, scoped, time-bounded capability token at activation. **Every host ABI call checks the token before executing the underlying operation.** The capability vocabulary is fixed and finite (the table in doc 02 §6); adding a capability requires a core change and a design review. Plugins cannot escalate at runtime; the only way to gain a new capability is to publish a new plugin version with the cap requested and have the admin re-approve.

## Consequences

### Positive

- **Static review is possible.** A plugin's full ability surface is its manifest plus the fixed capability vocabulary. A security reviewer (or a scanner — proposal Q02-6) can read the manifest and know exactly what the plugin can do. WordPress's model offers no such artifact.
- **The admin sees a concrete approval prompt.** "This plugin wants `db.read:core.posts:read`, `http.fetch:api.example.com`, `email:transactional`." Compare to WordPress's "install this plugin (it can do anything)."
- **Token-bound enforcement is the only path.** The token is verified on every host call. A capability that is not on the token is not callable. No bug in the dispatcher can leak an unapproved capability (ADR 0011's cache-invalidation host ABI gates on `cache.invalidate` exactly because of this contract).
- **Scopes are part of the capability.** `db.read:core.posts:read` is different from `db.read:plugin.tables:*`. A plugin that needs only its own tables cannot read core data; a plugin that needs read-only access to published posts cannot read drafts (the views encode the predicate — doc 02 §6.2).
- **Audit emits are first-class.** `audit.emit` is itself a capability; plugins that need to log are gated. Every `secret_get` is auto-emitted (sampled in steady state, always on first read after rotation) regardless of whether the plugin requests it.
- **Per-call performance is acceptable.** Token verification is a constant-time signature check against the host's verification key, on the order of microseconds. Doc 02 §6.1 caps token TTL at 5 minutes with transparent rotation; the check is fast enough to live on the hot path of every hook dispatch.

### Negative

- **Plugin authors have to think about capabilities.** A plugin that "just wants to do its job" has to enumerate which host ABIs it needs. The SDKs make this declarative (a `caps: ["db.read", ...]` field in the SDK build config), but it is one more thing to get right.
- **Coarse-grained capabilities are leaky.** `http.fetch` lets a plugin fetch any allowlisted host. The allowlist is part of the manifest, but inside the allowlist a plugin can do anything. We accept this and rely on the host allowlist plus rate limits.
- **Capability bumps require admin re-approval.** A plugin update that requests a new capability cannot auto-install; the admin must re-review. This is friction; it is also exactly the friction we want.
- **The fixed vocabulary is a backlog item.** Genuine new use cases will appear (e.g., a plugin that needs to mount a metrics endpoint, or to subscribe to a Postgres NOTIFY channel). Adding a capability is a core PR plus a design review, not a config flag.

### Neutral / accepted tradeoffs

- We do not support runtime capability negotiation. The decision is at install time; the only renegotiation is a plugin update with new caps requiring re-approval.
- We do not support per-call ACLs (finer than capability-level). Capabilities are the only authorization unit; their scoping fields (allowlists, namespaces) provide the granularity we need without per-call performance cost.
- Implicit grants (declaring `secrets.keys` grants the `secrets` capability automatically — doc 02 §6.7) are documented exceptions, not a general pattern. We do not invent more implicit grants without design review.

## Alternatives considered

### Option A: Blanket permissions (the WordPress model)
- Rejected. Defeats the sandbox. Doc 02 §6.8 enumerates the WP-versus-us contrast in detail. Our positioning (proposal S1) is "WordPress, but you can trust the plugins" — blanket permissions are the failure mode we are explicitly fixing.

### Option B: Capability negotiation at runtime
- Rejected. Unreviewable statically: a plugin is approved once and then can ask for arbitrary new capabilities later. The admin would face a stream of mid-runtime prompts, which everyone clicks through. Static review (doc 02 §10.4) requires the capability set to be settled at install time.

### Option C: Per-call ACLs (finer than capability-level)
- Rejected. Performance cost: every host ABI call would consult a policy engine. The host ABI is on the hot path of every hook dispatch (millions of calls per minute under load). Capability-level checks are constant-time signature verifications; per-call ACLs are not.

### Option D: Capability-free model with sandboxing at the syscall level
- Rejected. WebAssembly itself does that — no syscalls, no network, no FS. The problem is that the *host imports* the plugin needs are themselves capabilities. We need to gate which imports each plugin can call. Capability tokens are how.

### Option E: Open capability vocabulary (plugins can declare new capability names)
- Rejected. Defeats static review and admin approval — the admin cannot evaluate a capability they have never heard of. Adding a capability is a core change.

### Option F: Token-less capability checks (look up in a per-plugin permission table on every call)
- Considered. Equivalent in security but worse in performance (per-call DB or KV lookup vs constant-time signature check) and worse in invalidation (revoking a capability is a DB write, not a token-not-reissue). Doc 02 §6.1 picks tokens.

## References

- Design doc: `docs/02-plugin-system.md` §6 (host ABI capabilities — full table, scopes, tokens)
- Design doc: `docs/02-plugin-system.md` §6.1 (capability tokens), §6.2 (`db.read`/`db.write`), §6.6 (`cache.invalidate`), §6.7 (`secrets`), §6.8 (what plugins cannot do)
- Design doc: `docs/02-plugin-system.md` §10 (security model)
- Related ADRs: ADR 0005 (WASM runtime), ADR 0010 (Asynq enqueue gated by `queue` capability), ADR 0011 (`cache.invalidate` capability for plugin invalidations)
