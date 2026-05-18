package scenarios

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"time"
)

// Login mirrors tools/load/k6/scenarios/login.js: it sends one invalid
// then one valid credential pair per iteration. The SLO bucket is
// "authedAdmin" because the latency is dominated by the password
// verify on every attempt (a constant-time dummy verify runs even on
// the invalid branch).
//
// Credentials default to the dev fixtures; override via environment:
//
//	GONEXT_BENCH_LOGIN_VALID_EMAIL
//	GONEXT_BENCH_LOGIN_VALID_PASSWORD
//	GONEXT_BENCH_LOGIN_INVALID_EMAIL
//	GONEXT_BENCH_LOGIN_INVALID_PASSWORD
type Login struct{}

// Name implements [Scenario].
func (Login) Name() string { return "login" }

// Bucket implements [Scenario]. Values from lib/baseline.js authedAdmin.
func (Login) Bucket() SLO {
	return SLO{
		P95:          800 * time.Millisecond,
		P99:          1500 * time.Millisecond,
		MaxErrorRate: 0.01,
	}
}

// Setup is a no-op — credentials are read per-iteration so test code
// can flip them via t.Setenv.
func (Login) Setup(_ context.Context, _ string) error { return nil }

// Iter performs an invalid attempt followed by a valid attempt and
// returns the *combined* RTT. The aggregator counts this as one
// iteration; that matches the k6 default-function semantics.
//
// The function tolerates a 401 on the valid branch — dev fixtures
// vary — but a transport error on either step is fatal for the
// iteration.
func (Login) Iter(ctx context.Context, client *http.Client, baseURL string) Result {
	invalidEmail := envOr("GONEXT_BENCH_LOGIN_INVALID_EMAIL", "nobody@example.invalid")
	invalidPass := envOr("GONEXT_BENCH_LOGIN_INVALID_PASSWORD", "wrong-password")
	validEmail := envOr("GONEXT_BENCH_LOGIN_VALID_EMAIL", "admin@example.com")
	validPass := envOr("GONEXT_BENCH_LOGIN_VALID_PASSWORD", "changeme-dev-only")

	url := baseURL + "/api/v1/auth/login"
	start := time.Now()

	// Invalid branch first — a misconfigured run that lets valid creds
	// fire repeatedly would hit rate limiters and ruin the percentile.
	r1 := postJSON(ctx, client, url, map[string]string{"email": invalidEmail, "password": invalidPass})
	if r1.Err != nil {
		return Result{RTT: time.Since(start), Err: r1.Err}
	}

	r2 := postJSON(ctx, client, url, map[string]string{"email": validEmail, "password": validPass})
	if r2.Err != nil {
		return Result{RTT: time.Since(start), Err: r2.Err}
	}

	// Status from the second (valid) call — that is what the report
	// surfaces. The aggregator treats anything outside [200,300) as
	// an error.
	return Result{RTT: time.Since(start), Status: r2.Status}
}

// postJSON sends a JSON body and returns a Result with status + RTT
// (or transport error). The body is a tiny map — we hand-encode to
// avoid pulling in encoding/json's reflection cost into the hot loop.
func postJSON(ctx context.Context, client *http.Client, url string, body map[string]string) Result {
	buf := encodeJSONBody(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return Result{Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return Result{RTT: time.Since(start), Err: err}
	}
	if resp.Body != nil {
		_, _ = drain(resp)
		_ = resp.Body.Close()
	}
	return Result{RTT: time.Since(start), Status: resp.StatusCode}
}

// encodeJSONBody emits a JSON object for a string→string map without
// reflection. Keys and values are quoted via the small writer below;
// we don't need full RFC 8259 here (no nested objects, no escapes
// beyond ASCII control), but we still escape the backslash and
// double-quote characters so a stray '"' in a fixture password does
// not produce malformed JSON.
func encodeJSONBody(m map[string]string) []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	for k, v := range m {
		if !first {
			b.WriteByte(',')
		}
		first = false
		writeJSONString(&b, k)
		b.WriteByte(':')
		writeJSONString(&b, v)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func writeJSONString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
