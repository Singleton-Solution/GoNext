package rum

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// Deps is the dependency bag for Mount. Every field is required
// except Logger and Now which fall back to slog.Default / time.Now
// for production wiring convenience.
type Deps struct {
	// Store persists beacon events and serves percentile reads.
	// Required.
	Store EventStore

	// Policy resolves the jobs.admin capability check on the read
	// endpoints. Required.
	Policy policy.Policy

	// BeaconMiddleware is the optional middleware chain applied
	// only to the anonymous /_/rum/beacon endpoint. The expected
	// usage is to wire a ratelimit.Middleware here so a hostile
	// client can't hammer the ingest path. nil means "no
	// middleware", which is appropriate for tests and dev.
	BeaconMiddleware func(http.Handler) http.Handler

	// Logger receives structured log lines. nil falls back to
	// slog.Default.
	Logger *slog.Logger

	// Now is the time source. nil falls back to time.Now. Tests
	// pin this to a deterministic clock so the percentile
	// window aligns with the seeded data.
	Now func() time.Time
}

func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("admin/rum: Store is required")
	}
	if d.Policy == nil {
		return errors.New("admin/rum: Policy is required")
	}
	return nil
}

// Mount wires the RUM routes onto mux:
//
//	POST /_/rum/beacon                            — anonymous ingest
//	GET  {readBase}/percentiles                   — gated by jobs.admin
//	GET  {readBase}/slow-routes                   — gated by jobs.admin
//
// beaconBase is normally "/_/rum/beacon" and readBase is normally
// "/api/v1/admin/rum". Passing them explicitly lets a host that
// mounts the API under a non-standard prefix shift both halves
// independently.
func Mount(mux *http.ServeMux, beaconBase, readBase string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}

	beaconBase = strings.TrimRight(beaconBase, "/")
	readBase = strings.TrimRight(readBase, "/")

	beacon, err := NewBeaconHandler(deps.Store, deps.Now, deps.Logger)
	if err != nil {
		return err
	}
	query, err := NewQueryHandler(deps.Store, deps.Policy, deps.Now, deps.Logger)
	if err != nil {
		return err
	}

	var beaconHandler http.Handler = beacon
	if deps.BeaconMiddleware != nil {
		beaconHandler = deps.BeaconMiddleware(beaconHandler)
	}

	// The beacon endpoint is intentionally registered without a
	// method prefix on the mux pattern — the handler does its
	// own method check so we can return a 405 with a helpful
	// Allow header rather than the bland 404 net/http emits when
	// a "POST /x" pattern doesn't match.
	mux.Handle(beaconBase, beaconHandler)
	mux.HandleFunc("GET "+readBase+"/percentiles", query.ServePercentiles)
	mux.HandleFunc("GET "+readBase+"/slow-routes", query.ServeSlowestRoutes)
	return nil
}
