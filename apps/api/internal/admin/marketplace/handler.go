package marketplace

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	mp "github.com/Singleton-Solution/GoNext/packages/go/plugins/marketplace"
)

// BundleFetcher resolves a version's wasm bundle bytes by SHA-256
// digest, returning a reader that the lifecycle.Install path will
// consume.
//
// Production wiring points this at MinIO/S3 (the content-addressed
// object store keyed by the digest); tests pass an in-memory map.
// The fetcher returns ErrBundleNotFound when no object exists for
// the digest so the handler can map it to a 502 rather than a 500
// — a missing bundle is an environment issue, not a bug.
type BundleFetcher interface {
	Fetch(ctx context.Context, sha256Hex string) (io.ReadCloser, error)
}

// ErrBundleNotFound is returned by a BundleFetcher when the digest
// has no object behind it.
var ErrBundleNotFound = errors.New("admin/marketplace: bundle not found")

// PluginInstaller is the narrow contract the handler needs from
// lifecycle.Manager. Defining it locally lets the test harness
// substitute a fake that doesn't drag the whole lifecycle package
// into the test binary.
//
// The real implementation is satisfied by *lifecycle.Manager: its
// Install(ctx, io.Reader) signature matches verbatim.
type PluginInstaller interface {
	Install(ctx context.Context, bundle io.Reader) (string, error)
}

// HostIDProvider hashes the requesting host's identity into the
// opaque value the install_events table stores. Production wires
// this to the deployment-id + tenant signature; tests use a static
// stub.
type HostIDProvider interface {
	HostID(ctx context.Context) string
}

// staticHostID is the trivial implementation used in tests and as
// the production default until a richer hashing strategy lands.
type staticHostID string

func (s staticHostID) HostID(_ context.Context) string { return string(s) }

// NewStaticHostID returns a HostIDProvider that always reports the
// supplied id.
func NewStaticHostID(id string) HostIDProvider { return staticHostID(id) }

// Deps is the dependency bag for Mount. Every required field must be
// non-nil; validate() reports the missing one cleanly at boot.
type Deps struct {
	// Store is the marketplace read/write façade.
	Store Store

	// Installer dispatches to the host's plugin lifecycle.
	Installer PluginInstaller

	// Bundles fetches wasm bytes from object storage by digest.
	Bundles BundleFetcher

	// Policy gates the install capability check.
	Policy policy.Policy

	// HostID hashes the requesting host's identity for the install
	// event audit trail.
	HostID HostIDProvider

	// Logger receives structured log lines. nil falls back to
	// slog.Default — useful for tests.
	Logger *slog.Logger
}

func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("admin/marketplace: Store is required")
	}
	if d.Installer == nil {
		return errors.New("admin/marketplace: Installer is required")
	}
	if d.Bundles == nil {
		return errors.New("admin/marketplace: Bundles is required")
	}
	if d.Policy == nil {
		return errors.New("admin/marketplace: Policy is required")
	}
	return nil
}

// handlers is the resolved-Deps form passed around inside the
// package. Built once by Mount.
type handlers struct {
	store     Store
	installer PluginInstaller
	bundles   BundleFetcher
	policy    policy.Policy
	hostID    HostIDProvider
	logger    *slog.Logger
}

// Mount wires the marketplace routes onto mux under base (typically
// "/api/v1/admin/marketplace"). Returns an error rather than
// panicking so the boot path can surface a misconfiguration.
//
// Route tree:
//
//	GET    {base}/listings                         — catalogue browse
//	GET    {base}/listings/{slug}                  — listing detail
//	GET    {base}/listings/{slug}/versions         — version history
//	GET    {base}/listings/{slug}/ratings          — aggregate + reviews
//	POST   {base}/listings/{slug}/ratings          — submit / update a rating
//	POST   {base}/listings/{slug}/install          — install latest compatible version
//
// Read endpoints require a logged-in principal but no special
// capability — every operator can browse. Install + rating POST
// require CapInstallPlugins so a constrained role can't escalate
// by installing third-party code.
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.HostID == nil {
		deps.HostID = staticHostID("unset")
	}

	h := &handlers{
		store:     deps.Store,
		installer: deps.Installer,
		bundles:   deps.Bundles,
		policy:    deps.Policy,
		hostID:    deps.HostID,
		logger:    deps.Logger,
	}

	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base+"/listings", h.authed(h.listListings))
	mux.Handle("GET "+base+"/listings/{slug}", h.authed(h.getListing))
	mux.Handle("GET "+base+"/listings/{slug}/versions", h.authed(h.listVersions))
	mux.Handle("GET "+base+"/listings/{slug}/ratings", h.authed(h.listRatings))
	mux.Handle("POST "+base+"/listings/{slug}/ratings", h.gated(policy.CapInstallPlugins, h.submitRating))
	mux.Handle("POST "+base+"/listings/{slug}/install", h.gated(policy.CapInstallPlugins, h.install))
	return nil
}

// authed wraps a handler that requires a logged-in principal but no
// further capability check. Returns 401 if no principal is on the
// context.
func (h *handlers) authed(next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		next(w, r, pr)
	})
}

// gated wraps a handler that requires both authentication and a
// specific capability. Returns 401/403 with the standard error
// envelope.
func (h *handlers) gated(cap policy.Capability, next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if d := h.policy.Can(pr, cap, nil); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
		next(w, r, pr)
	})
}

// listListings handles GET /listings. Query params:
//
//	category — optional; matches plugin_listings.primary_category verbatim.
//	q        — optional; case-insensitive substring search over name/summary/slug.
//	sort     — optional; one of "recent" (default), "stars", "popular".
func (h *handlers) listListings(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	q := r.URL.Query()
	filter := ListFilter{
		Category: q.Get("category"),
		Query:    q.Get("q"),
		Sort:     SortKey(q.Get("sort")),
	}
	if filter.Sort == "" {
		filter.Sort = SortRecent
	}
	if !filter.Sort.Valid() {
		router.WriteError(w, http.StatusBadRequest, "invalid_sort",
			fmt.Sprintf("sort must be one of recent|stars|popular, got %q", filter.Sort))
		return
	}

	ctx := r.Context()
	listings, err := h.store.ListListings(ctx, filter)
	if err != nil {
		h.logger.ErrorContext(ctx, "admin/marketplace: list listings failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list listings")
		return
	}

	// Gather the per-listing sort signals. We resolve them via the
	// latest version of each listing (which is what the catalogue
	// sort uses); fan-out is O(N) round-trips. Acceptable for v1 —
	// hundreds of listings, not millions — and lets the store stay
	// schema-agnostic about what "the rating of a listing" means.
	stars := make(map[string]float64, len(listings))
	counts := make(map[string]int64, len(listings))
	installs := make(map[string]int64, len(listings))
	cards := make([]ListingCard, 0, len(listings))

	for _, l := range listings {
		versions, vErr := h.store.ListVersions(ctx, l.ID)
		if vErr != nil {
			h.logger.WarnContext(ctx, "admin/marketplace: list versions for listing failed",
				slog.String("slug", l.Slug), slog.Any("err", vErr))
		}
		var avg float64
		var cnt int64
		if len(versions) > 0 {
			agg, aErr := h.store.AggregateRatings(ctx, versions[0].ID)
			if aErr == nil {
				avg = agg.Average
				cnt = agg.Count
			}
		}
		inst, iErr := h.store.CountInstalls(ctx, l.ID)
		if iErr == nil {
			installs[l.Slug] = inst
		}
		stars[l.Slug] = avg
		counts[l.Slug] = cnt
		cards = append(cards, toCard(l, avg, cnt, installs[l.Slug]))
	}

	// Sort *after* the signals are gathered. We sort the projected
	// cards rather than the raw listings so the wire shape stays
	// authoritative.
	sortCards(cards, filter.Sort)

	router.WriteJSON(w, http.StatusOK, struct {
		Data []ListingCard `json:"data"`
	}{Data: cards})
}

// getListing handles GET /listings/{slug}.
func (h *handlers) getListing(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	slug := r.PathValue("slug")
	if slug == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_slug", "slug is required")
		return
	}
	ctx := r.Context()
	listing, err := h.store.GetListingBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, mp.ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "listing not found")
			return
		}
		h.logger.ErrorContext(ctx, "admin/marketplace: get listing failed",
			slog.String("slug", slug), slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to fetch listing")
		return
	}

	versions, _ := h.store.ListVersions(ctx, listing.ID)
	var (
		avg     float64
		count   int64
		latest  *VersionRow
	)
	if len(versions) > 0 {
		agg, aErr := h.store.AggregateRatings(ctx, versions[0].ID)
		if aErr == nil {
			avg = agg.Average
			count = agg.Count
		}
		row := toVersionRow(versions[0])
		latest = &row
	}
	installs, _ := h.store.CountInstalls(ctx, listing.ID)

	detail := ListingDetail{
		ListingCard:   toCard(listing, avg, count, installs),
		AuthorID:      listing.AuthorID,
		Status:        string(listing.Status),
		UpdatedAt:     listing.UpdatedAt,
		LatestVersion: latest,
	}
	router.WriteJSON(w, http.StatusOK, detail)
}

// listVersions handles GET /listings/{slug}/versions.
func (h *handlers) listVersions(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	slug := r.PathValue("slug")
	if slug == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_slug", "slug is required")
		return
	}
	ctx := r.Context()
	listing, err := h.store.GetListingBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, mp.ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "listing not found")
			return
		}
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to fetch listing")
		return
	}
	versions, err := h.store.ListVersions(ctx, listing.ID)
	if err != nil {
		h.logger.ErrorContext(ctx, "admin/marketplace: list versions failed",
			slog.String("slug", slug), slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list versions")
		return
	}

	type versionResp struct {
		VersionRow
		Compat []CompatRow `json:"compat"`
	}
	out := make([]versionResp, 0, len(versions))
	for _, v := range versions {
		compat, _ := h.store.ListCompat(ctx, v.ID)
		compatRows := make([]CompatRow, 0, len(compat))
		for _, c := range compat {
			compatRows = append(compatRows, toCompatRow(c))
		}
		out = append(out, versionResp{
			VersionRow: toVersionRow(v),
			Compat:     compatRows,
		})
	}
	router.WriteJSON(w, http.StatusOK, struct {
		Data []versionResp `json:"data"`
	}{Data: out})
}

// listRatings handles GET /listings/{slug}/ratings. The response
// bundles the per-version aggregate (using the latest published
// version) and the per-rating list.
func (h *handlers) listRatings(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	slug := r.PathValue("slug")
	if slug == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_slug", "slug is required")
		return
	}
	ctx := r.Context()
	listing, err := h.store.GetListingBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, mp.ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "listing not found")
			return
		}
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to fetch listing")
		return
	}

	versions, err := h.store.ListVersions(ctx, listing.ID)
	if err != nil {
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list versions")
		return
	}
	resp := RatingsResponse{Ratings: []RatingRow{}}
	if len(versions) > 0 {
		latest := versions[0].ID
		agg, aErr := h.store.AggregateRatings(ctx, latest)
		if aErr == nil {
			resp.Aggregate.Average = agg.Average
			resp.Aggregate.Count = agg.Count
		}
		ratings, rErr := h.store.ListRatings(ctx, latest)
		if rErr == nil {
			rows := make([]RatingRow, 0, len(ratings))
			for _, rt := range ratings {
				rows = append(rows, toRatingRow(rt))
			}
			resp.Ratings = rows
		}
	}
	router.WriteJSON(w, http.StatusOK, resp)
}

// submitRating handles POST /listings/{slug}/ratings.
func (h *handlers) submitRating(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	slug := r.PathValue("slug")
	if slug == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_slug", "slug is required")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16*1024))
	if err != nil {
		router.WriteError(w, http.StatusBadRequest, "read_body", "failed to read request body")
		return
	}
	defer func() { _ = r.Body.Close() }()
	var req SubmitRatingRequest
	if err := json.Unmarshal(body, &req); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	if req.Stars < 1 || req.Stars > 5 {
		router.WriteError(w, http.StatusBadRequest, "invalid_stars", "stars must be in 1..5")
		return
	}
	userID, err := uuid.Parse(pr.UserID)
	if err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_user_id",
			"rating requires a UUID-shaped principal user id")
		return
	}
	ctx := r.Context()

	// Confirm the slug exists and resolve the listing so we can pick
	// a default version when the caller didn't pin one.
	listing, err := h.store.GetListingBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, mp.ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "listing not found")
			return
		}
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to fetch listing")
		return
	}
	versionID := req.VersionID
	if versionID == uuid.Nil {
		versions, vErr := h.store.ListVersions(ctx, listing.ID)
		if vErr != nil || len(versions) == 0 {
			router.WriteError(w, http.StatusBadRequest, "no_version", "listing has no versions to rate")
			return
		}
		versionID = versions[0].ID
	}

	out, err := h.store.SubmitRating(ctx, mp.Rating{
		PluginVersionID: versionID,
		UserID:          userID,
		Stars:           req.Stars,
		ReviewText:      req.ReviewText,
	})
	if err != nil {
		if errors.Is(err, mp.ErrInvalidInput) {
			router.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}
		h.logger.ErrorContext(ctx, "admin/marketplace: submit rating failed",
			slog.String("slug", slug), slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to submit rating")
		return
	}
	router.WriteJSON(w, http.StatusCreated, toRatingRow(out))
}

// install handles POST /listings/{slug}/install. The handler resolves
// the latest non-deprecated version, fetches the wasm bytes via
// BundleFetcher, dispatches to lifecycle.Manager.Install, and records
// an install event in the marketplace audit trail.
func (h *handlers) install(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	slug := r.PathValue("slug")
	if slug == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_slug", "slug is required")
		return
	}
	ctx := r.Context()

	listing, err := h.store.GetListingBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, mp.ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "listing not found")
			return
		}
		h.logger.ErrorContext(ctx, "admin/marketplace: install: get listing failed",
			slog.String("slug", slug), slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to fetch listing")
		return
	}

	versions, err := h.store.ListVersions(ctx, listing.ID)
	if err != nil {
		h.logger.ErrorContext(ctx, "admin/marketplace: install: list versions failed",
			slog.String("slug", slug), slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list versions")
		return
	}
	target := pickLatestInstallable(versions)
	if target == nil {
		router.WriteError(w, http.StatusConflict, "no_installable_version",
			"listing has no non-deprecated versions")
		return
	}

	digest := hex.EncodeToString(target.WasmSHA256)
	bundle, err := h.bundles.Fetch(ctx, digest)
	if err != nil {
		if errors.Is(err, ErrBundleNotFound) {
			router.WriteError(w, http.StatusBadGateway, "bundle_missing",
				"the marketplace version's wasm bundle is not available in object storage")
			return
		}
		h.logger.ErrorContext(ctx, "admin/marketplace: install: fetch bundle failed",
			slog.String("slug", slug), slog.String("digest", digest), slog.Any("err", err))
		router.WriteError(w, http.StatusBadGateway, "bundle_fetch_failed", "failed to fetch plugin bundle")
		return
	}
	defer func() { _ = bundle.Close() }()

	pluginSlug, err := h.installer.Install(ctx, bundle)
	if err != nil {
		// Audit the failure so the install_events tally tracks the
		// error rate the moderation surface needs.
		_, _ = h.store.RecordInstallEvent(ctx, mp.InstallEvent{
			ListingID: listing.ID,
			VersionID: target.ID,
			HostID:    h.hostID.HostID(ctx),
			EventType: mp.EventErrored,
		})
		h.logger.ErrorContext(ctx, "admin/marketplace: install: lifecycle failed",
			slog.String("slug", slug), slog.Any("err", err))
		router.WriteError(w, http.StatusBadRequest, "install_failed", err.Error())
		return
	}

	// Success: record the installed event so the listing's lifetime
	// install counter ticks up.
	if _, evErr := h.store.RecordInstallEvent(ctx, mp.InstallEvent{
		ListingID: listing.ID,
		VersionID: target.ID,
		HostID:    h.hostID.HostID(ctx),
		EventType: mp.EventInstalled,
	}); evErr != nil {
		h.logger.WarnContext(ctx, "admin/marketplace: install: record event failed",
			slog.String("slug", slug), slog.Any("err", evErr))
	}

	router.WriteJSON(w, http.StatusOK, InstallResponse{
		Slug:       slug,
		Version:    target.Version,
		PluginSlug: pluginSlug,
	})
}

// pickLatestInstallable returns the newest non-deprecated version
// from a versions slice ordered by published_at DESC. nil when none
// qualify. Deprecated versions remain installable per the marketplace
// data model, but the install path prefers a non-deprecated row when
// one exists; only when *every* version is deprecated does the
// handler fall back to the first deprecated one.
func pickLatestInstallable(versions []mp.Version) *mp.Version {
	if len(versions) == 0 {
		return nil
	}
	for i := range versions {
		if versions[i].DeprecatedAt.IsZero() {
			return &versions[i]
		}
	}
	return &versions[0]
}

// sortCards reorders the projected cards by the supplied key. Stable
// so the natural created_at DESC order from the store is preserved for
// equal-key rows.
func sortCards(cards []ListingCard, key SortKey) {
	switch key {
	case SortStars:
		sort.SliceStable(cards, func(i, j int) bool { return cards[i].Stars > cards[j].Stars })
	case SortPopular:
		sort.SliceStable(cards, func(i, j int) bool { return cards[i].InstallCount > cards[j].InstallCount })
	}
}
