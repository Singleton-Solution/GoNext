package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteJSON_OK(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusCreated, map[string]string{"hello": "world"})

	if got, want := rec.Code, http.StatusCreated; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Content-Type"), contentTypeJSON; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["hello"] != "world" {
		t.Errorf("body = %v, want hello=world", body)
	}
}

func TestWriteJSON_NilBody(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusNoContent, nil)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

func TestWriteError(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusForbidden, "policy_denied", "no role on principal grants edit_posts")

	if got, want := rec.Code, http.StatusForbidden; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Content-Type"), contentTypeProblem; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}

	var pd ProblemDetails
	if err := json.Unmarshal(rec.Body.Bytes(), &pd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pd.Status != http.StatusForbidden {
		t.Errorf("Status = %d, want %d", pd.Status, http.StatusForbidden)
	}
	if pd.Code != "policy_denied" {
		t.Errorf("Code = %q, want policy_denied", pd.Code)
	}
	if pd.Title != "Forbidden" {
		t.Errorf("Title = %q, want Forbidden", pd.Title)
	}
	if pd.Type != "about:blank" {
		t.Errorf("Type = %q, want about:blank", pd.Type)
	}
}

func TestWriteProblem_Defaults(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	// Zero status should normalize to 500.
	WriteProblem(rec, ProblemDetails{Code: "internal"})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	var pd ProblemDetails
	if err := json.Unmarshal(rec.Body.Bytes(), &pd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pd.Type != "about:blank" {
		t.Errorf("Type = %q, want about:blank", pd.Type)
	}
	if pd.Title != "Internal Server Error" {
		t.Errorf("Title = %q, want Internal Server Error", pd.Title)
	}
}
