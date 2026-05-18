package verify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/packages/go/migrate/wxr"
)

// Verifier walks a WXR source for the second time and checks that
// every record made it across into the GoNext database.
//
// One Verifier per Run. The struct is small by design — fields are
// the substrate (DB pool, source factory) and a pluggable clock for
// tests. Run-scoped state lives in a private helper struct so a
// re-run doesn't leak counters between invocations.
type Verifier struct {
	// DB is the pool to verify against. Required. The verifier only
	// reads — it never issues writes — so a read-only role is
	// acceptable (and recommended for production runs against
	// migrated production databases).
	DB *pgxpool.Pool

	// SourceReader is a factory that returns a fresh io.Reader
	// positioned at the start of the WXR document. The verifier
	// calls it exactly once per Run; we don't accept a plain
	// io.Reader because rewinding would require seeking on the
	// caller's file/buffer, and most call sites prefer to open a
	// new handle. The returned Reader must be readable for the
	// entire duration of Run.
	//
	// Returns the Reader and an error. A nil Reader with a nil
	// error is treated as a usage error.
	SourceReader func() (io.Reader, error)

	// Now is the wall-clock for Took accounting. Defaults to
	// time.Now when nil.
	Now func() time.Time
}

// Run walks the source WXR a second time and emits a Report.
//
// The return value pair follows the same convention as
// importer.Run: the *Report is always non-nil and carries whatever
// partial state was accumulated when err became non-nil. err is
// reserved for fatal verifier failures (no DB connection, source
// open failure, malformed XML past recovery) — per-record
// discrepancies are appended to Report.Failures and Run keeps
// going.
//
// Run is safe to call exactly once per Verifier. A second Run on
// the same Verifier is supported but will open a fresh source
// reader; the report counters do not accumulate across calls.
func (v *Verifier) Run(ctx context.Context) (*Report, error) {
	report := &Report{}
	if v == nil {
		return report, fmt.Errorf("%w: nil verifier", ErrVerify)
	}
	if v.DB == nil {
		return report, fmt.Errorf("%w: nil DB pool", ErrVerify)
	}
	if v.SourceReader == nil {
		return report, fmt.Errorf("%w: nil SourceReader factory", ErrVerify)
	}

	now := v.Now
	if now == nil {
		now = time.Now
	}
	start := now()
	defer func() {
		report.Took = now().Sub(start)
		report.Finalize()
	}()

	r, err := v.SourceReader()
	if err != nil {
		return report, wrapVerifyErr("open source", err)
	}
	if r == nil {
		return report, fmt.Errorf("%w: SourceReader returned nil reader", ErrVerify)
	}
	if c, ok := r.(io.Closer); ok {
		defer func() { _ = c.Close() }()
	}

	parser := wxr.NewParser(r)
	if _, err := parser.Header(); err != nil {
		return report, wrapVerifyErr("header", err)
	}

	// Aggregator state. We collect every record into per-kind
	// slices first, then dispatch them to the comparator
	// functions in posts.go / terms.go / etc. The total memory
	// is bounded by the WXR's record count, which the importer
	// has already had in RAM at write time — the verifier's
	// footprint is no worse than the importer's, by construction.
	st := newRunState()

	for {
		select {
		case <-ctx.Done():
			return report, ctx.Err()
		default:
		}
		rec, err := parser.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return report, wrapVerifyErr("stream", err)
		}
		switch rr := rec.(type) {
		case *wxr.Author:
			st.authors = append(st.authors, rr)
		case *wxr.Category:
			st.categories = append(st.categories, rr)
		case *wxr.Tag:
			st.tags = append(st.tags, rr)
		case *wxr.Post:
			st.posts = append(st.posts, rr)
		}
	}

	// Run the per-kind comparators in a deterministic order so
	// the Failures slice is stable across runs (CI diffs, golden
	// files). Each comparator owns its own count check + per-
	// record checks; failures append to the same Report.
	if err := v.checkPosts(ctx, st, report); err != nil {
		return report, err
	}
	if err := v.checkTerms(ctx, st, report); err != nil {
		return report, err
	}
	if err := v.checkComments(ctx, st, report); err != nil {
		return report, err
	}
	if err := v.checkUsers(ctx, st, report); err != nil {
		return report, err
	}
	if err := v.checkPermalinks(ctx, st, report); err != nil {
		return report, err
	}

	return report, nil
}

// runState is the per-Run scratch space holding the source records
// we've parsed. Populated by Run; consumed by the per-kind
// comparators. Lives in this file because Run is the only place
// that constructs one — passing it around through method receivers
// avoids per-comparator state structs.
type runState struct {
	authors    []*wxr.Author
	categories []*wxr.Category
	tags       []*wxr.Tag
	posts      []*wxr.Post
}

func newRunState() *runState {
	return &runState{}
}
