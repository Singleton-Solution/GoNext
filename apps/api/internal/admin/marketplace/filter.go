package marketplace

import (
	"strings"

	mp "github.com/Singleton-Solution/GoNext/packages/go/plugins/marketplace"
)

// filterByQuery returns listings whose name, slug, or summary contains
// q (case-insensitive substring match). An empty q is a no-op.
//
// We deliberately do this in Go rather than push it down to the
// store — the catalogue is small (hundreds of listings, not millions)
// and an in-process filter sidesteps the trigram-index dependency the
// schema doesn't have yet.
func filterByQuery(in []mp.Listing, q string) []mp.Listing {
	q = strings.TrimSpace(strings.ToLower(q))
	if q == "" {
		return in
	}
	out := make([]mp.Listing, 0, len(in))
	for _, l := range in {
		if strings.Contains(strings.ToLower(l.Name), q) ||
			strings.Contains(strings.ToLower(l.Summary), q) ||
			strings.Contains(strings.ToLower(l.Slug), q) {
			out = append(out, l)
		}
	}
	return out
}
