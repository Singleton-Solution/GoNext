package users

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestList_Pagination(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	for i := 0; i < 5; i++ {
		store.Insert(User{
			ID:        idForTest(i),
			Handle:    "user" + string(rune('a'+i)),
			CreatedAt: time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.UTC),
		})
	}

	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/users", Deps{Store: store}); err != nil {
		t.Fatalf("mount: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/users?limit=2", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Data       []User `json:"data"`
		Pagination struct {
			NextCursor string `json:"next_cursor"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Data) != 2 {
		t.Errorf("len(data) = %d, want 2", len(body.Data))
	}
	if body.Pagination.NextCursor == "" {
		t.Error("expected next_cursor for partial page")
	}
}

func TestGet_ByID(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	store.Insert(User{ID: "01234567-89ab-cdef-0123-456789abcdef", Handle: "alice", CreatedAt: time.Now()})

	mux := http.NewServeMux()
	_ = Mount(mux, "/api/v1/users", Deps{Store: store})

	req := httptest.NewRequest("GET", "/api/v1/users/01234567-89ab-cdef-0123-456789abcdef", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var u User
	_ = json.Unmarshal(rr.Body.Bytes(), &u)
	if u.Handle != "alice" {
		t.Errorf("handle = %q, want alice", u.Handle)
	}
}

func TestGet_ByHandle(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	store.Insert(User{ID: "01234567-89ab-cdef-0123-456789abcdef", Handle: "alice", CreatedAt: time.Now()})

	mux := http.NewServeMux()
	_ = Mount(mux, "/api/v1/users", Deps{Store: store})

	req := httptest.NewRequest("GET", "/api/v1/users/alice", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestGet_NotFound(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	_ = Mount(mux, "/api/v1/users", Deps{Store: NewMemoryStore()})
	req := httptest.NewRequest("GET", "/api/v1/users/ghost", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != 404 {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestLooksLikeUUID(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"01234567-89ab-cdef-0123-456789abcdef": true,
		"01234567-89AB-CDEF-0123-456789ABCDEF": true,
		"alice":                                false,
		"01234567-89ab-cdef-0123-45678":        false,
		"01234567x89ab-cdef-0123-456789abcdef": false,
	}
	for in, want := range cases {
		if got := looksLikeUUID(in); got != want {
			t.Errorf("looksLikeUUID(%q) = %v, want %v", in, got, want)
		}
	}
}

func idForTest(i int) string {
	hex := "0123456789abcdef"
	c := hex[i%16]
	// Build a syntactically-valid UUID with the byte index baked in.
	return "0000000" + string(c) + "-0000-4000-8000-000000000000"
}
