package resolvers

import (
	"context"
	"errors"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/graphql/model"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// listFirstHardCap is the absolute upper bound on the page size of a
// list query. The cost analyzer enforces a softer cap via the cost
// budget; this is the safety net so a single buggy resolver cannot
// fetch unbounded rows. Tuned to match the REST handler's page-size
// limit so behavior is consistent across transports.
const listFirstHardCap = 100

// listFirstDefault is the fallback page size when the client omits
// `first:`. Pessimistically small — a public renderer should always
// pass an explicit `first:`, so this only kicks in for exploratory
// queries from the GraphQL playground.
const listFirstDefault = 20

// canSeePost is the visibility predicate for posts in the GraphQL
// surface. Published posts are visible to everyone; everything else
// requires the read_private_posts capability. The full visibility
// rules (drafts to the author themselves, scheduled posts to editors,
// password-protected posts) land in a follow-up issue — this is the
// scaffold.
func canSeePost(ctx context.Context, pol policy.Policy, row PostRow) bool {
	if row.Status == string(model.PostStatusPublished) {
		return true
	}
	p, ok := policy.FromContext(ctx)
	if !ok {
		return false
	}
	// The author can always see their own drafts.
	if p.UserID != "" && p.UserID == row.AuthorID {
		return true
	}
	if pol == nil {
		return false
	}
	return pol.Can(p, policy.CapReadPrivatePosts, nil).Allowed
}

// currentUserID returns the principal's UserID, or "" when anonymous.
// Small wrapper so resolvers don't repeat the two-line pattern.
func currentUserID(ctx context.Context) string {
	p, ok := policy.FromContext(ctx)
	if !ok {
		return ""
	}
	return p.UserID
}

// errUnauthorized / errForbidden are the canonical GraphQL errors for
// the two auth failure modes. We use sentinel errors so callers can
// detect them with errors.Is and so the HTTP layer can promote them
// to extension fields ({"extensions": {"code": "UNAUTHORIZED"}}).
var (
	errUnauthorized = &GQLAuthErrorPub{Code: "UNAUTHORIZED", Message: "authentication required"}
	errForbidden    = &GQLAuthErrorPub{Code: "FORBIDDEN", Message: "permission denied"}
)

// GQLAuthErrorPub is the typed error the auth resolvers return. The
// HTTP handler inspects it via errors.As and maps it onto the
// GraphQL error extensions map. Keeping a typed wrapper means we
// don't have to parse error message strings.
//
// The type is exported (with a slightly awkward name to flag that
// it's a serialization boundary, not a normal Go error) so the HTTP
// handler in the parent package can errors.As it without an
// internal-package dependency.
type GQLAuthErrorPub struct {
	Code    string
	Message string
}

func (e *GQLAuthErrorPub) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	return strings.ToLower(e.Code) + ": " + e.Message
}

// IsAuthError is a small convenience for callers so they don't need
// to type-assert with errors.As just to inspect the code.
func IsAuthError(err error) (*GQLAuthErrorPub, bool) {
	var ae *GQLAuthErrorPub
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}
