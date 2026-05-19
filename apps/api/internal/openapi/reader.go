package openapi

import (
	"bytes"
	"io"
)

// newSpecReader returns a fresh ReadSeeker over the embedded spec bytes.
// http.ServeContent needs a Seeker so it can support Range requests and
// HEAD; bytes.Reader satisfies both.
//
// A new reader is returned per call so concurrent handlers don't share the
// same cursor.
func newSpecReader() io.ReadSeeker {
	return bytes.NewReader(spec)
}

// newSpecYAMLReader is the YAML companion of newSpecReader.
func newSpecYAMLReader() io.ReadSeeker {
	return bytes.NewReader(specYAML)
}
