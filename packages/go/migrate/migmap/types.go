package migmap

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// =============================================================================
// Source / EntityType constants
// =============================================================================

// Source is the origin system identifier persisted in
// migration_map.source. The column is free-form TEXT so unrecognised
// sources are accepted by the schema — but importers SHOULD reuse
// one of these constants so the operator-facing telemetry stays
// canonical. New constants are cheap: add the string here and an
// importer can write it without a migration.
type Source string

const (
	// SourceWordPress is the value used by the WXR importer
	// (packages/go/migrate/wxr/). Issue #144 introduces it; this
	// constant exists so #144 and downstream importers don't drift
	// over the literal "wp" vs "wordpress".
	SourceWordPress Source = "wp"

	// SourceGhost is reserved for a future Ghost JSON importer. The
	// constant lives in this list so the column-level CHECK on
	// `source` doesn't need to change when that importer lands.
	SourceGhost Source = "ghost"
)

// EntityType is the kind of GoNext object the mapping target refers
// to. The migration_map.entity_type column has a CHECK constraint
// pinning the value to one of these strings — adding a new entity
// type therefore requires both a new constant here and a migration
// that extends the CHECK.
//
// We expose this as a Go type rather than free strings so the
// importer code reads `migmap.EntityPost` instead of `"post"`, and
// so a typo at the call site is a compile error.
type EntityType string

const (
	// EntityUser is the GoNext users table. Source IDs are typically
	// WP user_ids (numeric strings) or Ghost author slugs.
	EntityUser EntityType = "user"

	// EntityPost is the GoNext posts table. Covers every post_type
	// that WP exports (post, page, attachment, custom CPTs); the
	// post_type itself is preserved in meta.
	EntityPost EntityType = "post"

	// EntityTerm is the GoNext terms table. Covers categories, tags,
	// and any custom taxonomy a WP site might define.
	EntityTerm EntityType = "term"

	// EntityComment is the GoNext comments table.
	EntityComment EntityType = "comment"

	// EntityAttachment is the GoNext media/attachments table.
	// WP exports attachments as posts with post_type='attachment',
	// but in GoNext they live in a separate table — the importer
	// uses this constant to distinguish.
	EntityAttachment EntityType = "attachment"
)

// validEntityTypes is the in-Go mirror of the CHECK constraint on
// migration_map.entity_type. We could rely on Postgres to reject a
// bad value, but a Go-side check fails earlier (before the DB
// round-trip) with a clearer error.
var validEntityTypes = map[EntityType]struct{}{
	EntityUser:       {},
	EntityPost:       {},
	EntityTerm:       {},
	EntityComment:    {},
	EntityAttachment: {},
}

// Valid reports whether e is one of the well-known entity types. The
// underlying TEXT column is CHECK-constrained to this same set, so a
// false here means the insert would be rejected by Postgres anyway.
func (e EntityType) Valid() bool {
	_, ok := validEntityTypes[e]
	return ok
}

// =============================================================================
// Mapping
// =============================================================================

// Mapping is a single row in migration_map. It associates a
// (source, entity_type, source_id) tuple in the upstream system with
// a GoNext UUID.
//
// The zero value is not valid; use [Mapping.Validate] before persisting
// or rely on [Store.Put] to do it.
type Mapping struct {
	// Source is the origin-system identifier. See the Source* constants.
	Source Source

	// EntityType narrows what kind of GoNext object TargetID refers
	// to. Must match one of the EntityType constants — the underlying
	// column is CHECK-constrained to the same set.
	EntityType EntityType

	// SourceID is the upstream's native identifier, stringified
	// (the column is TEXT). For WP this is the numeric ID as a
	// decimal string; for Ghost it's the opaque slug.
	SourceID string

	// TargetID is the GoNext UUID assigned during import. The
	// importer mints this with gen_uuid_v7() (or the Go equivalent)
	// in the same transaction that records the mapping.
	TargetID uuid.UUID

	// Meta is per-mapping provenance — original login, source URL,
	// content hash, anything the importer wants to preserve for
	// later diagnostics. Stored as JSONB; the on-conflict merge
	// semantics in [PostgresStore.Put] mean a second-pass importer
	// can add keys without destroying earlier ones.
	//
	// Nil is treated as the empty map. Non-nil empty maps are
	// preserved verbatim.
	Meta map[string]any
}

// ErrInvalidMapping wraps every validation error returned by
// [Mapping.Validate]. Callers use errors.Is to distinguish a bad
// input from a transport error.
var ErrInvalidMapping = errors.New("migmap: invalid mapping")

// Validate runs the structural checks every store performs before
// touching the database: Source non-empty, EntityType in the known
// set, SourceID non-empty, TargetID non-zero. Length caps mirror the
// CHECK constraints in the SQL migration.
//
// The checks are deliberately tight — a malformed mapping is almost
// always a bug in the importer, and surfacing it here keeps the
// migration_map table clean of garbage rows.
func (m Mapping) Validate() error {
	if m.Source == "" {
		return fmt.Errorf("%w: Source is required", ErrInvalidMapping)
	}
	if len(m.Source) > 64 {
		return fmt.Errorf("%w: Source longer than 64 chars (%d)", ErrInvalidMapping, len(m.Source))
	}
	if !m.EntityType.Valid() {
		return fmt.Errorf("%w: unknown EntityType %q", ErrInvalidMapping, string(m.EntityType))
	}
	if m.SourceID == "" {
		return fmt.Errorf("%w: SourceID is required", ErrInvalidMapping)
	}
	if len(m.SourceID) > 255 {
		return fmt.Errorf("%w: SourceID longer than 255 chars (%d)", ErrInvalidMapping, len(m.SourceID))
	}
	if m.TargetID == uuid.Nil {
		return fmt.Errorf("%w: TargetID is the zero UUID", ErrInvalidMapping)
	}
	return nil
}
