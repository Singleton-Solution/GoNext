# 06 — Auth & Permissions

> Owner: Auth subsystem. Depends on [`00-architecture-overview.md`](00-architecture-overview.md). Cross-refs: [`02-plugin-system.md`](02-plugin-system.md) (plugin capabilities — distinct concept), [`05-admin-api.md`](05-admin-api.md) (route surface).
>
> Reader: senior engineer doing a security review. Assume familiarity with OWASP ASVS, OAuth2/OIDC, and WordPress's `roles/capabilities` model.

This document specifies **authentication** (proving who you are) and **authorization** (deciding what you can do) for the system. It does **not** cover plugin sandboxing — that belongs to `02-plugin-system.md` — but it does specify the interface between user identity and plugin-scoped capabilities (§14).

---

## 1. Goals & Non-Goals

### Goals

- **WordPress-shaped authorization model** so the mental model is portable: roles bundle capabilities, capabilities gate actions, plugins can register new capabilities. Object-level checks (`current_user_can('edit_post', 42)`) work the same way.
- **Modern auth UX**: passkeys (WebAuthn) as a first-class option alongside passwords, magic-link login, TOTP 2FA, OAuth/OIDC SSO.
- **Secure by default**: argon2id passwords, server-side opaque sessions in Redis, CSRF protection, rate-limiting, audit log.
- **Revocable**: every authentication artefact (session, PAT, OAuth grant) is revocable centrally without a token rotation.
- **Auditable**: every privileged action is logged with actor, target, IP, user-agent, timestamp.
- **Extensible**: plugins can add capabilities and consult the policy engine; they cannot bypass it.

### Non-Goals (v1)

- SAML / enterprise SSO. Defer to v2 (OIDC covers most cases; SAML can be added through the same pluggable provider interface).
- Multi-tenant user isolation. Single tenant in v1 (see overview §7).
- ABAC / policy-as-code DSL (Rego/Cedar). We evaluate this in §-Trade-offs and reject for v1.
- Federated identity standards beyond OIDC (no SCIM, no LDAP in core; plugins can add).
- "Bring-your-own-IdP" tenant configuration (related to multi-tenant).

---

## 2. User Model

### 2.1 Identity attributes

A `User` is the principal that holds capabilities. Identity is **email-centric** (every user must have a verified email) but a separate **handle** (username/slug) is exposed publicly for author URLs (`/author/{handle}`). Email is the canonical login identifier; handle is for display and URL routing.

This is a deliberate departure from WP, where `user_login` is the login identifier. The rationale:

- Emails are how people remember accounts; collisions on usernames are a real onboarding annoyance.
- WP's username-only login encourages username harvesting attacks (login page reveals which usernames exist when paired with bad rate-limiting).
- Email-first plays well with OIDC: the identity assertion from Google/GitHub is an email + sub.
- Handle decoupled from login means a user can change their public handle without losing identity.

For migration parity, the WP importer maps `user_login` → `handle` and `user_email` → `email`. If two WP users somehow share an email (rare but possible), the importer flags the conflict and asks the operator.

### 2.2 Postgres schema

```sql
-- Core identity row. One row per human (or service principal — see §5.2).
CREATE TABLE users (
    id              BIGSERIAL PRIMARY KEY,
    uuid            UUID NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    email           CITEXT NOT NULL UNIQUE,          -- case-insensitive
    email_verified_at TIMESTAMPTZ,                   -- NULL = unverified
    handle          CITEXT NOT NULL UNIQUE,          -- 3-30 chars [a-z0-9_-]
    display_name    TEXT NOT NULL DEFAULT '',
    bio             TEXT NOT NULL DEFAULT '',
    avatar_media_id BIGINT REFERENCES media(id),     -- nullable; falls back to gravatar/initials
    locale          TEXT NOT NULL DEFAULT 'en-US',   -- BCP-47
    timezone        TEXT NOT NULL DEFAULT 'UTC',     -- IANA tz name
    status          TEXT NOT NULL DEFAULT 'active'   -- active | suspended | deactivated | deleted
                    CHECK (status IN ('active','suspended','deactivated','deleted')),
    is_service      BOOLEAN NOT NULL DEFAULT FALSE,  -- §5.2: machine principal
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ                       -- soft-delete; hard delete handled by GDPR job
);

CREATE INDEX users_status_idx ON users (status) WHERE deleted_at IS NULL;

-- Password material is split from the identity row.
-- One row per active credential; rotating a password inserts a new row.
-- Old rows kept (with revoked_at) for compromise audits; pruned by retention job.
CREATE TABLE user_passwords (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    algorithm       TEXT NOT NULL,                   -- 'argon2id'
    params          JSONB NOT NULL,                  -- {memory, iterations, parallelism, salt_b64, hash_b64}
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at      TIMESTAMPTZ
);
CREATE INDEX user_passwords_active_idx
    ON user_passwords (user_id) WHERE revoked_at IS NULL;

-- TOTP and recovery codes
CREATE TABLE user_totp (
    user_id         BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret_enc      BYTEA NOT NULL,                  -- AES-GCM encrypted with KMS key
    enabled_at      TIMESTAMPTZ NOT NULL,
    last_used_step  BIGINT NOT NULL DEFAULT 0        -- replay protection
);

CREATE TABLE user_recovery_codes (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash       BYTEA NOT NULL,                  -- argon2id of the code
    used_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX user_recovery_codes_user_idx ON user_recovery_codes (user_id);

-- WebAuthn / passkeys
CREATE TABLE user_webauthn_credentials (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id   BYTEA NOT NULL UNIQUE,           -- raw, base64url in API
    public_key      BYTEA NOT NULL,                  -- COSE_Key
    sign_count      BIGINT NOT NULL DEFAULT 0,
    aaguid          UUID,
    transports      TEXT[] NOT NULL DEFAULT '{}',    -- usb, nfc, ble, internal, hybrid
    nickname        TEXT NOT NULL DEFAULT '',        -- user-supplied "MacBook Touch ID"
    backup_eligible BOOLEAN NOT NULL DEFAULT FALSE,
    backup_state    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at    TIMESTAMPTZ
);

-- OAuth/OIDC linked identities (Google, GitHub, generic OIDC)
CREATE TABLE user_external_identities (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider        TEXT NOT NULL,                   -- 'google', 'github', 'oidc:<configured-name>'
    subject         TEXT NOT NULL,                   -- IdP's stable sub
    email_at_link   CITEXT,                          -- email the IdP reported when linked
    raw_profile     JSONB NOT NULL DEFAULT '{}',
    linked_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (provider, subject)
);
```

Notes:

- `CITEXT` for `email` and `handle` gives case-insensitive uniqueness without lower() function indexes everywhere.
- Soft-delete via `deleted_at`: account stays referenceable for audit logs and content authorship attribution; hard-delete is a separate GDPR pipeline (§16) that anonymizes references.
- Password is its own table, never on `users`. This means a routine `SELECT * FROM users` cannot accidentally leak hashes to logs or API responses.
- `is_service = TRUE` rows are machine principals — they have a row in `users` so the same policy engine applies to them, but they have no password row and authenticate via machine tokens only (§5.2).
- Avatar: we store an `avatar_media_id` pointing at our own media table. If null, the renderer falls back to Gravatar (if the user opted in) or to an initials avatar generated server-side. A separate `users.gravatar_enabled` flag may be added later.

### 2.3 Email vs handle login

The login form accepts a single "Email or username" field. Server resolves: if the input contains `@`, it's an email lookup; otherwise a handle lookup. This is convenience-only — both resolve to the same user row. The OIDC `preferred_username` claim, if present, never becomes a login identifier (avoids account-takeover via IdP username collisions).

---

## 3. Password Storage

### 3.1 Algorithm

**Argon2id** with these defaults (calibrated to ~500ms on a baseline 2-core cloud VM):

| Parameter | Value | Notes |
|---|---|---|
| `memory` | 64 MiB | OWASP minimum is 19 MiB; we go higher because admin login is rare. |
| `iterations` | 3 | |
| `parallelism` | 2 | |
| `salt` | 16 bytes, CSPRNG | Per password. |
| `hash length` | 32 bytes | |

The PHC-string format is used internally (`$argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>`) but stored decomposed in `user_passwords.params` (JSONB) for queryability ("find users on old params"). The `algorithm` column allows future migration to argon2id-with-different-params or, if necessary, a new algorithm entirely.

### 3.2 Migration on parameter change

When defaults are tightened, we do **not** force re-hash. We re-hash **on next successful login**:

1. User submits password. Verify against stored params.
2. If verification succeeds AND stored params != current defaults, re-hash with current defaults.
3. Insert new `user_passwords` row, mark old as revoked.

This is a well-known pattern; it bounds re-hash work by login frequency rather than user count. A background job pings dormant users to log in if their params are dangerously stale (e.g., 18+ months on old params).

### 3.3 Pepper

A server-side **pepper** (32-byte secret from env/KMS) is HMAC-mixed into the salt before hashing. This means a stolen database without the pepper cannot be brute-forced. The pepper is rotated via a generations table; on rotation we re-pepper opportunistically (next login) the same way params migrate.

```
pepper_input = HMAC-SHA256(pepper, email || ":" || salt_raw)
argon_salt   = pepper_input  // 32 bytes, used as the argon2id salt
```

We deliberately do not HMAC the password itself, to avoid leaking timing/length signals. Mixing the pepper into the salt makes it cryptographically irrelevant which we pick — the argon2id output is the same — but the structure means the pepper is bound to the per-user salt and not reusable across DBs.

### 3.4 Password policy

- Min length 12, no max under 128. **No** complexity rules ("must have uppercase + digit + symbol"): NIST 800-63B is explicit that these reduce security.
- Reject against a **breach corpus** (HaveIBeenPwned k-anonymity API at signup/change time; cache the first 5 chars locally for the popular passwords list).
- Reject password == email, password == handle, password ∈ top-10k weakpasswords list.

---

## 4. Authentication Methods

The system supports five authentication factor sources, composable into multi-step flows:

| Method | Strength | Default in v1 | Notes |
|---|---|---|---|
| Email + password | Knowledge | Yes | Primary path; passkey is the upgrade target. |
| WebAuthn / passkey | Possession | **Optional in v1**, default in v2 | First-class but UX maturity varies. |
| Magic link | Possession (email) | Yes | Single-tap recovery; **not** sufficient alone for admin/super-admin (see §4.3). |
| OAuth / OIDC | Federated | Yes (Google, GitHub, generic) | Configurable per install. |
| TOTP | Possession | Yes (as 2nd factor) | Recovery codes accompany. |

### 4.1 Email + password

Standard flow, with these specifics:

- The password is **never** received via GET. POST only. The form submission is over HTTPS with HSTS (preload list eligible).
- Response on failure is identical for "user does not exist", "password is wrong", "account is locked" — to the millisecond, modulo unavoidable jitter. Specifically: we always perform an argon2id verification, against the stored hash if the user exists or against a dummy hash if not. The dummy hash is generated at process start with current params.
- On success: emit a session (§5) and audit-log `auth.login.success` with method `password`.

### 4.2 WebAuthn / passkeys

We support resident (discoverable) credentials, so passkey users can log in **without typing their email** — the browser presents available credentials, the server resolves the user from the credential ID.

Library: [`go-webauthn/webauthn`](https://github.com/go-webauthn/webauthn). RP ID is the eTLD+1 of the install. We allow cross-device authentication via the hybrid transport (QR-code passkey from phone to desktop).

**Required for v1?** No — strongly recommended, but not required. Reasoning: a meaningful fraction of self-hosters will deploy on `.local` or IP-only setups where WebAuthn won't work without HTTPS and a proper hostname. Forcing passkeys would lock them out of their own admin. We make passkeys discoverable in onboarding and prompt at first admin login.

Counter values (`sign_count`) are tracked. A decrease relative to the stored counter is flagged as **potentially cloned credential**: we don't block the login (some authenticators legitimately reset), but we trigger an `auth.webauthn.counter_regression` audit event and a Critical email to the user.

### 4.3 Magic link

User enters email; we generate a single-use, 15-minute, 256-bit token and email a link. Token format: `<base64url-20-bytes>.<base64url-12-bytes-mac>` where the MAC is keyed with a server secret and binds the token to the user ID and issue time. The DB stores `(token_id, user_id, expires_at, used_at)`; the token itself is not stored — only its `token_id` (the first 20 bytes).

**Constraint**: magic-link login alone is **not sufficient** for a user holding the `manage_options` or any `manage_*` capability (i.e., admins/super-admins). Magic link counts as one factor (something you have: control of the inbox); for admin sessions we require an additional factor (passkey, TOTP, or password). This is the [BeyondCorp principle](https://research.google/pubs/pub43231/) applied locally: privileged action requires evidence of intent, not just inbox access.

Rate-limiting: per email, 3 magic links per 15 minutes; per IP, 30 per 15 minutes.

### 4.4 OAuth / OIDC

Configurable providers:

- **Built-in**: Google, GitHub (each as its own OAuth2 client).
- **Generic OIDC**: any provider with a discovery doc (`/.well-known/openid-configuration`). Operator pastes issuer URL, client ID, client secret in admin; we fetch metadata.
- **Pluggable**: a plugin can register a new provider via the `auth.providers` hook. The plugin supplies metadata + a `verify` callback. The plugin runs in WASM so its `verify` function operates on host-supplied claims; it cannot read raw secrets (the host signs/exchanges, plugin only validates business logic).

OIDC code flow with PKCE (state nonce, replay-resistant). On success:

1. Find or create a `user_external_identities` row by `(provider, sub)`.
2. If existing → log in as that user.
3. If new and the IdP-asserted email matches an existing local user (and that email is verified), we **do not auto-link**. We send an email saying "Someone tried to link a Google account; click here to confirm." This blocks IdP-spoofed account takeover.
4. If new and no matching local user → create one. Email is marked verified iff the IdP marked it verified.

Linking another provider to an already-logged-in account is allowed and one-click.

### 4.5 2FA (TOTP + recovery codes)

TOTP per RFC 6238: SHA-1 (for compatibility with all authenticator apps), 30-second step, 6 digits. Drift: ±1 step accepted. `last_used_step` prevents replay within a step.

Enrollment:

1. User clicks "Enable 2FA".
2. Server generates 20-byte secret, encrypts with KMS key, stores in `user_totp` with `enabled_at = NULL`.
3. User sees QR code (`otpauth://totp/...`) + base32 secret.
4. User submits a verification code → server checks → sets `enabled_at = NOW()` and shows **10 recovery codes** (one-time, single-use, 10-char alphanumeric). Codes are hashed (argon2id) at storage.

Disabling 2FA requires re-authenticating with the current password (or passkey).

### 4.6 Sequence diagram — login with 2FA

```
Browser                         Go API                        Redis / Postgres
   │ POST /auth/login              │                                │
   │ {email, password}             │                                │
   │──────────────────────────────▶│                                │
   │                               │  rate-limit check (per IP+email)
   │                               │───────────────────────────────▶│
   │                               │  lookup user, fetch password   │
   │                               │◀───────────────────────────────│
   │                               │  argon2id verify (constant)    │
   │                               │  if fail → audit + 401         │
   │                               │  has TOTP enabled? yes         │
   │                               │  issue pre-session (60s, redis)│
   │                               │  with claim {user_id, step:2fa}│
   │                               │───────────────────────────────▶│
   │ 200 {challenge: "totp",       │                                │
   │      pre_session_token}       │                                │
   │◀──────────────────────────────│                                │
   │                               │                                │
   │ POST /auth/2fa/totp           │                                │
   │ {pre_session_token, code}     │                                │
   │──────────────────────────────▶│                                │
   │                               │  validate pre_session          │
   │                               │  verify TOTP (replay-checked)  │
   │                               │  delete pre_session            │
   │                               │  CREATE session (§5)           │
   │                               │───────────────────────────────▶│
   │                               │  audit auth.login.success      │
   │ 200 Set-Cookie: sid=<opaque>; │                                │
   │     HttpOnly; Secure;         │                                │
   │     SameSite=Lax              │                                │
   │◀──────────────────────────────│                                │
```

The `pre_session_token` is a 256-bit random value bound in Redis to `{user_id, factors_satisfied: ["password"], expires: 60s}`. It is **not** a session; it cannot authorize any non-auth route. This intermediate state is what lets us split the flow over two HTTP requests without leaving partially-authenticated state in a real session.

### 4.7 Sequence diagram — OIDC

```
Browser                  Go API                  IdP                     DB
   │ GET /auth/oidc/google  │                       │                       │
   │───────────────────────▶│                       │                       │
   │                        │ generate state, nonce │                       │
   │                        │ PKCE code_verifier    │                       │
   │                        │ store in short-lived  │                       │
   │                        │ cookie + redis (5min) │                       │
   │ 302 → IdP authz URL    │                       │                       │
   │◀───────────────────────│                       │                       │
   │ ───────────────────────────────────────────▶  │                       │
   │   user consents at IdP                         │                       │
   │ ◀───────────────────────────────────────────  │                       │
   │ GET /auth/oidc/google/callback?code=…&state=…  │                       │
   │───────────────────────▶│                       │                       │
   │                        │ check state, fetch    │                       │
   │                        │ PKCE verifier         │                       │
   │                        │ POST code → IdP token │                       │
   │                        │──────────────────────▶│                       │
   │                        │◀──────────────────────│                       │
   │                        │ verify id_token       │                       │
   │                        │  (sig, nonce, aud,    │                       │
   │                        │   exp, iss)           │                       │
   │                        │ find/create user     ─┼──────────────────────▶│
   │                        │ (per §4.4 rules)     ─┼──────────────────────▶│
   │                        │ CREATE session        │                       │
   │ 302 → /admin           │                       │                       │
   │ Set-Cookie: sid=…      │                       │                       │
   │◀───────────────────────│                       │                       │
```

---

## 5. Sessions

### 5.1 Browser sessions (the admin and the site)

**Opaque, server-side, Redis-backed.** No JWTs for browser sessions.

- The cookie value (`sid`) is a 256-bit random token, base64url-encoded.
- Redis stores `session:<sid_hash>` (we hash the cookie value with SHA-256 before storing as a key — if Redis dumps leak, the keys can't be replayed). Value is a JSON blob:

```json
{
  "user_id": 17,
  "created_at": "...",
  "last_seen_at": "...",
  "ip_first": "203.0.113.7",
  "ip_last": "203.0.113.7",
  "user_agent": "Mozilla/5.0 ...",
  "device_label": "MacBook · Chrome",
  "factors": ["password", "totp"],
  "csrf_token": "<32B base64>",
  "impersonator_user_id": null
}
```

- Cookie attributes: `HttpOnly; Secure; SameSite=Lax; Path=/`. We use `SameSite=Lax` (not Strict) because Strict breaks "user clicks an emailed link to the admin and is unexpectedly logged out" — a common WP grievance. Top-level GETs over Lax are still sent, which is what we want.
- Expiry: rolling 30 days idle, 90 days absolute, configurable per install.
- One `Set-Cookie` per origin: `<install>.com` for the public site and admin both. If admin is on a subdomain (`admin.<install>.com`), the cookie scope is the eTLD+1 with a flag we use to gate routes by audience.

### 5.2 Why opaque, not JWT?

| Concern | Opaque (Redis) | JWT |
|---|---|---|
| Revocation | Instant (`DEL`). | Hard — needs blacklist (which is a Redis lookup anyway, defeating the point). |
| Update of claims (role change, etc.) | Trivial. | Either token rotates or claims go stale. |
| Stolen-token replay window | Bounded by revoke. | Until exp. |
| Server-side state needed | Yes, Redis. | "None", but in practice always Redis. |
| Stateless horizontal scale | Redis is a network hop. | One less network hop. |
| Crypto complexity | None. | Sig algo choice, key rotation, kid handling. |

For a CMS where admins fire admins, lock out users, and need "log me out everywhere" to work right now, opaque wins.

We **do** use signed/JWT tokens for stateless service-to-service API access (§5.4) where revocation is acceptable to delay by the TTL.

### 5.3 Session metadata & "Where you're logged in"

Each session row carries `device_label` derived from User-Agent at creation (we ship a small UA parser table; libraries like `mssola/user_agent` are fine but we prefer a curated mapping to avoid surprise on weird UAs). The admin page `/me/sessions` lists active sessions with: device, browser, IP (CIDR-truncated for display: `203.0.113.0/24` shown, full IP visible only on hover), last seen, current?, and a "Revoke" button.

A "Log out everywhere" button revokes all sessions including the current one. Revoke = `DEL session:<sid_hash>` in Redis + insert into `revoked_sessions` (for audit). The session middleware checks Redis first; if absent the cookie is cleared.

### 5.4 API tokens

Three distinct kinds, **stored in separate tables** so we never confuse a PAT with a machine token in code:

```sql
-- Personal access tokens (user-scoped, replace cookie for CLI/scripts)
CREATE TABLE personal_access_tokens (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,                   -- user-supplied "deploy script"
    token_prefix    TEXT NOT NULL,                   -- first 8 chars, for display
    token_hash      BYTEA NOT NULL,                  -- SHA-256 of full token
    scopes          TEXT[] NOT NULL,                 -- ['read:posts', 'write:posts']
    last_used_at    TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ,                     -- optional
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at      TIMESTAMPTZ
);
CREATE INDEX pat_hash_idx ON personal_access_tokens (token_hash) WHERE revoked_at IS NULL;

-- Machine tokens (service principal, service-to-service)
CREATE TABLE machine_tokens (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id),  -- must be is_service = true
    name            TEXT NOT NULL,
    token_prefix    TEXT NOT NULL,
    token_hash      BYTEA NOT NULL,
    scopes          TEXT[] NOT NULL,
    allowed_cidrs   CIDR[] NOT NULL DEFAULT '{}',    -- empty = any
    expires_at      TIMESTAMPTZ,
    last_used_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at      TIMESTAMPTZ
);

-- OAuth2 grants (third-party app acting on behalf of user)
CREATE TABLE oauth_clients (
    id              BIGSERIAL PRIMARY KEY,
    client_id       TEXT NOT NULL UNIQUE,
    client_secret_hash BYTEA,                         -- NULL for public clients
    name            TEXT NOT NULL,
    redirect_uris   TEXT[] NOT NULL,
    allowed_scopes  TEXT[] NOT NULL,
    created_by_user_id BIGINT REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE oauth_grants (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_id       BIGINT NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    scopes          TEXT[] NOT NULL,
    refresh_token_hash BYTEA,                         -- rotated
    access_token_hash  BYTEA,
    access_expires_at  TIMESTAMPTZ,
    refresh_expires_at TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

**Token format**: `gonext_pat_<43-char-base64url>` (32 bytes). The prefix is purposeful — secret scanners on GitHub/GitLab match it, and we can quickly tell "is this a token?" in support tickets. The `token_prefix` column stores `gonext_pat_xxxxxxxx` (first 8 of the body) for showing in the UI ("token starts with xxxxxxxx"). The full token is shown **once** at creation; thereafter only the hash is stored.

**Scopes** are a flat list of strings. Same vocabulary as capabilities (see §6) but namespaced: `read:posts`, `write:posts`, `read:users`, `manage:plugins`, etc. The OAuth consent screen explains each scope in human language pulled from the capability registry.

---

## 6. Authorization Model

This is the WP-shaped layer. The data is in three tables:

```sql
CREATE TABLE roles (
    id              BIGSERIAL PRIMARY KEY,
    slug            TEXT NOT NULL UNIQUE,            -- 'editor'
    name            TEXT NOT NULL,                   -- 'Editor'
    is_builtin      BOOLEAN NOT NULL DEFAULT FALSE,
    description     TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE capabilities (
    id              BIGSERIAL PRIMARY KEY,
    slug            TEXT NOT NULL UNIQUE,            -- 'edit_others_posts'
    description     TEXT NOT NULL DEFAULT '',
    is_builtin      BOOLEAN NOT NULL DEFAULT FALSE,
    registered_by_plugin TEXT,                       -- plugin slug or NULL for core
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE role_capabilities (
    role_id         BIGINT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    capability_id   BIGINT NOT NULL REFERENCES capabilities(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, capability_id)
);

CREATE TABLE user_roles (
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id         BIGINT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    granted_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    granted_by      BIGINT REFERENCES users(id),
    PRIMARY KEY (user_id, role_id)
);

-- Direct capability grants (rare, for edge cases). WP allows this and we mirror it.
CREATE TABLE user_capabilities (
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    capability_id   BIGINT NOT NULL REFERENCES capabilities(id) ON DELETE CASCADE,
    granted_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    granted_by      BIGINT REFERENCES users(id),
    PRIMARY KEY (user_id, capability_id)
);
```

### 6.1 Built-in roles & capabilities

Roles, in increasing privilege (the WP set, lightly normalized):

| Role | Description |
|---|---|
| `subscriber` | Read-only logged-in user. Can comment, manage their profile. |
| `contributor` | Can author drafts but not publish. |
| `author` | Can publish and manage their own posts. |
| `editor` | Can publish and manage all posts and pages. |
| `admin` | Site-level admin: settings, plugins, themes, users. |
| `super_admin` | Reserved for v2 multisite — in v1, identical to `admin` plus has `manage_install` (can write to filesystem, run migrations). |

<!-- fixed per review (C12): role slugs normalized to lowercase singular form across the design (`subscriber`, `contributor`, `author`, `editor`, `admin`, `super_admin`). Replaces the prior `administrator` slug. Doc 05 and doc 08 reference these same slugs. -->


Built-in capabilities (non-exhaustive; the full list is generated from the registry at compile time):

- Content: `read`, `read_private_posts`, `edit_post`, `edit_posts`, `edit_published_posts`, `edit_others_posts`, `edit_private_posts`, `delete_post`, `delete_posts`, `delete_published_posts`, `delete_others_posts`, `publish_posts`.
- Pages: corresponding `*_pages` set (separate from posts).
- Taxonomies: `manage_categories`, `manage_tags`, plus per-taxonomy `manage_{tax}`.
- Media: `upload_files`, `edit_others_media`.
- Users: `list_users`, `create_users`, `edit_users`, `delete_users`, `promote_users`.
- Site: `manage_options`, `manage_install`.
- Plugins/themes: `install_plugins`, `activate_plugins`, `manage_plugin_settings`, `install_themes`, `switch_themes`, `edit_themes`.
- Comments: `moderate_comments`, `edit_comment`.

WP's `_others_`, `_published_`, `_private_` capability variants are kept verbatim — they encode the most common object-level dimensions (ownership, publication state) directly in the capability name, which makes the **meta-capability** mapping (§6.3) simple.

### 6.2 Plugin-registered capabilities

A plugin can register **user-facing** capabilities through its `manifest.json` (the canonical plugin manifest format — see [`02-plugin-system.md`](02-plugin-system.md) §2.2):

```json
{
  "slug": "gn-forms",
  "name": "WPC Forms",
  "version": "1.0.0",
  "abi_version": "1",

  "grants_capabilities": [
    { "slug": "manage_forms", "description": "Create, edit, delete, and view form submissions." },
    { "slug": "export_forms", "description": "Download form submission data as CSV." }
  ]
}
```

<!-- fixed per review (P1, C6): manifest format is `manifest.json` (canonical per doc 02). The earlier `plugin.toml` form was an invention of this doc and is removed. Two vocabularies share the same manifest in two different slots: the top-level `capabilities` object holds SANDBOX permissions (what the WASM runtime allows the plugin to do — db reads, http fetch, etc.) and `grants_capabilities` holds USER capabilities the plugin registers (added to the role/capability system here). The distinction is enforced in §14. -->

On plugin install, the entries under `grants_capabilities` become rows in `capabilities` with `registered_by_plugin = "<plugin-slug>"`. The installer then asks the operator: "Which roles should hold these capabilities?" — defaults to `admin` only. Plugin uninstall removes the capability rows (which cascades through `role_capabilities` and `user_capabilities`).

This is the **user-facing** capability registration. It is distinct from the plugin's **own** sandbox capabilities (what the plugin's WASM code can do — db access, network, etc.), which live in the manifest's top-level `capabilities` object and are governed by the plugin runtime ([`02-plugin-system.md`](02-plugin-system.md) §6). The grammar there is dotted-and-scoped: `db.read`, `db.write`, `http.fetch`, with scopes carried as string arrays inside the capability's value object — e.g., `{"db": {"read": ["core.posts", "plugin.tables"]}, "http": {"fetch": {"allow_hosts": ["api.example.com"]}}}`. See §14 for the interaction; see [`02-plugin-system.md`](02-plugin-system.md) §6 for the host ABI capability reference.

#### 6.2.1 Plugin-defined CPT capabilities

When a plugin registers a custom post type via the `cpt.register` host ABI (see [`02-plugin-system.md`](02-plugin-system.md)), the registration payload includes:

- `capability_type` — `post` (inherits the standard post family `edit_post`/`edit_posts`/`publish_posts`/...) **or** a new prefix like `book`. When a new prefix is supplied, the host auto-derives the standard family from it: `edit_book`, `edit_books`, `edit_others_books`, `edit_published_books`, `edit_private_books`, `delete_book`, `delete_books`, `delete_others_books`, `delete_published_books`, `delete_private_books`, `publish_books`, `read_book`, `read_private_books`.
- `capabilities` — optional explicit mapping `{edit, edit_others, publish, read, delete, delete_others, edit_published, edit_private, delete_published, delete_private, read_private}` that overrides the auto-derived names. (For example, a CPT can map `publish` to an existing `publish_posts` capability so editors automatically have permission.)

The CPT registration schema (the columns of `post_types` and how `capability_type`/`capabilities` are persisted) is owned by [`01-core-cms.md`](01-core-cms.md) §1.3. Plugins are responsible for declaring these on registration; the host materializes the resulting capability slugs into the `capabilities` table with `registered_by_plugin = "<plugin-slug>"` and assigns them to `admin` by default. Operators promote them to other roles via the same UI as `grants_capabilities`.

**Capability check at request time.** `current_user_can('edit_book', book_id)` resolves like this:

1. Resolve the post by id; read its `post_type` row.
2. Read the post type's `capability_type` and `capabilities` map.
3. Compute the primitive capability for the (action, status, ownership) triple via the same meta-cap mapping as §6.3 — using the CPT's `capabilities` map when present, otherwise the auto-derived `{capability_type}` family.
4. Run `policy.Can` with that primitive capability.

This keeps the check identical in shape to `edit_post`; the only thing CPTs change is the **name** of the primitive capability the mapping yields. Plugin uninstall cascades through `role_capabilities` and `user_capabilities` for any CPT-derived capabilities the plugin registered.

### 6.3 Meta capabilities & object-level checks

WP has the concept of **meta capabilities** (`edit_post` with a specific post ID), which the system maps to a **primitive capability** (`edit_posts`, `edit_others_posts`, etc.) based on the object. We do the same, but in Go:

```go
// Pseudocode: meta-cap mapping for 'edit_post'.
func mapEditPost(ctx, user, postID) (primitive string, ok bool) {
    p, err := posts.Get(ctx, postID)
    if err != nil { return "", false }

    switch {
    case p.Status == "trash":
        // Trashed posts: must have delete_post primitive on it.
        return mapDeletePost(ctx, user, postID)
    case p.AuthorID == user.ID && p.Status == "draft":
        return "edit_posts", true
    case p.AuthorID == user.ID && p.Status == "publish":
        return "edit_published_posts", true
    case p.AuthorID == user.ID && p.Status == "private":
        return "edit_private_posts", true
    case p.AuthorID != user.ID && p.Status == "publish":
        return "edit_others_posts", true
    // ... and so on
    }
}
```

The `Can(user, "edit_post", postID)` call always goes through this mapping. Plugins can override the mapping via the `map_meta_cap` filter (matching WP's `map_meta_cap` hook), but the host applies a safety policy: a plugin **cannot grant** a capability the user wouldn't otherwise have unless the plugin is allowed `policy.grant` permission (rare). It can only **deny** or further restrict.

---

## 7. Policy Implementation in Go

### 7.1 Package layout

```
internal/auth/
├── identity/         // users, passwords, external identities
├── session/          // cookie sessions, redis store
├── token/            // PAT, machine tokens, OAuth
├── webauthn/         // passkey routines
├── totp/             // 2FA
├── ratelimit/        // login + token introspection limits
└── policy/           // <-- this section
    ├── policy.go     // Can() entry point + types
    ├── registry.go   // capability + meta-cap registration
    ├── post.go       // PostPolicy
    ├── user.go       // UserPolicy
    ├── plugin.go     // PluginPolicy
    └── middleware.go // HTTP middleware
```

### 7.2 Core types

```go
package policy

// Principal is whatever is acting. Most often a *User, but plugin code runs
// with a PluginPrincipal that is NOT a user (§14).
type Principal interface {
    ID() string                // stable string ID (user:17, plugin:contact-form)
    Capabilities() Capabilities // resolved set; lazy-loaded once per request
    IsService() bool
}

type Capabilities interface {
    Has(slug string) bool
    All() []string
}

// Decision is the result of a policy evaluation. It always carries a reason.
type Decision struct {
    Allowed bool
    Reason  string                 // human-readable; safe to surface
    Code    string                 // machine-readable: 'missing_cap', 'wrong_owner', etc.
    NeededCapability string        // optional, for UX prompts
}

// The single entry point.
//
// action  is a capability slug ('edit_post', 'install_plugins'), possibly a meta-cap.
// resource is optional; required for object-level checks.
//
// Implementations live in per-resource policy files (PostPolicy, UserPolicy, etc.)
// and are registered with the registry at startup.
func Can(ctx context.Context, p Principal, action string, resource any) Decision
```

The `Can` function:

1. Resolves a `PolicyFn` from the registry by action name.
2. If a registered policy exists, calls it with `(ctx, p, resource)`.
3. If none, falls back to a primitive check: does `p` hold the capability `action`? (For actions like `manage_options` that don't need object context, this is the whole story.)
4. Applies the `map_meta_cap` filter chain from registered plugins.
5. Returns a `Decision`.

The decision is **always** computed; we never silently allow. A missing policy registration for a known capability is a startup panic.

### 7.3 A concrete policy

```go
// internal/auth/policy/post.go
package policy

func init() {
    Register("edit_post", evalEditPost)
    Register("publish_post", evalPublishPost)
    Register("delete_post", evalDeletePost)
    Register("read_post", evalReadPost)
}

func evalEditPost(ctx context.Context, p Principal, resource any) Decision {
    post, ok := resource.(*content.Post)
    if !ok {
        return Decision{Code: "bad_resource", Reason: "edit_post needs a *Post"}
    }
    caps := p.Capabilities()

    isOwner := postOwnedBy(post, p)
    switch {
    case post.Status == content.StatusTrashed:
        return evalDeletePost(ctx, p, resource)  // delegated

    case isOwner && post.Status == content.StatusDraft:
        if caps.Has("edit_posts") {
            return Decision{Allowed: true, Reason: "owns draft and has edit_posts"}
        }
        return Decision{Allowed: false, Code: "missing_cap",
            NeededCapability: "edit_posts",
            Reason: "you need edit_posts to edit your draft"}

    case isOwner && post.Status == content.StatusPublished:
        if caps.Has("edit_published_posts") {
            return Decision{Allowed: true, Reason: "owns published and has edit_published_posts"}
        }
        return Decision{Allowed: false, Code: "missing_cap",
            NeededCapability: "edit_published_posts",
            Reason: "you can't edit your already-published posts"}

    case !isOwner:
        if caps.Has("edit_others_posts") {
            // Even with edit_others_posts, you still need the status-specific cap.
            if post.Status == content.StatusPublished && !caps.Has("edit_published_posts") {
                return Decision{Allowed: false, Code: "missing_cap",
                    NeededCapability: "edit_published_posts",
                    Reason: "you can edit others' content but not when published"}
            }
            if post.Status == content.StatusPrivate && !caps.Has("edit_private_posts") {
                return Decision{Allowed: false, Code: "missing_cap",
                    NeededCapability: "edit_private_posts"}
            }
            return Decision{Allowed: true, Reason: "has edit_others_posts"}
        }
        return Decision{Allowed: false, Code: "missing_cap",
            NeededCapability: "edit_others_posts",
            Reason: "this is someone else's post"}
    }
    return Decision{Allowed: false, Code: "default_deny", Reason: "no rule matched"}
}

func postOwnedBy(p *content.Post, pr Principal) bool {
    u, ok := pr.(*identity.User)
    if !ok { return false }
    return p.AuthorID == u.ID
}
```

### 7.4 Middleware

```go
// internal/auth/policy/middleware.go
package policy

// Require returns middleware that enforces a primitive capability before
// hitting the handler. For object-level checks, the handler still has to
// call Can() with the resource.
func Require(action string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            p, ok := PrincipalFrom(r.Context())
            if !ok {
                http.Error(w, "unauthorized", http.StatusUnauthorized)
                return
            }
            d := Can(r.Context(), p, action, nil)
            if !d.Allowed {
                writeDenied(w, d) // 403 with d.Reason
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

Two-layer enforcement:

1. **Route-level** (`policy.Require("edit_posts")`) — fast fail for principals who can't possibly perform the action. Catches obviously unauthorized callers before any DB load.
2. **Service-level** — inside the handler/service, after loading the target resource, call `policy.Can(ctx, p, "edit_post", post)`. The service layer is where object-level checks live.

This is the only sanctioned pattern. Scattering ad-hoc checks (`if user.Role != "admin"`) anywhere else is forbidden by lint rule (custom go-analyzer rejects role-comparison expressions outside the `policy` package).

### 7.5 Capability resolution & caching

When a request authenticates, the auth middleware:

1. Loads the user (one query).
2. Loads roles + direct capabilities in a single query joining `user_roles`, `role_capabilities`, `user_capabilities`, `capabilities`.
3. Materializes the cap set as a `map[string]struct{}` and stashes it on the `Principal`.

Cap set is cached per-session in Redis (key `caps:<user_id>:<generation>`), TTL 5 minutes. A `caps_generation` integer on each user is bumped on any role/cap change; the next request re-resolves. This avoids "I just demoted the admin but they still have admin powers" while keeping the hot path cheap.

---

## 8. Row-Level Security — Postgres or App?

**Decision: app-level for v1. Revisit RLS for multi-tenant in v2.** This decision concerns **user-facing CMS authorization** (the policy engine for posts/users/etc. described in this doc). It is distinct from **plugin DB role isolation**, which uses per-plugin Postgres roles with GRANTed access plus application-layer scoping; see [`02-plugin-system.md`](02-plugin-system.md) §6.2 for that mechanism. RLS on core tables is reserved as an optional defense-in-depth layer for v2 multi-tenant in both layers.

<!-- fixed per review (P6, C24): clarified that the v1 RLS rejection applies to the user-facing CMS authorization layer. Plugin DB isolation in v1 is app-level enforcement through per-plugin Postgres roles, not RLS — see doc 02 §6.2. -->


PG RLS arguments for: defense-in-depth, hard to bypass even if app logic is buggy, neat for multi-tenant.

Arguments against (for our v1):

- Our authorization model is **rich**: meta-caps depend on post status, ownership, and the plugin-extensible filter chain. Expressing this in `USING` clauses means baking application-domain logic into Postgres policies, which:
  - Couples migrations: a capability change becomes a DB migration.
  - Hurts performance: complex policies turn `WHERE` clauses into stacked subqueries, breaking index plans.
  - Hurts debuggability: silent row hiding is the worst possible developer experience when content "disappears."
- Plugins cannot register their own RLS policies safely — the WASM sandbox would need a SQL parser/policy compiler.
- We get the same defense-in-depth from **mandatory `policy.Can` calls** + lint enforcement, with the benefit of explicit decisions and audit logs.

Where RLS **is** appropriate: the multi-tenant separation of v2 — `tenant_id` columns with an `app.current_tenant_id` session-local GUC. That's a cleanly mechanical predicate that RLS expresses well.

For v1: the only DB-level enforcement we use is **column-level grants** for the application user (cannot SELECT `users.deleted_at` directly through the public-facing connection pool, for example) and `pgcrypto` for keyed columns.

---

## 9. CSRF Protection

Cookie sessions are vulnerable to CSRF. We use **double-submit cookie + SameSite=Lax**, not Synchronizer Token Pattern.

- On session create, we generate a `csrf_token` (32 random bytes, base64url) and store it in the session blob in Redis. We also set a separate cookie `csrf=<token>` with `Secure; SameSite=Lax; **not** HttpOnly` (so JS can read it).
- For any state-changing request from the admin (`POST`/`PUT`/`PATCH`/`DELETE`), the admin frontend reads the `csrf` cookie and sends `X-CSRF-Token` header.
- Server validates `X-CSRF-Token` == session's `csrf_token`. Mismatch → 403.

Why double-submit over a per-form synchronizer token: the admin is an SPA-style Next.js app making many fetch calls. A per-form token would require server-rendered forms, which we don't have. Double-submit is the de facto standard for SPAs and is sound when the session cookie is `SameSite=Lax` and the CSRF cookie is `Secure`.

Routes exempted from CSRF: `/auth/login` (no session yet), `/auth/oidc/callback`, webhook endpoints (which use signature headers instead). API token requests (PAT/OAuth) are exempt because tokens are not auto-attached by browsers.

For state-changing GETs (which we don't allow; they're a sin anyway): the lint rule above forbids registering a handler for `GET` with a non-idempotent service call.

---

## 10. Password Reset Flow

Goal: secure, leak-resistant, single-use, time-bounded.

```
POST /auth/password/forgot      (body: email)
  -> always respond 200 with generic "if the email exists, you'll get a link"
  -> if email exists & user is active:
       generate 256-bit token, store {token_hash, user_id, expires=15min, used_at=NULL}
       email link: https://site/auth/password/reset?token=<base64url>
  -> rate-limit: per email 3/hour, per IP 30/hour

GET /auth/password/reset?token=<t>
  -> server: lookup by HMAC(token), check expiry, NOT-used, user-active
  -> render form (or 410 if invalid)

POST /auth/password/reset       (body: token, new_password)
  -> verify token (timing-safe), atomically mark used_at=NOW()
  -> validate new password against policy + breach corpus
  -> insert new user_passwords row, revoke old
  -> revoke all sessions for the user except the one creating the reset (or all)
  -> audit auth.password.reset
  -> send "your password was changed" email (out-of-band; surface to user immediately)
```

Schema:

```sql
CREATE TABLE password_reset_tokens (
    id            BIGSERIAL PRIMARY KEY,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash    BYTEA NOT NULL UNIQUE,             -- SHA-256 of token
    issued_ip     INET,
    issued_ua     TEXT,
    expires_at    TIMESTAMPTZ NOT NULL,
    used_at       TIMESTAMPTZ
);
CREATE INDEX prt_active_idx ON password_reset_tokens (user_id) WHERE used_at IS NULL;
```

Key properties:

- **Leak-resistant**: response identical for "email not found" vs "email exists" — the timing must also be near-identical, which we ensure by always doing a dummy hash if user not found, then enqueuing the email job either way (job is a no-op when no user).
- **Single use**: enforced by `used_at` and a CHECK on update.
- **Short window**: 15 minutes.
- **All sessions revoked**: if a password reset happens, by definition we don't trust prior sessions.
- **Notification**: we send a separate "password changed" email so a victim of a reset they didn't initiate sees it immediately.

---

## 11. Email Verification

**Required at signup. Yes.** Reasons:

- Magic-link login depends on inbox control; an unverified email is a fake factor.
- Password reset goes to the email; unverified means we can't reliably recover the account.
- Spam accounts ruin the comments system, search, and any future federation.

The flow: on signup, the user is created with `email_verified_at = NULL` and a session is issued **with a restricted capability set** — only `read`, `edit_own_profile`, `verify_email`. They cannot do anything else (post, comment, install plugins, etc.) until they click the verification link. The verification UI is shown above the fold on every admin page until done. Verification email expires in 24 hours, requestable again subject to rate-limiting.

Operators can disable verification (for trusted closed deployments) via a setting; the policy engine then treats unverified users as if verified.

For OIDC signups where the IdP marks the email verified, we trust that and set `email_verified_at` to `NOW()` immediately (only for IdPs in the configured trusted list).

---

## 12. Brute Force & Abuse Mitigation

### 12.1 Login rate limits

Two-bucket limit using Redis token buckets:

- **Per IP**: 20 attempts per 5 minutes (rolling).
- **Per email**: 5 attempts per 15 minutes (rolling). Counts only against existing emails (so attackers can't lock arbitrary emails by spamming a known target — though see lockout below).

Exceeded → 429 with `Retry-After`. We do **not** show CAPTCHA in v1 (privacy concerns with hosted CAPTCHA; complexity); we just rate-limit hard. If abuse is observed, an operator can enable a hCaptcha plugin.

### 12.2 Account lockout

After 10 failures against a single account, lock the account for 30 minutes with auto-unlock. Failed-attempt counter resets on successful login. Lockout state is communicated to the user **only after** a successful password (so attackers don't get a "you locked them" oracle):

- During lockout, `POST /auth/login` with a wrong password still returns the standard "Invalid email or password."
- With the **correct** password during lockout: "Your account is temporarily locked. Try again in N minutes, or click here to unlock via email."

The email unlock is a magic-link-shaped token that bypasses the lock when clicked. This is the safety valve: brute force from one IP shouldn't lock out the legitimate user from another IP.

### 12.3 Anomaly detection

A lightweight scorer runs on every successful login. Inputs: prior IPs (Geo-IP'd to ASN + country), prior user-agent fingerprints (rough device class), time-of-day, time-since-last-login. Outputs: a score and reasons.

If "new country" or "new device class" → after the login completes, send an email "New sign-in from <country>, <device>. Was this you?" with a one-click "this wasn't me" button that revokes all sessions and forces password reset.

If "new country" AND no recent 2FA → require **step-up auth**: the session is created in a "limited" state (read-only) and the user is prompted to provide a 2nd factor to elevate. (If they have no 2nd factor configured, they can confirm via email.)

This is fail-open by design: false positives degrade UX but don't lock anyone out. The scorer is intentionally simple; complex ML on auth logs is a v2 conversation.

### 12.4 Credential stuffing defense

Beyond the per-IP/per-email limits, we monitor for **distributed** stuffing patterns:

- Many distinct IPs, many distinct emails, very low per-IP and per-email rate (so individual limits don't fire), high failure rate.
- Counted in a sketch (HLL/CMS in Redis) every minute.
- Threshold breach → operator notification + ability to globally require CAPTCHA (one toggle).

---

## 13. Audit Log

Everything that touches authentication, authorization, or privileged objects emits an audit event.

```sql
CREATE TABLE audit_log (
    id              BIGSERIAL PRIMARY KEY,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    actor_user_id   BIGINT REFERENCES users(id),     -- NULL if pre-auth (failed login)
    actor_kind      TEXT NOT NULL,                    -- 'user', 'service', 'plugin', 'system'
    actor_label     TEXT,                             -- e.g. plugin slug
    impersonator_id BIGINT REFERENCES users(id),      -- §15
    event           TEXT NOT NULL,                    -- 'auth.login.success'
    target_kind     TEXT,                             -- 'post', 'user', 'plugin', 'setting'
    target_id       TEXT,
    ip              INET,
    user_agent      TEXT,
    request_id      UUID,
    outcome         TEXT NOT NULL,                    -- 'success', 'denied', 'error'
    reason          TEXT,                             -- policy.Decision.Reason for denials
    metadata        JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_actor_idx ON audit_log (actor_user_id, occurred_at DESC);
CREATE INDEX audit_event_idx ON audit_log (event, occurred_at DESC);
CREATE INDEX audit_target_idx ON audit_log (target_kind, target_id, occurred_at DESC);
```

### 13.1 Events logged (selected)

| Event | Trigger |
|---|---|
| `auth.login.success` / `.failed` / `.locked` | Login attempts. |
| `auth.logout` | Session revoke (single or all). |
| `auth.password.changed` / `.reset` | Password material change. |
| `auth.totp.enabled` / `.disabled` | 2FA changes. |
| `auth.webauthn.added` / `.removed` | Credentials. |
| `auth.session.revoked` | Manual session revocation (incl. by another user e.g. admin). |
| `auth.token.created` / `.revoked` / `.used` | PAT / machine token lifecycle. (`.used` is sampled, not every request.) |
| `user.created` / `.suspended` / `.role_changed` / `.cap_granted` | User admin actions. |
| `plugin.installed` / `.activated` / `.uninstalled` / `.cap_registered` | Plugin lifecycle. |
| `setting.updated` | Any change to `manage_options`-gated settings. |
| `policy.denied` | Any `Can()` returning `Allowed=false` for a non-trivial action. (Sampled — denials in tight loops shouldn't flood.) |
| `auth.impersonation.start` / `.end` | §15. |

### 13.2 Retention

- Auth & permission events: **365 days** by default, configurable up to 7 years.
- Critical events (`role_changed`, `cap_granted`, `plugin.installed`, `impersonation.*`, anything with `manage_*` capabilities): **never auto-deleted** in v1. Operators can purge but it's a manual action.
- Audit log table partitioned monthly (`audit_log_2026_05` etc.) for cheap retention drops.

### 13.3 Tamper resistance

In v1 we don't ship a hash-chain (it's overkill for self-hosted CMS). We do:

- Periodic export to S3 (configurable cadence) with object-lock if available — "write once, then immutable."
- Operators can configure a syslog/SIEM forwarder; the audit logger ships through it in addition to the local table.

---

## 14. Capability Inheritance for Plugins (THE distinction)

This is the most error-prone area in this design and it warrants explicit treatment.

### 14.1 Two different capability systems

| System | Subject | Scope | Stored where |
|---|---|---|---|
| **User capabilities** | A `User` row | What the human (or service principal) can do via the admin UI or REST API. `edit_posts`, `manage_options`. | `capabilities`, `role_capabilities`, `user_capabilities`. Registered by plugins through the manifest's `grants_capabilities` array. |
| **Plugin capabilities** | A `Plugin` (WASM module) | What the plugin's code can do when it runs — read DB tables, send email, fetch URLs. `db.read` (scope `core.posts`), `http.fetch` (scope `api.example.com`). | The manifest's top-level `capabilities` block (`02-plugin-system.md` §2.2). |

These are **different vocabularies, different enforcement points, and different principals**. Confusing them is the #1 footgun.

### 14.2 When a plugin hook fires, whose capabilities apply?

When the hook bus invokes a plugin handler for, say, the `save_post` action, **two distinct authorizations are in play simultaneously**:

1. **The user's authorization to do the thing** is checked **before** the hook fires, in the core service code: `policy.Can(ctx, currentUser, "edit_post", post)`. If denied, the hook never fires.

2. **The plugin's authorization to run** is enforced by the WASM runtime: the plugin can only call host functions it has declared permissions for. The plugin running inside `save_post` cannot suddenly read the users table if its manifest's `capabilities` block doesn't grant `db.read` with scope `core.users`.

The plugin's permissions are **not** the user's permissions. A plugin acting in response to a hook does **not** inherit the user's caps. Even if the triggering user is a `super_admin`, the plugin can only do what its manifest declared.

This is deliberate: a contact-form plugin author asked "I need to read post titles" → manifest grants `db.read` with scope `core.posts`. That plugin running in response to admin save should not suddenly be able to read `users.email` just because an admin happened to trigger the hook.

### 14.3 What if a plugin needs to act on behalf of a user?

For routes a plugin **registers** (its own admin pages, its own REST endpoints), the request authenticates the user normally; the plugin handler receives a `PluginContext` exposing **the user's capabilities** (read-only) so the plugin can ask "is this user allowed to do X?" without re-implementing the check. The plugin asks via a host call:

```rust
// from plugin SDK
if ctx.user_can("manage_forms")? {
    // proceed
}
```

The host evaluates the policy as if the user is calling — the plugin doesn't gain the user's caps; it just gets a yes/no.

If a plugin needs to do something **the user can't** (e.g., a backup plugin needs to read all posts including private ones, but the triggering user is an editor), the plugin must:

1. Declare the broader capability (`db.read` with scope `core.posts` plus `read_private` qualifier) in its manifest's `capabilities` block.
2. Get the operator to approve that permission at install time.
3. Then act with **plugin authority**, not the user's. Such actions are audit-logged with `actor_kind = 'plugin'` and `actor_label = '<plugin-slug>'`, and the triggering user (if any) is recorded in `metadata.triggered_by_user_id` (see §14.6 for the host-side `audit.emit` ABI plugins use to record their own actions).

### 14.4 Capability registration (revisited)

When a plugin's `manifest.json` declares an entry under `grants_capabilities`, that adds a row to the **user** capabilities table — it's a permission **for human users**, not for the plugin itself. The plugin's own sandbox permissions are separately declared in the top-level `capabilities` block. The two coexist in the same file in two different slots:

```json
{
  "slug": "gn-forms",
  "name": "WPC Forms",
  "version": "1.0.0",
  "abi_version": "1",

  "capabilities": {
    "db": {
      "read":  ["core.posts", "plugin.tables"],
      "write": ["plugin.tables"]
    },
    "http": {
      "fetch": {"allow_hosts": ["api.captcha.example.com"]}
    },
    "kv": true,
    "queue": true
  },

  "grants_capabilities": [
    { "slug": "manage_forms", "description": "Create, edit, and delete forms." },
    { "slug": "export_forms", "description": "Download form submissions as CSV." }
  ]
}
```

<!-- fixed per review (P1, P2, C7): single canonical `manifest.json` (JSON, not TOML); two-slot vocabulary explicit — `capabilities` block (sandbox/plugin perms) vs `grants_capabilities` array (user-facing perms registered into the role system). DB permission grammar is dotted with scopes as separate strings, matching doc 02 §6. -->

When the plugin's "Create form" page is accessed: the user must hold `manage_forms` (a user capability); the plugin's code must hold `db.write` with scope `plugin.tables` (a plugin sandbox permission). Both are checked at their respective boundaries.

### 14.5 The single rule

> **A plugin's WASM code never inherits the requesting user's user-facing capabilities. The plugin holds its manifest-declared plugin permissions; the user holds their user capabilities; the policy engine checks both, separately, at the appropriate boundaries.**

The lint rule reinforces this: any code in the plugin runtime that calls `policy.Can` with a `*User` must wrap it explicitly with `policy.AsCheckOnBehalfOf(user, ...)` — this is the only sanctioned pattern, and it's audit-logged.

### 14.6 Plugin audit emission

<!-- fixed per review (B7): document the explicit `audit.emit` host ABI so plugin actions are first-class entries in the audit log, attributable to the plugin slug. -->

Plugins emit their own entries in the `audit_log` table (§13) through the `host.audit.emit(event_type, metadata)` host ABI, gated by the `audit.emit` capability in the plugin manifest's `capabilities` block. The host writes the row with `actor_kind = 'plugin'`, `actor_label = '<plugin-slug>'`, and `metadata` merged with host-supplied fields (`plugin_version`, `request_id`, the triggering user if any). `event` strings are validated against a documented pattern (`{slug}.{noun}.{verb}`, e.g., `gn-forms.submission.exported`); arbitrary system-reserved prefixes (`auth.*`, `user.*`, `policy.*`) are rejected so plugins cannot forge core events.

Per-plugin rate limits apply (default 60 emissions per minute, configurable per install). Plugins that need to record high-volume telemetry should batch or sample at the source. Plugins lacking the `audit.emit` capability cannot write to `audit_log` directly; their actions are still audit-logged when they cross other capability-gated boundaries (host db writes, http fetches, etc.), via the auto-emission path the host runs for state-changing host calls.

---

## 15. Impersonation ("View as user X")

Use cases: an admin debugging "why can't this user see the draft" or supporting a customer in a hosted offering.

### 15.1 Mechanism

The acting admin holds `impersonate_users` (a sensitive capability not granted by default; only `super_admin` has it, and the operator must explicitly grant it to `admin` if desired).

```
POST /admin/users/{id}/impersonate
  -> capability check: impersonate_users + target is not super_admin (cannot impersonate above your level)
  -> require a re-auth: prompt for password / passkey / 2FA in the last 5 minutes
  -> create a new session with:
       user_id = TARGET_ID
       impersonator_user_id = ADMIN_ID
       expires_in = 30 min absolute
       restricted_caps = TARGET's caps minus dangerous ones (see below)
  -> audit auth.impersonation.start (actor=admin, target=target, request_id, reason from form)
  -> redirect to /admin with a persistent banner "You are viewing as <handle>. Exit impersonation."
```

### 15.2 Limits on impersonation

The impersonation session is **not** identical to the target user's normal session:

- **No write to security material**: cannot change the target's password, 2FA, sessions, or recovery codes. (The middleware checks `impersonator_user_id IS NULL` for these routes.)
- **No further impersonation**: cannot impersonate from an impersonation session.
- **No PAT creation**: cannot mint tokens as the target.
- **Cap floor**: cannot exceed the target's capabilities (you become them, not augmented them).
- **Cap ceiling**: certain dangerous capabilities (e.g., `manage_install`, `delete_users`) are stripped even if the target has them — impersonation is for viewing/repro, not destructive ops.

### 15.3 Audit

Every action taken in an impersonation session has the `impersonator_id` set in audit rows, so post-hoc you can answer "who did Alice do this as?" — admin oversight is intact.

Email notification to the target user happens unless the operator has globally disabled it (some SaaS support contexts require silent impersonation; we expose the toggle but default to notify).

---

## 16. GDPR — Data Export & Deletion

### 16.1 Data export

A user requests their data:

```
POST /me/data-export
  -> queues a background job
  -> job collects: profile, posts (own), comments, media uploaded, sessions, audit log entries where actor=user
  -> writes a ZIP to private S3, signed URL emailed to user
  -> URL expires in 7 days
```

Admins can also generate exports on a user's behalf via `manage_users` + a separate `export_user_data` capability.

### 16.2 Deletion

WP doesn't have a great GDPR story. We do:

```
POST /me/delete-account
  -> requires re-auth
  -> 7-day grace period: account moves to status='deactivated', cannot log in, hidden from public
  -> after grace (or admin force): hard delete pipeline
```

**Hard delete pipeline** (runs as a background job, transactional per user):

| Data | Action |
|---|---|
| `users` row | Anonymize: `email = '<id>@deleted.invalid'`, `handle = 'deleted-<id>'`, `display_name = 'Deleted user'`, `bio = ''`, etc. `status = 'deleted'`. The row stays so foreign keys don't break. |
| Authored posts | **Operator setting**: default to **reassign** to a `system:deleted` user; option to delete. Public attribution shown as "Deleted user." |
| Comments | Anonymize author fields; keep body unless user-deleted in the request. |
| Media uploaded | Reassigned to system:deleted (referenced from posts); private media (avatar) deleted from S3. |
| Sessions / tokens / passkeys / TOTP / external identities | Hard delete. |
| Audit log | **Retained**, with the actor still pointing to the (now anonymized) user row. Required for compliance and is **not** personal data once the user is anonymized. |
| Password reset tokens, magic links | Hard delete. |

The default "anonymize, don't drop" approach for content respects the public record (a blog post about Linux kernel internals doesn't need to vanish because the author requested deletion) while honoring the privacy obligation. The user can opt-in to "delete my posts too" at request time.

The pipeline emits `user.deleted` events and runs `on_user_delete` hooks for plugins, which is how a plugin like a forum can do its own cleanup (delete forum posts, etc.). The hook is fire-and-forget; failures are logged but don't block the deletion.

### 16.3 Right to rectification, portability, etc.

Profile editing covers rectification. Export gives portability (JSON-LD + ActivityStreams shape so the export is machine-readable; we can ship Mastodon/ActivityPub bridges later). Restriction of processing is implemented as the deactivation state.

---

## 17. Trade-offs & Rejected Alternatives

### 17.1 Casbin vs hand-rolled policy package

[Casbin](https://casbin.org/) is the most popular Go authorization library. We considered it.

**For Casbin**: well-tested, configurable, supports many models (RBAC, ABAC), production-proven.

**Against**: Casbin's model is a generic matcher over `(sub, obj, act)`. Our model is WP-compatible meta-capabilities with status-and-ownership-dependent mappings. Expressing this in Casbin's matcher DSL is possible but ugly — we'd end up with a custom function library inside Casbin, defeating the "use the standard tool" advantage. Plus, debugging a denial in Casbin requires reading the matcher; in our hand-rolled package, the `Decision.Reason` string explains it inline.

**Decision**: hand-rolled. The policy code is shallow (one file per resource type) and idiomatic Go; the contract is tight (everything flows through `Can`); the tests are first-class.

### 17.2 OPA / Cedar / policy-as-code

OPA (Rego) and AWS Cedar are the modern external-policy-engine answers. They let you write policy in a dedicated DSL, version it, and evaluate in a process or sidecar.

**Against, for v1**:

- Plugins authoring policies in Rego/Cedar is a steep learning curve for plugin developers.
- We'd run OPA as a sidecar or in-process; either adds complexity for a self-hoster.
- Our policy is small enough that the DSL's expressive power doesn't pay for itself.

**Revisit in v2** if/when we have multi-tenant and tenant-level policy customization is a feature. Cedar is the more likely winner — its scheme of "entities + actions + context" maps cleanly to RBAC + ABAC.

### 17.3 JWT for browser sessions

Covered in §5.2. Short version: revocation matters too much for our use case.

We do use JWT-shaped tokens (signed, not encrypted, with `kid` rotation) for:

- Internal service-to-service auth where revocation TTL is acceptable.
- Short-lived (10-second) tokens issued to plugins for callbacks.

Library: `github.com/lestrrat-go/jwx`. Algorithm: EdDSA (Ed25519).

### 17.4 Cookie-less auth in the admin (everything in Authorization headers)

Considered: don't use cookies at all, force the SPA to attach tokens to every request.

**Against**: token storage in the browser is a XSS-amplification problem — `localStorage` is reachable from any script; `Authorization` header populated from `localStorage` means an XSS bug equals a token leak. `HttpOnly` cookies are the safer default. CSRF is solvable (we do); XSS-leaked tokens are not retroactively containable.

**Decision**: keep cookies for the browser admin.

### 17.5 OAuth2 vs WebAuthn-only as the future

We're not betting the farm on either. The architecture treats authentication methods as a pluggable list; we support both and expect the mix to shift over the years.

### 17.6 Per-row tenant_id columns from v1

We could add `tenant_id` everywhere now in anticipation of multi-tenant. Rejected: YAGNI; the cost of a global rewrite later is real but smaller than the cost of carrying a dead column through every query for two years.

### 17.7 Time-based session tokens (single value rotated each request)

Considered as defense-in-depth: rotate the session ID on every request, like Express's session module. Rejected: causes confusing UX when two tabs race the rotation; the cost (cookie writes on every response) outweighs the marginal security gain over our existing controls (HttpOnly, Secure, short idle TTL, anomaly detection).

---

## 18. Open Questions

1. **Step-up auth as a first-class concept** — should we generalize the "this action requires recent auth" pattern (currently used for impersonation and 2FA disable) into a `policy.RequireRecentAuth(duration)` middleware? Likely yes, but the UX (modal vs full-page re-login) needs design input.
2. **Are passkeys required for `super_admin`?** Strong argument for yes (passkey-or-bust for the most privileged role), but it's a self-host hostility risk. Tentatively: **required in hosted SaaS, recommended in self-host**.
3. **OIDC for the public site (not just admin)** — if subscribers log in to comment, can they use Google? Probably yes, but it changes spam economics (one captcha-free signup tap → bot army).
4. **Public-facing user discovery via the API** — `GET /api/users` for the public reader frontend (author pages) needs to expose handles + display_name without leaking email/role. Current plan: a separate `public_profile` view, but want a security review of every column.
5. **PAT scope granularity** — at what point do scopes need to express "this token can only access posts in category X"? WP plugins like REST API Auth go this far. Likely v2.
6. **Audit log access** — admins can read their own actions; can they read everyone's? Today: `view_audit_log` capability gates the global view. But that capability itself is sensitive (compliance-grade access). Should there be a separate "compliance officer" role distinct from `admin`? Lean yes, hold for v1.5.
7. **Plugin-issued sessions** — can a plugin authenticate users itself (e.g., a "LDAP login" plugin)? Architecturally yes via the `auth.providers` hook, but the security review of that boundary is its own project. Defer concrete design to a plugin-specific RFC after the v1 plugin runtime is solid.
8. **WebAuthn account recovery** — if a user loses all passkeys and has no other factor, what's the recovery path? Currently: email magic link + password if set. If the user has no password (passkey-only), email magic link alone elevates only to a limited session that can register a new passkey. Need UX prototyping.
9. **Rate limit storage** — Redis. What happens during Redis outages? Fail-open (allow) leaves a window for brute force; fail-closed makes Redis a hard dependency for login. Lean toward fail-closed for admin routes and fail-open with elevated logging for public read routes.
10. **GDPR data export format** — JSON-LD vs the upcoming W3C DataPortability schema vs a plain ZIP of JSON + media. Lean: ZIP of JSON files + media, with a top-level `manifest.json` describing structure. Not standards-compliant but operator-friendly.

---
