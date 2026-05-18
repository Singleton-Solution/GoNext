package health

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/metrics"
)

// buildSeededRecorder wires a recorder and seeds it with one
// invocation and one trap for "acme.spellcheck" so the HTTP tests
// have something to render.
func buildSeededRecorder(t *testing.T) *recorder {
	t.Helper()
	r := NewRecorder(metrics.NewRegistry())
	r.ObserveInvocation("acme.spellcheck", "post.before_save", ResultOK, 2*time.Millisecond)
	r.ObserveTrap("acme.spellcheck", "wasm error: integer divide by zero",
		TrapDetail{Hook: "post.before_save", Reason: "wasm error: integer divide by zero", Payload: []byte(`{"x":1}`)})
	return r
}

func TestHandler_ReportForKnownPlugin(t *testing.T) {
	r := buildSeededRecorder(t)
	h := NewHandler(r)
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/plugins/acme.spellcheck/health")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %s, body = %s", resp.Status, body)
	}
	var rep Report
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rep.Plugin != "acme.spellcheck" {
		t.Errorf("Plugin = %q", rep.Plugin)
	}
	if rep.Invocations != 1 {
		t.Errorf("Invocations = %d, want 1", rep.Invocations)
	}
	if rep.Traps != 1 {
		t.Errorf("Traps = %d, want 1", rep.Traps)
	}
}

func TestHandler_UnknownPluginReturns404(t *testing.T) {
	r := buildSeededRecorder(t)
	h := NewHandler(r)
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/plugins/no.such.plugin/health")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %s, want 404", resp.Status)
	}
}

func TestHandler_KnownPluginsPredicateAllowsEmptyReport(t *testing.T) {
	r := NewRecorder(metrics.NewRegistry())
	h := NewHandler(r, WithKnownPlugins(func(slug string) bool {
		return slug == "installed.but.idle"
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/plugins/installed.but.idle/health")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %s, want 200", resp.Status)
	}
}

func TestHandler_ListPlugins(t *testing.T) {
	r := buildSeededRecorder(t)
	h := NewHandler(r)
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/plugins/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %s, want 200", resp.Status)
	}
	var got struct {
		Plugins []string `json:"plugins"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Plugins) != 1 || got.Plugins[0] != "acme.spellcheck" {
		t.Errorf("Plugins = %v, want [acme.spellcheck]", got.Plugins)
	}
}

func TestHandler_TrapByID(t *testing.T) {
	r := buildSeededRecorder(t)
	traps := r.RecentTraps("acme.spellcheck")
	if len(traps) != 1 {
		t.Fatalf("expected 1 trap, got %d", len(traps))
	}
	h := NewHandler(r)
	srv := httptest.NewServer(h)
	defer srv.Close()

	url := srv.URL + "/api/v1/plugins/acme.spellcheck/traps/" + jsonNum(traps[0].ID)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %s, body = %s", resp.Status, body)
	}
	var ev TrapEvent
	if err := json.NewDecoder(resp.Body).Decode(&ev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ev.ID != traps[0].ID {
		t.Errorf("ev.ID = %d, want %d", ev.ID, traps[0].ID)
	}
	if string(ev.Payload) != `{"x":1}` {
		t.Errorf("ev.Payload = %s", ev.Payload)
	}

	// 404 for an unknown ID.
	resp, err = http.Get(srv.URL + "/api/v1/plugins/acme.spellcheck/traps/9999")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %s, want 404", resp.Status)
	}

	// 400 for a malformed ID.
	resp, err = http.Get(srv.URL + "/api/v1/plugins/acme.spellcheck/traps/abc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %s, want 400", resp.Status)
	}
}

func TestHandler_RejectsNonGET(t *testing.T) {
	r := buildSeededRecorder(t)
	h := NewHandler(r)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/plugins/acme.spellcheck/health", strings.NewReader(""))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %s, want 405", resp.Status)
	}
}

// jsonNum renders a uint64 as a base-10 string. We use a tiny helper
// rather than strconv.FormatUint so the test file stays self-
// contained at a glance.
func jsonNum(n uint64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
