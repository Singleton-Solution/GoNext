// Adapters between the public route packages (login, posts, search …)
// and the pgxpool-backed persistence layer. These exist to keep main.go
// readable: every wiring block in main.go ends up calling exactly one
// adapter constructor here, and the inline closures (UserLookup, etc.)
// live next to their SQL rather than mid-stream in the boot sequence.
//
// All adapters are read-only over the pool; lifecycle (Close) stays
// with the orchestrator registration in main.go.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	adminmedia "github.com/Singleton-Solution/GoNext/apps/api/internal/admin/media"
	"github.com/Singleton-Solution/GoNext/apps/api/internal/auth/login"
	restimg "github.com/Singleton-Solution/GoNext/apps/api/internal/rest/img"
)

// mediaLookupAdapter wires the admin/media.MemoryStore (or the
// future Postgres-backed Store) to the restimg.AssetLookup interface
// the public proxy depends on. The adapter exists to keep the
// public proxy package import-cycle-free — it never depends on
// admin/media.
type mediaLookupAdapter struct {
	store adminmedia.Store
}

func (a mediaLookupAdapter) LookupByID(ctx context.Context, id string) (restimg.AssetRef, error) {
	asset, err := a.store.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, adminmedia.ErrNotFound) {
			return restimg.AssetRef{}, restimg.ErrAssetNotFound
		}
		return restimg.AssetRef{}, err
	}
	return restimg.AssetRef{
		ID:         asset.ID,
		StorageKey: asset.StorageKey,
		MIMEType:   asset.MimeType,
	}, nil
}

// mediaSourceAdapter exposes the storage backend's read side to the
// restimg handler. The MemoryPutter is write-only on the admin/media
// interface but exposes a Stored() accessor for tests; we use that
// here to feed the on-the-fly transform. The Postgres + minio-go
// wiring (when it lands) will swap this for a minio.GetObject call.
type mediaSourceAdapter struct {
	putter *adminmedia.MemoryPutter
}

func (a mediaSourceAdapter) GetObject(_ context.Context, key string) ([]byte, error) {
	body := a.putter.Stored(key)
	if body == nil {
		return nil, restimg.ErrSourceNotFound
	}
	return body, nil
}

// userLookupByEmail returns a login.UserLookup closure that resolves
// the (id, email, password_hash, status) tuple from `users` joined
// against `user_passwords`. The lookup is case-insensitive on email
// — the users table uses citext, which gives us that for free in SQL.
//
// The closure returns login.ErrUserNotFound when no row matches; any
// other error is surfaced verbatim so the service can log it and treat
// it as a credential failure (per the constant-time guarantee in
// login/doc.go).
//
// password_hash is LEFT JOINed so an OAuth-only user (row in `users`
// but none in `user_passwords`) returns an empty Hash. The login
// service treats empty Hash as "wrong credentials" rather than panicking
// on the missing row — see login.UserRecord.Hash.
func userLookupByEmail(pool *pgxpool.Pool) login.UserLookup {
	return func(ctx context.Context, email string) (login.UserRecord, error) {
		email = strings.TrimSpace(email)
		if email == "" {
			return login.UserRecord{}, login.ErrUserNotFound
		}
		var rec login.UserRecord
		var hash *string
		// users.meta.roles is the canonical role-grant source (see
		// cli/gonext/cmd/init/admin.go which seeds the bootstrap
		// super_admin there). We project it as a JSON array of text
		// using `meta->'roles'` and let pgx scan it into a
		// []string via a JSON-aware destination. The COALESCE keeps
		// older rows (or future OAuth-only signups) with a null
		// roles array from blowing up the scan.
		var rolesJSON []byte
		err := pool.QueryRow(ctx, `
			SELECT u.id::text,
			       u.email::text,
			       p.password_hash,
			       u.status,
			       COALESCE(u.meta->'roles', '[]'::jsonb)
			FROM users u
			LEFT JOIN user_passwords p ON p.user_id = u.id
			WHERE u.email = $1::citext
			LIMIT 1
		`, email).Scan(&rec.ID, &rec.Email, &hash, &rec.Status, &rolesJSON)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return login.UserRecord{}, login.ErrUserNotFound
			}
			return login.UserRecord{}, fmt.Errorf("login: user lookup: %w", err)
		}
		if hash != nil {
			rec.Hash = *hash
		}
		// Decode roles. A non-array or unmarshal error degrades to
		// "no roles" rather than failing login — the audit log
		// already captures the user_id, so an operator can spot a
		// bad meta payload from the privilege-less behavior.
		if len(rolesJSON) > 0 {
			var roles []string
			if jsonErr := json.Unmarshal(rolesJSON, &roles); jsonErr == nil {
				rec.Roles = roles
			}
		}
		return rec, nil
	}
}

// userLookupByID is the by-ID variant of userLookupByEmail. Used by the
// TOTP finalize path. Same projection (status + meta.roles) so the
// principal carried into the session matches what the password path
// would produce.
func userLookupByID(pool *pgxpool.Pool) login.UserByIDLookup {
	return func(ctx context.Context, userID string) (login.UserRecord, error) {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			return login.UserRecord{}, login.ErrUserNotFound
		}
		var rec login.UserRecord
		var hash *string
		var rolesJSON []byte
		err := pool.QueryRow(ctx, `
			SELECT u.id::text,
			       u.email::text,
			       p.password_hash,
			       u.status,
			       COALESCE(u.meta->'roles', '[]'::jsonb)
			FROM users u
			LEFT JOIN user_passwords p ON p.user_id = u.id
			WHERE u.id = $1::uuid
			LIMIT 1
		`, userID).Scan(&rec.ID, &rec.Email, &hash, &rec.Status, &rolesJSON)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return login.UserRecord{}, login.ErrUserNotFound
			}
			return login.UserRecord{}, fmt.Errorf("login: user lookup by id: %w", err)
		}
		if hash != nil {
			rec.Hash = *hash
		}
		if len(rolesJSON) > 0 {
			var roles []string
			if jsonErr := json.Unmarshal(rolesJSON, &roles); jsonErr == nil {
				rec.Roles = roles
			}
		}
		return rec, nil
	}
}
