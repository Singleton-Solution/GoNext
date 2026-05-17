// Package session is the GoNext server-side, opaque-cookie, Redis-backed
// session store.
//
// Sessions are intentionally opaque (not JWT). The cookie value is a
// 256-bit CSPRNG token, base64url-encoded; Redis owns the truth. Trade
// the extra Redis hop for these properties:
//
//   - Revocation is a DEL — "log me out everywhere" works in under a
//     millisecond, no blacklist gymnastics.
//   - Session metadata (role changes, factors, impersonation) is mutable
//     server-side without rotating a token.
//   - The cookie value carries zero claims; if it's leaked, the attacker
//     has nothing to inspect or forge.
//
// Storage layout:
//
//	session:<token>           // JSON-encoded session blob, EXAT idle TTL
//	user_sessions:<user_id>   // Redis SET of active tokens for the user
//
// The user-sessions set is what makes [Manager.DeleteAllForUser] and
// [Manager.List] cheap; without it, "where are you logged in?" would
// require a SCAN of the entire session keyspace.
//
// Cookie attributes are fixed to safe defaults: HttpOnly + Secure +
// SameSite=Lax + Path=/. Lax (not Strict) is deliberate: Strict breaks
// "user clicks an emailed link to the admin and is unexpectedly logged
// out", a common WordPress grievance. See docs/06-auth-permissions.md §5.
//
// Typical wiring at boot:
//
//	mgr, err := session.New(ctx, cfg.Redis, log.FromContext(ctx))
//	if err != nil {
//	    return fmt.Errorf("session manager: %w", err)
//	}
//	defer mgr.Close()
//
// And in a login handler:
//
//	token, err := mgr.Create(ctx, userID, map[string]any{
//	    "factors": []string{"password", "totp"},
//	}, 90*24*time.Hour, 30*24*time.Hour)
//	if err != nil {
//	    return err
//	}
//	session.SetCookie(w, token, session.CookieOptions{MaxAge: 90 * 24 * time.Hour})
package session
