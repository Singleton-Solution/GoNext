package router

import (
	"encoding/json"
	"net/http"
)

// contentTypeJSON is the canonical media type for success responses.
// Includes the charset so older clients (and rare proxies) don't try
// to guess at the encoding.
const contentTypeJSON = "application/json; charset=utf-8"

// contentTypeProblem is the canonical media type for RFC 7807 problem
// details responses. Distinguishing the error type from regular JSON is
// the whole point of the RFC — it tells clients that the body is a
// machine-readable error envelope, not the resource they asked for.
const contentTypeProblem = "application/problem+json; charset=utf-8"

// WriteJSON writes body as JSON with the given status code. The
// Content-Type header is set to application/json; status is written
// before the body so handlers can't accidentally call WriteHeader twice.
//
// A nil body writes the status with no body — useful for 204 No Content
// or 304 Not Modified responses.
//
// Encoding errors are silently swallowed: the response has already been
// committed by the time json.Encoder runs, so there is no graceful
// recovery. The right place to catch encoding bugs is the unit test
// covering the handler.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

// ProblemDetails is the RFC 7807 problem-details payload. The struct
// names match the JSON keys per the RFC, with omitempty for the
// optional fields so a minimal {type,title,status} body stays compact.
//
// The Code field is a non-RFC extension that gives callers a stable
// machine-readable identifier for the error class (e.g. "version_mismatch",
// "validation_error") independent of the human-readable Title/Detail.
// Clients build branching logic on Code; humans read Detail.
type ProblemDetails struct {
	// Type is a URI that identifies the problem type. We use a stable
	// "about:blank" sentinel for problems that don't yet warrant their
	// own documentation page; once a problem grows a docs entry, we
	// swap in the URL without breaking clients (the type is opaque to
	// them).
	Type string `json:"type"`

	// Title is a short, human-readable summary of the problem type.
	// Should remain stable across occurrences (it is a label, not a
	// per-instance message).
	Title string `json:"title"`

	// Status is the HTTP status code, repeated in the body for the
	// benefit of clients that lose the response status (some logging
	// pipelines strip it).
	Status int `json:"status"`

	// Detail is the human-readable, per-instance explanation. Safe to
	// surface in UIs.
	Detail string `json:"detail,omitempty"`

	// Instance is a URI reference identifying the specific occurrence
	// of the problem. Often the request path. Optional.
	Instance string `json:"instance,omitempty"`

	// Code is a machine-readable error class. Non-RFC extension. Stable
	// across releases; clients branch on this string.
	Code string `json:"code,omitempty"`
}

// problemTypeBlank is the RFC-blessed sentinel for problems that don't
// yet have a dedicated documentation URL. Per RFC 7807 §4.2, clients
// MUST NOT rely on this field except as opaque identification.
const problemTypeBlank = "about:blank"

// WriteError writes a ProblemDetails response with the given status,
// machine-readable code, and human-readable message.
//
// The Title is derived from http.StatusText so the body is self-describing
// even when only Code+Detail are filled in by the caller. Instance is
// left empty here; route packages that want to attach the request path
// build a ProblemDetails directly and call WriteProblem.
func WriteError(w http.ResponseWriter, status int, code, msg string) {
	pd := ProblemDetails{
		Type:   problemTypeBlank,
		Title:  http.StatusText(status),
		Status: status,
		Detail: msg,
		Code:   code,
	}
	WriteProblem(w, pd)
}

// WriteProblem writes pd as a fully-formed problem-details response.
// Sets the application/problem+json Content-Type so clients can branch
// on media type rather than parsing the body to detect an error.
//
// If pd.Status is zero, it is normalized to 500 — handlers should
// always set it explicitly, but we'd rather return a coherent 500 than
// a 0-status response when a caller forgets.
func WriteProblem(w http.ResponseWriter, pd ProblemDetails) {
	if pd.Type == "" {
		pd.Type = problemTypeBlank
	}
	if pd.Status == 0 {
		pd.Status = http.StatusInternalServerError
	}
	if pd.Title == "" {
		pd.Title = http.StatusText(pd.Status)
	}
	w.Header().Set("Content-Type", contentTypeProblem)
	w.WriteHeader(pd.Status)
	_ = json.NewEncoder(w).Encode(pd)
}
