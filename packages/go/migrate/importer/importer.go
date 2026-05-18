package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/packages/go/migrate/wxr"
)

// Importer wires a WXR parser, the html2blocks converter, and a
// Postgres pool into a streaming import pipeline.
//
// Construct with New; one Importer per Run. The same instance can
// be reused across Runs but it is not safe for concurrent use —
// each Run carries Run-scoped state (the WP-id maps below).
type Importer struct {
	// Pool is the Postgres pool the importer writes through. It
	// must already have the schema applied (migrations up to and
	// including 000006_comments). Required.
	Pool *pgxpool.Pool

	// Opts are the import options. See type Options. The zero
	// value is acceptable; defaults are applied by Run.
	Opts Options

	// Now returns the current wall time. Pluggable for tests that
	// pin the Report duration. Defaults to time.Now.
	Now func() time.Time
}

// New constructs an Importer. The Opts value is stored by value;
// later mutations to the caller's struct do not affect the
// Importer. Pool may be nil for a Dryrun-only call site that wants
// to walk the WXR for validation without any DB access — Run
// detects nil and short-circuits accordingly.
func New(pool *pgxpool.Pool, opts Options) *Importer {
	return &Importer{
		Pool: pool,
		Opts: opts,
		Now:  time.Now,
	}
}

// Run streams the WXR document on r and applies every record to
// the database (or simulates the application when Opts.Dryrun).
//
// The returned *Report is never nil — it carries the partial
// counts even when the second return value is non-nil. err is
// non-nil only for fatal failures (malformed XML past recovery,
// connection loss, OnConflict=Fail conflicts, ctx.Done). Per-
// record failures land in Report.Errors and Run continues.
//
// The import is single-pass through the WXR; preamble records
// (authors, categories, tags) are committed before any post is
// touched, so per-post FK lookups can rely on them being present.
func (imp *Importer) Run(ctx context.Context, r io.Reader) (*Report, error) {
	if r == nil {
		return &Report{}, fmt.Errorf("importer: nil reader")
	}
	if imp == nil {
		return &Report{}, fmt.Errorf("importer: nil receiver")
	}
	opts := imp.Opts.resolved()
	now := imp.Now
	if now == nil {
		now = time.Now
	}
	if !opts.Dryrun && imp.Pool == nil {
		return &Report{}, fmt.Errorf("importer: nil pool for non-dryrun")
	}

	start := now()
	report := &Report{}

	state := newRunState()
	parser := wxr.NewParser(r)

	// 1. Header. Header() returns nil only on a fatal preamble
	// error — we surface those, since a WXR with no wp:wxr_version
	// (or an unsupported one) cannot be meaningfully imported.
	if _, err := parser.Header(); err != nil {
		report.Took = now().Sub(start)
		return report, fmt.Errorf("importer: header: %w", err)
	}

	// 2. Stream records, batching posts by Opts.BatchSize. The
	// preamble (authors, categories, tags) arrives lexically
	// before any post, so we drain them in a dedicated transaction
	// first, then loop on post batches.
	postBatch := make([]*wxr.Post, 0, opts.BatchSize)

	flushPosts := func() error {
		if len(postBatch) == 0 {
			return nil
		}
		err := imp.commitPostBatch(ctx, opts, state, postBatch, report)
		// Always clear the batch even on error — committed rows
		// are gone from the batch, and the partial commit's state
		// is the caller's responsibility (ConflictFail rolls the
		// failing batch back, the rest survives).
		postBatch = postBatch[:0]
		return err
	}

	// Preamble records (authors, categories, tags) are drained
	// into a small slice and committed once before posts begin.
	// They appear before any post in the WXR stream by spec.
	var preamble []wxr.Record
	preambleDone := false

	for {
		select {
		case <-ctx.Done():
			report.Took = now().Sub(start)
			return report, ctx.Err()
		default:
		}

		rec, err := parser.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// Malformed XML mid-stream: flush whatever we already
			// have, record the error, and abort the run. The
			// partial state from previous batches stays in the
			// DB (committed rows are committed).
			if !preambleDone {
				if pErr := imp.commitPreamble(ctx, opts, state, preamble, report); pErr != nil && !errors.Is(pErr, ErrAborted) {
					report.Errors = append(report.Errors, ImportError{
						Stage:  "tx",
						Reason: "preamble commit: " + pErr.Error(),
					})
				}
				preambleDone = true
			}
			if fErr := flushPosts(); fErr != nil && !errors.Is(fErr, ErrAborted) {
				report.Errors = append(report.Errors, ImportError{
					Stage:  "tx",
					Reason: "final flush: " + fErr.Error(),
				})
			}
			report.Errors = append(report.Errors, ImportError{
				Stage:  "stream",
				Reason: err.Error(),
			})
			report.Took = now().Sub(start)
			return report, fmt.Errorf("importer: stream: %w", err)
		}

		switch r := rec.(type) {
		case *wxr.Author, *wxr.Category, *wxr.Tag:
			if preambleDone {
				// Some WXR exports interleave trailing preamble
				// records after items (rare; observed with
				// custom-taxonomy plugins). Apply them inline.
				if err := imp.applyTrailingPreamble(ctx, opts, state, r.(wxr.Record), report); err != nil {
					if errors.Is(err, ErrAborted) {
						report.Took = now().Sub(start)
						return report, err
					}
				}
				continue
			}
			preamble = append(preamble, r.(wxr.Record))

		case *wxr.Post:
			if !preambleDone {
				if err := imp.commitPreamble(ctx, opts, state, preamble, report); err != nil {
					if errors.Is(err, ErrAborted) {
						report.Took = now().Sub(start)
						return report, err
					}
					report.Errors = append(report.Errors, ImportError{
						Stage:  "tx",
						Reason: "preamble commit: " + err.Error(),
					})
				}
				preambleDone = true
			}
			postBatch = append(postBatch, r)
			if len(postBatch) >= opts.BatchSize {
				if err := flushPosts(); err != nil {
					if errors.Is(err, ErrAborted) {
						report.Took = now().Sub(start)
						return report, err
					}
				}
			}
		}
	}

	// 3. Drain any partial preamble or post batch on EOF.
	if !preambleDone {
		if err := imp.commitPreamble(ctx, opts, state, preamble, report); err != nil {
			if errors.Is(err, ErrAborted) {
				report.Took = now().Sub(start)
				return report, err
			}
			report.Errors = append(report.Errors, ImportError{
				Stage:  "tx",
				Reason: "preamble commit: " + err.Error(),
			})
		}
	}
	if err := flushPosts(); err != nil {
		if errors.Is(err, ErrAborted) {
			report.Took = now().Sub(start)
			return report, err
		}
		report.Errors = append(report.Errors, ImportError{
			Stage:  "tx",
			Reason: "final flush: " + err.Error(),
		})
	}

	report.Took = now().Sub(start)
	return report, nil
}

// commitPreamble walks the preamble slice and applies each
// record in source order. Authors, categories, and tags share a
// single transaction so a single duplicated row under
// ConflictFail doesn't strand half the preamble.
func (imp *Importer) commitPreamble(
	ctx context.Context,
	opts Options,
	state *runState,
	preamble []wxr.Record,
	report *Report,
) error {
	if len(preamble) == 0 {
		return nil
	}
	if opts.Dryrun {
		// Walk-only path: count the records but issue no SQL.
		for _, rec := range preamble {
			switch r := rec.(type) {
			case *wxr.Author:
				report.Authors++
				state.recordAuthor(r, dryrunUUID(r.ID))
			case *wxr.Category:
				report.Categories++
				state.recordTerm("category", r.Nicename, dryrunUUID(r.TermID))
			case *wxr.Tag:
				report.Tags++
				state.recordTerm("tag", r.Slug, dryrunUUID(r.TermID))
			}
		}
		return nil
	}

	tx, err := imp.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("preamble: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, rec := range preamble {
		if cErr := imp.applyOnePreamble(ctx, tx, opts, state, rec, report); cErr != nil {
			return cErr
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("preamble: commit: %w", err)
	}
	return nil
}

// applyTrailingPreamble handles the rare WXR variant that emits a
// late <wp:category> definition after items have started. We open
// a one-off transaction per record because we've already left the
// preamble batch behind.
func (imp *Importer) applyTrailingPreamble(
	ctx context.Context,
	opts Options,
	state *runState,
	rec wxr.Record,
	report *Report,
) error {
	if opts.Dryrun {
		switch r := rec.(type) {
		case *wxr.Author:
			report.Authors++
			state.recordAuthor(r, dryrunUUID(r.ID))
		case *wxr.Category:
			report.Categories++
			state.recordTerm("category", r.Nicename, dryrunUUID(r.TermID))
		case *wxr.Tag:
			report.Tags++
			state.recordTerm("tag", r.Slug, dryrunUUID(r.TermID))
		}
		return nil
	}
	tx, err := imp.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("trailing preamble: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if cErr := imp.applyOnePreamble(ctx, tx, opts, state, rec, report); cErr != nil {
		return cErr
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("trailing preamble: commit: %w", err)
	}
	return nil
}

// applyOnePreamble dispatches a single preamble record to its
// per-kind upsert. The transaction is owned by the caller.
func (imp *Importer) applyOnePreamble(
	ctx context.Context,
	tx pgx.Tx,
	opts Options,
	state *runState,
	rec wxr.Record,
	report *Report,
) error {
	switch r := rec.(type) {
	case *wxr.Author:
		report.Authors++
		if err := imp.upsertUser(ctx, tx, opts, state, r); err != nil {
			if errors.Is(err, ErrAborted) {
				return err
			}
			report.Errors = append(report.Errors, *newImportError("author", r.ID, r.Login, err))
		}
	case *wxr.Category:
		report.Categories++
		if err := imp.upsertCategory(ctx, tx, opts, state, r); err != nil {
			if errors.Is(err, ErrAborted) {
				return err
			}
			report.Errors = append(report.Errors, *newImportError("category", r.TermID, r.Nicename, err))
		}
	case *wxr.Tag:
		report.Tags++
		if err := imp.upsertTag(ctx, tx, opts, state, r); err != nil {
			if errors.Is(err, ErrAborted) {
				return err
			}
			report.Errors = append(report.Errors, *newImportError("tag", r.TermID, r.Slug, err))
		}
	}
	return nil
}

// commitPostBatch processes a slice of *Post records inside a
// single transaction. ConflictFail aborts the batch (and the run);
// ConflictSkip and ConflictUpdate degrade individual failures into
// Report.Errors and keep going.
func (imp *Importer) commitPostBatch(
	ctx context.Context,
	opts Options,
	state *runState,
	batch []*wxr.Post,
	report *Report,
) error {
	if len(batch) == 0 {
		return nil
	}
	if opts.Dryrun {
		for _, p := range batch {
			report.Posts++
			if p.PostType == "attachment" {
				report.Attachments++
			}
			report.Comments += len(p.Comments)
			state.recordPost(p.PostID, dryrunUUID(p.PostID))
		}
		return nil
	}

	tx, err := imp.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("post batch: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, p := range batch {
		report.Posts++
		if p.PostType == "attachment" {
			report.Attachments++
		}
		if err := imp.upsertPost(ctx, tx, opts, state, p, report); err != nil {
			if errors.Is(err, ErrAborted) {
				return err
			}
			report.Errors = append(report.Errors, *newImportError("post", p.PostID, p.Name, err))
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("post batch: commit: %w", err)
	}
	return nil
}
