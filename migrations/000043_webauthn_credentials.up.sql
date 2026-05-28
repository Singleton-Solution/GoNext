-- WebAuthn passkey credentials (issue #159).
--
-- One row per registered passkey. The (user_id, credential_id)
-- combination is the natural key — but credential_id alone is
-- globally unique under the WebAuthn spec, so we index it as well
-- to make the assertion path (look up credential by id, then
-- consult the holder) fast.
--
-- sign_count is the authenticator-provided monotonic counter the
-- assertion handler validates each login against. The WebAuthn
-- spec recommends rejecting an assertion whose count <= the
-- last-seen value; we update this column in-place inside the
-- finish-login handler.
--
-- attestation_type records the attestation format ("none",
-- "packed", "tpm", etc.) returned at registration. It's stored
-- for audit / fingerprinting purposes; today nothing consumes it
-- programmatically.
--
-- last_used_at is touched on every successful login. The admin UI
-- shows it under "Last used: 2 hours ago" so a user with multiple
-- passkeys can identify which one is the dormant phone they want
-- to delete.

CREATE TABLE webauthn_credentials (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id   bytea       NOT NULL,
    public_key      bytea       NOT NULL,
    sign_count      bigint      NOT NULL DEFAULT 0,
    attestation_type text       NOT NULL DEFAULT '',
    -- Friendly name surfaced in /settings/account. Defaults to
    -- "Passkey" when the client doesn't pass one; the user can
    -- rename it later.
    name            text        NOT NULL DEFAULT 'Passkey',
    created_at      timestamptz NOT NULL DEFAULT now(),
    last_used_at    timestamptz
);

-- credential_id is globally unique under the WebAuthn spec. The
-- assertion handler looks credentials up by id alone (the user
-- handle is optional in the assertion payload), so this is the
-- hot index.
CREATE UNIQUE INDEX webauthn_credentials_credential_id_key
    ON webauthn_credentials (credential_id);

-- Index on user_id for the "list my passkeys" admin view.
CREATE INDEX webauthn_credentials_user_id_idx
    ON webauthn_credentials (user_id);
