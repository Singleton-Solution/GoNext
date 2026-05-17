package pool

import "errors"

// errorsAs is a thin wrapper so metrics.go can avoid importing
// "errors" directly (keeps that file metrics-only). It is the
// stdlib's errors.As, no behavioral change.
func errorsAs(err error, target any) bool { return errors.As(err, target) }
