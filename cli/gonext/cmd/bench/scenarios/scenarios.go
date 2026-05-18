// Package scenarios defines the built-in `gonext bench` workloads.
//
// A [Scenario] is a tiny stateless plan for one URL-shape mix that the
// bench runner can replay across N virtual users. The interface is
// deliberately narrow so additional scenarios can be added without
// touching the runner.
package scenarios

import (
	"context"
	"net/http"
	"time"
)

// Result is the outcome of a single iteration. Scenarios may make more
// than one HTTP request per iteration (login, restshim do); in that
// case Status reflects the *last* request and RTT is the sum.
// Aggregators care about counts and percentiles, not individual
// substeps.
type Result struct {
	// RTT is the wall-clock duration of the iteration.
	RTT time.Duration
	// Status is the final HTTP status, or 0 if no response was
	// received (transport error, context cancel, etc.).
	Status int
	// Err is non-nil for transport errors. A non-2xx response is
	// counted as a "request error" by the aggregator but Err remains
	// nil — scenarios that need to fail-fast on unexpected status
	// should return their own error here.
	Err error
}

// SLO is the threshold budget a scenario advertises. Numbers come from
// docs/11-testing-ci.md §11 (the same place k6's lib/baseline.js
// sources its values from).
type SLO struct {
	// P95 is the maximum acceptable 95th-percentile latency.
	P95 time.Duration
	// P99 is the maximum acceptable 99th-percentile latency.
	P99 time.Duration
	// MaxErrorRate is the highest tolerable fraction of failed
	// requests (transport error or non-2xx). 0.01 == 1%.
	MaxErrorRate float64
}

// Scenario is the contract every workload implements.
type Scenario interface {
	// Name is a short, kebab-free identifier used for the CLI arg and
	// report headers. It MUST be stable since users put it in scripts.
	Name() string
	// Bucket returns the SLO budget for this scenario.
	Bucket() SLO
	// Setup runs once before any VU is spawned. It is the right place
	// to construct an HTTP client, warm a token, etc. The default
	// implementation is fine for most scenarios — they pass a nil
	// client and let Iter resolve it from defaults.
	Setup(ctx context.Context, baseURL string) error
	// Iter performs one logical iteration. It must respect ctx — the
	// runner cancels ctx to stop the run. Implementations should
	// return a Result with Status=0 + Err set if the request could
	// not be issued at all.
	Iter(ctx context.Context, client *http.Client, baseURL string) Result
}

// DefaultClient is shared by all built-in scenarios. It is constructed
// with conservative timeouts so a wedged endpoint can't lock up VUs.
//
// The transport keeps a small pool of idle connections per host so
// keepalive amortisation works the same way k6 does.
func DefaultClient() *http.Client {
	tr := &http.Transport{
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 64,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  false,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   30 * time.Second,
	}
}

// All returns the built-in scenarios in a stable order. The order is
// the order the report prints them in when no scenario is specified.
// RestShim is returned by pointer because it carries an internal
// counter; the value scenarios are stateless.
func All() []Scenario {
	return []Scenario{
		Homepage{},
		Posts{},
		Login{},
		&RestShim{},
	}
}

// timeRequest issues a single GET with the supplied client and returns
// a Result. It is the workhorse helper for the read-only scenarios.
func timeRequest(ctx context.Context, client *http.Client, method, url string, headers http.Header) Result {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return Result{Err: err}
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return Result{RTT: time.Since(start), Err: err}
	}
	// Drain so the connection can be reused. Body close is what makes
	// keepalive work.
	if resp.Body != nil {
		_, _ = drain(resp)
		_ = resp.Body.Close()
	}
	return Result{RTT: time.Since(start), Status: resp.StatusCode}
}

// drain reads up to a small cap from the response body. We don't care
// about the bytes; we just need keepalive to kick in.
func drain(resp *http.Response) (int64, error) {
	const cap = 1 << 20 // 1 MiB
	buf := make([]byte, 32*1024)
	var n int64
	for {
		read, err := resp.Body.Read(buf)
		n += int64(read)
		if err != nil || n >= cap {
			return n, err
		}
	}
}
