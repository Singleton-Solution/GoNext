package importer

import (
	"errors"
	"fmt"
)

// ErrAborted is the sentinel returned by Run when OnConflict ==
// ConflictFail and a conflict is encountered, or when the WXR
// stream is malformed past the point of recovery. The Report
// returned alongside still contains the partial counts so callers
// can surface "we got to N posts before bailing".
var ErrAborted = errors.New("importer: aborted")

// ImportError is one record-scoped failure recorded on the Report.
// The fields are deliberately string-y rather than typed: the
// downstream consumer is a human reading a CLI report or a JSON
// summary, and the importer never branches on a previous error's
// shape.
//
// ImportError satisfies the error interface so a single record's
// failure can be returned up the stack without losing context when
// the failure is fatal.
type ImportError struct {
	// Stage is a short label describing where in the import the
	// failure originated: "options", "header", "author", "category",
	// "tag", "post", "post.convert", "post.upsert", "term.relate",
	// "comment", "tx". Used to filter the Errors slice and to drive
	// the CLI report grouping.
	Stage string

	// WPID is the WordPress-side identifier for the failing record:
	// wp:author_id for authors, wp:term_id for categories/tags,
	// wp:post_id for posts, wp:comment_id for comments. Empty when
	// the failure is not record-scoped (e.g. stage="header").
	WPID string

	// Slug is the natural-key string for the record where it is
	// shorter than the WPID and more memorable. Empty when not
	// applicable.
	Slug string

	// Reason is a free-form explanation of the failure. Wraps the
	// underlying error's message when one exists.
	Reason string

	// err is the wrapped error, if any. We keep it private so the
	// only way to surface it is via Unwrap; that keeps the JSON
	// projection clean (callers iterate Errors and read the string
	// fields, not the error chain).
	err error
}

// Error implements error.
func (e *ImportError) Error() string {
	switch {
	case e.WPID != "" && e.Slug != "":
		return fmt.Sprintf("importer[%s] wp:%s (slug=%q): %s", e.Stage, e.WPID, e.Slug, e.Reason)
	case e.WPID != "":
		return fmt.Sprintf("importer[%s] wp:%s: %s", e.Stage, e.WPID, e.Reason)
	case e.Slug != "":
		return fmt.Sprintf("importer[%s] slug=%q: %s", e.Stage, e.Slug, e.Reason)
	default:
		return fmt.Sprintf("importer[%s]: %s", e.Stage, e.Reason)
	}
}

// Unwrap returns the wrapped error so errors.Is / errors.As can
// match on the underlying cause when callers care.
func (e *ImportError) Unwrap() error { return e.err }

// newImportError builds an *ImportError from a wrapped error,
// using the error's message as the Reason. Returns nil when err
// is nil so callers can write
//
//	if e := newImportError(...); e != nil { ... }
//
// without a redundant nil check on the original error.
func newImportError(stage, wpID, slug string, err error) *ImportError {
	if err == nil {
		return nil
	}
	return &ImportError{
		Stage:  stage,
		WPID:   wpID,
		Slug:   slug,
		Reason: err.Error(),
		err:    err,
	}
}
