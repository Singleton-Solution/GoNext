package wprest

import (
	"encoding/json"
	"net/http"
)

// wpError is the canonical WordPress REST API error envelope.
//
// Live WP responses look exactly like this:
//
//	{
//	  "code":    "rest_post_invalid_id",
//	  "message": "Invalid post ID.",
//	  "data":    {"status": 404}
//	}
//
// The code is a stable machine-readable identifier (snake_case, namespaced
// by resource); message is human-readable; data carries the HTTP status and
// any additional fields a particular endpoint needs to surface (e.g. an
// invalid-param list). WP clients branch on `code` and surface `message` to
// the user — both fields are part of the contract.
type wpError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Data    wpErrorData    `json:"data"`
	Addn    map[string]any `json:"-"` // reserved for future per-error extensions
}

// wpErrorData is the `data` sub-object on a WP error response. Status is
// the only field we set today; the type is open for future extension
// (`params`, `details`, etc.) without breaking clients.
type wpErrorData struct {
	Status int `json:"status"`
}

// writeError writes a WP-shaped error JSON body with the given status,
// machine code, and human message. The Content-Type is plain
// application/json — WP does not use application/problem+json (that's
// our native shape; WP clients would not recognize it).
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	body := wpError{
		Code:    code,
		Message: message,
		Data:    wpErrorData{Status: status},
	}
	// Encoding errors are swallowed: status has already been written.
	// The right place to catch this is the unit test for the handler.
	_ = json.NewEncoder(w).Encode(body)
}

// Canonical error codes used by the shim. The strings here mirror
// well-known WP REST error codes one-for-one — keeping the literals
// stable is part of the compatibility contract.
const (
	errCodeMethodNotAllowed = "rest_no_route"
	errCodeInvalidPostID    = "rest_post_invalid_id"
	errCodeInvalidPageID    = "rest_page_invalid_id"
	errCodeInvalidUserID    = "rest_user_invalid_id"
	errCodeInvalidTermID    = "rest_term_invalid"
	errCodeForbidden        = "rest_forbidden"
	errCodeUnauthenticated  = "rest_not_logged_in"
	errCodeInvalidParam     = "rest_invalid_param"
	errCodeNotFound         = "rest_no_route"

	// Write-path codes. Live WP emits these literals on the matching
	// failure modes; keeping the strings identical is what lets a WP
	// plugin's error branching work without changes.
	errCodeInvalidNonce  = "rest_cookie_invalid_nonce"
	errCodeInvalidJSON   = "rest_invalid_json"
	errCodeBodyTooLarge  = "rest_request_too_large"
	errCodePostExists    = "rest_post_exists"
	errCodeUserExists    = "rest_user_exists"
	errCodeTermExists    = "rest_term_exists"
	errCodeCannotCreate  = "rest_cannot_create"
	errCodeCannotEdit    = "rest_cannot_edit"
	errCodeCannotDelete  = "rest_cannot_delete"
)

// writeMethodNotAllowed is the canonical 405 used for the write methods
// the read-only shim refuses. The body shape matches what live WP returns
// when a route doesn't accept the requested method.
func writeMethodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, errCodeMethodNotAllowed,
		"No route was found matching the URL and request method.")
}
