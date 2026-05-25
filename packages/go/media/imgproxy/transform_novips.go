//go:build !vips

package imgproxy

import (
	"errors"
)

// newGovipsTransformer is the no-vips build's stub. It always returns
// an error so Default falls back to the stdlib backend. The error
// surfaces in the boot-time log line so an operator can tell why their
// GONEXT_IMAGEPROC=govips pin didn't take effect ("the binary was
// compiled without the vips tag" is the answer 100% of the time, but
// the explicit log is friendlier than silent fallback).
//
// To enable the govips backend, build with `-tags vips` and link
// against libvips at runtime. See apps/api/Dockerfile for the
// production wiring.
func newGovipsTransformer() (Transformer, error) {
	return nil, errors.New("imgproxy: govips backend not compiled in (build with -tags vips)")
}
