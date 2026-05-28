package menus

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	pkgmenus "github.com/Singleton-Solution/GoNext/packages/go/menus"
)

const base = "/api/v1/menus"

type harness struct {
	mux   *http.ServeMux
	store *pkgmenus.MemoryStore
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := pkgmenus.NewMemoryStore()
	mux := http.NewServeMux()
	if err := Mount(mux, base, Deps{
		Store:  store,
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return &harness{mux: mux, store: store}
}

// seedMenu creates a menu with the given slug and the supplied label
// list. Each label gets a synthesised path (001, 002, ...) so the
// store accepts them.
func (h *harness) seedMenu(t *testing.T, slug, name string, items []seedItem) {
	t.Helper()
	m, err := h.store.CreateMenu(context.Background(), pkgmenus.Menu{Slug: slug, Name: name})
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}
	for i, si := range items {
		_, err := h.store.CreateItem(context.Background(), pkgmenus.MenuItem{
			MenuID: m.ID,
			Path:   pathToken(i + 1),
			Label:  si.label,
			URL:    si.url,
		})
		if err != nil {
			t.Fatalf("CreateItem: %v", err)
		}
	}
}

type seedItem struct{ label, url string }

func pathToken(n int) string {
	// Quick 1..999 → 3-digit zero-padded helper that satisfies the
	// pathRe (^[0-9]{3}(\.[0-9]{3})*$) the store enforces.
	if n < 10 {
		return "00" + string(rune('0'+n))
	}
	if n < 100 {
		return "0" + intToASCII(n)
	}
	return intToASCII(n)
}

func intToASCII(n int) string {
	// Tiny inline itoa — avoids pulling strconv just for tests.
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+(n%10))) + digits
		n /= 10
	}
	return digits
}

func TestByLocationReturnsItems(t *testing.T) {
	h := newHarness(t)
	h.seedMenu(t, "primary", "Primary", []seedItem{
		{"Pricing", "/pricing"},
		{"Docs", "/docs"},
		{"Status", "https://status.example.com"},
	})

	req := httptest.NewRequest(http.MethodGet, base+"/by-location/primary", nil)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Items []publicItem `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 3 {
		t.Fatalf("want 3 items, got %d (%+v)", len(got.Items), got.Items)
	}
	if got.Items[0].Label != "Pricing" || got.Items[0].Href != "/pricing" || got.Items[0].External {
		t.Fatalf("item[0] wrong: %+v", got.Items[0])
	}
	if !got.Items[2].External {
		t.Fatalf("expected status link to be external: %+v", got.Items[2])
	}
}

func TestByLocationMissingMenuReturnsEmpty(t *testing.T) {
	h := newHarness(t)

	req := httptest.NewRequest(http.MethodGet, base+"/by-location/footer-product", nil)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)

	// Critical contract: missing menu is 200 with empty items, not 404.
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Items []publicItem `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Items == nil {
		t.Fatalf("items must be a JSON array, not null")
	}
	if len(got.Items) != 0 {
		t.Fatalf("want 0 items, got %d", len(got.Items))
	}
}

func TestByLocationEmptyMenuReturnsEmpty(t *testing.T) {
	h := newHarness(t)
	h.seedMenu(t, "primary", "Primary", nil)

	req := httptest.NewRequest(http.MethodGet, base+"/by-location/primary", nil)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Validate the exact JSON envelope — empty items must serialise as
	// `[]`, not `null`, so the typescript client doesn't need a null
	// guard.
	if body != "{\"items\":[]}\n" {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestListReturnsConfiguredMenus(t *testing.T) {
	h := newHarness(t)
	h.seedMenu(t, "primary", "Primary", []seedItem{{"Pricing", "/pricing"}})
	h.seedMenu(t, "footer-product", "Footer Product", nil)

	req := httptest.NewRequest(http.MethodGet, base, nil)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Menus []menuSummary `json:"menus"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Menus) != 2 {
		t.Fatalf("want 2 menus, got %d (%+v)", len(got.Menus), got.Menus)
	}
}

func TestListEmptyStoreReturnsEmptyArray(t *testing.T) {
	h := newHarness(t)

	req := httptest.NewRequest(http.MethodGet, base, nil)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if rec.Body.String() != "{\"menus\":[]}\n" {
		t.Fatalf("unexpected body: %q", rec.Body.String())
	}
}

func TestNoAuthRequired(t *testing.T) {
	// The whole point of this surface is anonymous visitors hit it.
	// The harness deliberately never injects a policy.Principal — if
	// these routes ever pick up a gate, this test fails.
	h := newHarness(t)
	h.seedMenu(t, "primary", "Primary", []seedItem{{"Home", "/"}})

	req := httptest.NewRequest(http.MethodGet, base+"/by-location/primary", nil)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous read failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestIsExternalURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"/", false},
		{"/pricing", false},
		{"/docs/setup", false},
		{"#anchor", false},
		{"https://example.com", true},
		{"http://example.com", true},
		{"//example.com", true},
		{"mailto:hi@example.com", true},
		{"tel:+15551234567", true},
	}
	for _, tc := range cases {
		if got := isExternalURL(tc.in); got != tc.want {
			t.Errorf("isExternalURL(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
