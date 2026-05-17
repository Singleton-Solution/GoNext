package csrf

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// These tests live in the csrf package (not csrf_test) so they can
// reach the unexported verifyToken / mintToken helpers and assert on
// the unexported error sentinels.

func TestVerifyToken_TableDriven(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	cfg := Options{
		TTL: time.Hour,
		Now: func() time.Time { return clock },
	}
	key := []byte("test-key-1234567890abcdef-extra-")
	// Mint a valid baseline.
	good, err := mintToken(key, cfg)
	if err != nil {
		t.Fatalf("mintToken: %v", err)
	}

	cases := []struct {
		name    string
		tok     string
		wantErr error
	}{
		{"valid", good, nil},
		{"empty", "", errMissingToken},
		{"no-dots", "abcdef", errMalformedToken},
		{"one-dot", "abc.def", errMalformedToken},
		{"empty-id", ".123.sig", errMalformedToken},
		{"empty-ts", "id..sig", errMalformedToken},
		{"empty-sig", "id.123.", errMalformedToken},
		{"non-numeric-ts", "id.notanumber.sig", errMalformedToken},
		{"bad-base64-sig", "id.123.not!base64", errMalformedToken},
		{"flipped-bit-sig", good[:len(good)-2] + "XX", errInvalidHMAC},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := verifyToken(tc.tok, key, cfg)
			if tc.wantErr == nil {
				if err != nil {
					t.Errorf("verifyToken: unexpected error %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("verifyToken: got %v, want chain-contains %v", err, tc.wantErr)
			}
		})
	}
}

func TestVerifyToken_ExpiredAndFuture(t *testing.T) {
	mintClock := time.Unix(1_700_000_000, 0)
	mintCfg := Options{TTL: time.Hour, Now: func() time.Time { return mintClock }}
	key := []byte("expiry-test-key-32-bytes-long-OK!")
	tok, err := mintToken(key, mintCfg)
	if err != nil {
		t.Fatalf("mintToken: %v", err)
	}

	// Expired (clock + 2h, TTL = 1h).
	t.Run("expired", func(t *testing.T) {
		cfg := Options{TTL: time.Hour, Now: func() time.Time { return mintClock.Add(2 * time.Hour) }}
		if _, err := verifyToken(tok, key, cfg); !errors.Is(err, errExpiredToken) {
			t.Errorf("got %v, want errExpiredToken", err)
		}
	})

	// Future (clock - 2 minutes, TTL = 1h). 2 minutes > our 1-minute
	// future tolerance, so this must fail.
	t.Run("future-beyond-tolerance", func(t *testing.T) {
		cfg := Options{TTL: time.Hour, Now: func() time.Time { return mintClock.Add(-2 * time.Minute) }}
		if _, err := verifyToken(tok, key, cfg); !errors.Is(err, errExpiredToken) {
			t.Errorf("got %v, want errExpiredToken", err)
		}
	})

	// Within future-clock-skew tolerance (clock - 30s).
	t.Run("future-within-tolerance", func(t *testing.T) {
		cfg := Options{TTL: time.Hour, Now: func() time.Time { return mintClock.Add(-30 * time.Second) }}
		if _, err := verifyToken(tok, key, cfg); err != nil {
			t.Errorf("got %v, want nil (within 1-min skew tolerance)", err)
		}
	})

	// Exactly at TTL boundary — should still pass (age == TTL).
	t.Run("at-ttl-boundary", func(t *testing.T) {
		cfg := Options{TTL: time.Hour, Now: func() time.Time { return mintClock.Add(time.Hour) }}
		if _, err := verifyToken(tok, key, cfg); err != nil {
			t.Errorf("got %v at exact TTL boundary, want nil", err)
		}
	})

	// 1 second past TTL — must fail.
	t.Run("one-second-past-ttl", func(t *testing.T) {
		cfg := Options{TTL: time.Hour, Now: func() time.Time { return mintClock.Add(time.Hour + time.Second) }}
		if _, err := verifyToken(tok, key, cfg); !errors.Is(err, errExpiredToken) {
			t.Errorf("got %v, want errExpiredToken", err)
		}
	})
}

func TestMintToken_FormatAndUniqueness(t *testing.T) {
	cfg := Options{TTL: time.Hour, Now: time.Now}
	key := []byte("uniqueness-test-key-32-bytes-OK!")

	seen := make(map[string]struct{}, 32)
	for i := 0; i < 32; i++ {
		tok, err := mintToken(key, cfg)
		if err != nil {
			t.Fatalf("mintToken[%d]: %v", i, err)
		}
		// Two dots.
		if n := strings.Count(tok, "."); n != 2 {
			t.Errorf("token %q has %d dots, want 2", tok, n)
		}
		if _, dup := seen[tok]; dup {
			t.Errorf("duplicate token after %d mints: %q", i, tok)
		}
		seen[tok] = struct{}{}
	}
}

func TestIsSkippedPath(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		prefixes []string
		want     bool
	}{
		{"exact match", "/auth/login", []string{"/auth/login"}, true},
		{"prefix match", "/webhooks/stripe", []string{"/webhooks/"}, true},
		{"no match", "/admin/users", []string{"/auth/", "/webhooks/"}, false},
		{"empty prefixes", "/admin/users", nil, false},
		{"empty string in prefixes", "/admin/users", []string{""}, false},
		{"empty path", "", []string{"/auth/"}, false},
		{"path under nested skip", "/api/v1/webhooks/foo", []string{"/api/v1/webhooks/"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSkippedPath(tc.path, tc.prefixes); got != tc.want {
				t.Errorf("isSkippedPath(%q, %v) = %v, want %v", tc.path, tc.prefixes, got, tc.want)
			}
		})
	}
}
