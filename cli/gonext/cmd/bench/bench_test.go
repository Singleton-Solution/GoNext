package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRun_NoArgsRunsAllScenariosAgainstStub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	t.Setenv("GONEXT_WEB_BASE_URL", srv.URL)

	var out, errb bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	code := RunCtx(ctx, []string{
		"--host", srv.URL,
		"--vus", "2",
		"--duration", "200ms",
		"--ramp", "0",
		"--no-slo",
	}, &out, &errb)
	if code != ExitOK {
		t.Errorf("exit = %d, want %d. stderr=%s", code, ExitOK, errb.String())
	}
	s := out.String()
	for _, want := range []string{"homepage", "posts", "login", "restshim"} {
		if !strings.Contains(s, want) {
			t.Errorf("text output missing scenario %q\n%s", want, s)
		}
	}
}

func TestRun_PicksNamedScenarioOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("GONEXT_WEB_BASE_URL", srv.URL)

	var out, errb bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	code := RunCtx(ctx, []string{
		"--host", srv.URL,
		"--vus", "1",
		"--duration", "100ms",
		"--ramp", "0",
		"--no-slo",
		"homepage",
	}, &out, &errb)
	if code != ExitOK {
		t.Errorf("exit = %d, want %d. stderr=%s", code, ExitOK, errb.String())
	}
	s := out.String()
	if !strings.Contains(s, "homepage") {
		t.Errorf("expected 'homepage' in output, got %s", s)
	}
	if strings.Contains(s, "restshim") {
		t.Errorf("expected only 'homepage' run; output mentions restshim:\n%s", s)
	}
}

func TestRun_UnknownScenarioRejected(t *testing.T) {
	var out, errb bytes.Buffer
	code := RunCtx(context.Background(), []string{"nope"}, &out, &errb)
	if code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(errb.String(), "unknown scenario") {
		t.Errorf("stderr missing 'unknown scenario': %s", errb.String())
	}
}

func TestRun_BadFlagRejected(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"vus zero", []string{"--vus", "0"}},
		{"duration zero", []string{"--duration", "0"}},
		{"ramp negative", []string{"--ramp", "-1s"}},
		{"ramp >= duration", []string{"--duration", "1s", "--ramp", "1s"}},
		{"bad output", []string{"--output", "xml"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := RunCtx(context.Background(), tc.args, &out, &errb)
			if code != ExitUsage {
				t.Errorf("exit = %d, want %d. stderr=%s", code, ExitUsage, errb.String())
			}
		})
	}
}

func TestRun_HelpFlagExitsZero(t *testing.T) {
	var out, errb bytes.Buffer
	code := RunCtx(context.Background(), []string{"-h"}, &out, &errb)
	if code != ExitOK {
		t.Errorf("exit = %d, want %d", code, ExitOK)
	}
}

func TestRun_JSONOutputParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	t.Setenv("GONEXT_WEB_BASE_URL", srv.URL)

	var out, errb bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	code := RunCtx(ctx, []string{
		"--host", srv.URL,
		"--vus", "1",
		"--duration", "100ms",
		"--ramp", "0",
		"--output", "json",
		"--no-slo",
		"homepage",
	}, &out, &errb)
	if code != ExitOK {
		t.Errorf("exit = %d, want %d. stderr=%s", code, ExitOK, errb.String())
	}
	var reps []Report
	if err := json.Unmarshal(out.Bytes(), &reps); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if len(reps) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reps))
	}
	if reps[0].Scenario != "homepage" {
		t.Errorf("scenario = %s, want homepage", reps[0].Scenario)
	}
}

func TestRun_SLOFailExitsNonZero(t *testing.T) {
	// Server that adds an artificial 50ms of latency — well above the
	// 5ms p95 budget we're going to pretend we wanted. We achieve that
	// by hand-crafting a scenario via the fake-server URL and letting
	// the SLO check trip on a slow handler.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep long enough that the homepage 250ms budget is busted.
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("GONEXT_WEB_BASE_URL", srv.URL)

	var out, errb bytes.Buffer
	// Use a longer duration so we collect enough samples for a stable
	// p95 — otherwise nearest-rank on 1-2 samples is hilariously
	// noisy. With 1 VU and 300ms latency, 2 seconds gives us ~6
	// samples — enough for the 95th-percentile rank to land on the
	// 300ms bucket.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	code := RunCtx(ctx, []string{
		"--host", srv.URL,
		"--vus", "1",
		"--duration", "2s",
		"--ramp", "0",
		"homepage",
	}, &out, &errb)
	if code != ExitFail {
		t.Errorf("exit = %d, want %d. stdout=%s stderr=%s", code, ExitFail, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "FAIL") {
		t.Errorf("expected FAIL in output, got %s", out.String())
	}
}

func TestRun_SLOFailButNoSLOExitsZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("GONEXT_WEB_BASE_URL", srv.URL)

	var out, errb bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	code := RunCtx(ctx, []string{
		"--host", srv.URL,
		"--vus", "1",
		"--duration", "2s",
		"--ramp", "0",
		"--no-slo",
		"homepage",
	}, &out, &errb)
	if code != ExitOK {
		t.Errorf("exit = %d, want %d (--no-slo should suppress the fail). stderr=%s",
			code, ExitOK, errb.String())
	}
}

func TestRun_CancelStopsWithin1s(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("GONEXT_WEB_BASE_URL", srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int)
	go func() {
		var out, errb bytes.Buffer
		code := RunCtx(ctx, []string{
			"--host", srv.URL,
			"--vus", "8",
			"--duration", "30s",
			"--ramp", "0",
			"--no-slo",
		}, &out, &errb)
		done <- code
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunCtx did not return within 2s after cancel")
	}
}
