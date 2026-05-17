package delivery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SignatureHeader is the HTTP header the deliverer sets on every attempt.
// Subscribers compute the expected value with the shared secret and compare
// against this header. Format mirrors Stripe's webhook signatures:
//
//	X-GoNext-Signature: t=<unix>,v1=<hex(hmacSha256(secret, t + "." + body))>
//
// Multiple v<n> versions can be added in the future (e.g. v2= for a new
// keyed hash). Verifiers should ignore unknown versions and treat a
// missing v1 as failure rather than malformed.
const SignatureHeader = "X-GoNext-Signature"

// EventIDHeader carries the event identifier. Constant across retries so
// the subscriber can dedupe on it.
const EventIDHeader = "X-GoNext-Event-Id"

// DeliveryIDHeader carries the per-attempt delivery identifier. Useful for
// correlating subscriber-side logs with our side.
const DeliveryIDHeader = "X-GoNext-Delivery-Id"

// TimestampHeader carries the same unix seconds that went into the
// signature, in plain form so subscribers don't have to parse the
// signature header to find it.
const TimestampHeader = "X-GoNext-Timestamp"

// AttemptHeader carries the 1-based attempt number.
const AttemptHeader = "X-GoNext-Attempt"

// EventTypeHeader carries the event type for routing convenience.
const EventTypeHeader = "X-GoNext-Event-Type"

// SubscriptionIDHeader carries the subscription identifier so a
// subscriber operating multiple endpoints can identify which one we hit.
const SubscriptionIDHeader = "X-GoNext-Subscription-Id"

// reservedHeaders is the set of X-GoNext-* (and Content-Type) headers
// that the deliverer always sets; user-supplied custom headers cannot
// override them.
var reservedHeaders = map[string]struct{}{
	SignatureHeader:      {},
	EventIDHeader:        {},
	DeliveryIDHeader:     {},
	TimestampHeader:      {},
	AttemptHeader:        {},
	EventTypeHeader:      {},
	SubscriptionIDHeader: {},
	"Content-Type":       {},
}

// Sign computes the X-GoNext-Signature value for the given timestamp and
// body using the supplied HMAC secret.
//
// The signed string is `<unix>.<body>`. Subscribers reconstruct it from
// the t= field of the header and the raw request body.
//
// The function is pure and safe to call concurrently. It accepts the
// timestamp as a parameter (rather than reading time.Now() internally) so
// retry attempts produce a fresh signature while the test suite can pin
// the value for deterministic vectors.
func Sign(secret []byte, ts time.Time, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	// Constant separator: a single '.' between unix seconds and the
	// body. The dot is unambiguous against integers and trivially fast
	// to parse on the verifier side.
	mac.Write([]byte(strconv.FormatInt(ts.Unix(), 10)))
	mac.Write([]byte{'.'})
	mac.Write(body)
	sum := mac.Sum(nil)
	return fmt.Sprintf("t=%d,v1=%s", ts.Unix(), hex.EncodeToString(sum))
}

// SignaturePair is the parsed form of an X-GoNext-Signature header.
type SignaturePair struct {
	Timestamp time.Time
	V1Hex     string // lowercase hex of the v1 signature, empty if missing
}

// ErrMalformedSignature is returned by ParseSignature when the header
// can't be decoded. Distinct from "signature didn't verify" so callers
// can return 400 (we sent garbage) vs 401 (subscriber's secret is wrong).
var ErrMalformedSignature = errors.New("malformed signature header")

// ParseSignature decodes the X-GoNext-Signature header value into its
// timestamp and v1 hex. Unknown v<n> versions are silently skipped, so a
// future header that adds `v2=...` remains backward-compatible.
//
// Returns ErrMalformedSignature if t= is missing or unparseable. A
// missing v1= is not an error here — Verify treats it as a failed
// verification with a distinct outcome.
func ParseSignature(header string) (SignaturePair, error) {
	if header == "" {
		return SignaturePair{}, ErrMalformedSignature
	}
	var out SignaturePair
	tsSet := false
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return SignaturePair{}, ErrMalformedSignature
		}
		switch k {
		case "t":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return SignaturePair{}, fmt.Errorf("%w: t= not int", ErrMalformedSignature)
			}
			out.Timestamp = time.Unix(n, 0).UTC()
			tsSet = true
		case "v1":
			// Normalize to lowercase hex; the verifier compares with
			// hmac.Equal so case sensitivity doesn't matter, but keeping
			// the parsed form canonical helps tests.
			out.V1Hex = strings.ToLower(v)
		default:
			// Unknown version — forward-compat: skip.
		}
	}
	if !tsSet {
		return SignaturePair{}, fmt.Errorf("%w: missing t=", ErrMalformedSignature)
	}
	return out, nil
}

// Verify checks the signature header against the given body and secret.
//
// The check has two halves:
//
//  1. Constant-time HMAC comparison of the v1 field against the
//     locally-computed expected value. hmac.Equal handles the constant
//     time; we do the same hex normalization on both sides first.
//
//  2. Skew check: |now - t| must be <= maxSkew. Defends against replay
//     of a captured request. Five minutes is the default in Stripe's
//     guidance and a reasonable starting point; subscribers operating
//     in higher-latency environments can pass a longer window. Zero
//     maxSkew skips the skew check (useful in tests with pinned time;
//     production callers should never pass zero).
//
// Returns nil on success. Returns ErrMalformedSignature when the header
// can't be parsed. Returns a distinct sentinel for skew vs mismatch so
// the subscriber can log usefully.
func Verify(secret []byte, body []byte, header string, now time.Time, maxSkew time.Duration) error {
	pair, err := ParseSignature(header)
	if err != nil {
		return err
	}
	if maxSkew > 0 {
		delta := now.Sub(pair.Timestamp)
		if delta < 0 {
			delta = -delta
		}
		if delta > maxSkew {
			return fmt.Errorf("signature timestamp outside skew window (delta=%s)", delta)
		}
	}
	expected := Sign(secret, pair.Timestamp, body)
	// expected has shape "t=<n>,v1=<hex>". Pull the v1 hex out so we
	// compare just the keyed-hash bytes.
	_, v1, _ := strings.Cut(expected, ",v1=")
	if pair.V1Hex == "" {
		return errors.New("signature: v1 missing")
	}
	a, _ := hex.DecodeString(pair.V1Hex)
	b, _ := hex.DecodeString(v1)
	if !hmac.Equal(a, b) {
		return errors.New("signature: v1 mismatch")
	}
	return nil
}
