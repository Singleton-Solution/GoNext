// Package dev implements the host-side /_/plugins/dev/install endpoint
// that the `gonext plugin dev` CLI talks to during a watch loop. See
// docs/02-plugin-system.md for the workflow.
//
// The endpoint is only mounted when Config.Plugins.DevMode is true. The
// production default is DevMode=false, so prod images never expose it
// at all — the route is registered or it isn't, there is no "off"
// behaviour at request time.
//
// This file groups the typed error → JSON converter used by the
// handler. It exists in its own file (rather than inside handler.go)
// because we want every error path to flow through one place: leaking
// a stack trace or a filesystem path into the JSON body is the kind of
// foot-gun a single funnel keeps from happening.
package dev

import (
	"encoding/json"
	"net/http"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/manifest"
)

// errorBody is the JSON envelope every non-2xx response uses. The
// shape matches what the CLI's httpUploader displays on a non-2xx
// response (it prints the body verbatim), and lets a human reading the
// error in a terminal understand WHAT went wrong without needing the
// full stack.
//
// Fields:
//
//	code    — stable machine token. Clients branch on this.
//	message — human-readable summary. Never contains paths/internals.
//	errors  — optional list, populated only for 422 validation results.
//	          Each item carries a JSON-Pointer-ish path and a message
//	          pulled straight from the manifest validator.
type errorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Errors  []errorDetail  `json:"errors,omitempty"`
}

// errorDetail is one validation issue. Mirrors manifest.ValidationError
// at the wire layer so the CLI can render the same list the host saw.
type errorDetail struct {
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

// Stable error codes. Keep the literals frozen — the CLI may branch on
// them in the future, and any silent rename would break compatibility.
const (
	codeUnauthorized       = "unauthorized"
	codeBadRequest         = "bad_request"
	codePayloadTooLarge    = "payload_too_large"
	codeManifestInvalid    = "manifest_invalid"
	codeReloadInProgress   = "reload_in_progress"
	codeInternal           = "internal"
)

// writeError serialises an error envelope and writes it with the given
// HTTP status. It is the ONLY place errors leave this package, so the
// "no paths, no stack traces" rule lives here and only here.
//
// The handler is expected to have already mapped any internal error
// into one of these high-level shapes — writeError does NOT inspect or
// format raw error values it doesn't recognise.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{
		Code:    code,
		Message: message,
	})
}

// writeValidationErrors is the 422 surface. It accepts a manifest.Errors
// list and renders it as a structured payload so the CLI / IDE can
// underline the exact field that broke. We deliberately surface every
// issue — Validate already aggregates them — so the developer fixes
// their manifest in one round trip.
func writeValidationErrors(w http.ResponseWriter, errs manifest.Errors) {
	details := make([]errorDetail, 0, len(errs))
	for _, e := range errs {
		details = append(details, errorDetail{
			Path:    e.Path,
			Message: e.Message,
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(errorBody{
		Code:    codeManifestInvalid,
		Message: "manifest failed schema validation",
		Errors:  details,
	})
}
