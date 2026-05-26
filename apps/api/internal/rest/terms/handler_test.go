package terms

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTaxonomies_List(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	store.AddTaxonomy(Taxonomy{Slug: "category", Name: "Category", NamePlural: "Categories", Hierarchical: true, CreatedAt: time.Now()})
	store.AddTaxonomy(Taxonomy{Slug: "tag", Name: "Tag", NamePlural: "Tags", Hierarchical: false, CreatedAt: time.Now()})

	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/terms", "/api/v1/taxonomies", Deps{Store: store}); err != nil {
		t.Fatalf("mount: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/taxonomies", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body struct {
		Data []Taxonomy `json:"data"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if len(body.Data) != 2 {
		t.Errorf("len = %d, want 2", len(body.Data))
	}
}

func TestTerms_FilterByTaxonomy(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	store.AddTerm(Term{ID: "t1", Slug: "news", Name: "News", Taxonomy: "category", Path: "news", Depth: 1})
	store.AddTerm(Term{ID: "t2", Slug: "go", Name: "Go", Taxonomy: "tag", Path: "go", Depth: 1})

	mux := http.NewServeMux()
	_ = Mount(mux, "/api/v1/terms", "/api/v1/taxonomies", Deps{Store: store})

	req := httptest.NewRequest("GET", "/api/v1/terms?taxonomy=tag", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body struct {
		Data []Term `json:"data"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if len(body.Data) != 1 || body.Data[0].ID != "t2" {
		t.Errorf("filter failed: %+v", body.Data)
	}
}

func TestTerms_GetNotFound(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	_ = Mount(mux, "/api/v1/terms", "/api/v1/taxonomies", Deps{Store: NewMemoryStore()})
	req := httptest.NewRequest("GET", "/api/v1/terms/ghost", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != 404 {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}
