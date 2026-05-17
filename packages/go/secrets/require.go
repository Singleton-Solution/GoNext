package secrets

import (
	"errors"
	"fmt"
)

// Require fetches every key from store and returns a single aggregated
// error listing every key that was missing or failed to fetch. It does
// *not* return the values — its only job is to fail fast at boot.
//
// Aggregation matters: operators who misconfigure five secrets shouldn't
// have to fix and restart five times. The returned error joins every
// per-key failure via errors.Join, so callers can errors.Is(err, ErrNotFound)
// to detect the common case.
//
// Values are never included in the returned error. Only key names appear.
func Require(store Store, keys ...string) error {
	if store == nil {
		return errors.New("secrets: Require called with nil store")
	}
	var errs []error
	for _, k := range keys {
		if _, err := store.Get(k); err != nil {
			// Get already wrapped with the key and ErrNotFound where applicable;
			// we add a "required:" prefix so the aggregated message scans well.
			errs = append(errs, fmt.Errorf("required %w", err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}
