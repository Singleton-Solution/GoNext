package pat

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// UserCapsFunc resolves the effective capabilities for a userID. The
// auth wiring in the API binary plugs this in with whatever it's
// already using to expand role memberships — typically a BasicPolicy's
// Capabilities call against the user's loaded roles.
//
// Returning an empty set is legal (deny-all) and is how the middleware
// represents "user has no caps" without raising.
type UserCapsFunc func(ctx context.Context, userID string) (policy.CapabilitySet, error)

// Middleware wires a PAT-aware authentication step in front of the
// downstream handler. The shape matches packages/go/httpx.Middleware so
// it composes with the rest of the chassis.
//
// Per-request behavior:
//
//  1. If no `Authorization: Bearer gnp_*` header is present, the
//     middleware does NOT raise — it hands off to next. Cookie-based
//     session auth runs in a parallel middleware; PATs are additive,
//     not a replacement.
//
//  2. If the header IS present and ValidShape says yes, the middleware
//     resolves the token through the store. Any of ErrInvalid /
//     ErrNotFound / ErrExpired / ErrRevoked → 401. Any other error
//     (store down) → 500 with a generic message.
//
//  3. On success the middleware:
//       a. Builds a Principal with UserID = token.UserID and an empty
//          Roles slice. Scopes are NOT roles — they're already a
//          resolved capability list.
//       b. Computes `scopes ∩ user-caps` and stashes the resulting
//          CapabilitySet on the request context via WithCapabilities.
//          The downstream Require middleware needs to know how to read
//          the intersected set (see scopes.go's CapsFromContext).
//       c. Calls store.TouchUsed in the background so the request hot
//          path doesn't block on the DB write.
type Config struct {
	// Store is the PAT persistence layer. Required.
	Store Store

	// UserCaps resolves the user's effective capabilities. Required.
	UserCaps UserCapsFunc

	// Logger receives structured log lines (token-id, decision). nil
	// falls back to slog.Default.
	Logger *slog.Logger

	// Now, if set, replaces time.Now. Used by tests to pin expiry.
	Now func() time.Time
}

// Middleware returns the configured net/http middleware. The Config is
// copied; later mutations don't affect the returned closure.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	if cfg.Store == nil {
		panic("pat: Middleware requires a Store")
	}
	if cfg.UserCaps == nil {
		panic("pat: Middleware requires a UserCaps resolver")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			candidate, ok := ParseBearer(header)
			if !ok || !ValidShape(candidate) {
				// No bearer token: pass through to the next
				// authentication step (cookies, etc.).
				next.ServeHTTP(w, r)
				return
			}

			ctx := r.Context()
			row, err := cfg.Store.Lookup(ctx, candidate)
			if err != nil {
				switch {
				case errors.Is(err, ErrInvalid),
					errors.Is(err, ErrNotFound),
					errors.Is(err, ErrExpired),
					errors.Is(err, ErrRevoked):
					logger.WarnContext(ctx, "pat: authentication failed",
						slog.String("reason", err.Error()),
					)
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				default:
					logger.ErrorContext(ctx, "pat: store error",
						slog.Any("err", err),
					)
					http.Error(w, "internal server error", http.StatusInternalServerError)
					return
				}
			}

			// Resolve the user's effective caps and intersect.
			userCaps, err := cfg.UserCaps(ctx, row.UserID)
			if err != nil {
				logger.ErrorContext(ctx, "pat: user caps lookup failed",
					slog.String("user_id", row.UserID),
					slog.Any("err", err),
				)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			effective := Intersect(row.Scopes, userCaps)

			// Build a Principal carrying the token's UserID and an
			// empty Roles slice. Downstream Require checks pull the
			// caps from the context (CapsFromContext) which we set
			// next; the BasicPolicy.Can path keeps working for the
			// non-PAT case where Roles is populated.
			principal := policy.Principal{
				UserID: row.UserID,
				Roles:  nil,
			}
			ctx = policy.WithPrincipal(ctx, principal)
			ctx = WithCapabilities(ctx, effective)
			ctx = WithTokenID(ctx, row.ID)
			r = r.WithContext(ctx)

			// Update last_used_at asynchronously; failures are
			// non-fatal for the request hot path.
			go func(id string, t time.Time) {
				// Background goroutine, separate context so a slow
				// touch can't block request cancellation.
				bg, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				if err := cfg.Store.TouchUsed(bg, id, t); err != nil {
					logger.WarnContext(bg, "pat: touch failed",
						slog.String("token_id", id),
						slog.Any("err", err),
					)
				}
			}(row.ID, now())

			next.ServeHTTP(w, r)
		})
	}
}

// Intersect returns the capability set common to scopes and userCaps.
// A capability appears in the output iff it is listed in scopes AND
// present in userCaps. The narrower side wins:
//
//   - Token scoped to {posts.read} on a user with {posts.read,
//     posts.write} → {posts.read}.
//   - Token scoped to {posts.write} on a user with only {posts.read}
//     → {} (token can't escalate beyond the user's caps).
//   - Token with no scopes → {} (deny-all, useless but legal).
//
// The function is exported so the handler that previews a token
// (POST /me/tokens) can compute the resulting cap set up front and
// surface it to the operator.
func Intersect(scopes []string, userCaps policy.CapabilitySet) policy.CapabilitySet {
	out := make(policy.CapabilitySet, len(scopes))
	for _, s := range scopes {
		c := policy.Capability(s)
		if userCaps.Has(c) {
			out[c] = struct{}{}
		}
	}
	return out
}
