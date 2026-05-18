package plugin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/health"
)

// fakeServer wires the two endpoints replay needs (GET /traps/{id}
// and POST /replay) onto an httptest.Server. The trap event is
// supplied at construction; the replay handler returns a caller-
// supplied result so each test can drive a different scenario.
func fakeServer(t *testing.T, ev health.TrapEvent, rerunResult, rerunReason string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/plugins/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/plugins/")
		parts := strings.Split(path, "/")
		switch {
		case len(parts) == 3 && parts[1] == "traps":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ev)
		case len(parts) == 2 && parts[1] == "replay":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"result": rerunResult,
				"reason": rerunReason,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return httptest.NewServer(mux)
}

func TestExecuteReplay_MatchingTrapReproduces(t *testing.T) {
	ev := health.TrapEvent{
		ID:               42,
		Plugin:           "acme.spellcheck",
		Hook:             "post.before_save",
		Reason:           "wasm error: integer divide by zero",
		NormalisedReason: "integer_divide_by_zero",
		Payload:          []byte(`{"x":1}`),
		At:               time.Now().UTC(),
	}
	srv := fakeServer(t, ev, "trap", ev.Reason)
	defer srv.Close()

	diff, err := executeReplay(srv.Client(), srv.URL, "acme.spellcheck", "42")
	if err != nil {
		t.Fatalf("executeReplay: %v", err)
	}
	if !diff.Matches {
		t.Errorf("expected Matches=true, got %+v", diff)
	}
	if diff.OriginalReason != ev.Reason {
		t.Errorf("OriginalReason = %q", diff.OriginalReason)
	}
	if diff.RerunResult != "trap" {
		t.Errorf("RerunResult = %q", diff.RerunResult)
	}
	if diff.TrapID != 42 {
		t.Errorf("TrapID = %d", diff.TrapID)
	}
	if diff.Hook != "post.before_save" {
		t.Errorf("Hook = %q", diff.Hook)
	}
}

func TestExecuteReplay_BehaviourChanged(t *testing.T) {
	ev := health.TrapEvent{
		ID:     43,
		Plugin: "acme.spellcheck",
		Hook:   "post.before_save",
		Reason: "wasm error: integer divide by zero",
	}
	srv := fakeServer(t, ev, "ok", "")
	defer srv.Close()

	diff, err := executeReplay(srv.Client(), srv.URL, "acme.spellcheck", "43")
	if err != nil {
		t.Fatalf("executeReplay: %v", err)
	}
	if diff.Matches {
		t.Errorf("expected Matches=false, got %+v", diff)
	}
	if diff.RerunResult != "ok" {
		t.Errorf("RerunResult = %q", diff.RerunResult)
	}
}

func TestExecuteReplay_TrapNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := executeReplay(srv.Client(), srv.URL, "acme.spellcheck", "999")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got %v", err)
	}
}

func TestExecuteReplay_DevModeRefused(t *testing.T) {
	ev := health.TrapEvent{ID: 1, Plugin: "p", Hook: "h", Reason: "r"}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/plugins/p/traps/1", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(ev)
	})
	mux.HandleFunc("/api/v1/plugins/p/replay", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := executeReplay(srv.Client(), srv.URL, "p", "1")
	if err == nil || !strings.Contains(err.Error(), "dev mode") {
		t.Errorf("expected dev-mode error, got %v", err)
	}
}

func TestRunReplay_PrintsHumanDiff(t *testing.T) {
	ev := health.TrapEvent{
		ID:     44,
		Plugin: "acme.spellcheck",
		Hook:   "post.before_save",
		Reason: "wasm error: integer divide by zero",
	}
	srv := fakeServer(t, ev, "trap", ev.Reason)
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := runReplay([]string{"--server", srv.URL, "acme.spellcheck", "44"}, &stdout, &stderr)
	if code != ExitOK {
		t.Errorf("code = %d, want %d (stderr=%s)", code, ExitOK, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"acme.spellcheck", "Trap ID    44", "Original", "Re-run", "same failure reproduced"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected stdout to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRunReplay_JSONFormat(t *testing.T) {
	ev := health.TrapEvent{ID: 45, Plugin: "p", Hook: "h", Reason: "r"}
	srv := fakeServer(t, ev, "ok", "")
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := runReplay([]string{"--server", srv.URL, "--json", "p", "45"}, &stdout, &stderr)
	if code != ExitFail {
		// "ok" rerun means bug fixed -> ExitFail per the
		// command's contract.
		t.Errorf("code = %d, want %d (stderr=%s)", code, ExitFail, stderr.String())
	}
	var diff ReplayDiff
	if err := json.Unmarshal(stdout.Bytes(), &diff); err != nil {
		t.Fatalf("decode stdout: %v (out=%s)", err, stdout.String())
	}
	if diff.Matches {
		t.Error("expected Matches=false for rerun=ok")
	}
}

func TestRunReplay_UsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runReplay([]string{}, &stdout, &stderr); code != ExitUsage {
		t.Errorf("empty args: code = %d, want %d", code, ExitUsage)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runReplay([]string{"only-one"}, &stdout, &stderr); code != ExitUsage {
		t.Errorf("one arg: code = %d, want %d", code, ExitUsage)
	}
}
