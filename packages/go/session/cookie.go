package session

import (
	"net/http"
	"time"
)

// CookieName is the default name of the session cookie. It is short on
// purpose — the cookie ships on every request to the origin, so every
// byte matters.
const CookieName = "sid"

// CookieOptions tunes the small set of fields a caller may legitimately
// want to override on the session cookie. The fields that affect
// security (HttpOnly, Secure, SameSite, Path) are not exposed: the
// helpers always set them to safe defaults. See docs/06-auth-permissions.md §5.1.
type CookieOptions struct {
	// Name overrides [CookieName] when non-empty. Useful when an
	// install runs multiple session scopes on the same eTLD+1 (e.g.
	// "sid" for the public site, "admin_sid" for the admin shell).
	Name string

	// Domain is the cookie's Domain attribute. Leave empty to scope
	// the cookie to the exact host that set it (browser default).
	Domain string

	// MaxAge is the cookie's Max-Age in seconds, derived from the
	// session's absolute TTL. Zero means "session cookie" (lives until
	// the browser closes). Negative means "delete now".
	MaxAge time.Duration

	// Insecure, when true, drops the Secure attribute. This exists
	// solely so the local development server can use plain HTTP. It is
	// a foot-gun in production; the field is named with a warning in
	// the identifier itself.
	Insecure bool
}

// SetCookie writes the session cookie to w with HttpOnly + Secure +
// SameSite=Lax + Path=/, plus the small set of caller-tunable fields
// in opts. The token is written verbatim — it is already URL-safe
// (base64url-without-padding).
func SetCookie(w http.ResponseWriter, token string, opts CookieOptions) {
	name := opts.Name
	if name == "" {
		name = CookieName
	}
	c := &http.Cookie{
		Name:     name,
		Value:    token,
		Path:     "/",
		Domain:   opts.Domain,
		HttpOnly: true,
		Secure:   !opts.Insecure,
		SameSite: http.SameSiteLaxMode,
	}
	if opts.MaxAge > 0 {
		c.MaxAge = int(opts.MaxAge.Seconds())
		c.Expires = time.Now().Add(opts.MaxAge)
	} else if opts.MaxAge < 0 {
		c.MaxAge = -1
		c.Expires = time.Unix(0, 0)
	}
	http.SetCookie(w, c)
}

// ClearCookie writes a Set-Cookie header that instructs the browser to
// delete the session cookie. It must mirror the original cookie's Name,
// Domain, and Path or the browser will treat it as a different cookie
// and ignore the deletion.
func ClearCookie(w http.ResponseWriter, opts CookieOptions) {
	name := opts.Name
	if name == "" {
		name = CookieName
	}
	c := &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		Domain:   opts.Domain,
		HttpOnly: true,
		Secure:   !opts.Insecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	}
	http.SetCookie(w, c)
}
