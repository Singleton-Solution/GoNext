package media

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMedia_List_FilterByClass(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	store.Insert(Asset{ID: "a1", MimeType: "image/png", CreatedAt: time.Now()})
	store.Insert(Asset{ID: "a2", MimeType: "video/mp4", CreatedAt: time.Now().Add(-time.Hour)})
	store.Insert(Asset{ID: "a3", MimeType: "application/pdf", CreatedAt: time.Now().Add(-2 * time.Hour)})

	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/media", Deps{Store: store}); err != nil {
		t.Fatalf("mount: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/media?mime_class=image", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	var body struct {
		Data []Asset `json:"data"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if len(body.Data) != 1 || body.Data[0].ID != "a1" {
		t.Errorf("filter failed: %+v", body.Data)
	}
}

func TestMedia_Get_NotFound(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	_ = Mount(mux, "/api/v1/media", Deps{Store: NewMemoryStore()})
	req := httptest.NewRequest("GET", "/api/v1/media/ghost", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != 404 {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestMedia_InvalidMimeClass(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	_ = Mount(mux, "/api/v1/media", Deps{Store: NewMemoryStore()})
	req := httptest.NewRequest("GET", "/api/v1/media?mime_class=bogus", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != 400 {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}
