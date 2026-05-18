package marketplace

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Ratings is the store for the plugin_ratings table.
//
// The schema's composite PK is (plugin_version_id, user_id). Submit is
// modelled as an UPSERT so a user changing their mind doesn't need
// caller-side update/insert dispatch — and the database handles the
// "is this a new rating" vs "this user already rated this version"
// question atomically.
type Ratings struct {
	db PgxQuerier

	// NowFunc, if set, replaces time.Now for the CreatedAt default
	// Submit assigns when the row is new.
	NowFunc nowFunc
}

func NewRatings(db PgxQuerier) *Ratings {
	if db == nil {
		panic("marketplace.NewRatings: db is required")
	}
	return &Ratings{db: db}
}

// Submit writes a rating.
//
// The behaviour for "this user has already rated this version" is
// deliberately UPSERT-style: the existing row's stars and review_text
// are overwritten with the new values, and created_at is *preserved*
// (the original moment of first rating is the meaningful timestamp,
// not the moment the user changed their mind).
//
// Returns the stored Rating on success. ErrInvalidInput on out-of-
// range stars; ErrAlreadyExists is intentionally NOT returned —
// repeat submits are a feature, not a conflict.
//
// Callers that want strict "first rating wins" semantics should layer
// their own check on top; UPSERT here matches the marketplace UX where
// editing one's review is expected.
func (r *Ratings) Submit(ctx context.Context, in Rating) (Rating, error) {
	if in.PluginVersionID == uuid.Nil {
		return Rating{}, fmt.Errorf("%w: plugin_version_id is required", ErrInvalidInput)
	}
	if in.UserID == uuid.Nil {
		return Rating{}, fmt.Errorf("%w: user_id is required", ErrInvalidInput)
	}
	if in.Stars < 1 || in.Stars > 5 {
		return Rating{}, fmt.Errorf("%w: stars must be 1..5, got %d", ErrInvalidInput, in.Stars)
	}

	var reviewArg any
	if in.ReviewText != "" {
		reviewArg = in.ReviewText
	}

	const sql = `
		INSERT INTO plugin_ratings
			(plugin_version_id, user_id, stars, review_text)
		VALUES
			($1, $2, $3, $4)
		ON CONFLICT (plugin_version_id, user_id)
		DO UPDATE SET
			stars       = EXCLUDED.stars,
			review_text = EXCLUDED.review_text
		RETURNING plugin_version_id, user_id, stars,
		          COALESCE(review_text, ''),
		          created_at
	`
	row := r.db.QueryRow(ctx, sql,
		in.PluginVersionID, in.UserID, in.Stars, reviewArg,
	)
	var out Rating
	if err := row.Scan(
		&out.PluginVersionID, &out.UserID, &out.Stars, &out.ReviewText, &out.CreatedAt,
	); err != nil {
		if isCheckViolation(err) {
			// Defence in depth: the application-side guard above
			// should already have caught the out-of-range case.
			return Rating{}, fmt.Errorf("%w: stars out of range", ErrInvalidInput)
		}
		return Rating{}, fmt.Errorf("marketplace.Ratings.Submit: %w", err)
	}
	return out, nil
}

// Aggregate returns the avg+count summary for one version.
//
// Returns a zero Aggregate (Count == 0, Average == 0) when the version
// has no ratings — no ErrNotFound here, because "this version has no
// ratings" is a legitimate read result for a UI rendering "0 reviews".
func (r *Ratings) Aggregate(ctx context.Context, versionID uuid.UUID) (Aggregate, error) {
	if versionID == uuid.Nil {
		return Aggregate{}, fmt.Errorf("%w: version_id is required", ErrInvalidInput)
	}
	const sql = `
		SELECT
			COALESCE(AVG(stars)::FLOAT8, 0),
			COUNT(*)
		  FROM plugin_ratings
		 WHERE plugin_version_id = $1
	`
	var avg float64
	var count int64
	if err := r.db.QueryRow(ctx, sql, versionID).Scan(&avg, &count); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// COUNT(*) over an empty set returns 0; pgx.ErrNoRows
			// would only fire if the row itself was missing, which
			// can't happen for an aggregate. Defensive.
			return Aggregate{PluginVersionID: versionID}, nil
		}
		return Aggregate{}, fmt.Errorf("marketplace.Ratings.Aggregate: %w", err)
	}
	out := Aggregate{
		PluginVersionID: versionID,
		Average:         avg,
		Count:           count,
	}
	return out, nil
}
