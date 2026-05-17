package session

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSetCookie_DefaultsAreSafe(t *testing.T) {
	rec := httptest.NewRecorder()
	SetCookie(rec, "tok123", CookieOptions{MaxAge: time.Hour})
	got := rec.Result().Cookies()
	if len(got) != 1 {
		t.Fatalf("want 1 cookie, got %d", len(got))
	}
	c := got[0]
	if c.Name != CookieName {
		t.Errorf("name: got %q want %q", c.Name, CookieName)
	}
	if c.Value != "tok123" {
		t.Errorf("value: got %q", c.Value)
	}
	if !c.HttpOnly {
		t.Error("HttpOnly should be true")
	}
	if !c.Secure {
		t.Error("Secure should be true by default")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite: got %v want Lax", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("Path: got %q want /", c.Path)
	}
	if c.MaxAge != int(time.Hour.Seconds()) {
		t.Errorf("MaxAge: got %d want %d", c.MaxAge, int(time.Hour.Seconds()))
	}
}

func TestSetCookie_Insecure(t *testing.T) {
	rec := httptest.NewRecorder()
	SetCookie(rec, "tok", CookieOptions{Insecure: true, MaxAge: time.Hour})
	c := rec.Result().Cookies()[0]
	if c.Secure {
		t.Error("Insecure=true should drop Secure")
	}
	// HttpOnly is still on — the dev opt-out is for Secure only.
	if !c.HttpOnly {
		t.Error("HttpOnly should remain on even when Insecure")
	}
}

func TestSetCookie_CustomNameAndDomain(t *testing.T) {
	rec := httptest.NewRecorder()
	SetCookie(rec, "tok", CookieOptions{
		Name:   "admin_sid",
		Domain: "admin.example.com",
		MaxAge: time.Hour,
	})
	c := rec.Result().Cookies()[0]
	if c.Name != "admin_sid" {
		t.Errorf("Name: got %q", c.Name)
	}
	if c.Domain != "admin.example.com" {
		t.Errorf("Domain: got %q", c.Domain)
	}
}

func TestSetCookie_SessionCookie_ZeroMaxAge(t *testing.T) {
	rec := httptest.NewRecorder()
	SetCookie(rec, "tok", CookieOptions{})
	header := rec.Header().Get("Set-Cookie")
	// A zero-MaxAge cookie should have no Max-Age and no Expires.
	if strings.Contains(strings.ToLower(header), "max-age") {
		t.Errorf("session cookie should not set Max-Age: %q", header)
	}
}

func TestClearCookie_DeletesByExpiry(t *testing.T) {
	rec := httptest.NewRecorder()
	ClearCookie(rec, CookieOptions{})
	c := rec.Result().Cookies()[0]
	if c.MaxAge != -1 {
		t.Errorf("MaxAge: got %d want -1", c.MaxAge)
	}
	if !c.Expires.Before(time.Now()) {
		t.Errorf("Expires should be in the past, got %v", c.Expires)
	}
	if c.Value != "" {
		t.Errorf("Value should be cleared, got %q", c.Value)
	}
	// Critical: the Path/Domain must match the SetCookie call or the
	// browser ignores the deletion. We always emit Path=/.
	if c.Path != "/" {
		t.Errorf("Path: got %q want /", c.Path)
	}
}

func TestClearCookie_CustomNameDomain(t *testing.T) {
	rec := httptest.NewRecorder()
	ClearCookie(rec, CookieOptions{Name: "x", Domain: "example.com"})
	c := rec.Result().Cookies()[0]
	if c.Name != "x" || c.Domain != "example.com" {
		t.Errorf("name/domain mismatch: name=%q domain=%q", c.Name, c.Domain)
	}
}
