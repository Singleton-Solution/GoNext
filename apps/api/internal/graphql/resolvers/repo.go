package resolvers

import (
	"context"
	"time"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/graphql/model"
)

// PostRow is the domain representation of a post used by the GraphQL
// resolvers. We define it here (not pulled from a future `domain`
// package) because the full domain model has not landed yet — issue
// #83 is the GraphQL scaffold; the persistent post type comes online
// in a sibling issue. The shape mirrors the SQL columns from
// migrations/000004_posts.up.sql so swapping the in-memory PostRepo
// for a pgx-backed implementation later does not change the resolver
// signatures.
//
// Time fields use *time.Time on optional values because GraphQL maps
// `DateTime` to a non-null Go value and a nullable GraphQL field to a
// pointer; the resolver converts pointer-zero to a GraphQL null.
type PostRow struct {
	ID          string
	Title       string
	Slug        string
	Status      string
	Excerpt     *string
	AuthorID    string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	PublishedAt *time.Time
}

// UserRow is the domain representation of a user. Same scaffolding
// pattern as PostRow — it tracks the columns from
// migrations/000002_users.up.sql.
type UserRow struct {
	ID          string
	Handle      string
	DisplayName *string
	Email       string
	CreatedAt   time.Time
}

// PostFilter is the persistence-layer projection of model.PostFilter.
// Resolvers translate the GraphQL input into this shape before
// handing it to the repository — keeping the GraphQL model types out
// of the persistence boundary.
type PostFilter struct {
	Status      *string
	AuthorID    *string
	TitlePrefix *string
}

// PostPage is the persistence-layer cursor pagination result.
// Resolvers wrap this into a model.PostConnection.
type PostPage struct {
	Rows       []PostRow
	TotalCount int
	HasNext    bool
}

// PostRepo is the interface the GraphQL resolvers depend on. Tests
// substitute an in-memory implementation; production wires up a
// pgx-backed implementation that lives in apps/api/internal/posts (a
// future package).
//
// The interface is deliberately narrow — only the queries the
// resolvers actually use. Adding fields means widening this
// interface, which is the right place to feel the cost of a fat API.
type PostRepo interface {
	ByID(ctx context.Context, id string) (*PostRow, error)
	List(ctx context.Context, filter PostFilter, first int, afterCursor string) (*PostPage, error)
	Create(ctx context.Context, in PostRow) (*PostRow, error)
}

// UserRepo is the interface the GraphQL resolvers depend on for user
// lookups. ByIDs is the batched variant the dataloader calls — it
// MUST return rows in the SAME order as the input ids and use a NIL
// entry for any id that was not found, otherwise the dataloader
// shape contract breaks (see dataloader/loader.go for the contract).
type UserRepo interface {
	ByID(ctx context.Context, id string) (*UserRow, error)
	ByIDs(ctx context.Context, ids []string) ([]*UserRow, error)
}

// toGraphQLPost is the persistence -> GraphQL projection. It does NOT
// resolve the author (the resolver fans out through the dataloader
// for that); it leaves Author=nil so the Post.author field resolver
// runs.
func toGraphQLPost(r PostRow) *model.Post {
	p := &model.Post{
		ID:        r.ID,
		Title:     r.Title,
		Slug:      r.Slug,
		Status:    model.PostStatus(r.Status),
		Excerpt:   r.Excerpt,
		AuthorID:  r.AuthorID,
		CreatedAt: model.NewDateTime(r.CreatedAt),
		UpdatedAt: model.NewDateTime(r.UpdatedAt),
	}
	if r.PublishedAt != nil {
		dt := model.NewDateTime(*r.PublishedAt)
		p.PublishedAt = &dt
	}
	return p
}

// toGraphQLUser is the persistence -> GraphQL projection. Sensitive
// fields are masked here based on the viewing principal — callers
// pass viewerID and a "canSeeOthers" flag so this layer doesn't have
// to know about the capability system.
func toGraphQLUser(r UserRow, viewerID string, canSeeOthers bool) *model.User {
	out := &model.User{
		ID:          r.ID,
		Handle:      r.Handle,
		DisplayName: r.DisplayName,
		CreatedAt:   model.NewDateTime(r.CreatedAt),
	}
	// Email is exposed to the viewer themselves, or to principals
	// with the list_users capability (handled by the caller). For
	// everyone else, the field stays nil so the GraphQL response is
	// {"email": null}.
	if r.ID == viewerID || canSeeOthers {
		email := r.Email
		out.Email = &email
	}
	return out
}
