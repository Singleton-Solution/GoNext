package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// AbortOrphansResult is the value returned by AbortOrphanedMultiparts.
// Tests and the worker's structured log assert on it; production code
// surfaces the counts to the operator-visible cron metrics.
type AbortOrphansResult struct {
	// Scanned is the number of incomplete uploads the listing
	// returned within the orphan window.
	Scanned int

	// Aborted is the number of uploads successfully aborted.
	Aborted int

	// Errors is the number of uploads the abort call failed for. The
	// cron does NOT return on the first error — it logs the offender
	// and continues so a transient backend hiccup does not leave the
	// rest of the orphan list hanging.
	Errors int
}

// AbortOrphansOptions tunes AbortOrphanedMultiparts.
type AbortOrphansOptions struct {
	// OlderThan is the age threshold: only uploads initiated before
	// (now - OlderThan) are aborted. Defaults to 24h. The default
	// matches the docs/12-jobs-cron.md cadence — the cron runs daily,
	// so a 24h window leaves a comfortable "started this morning,
	// still legitimate" buffer for active operators.
	OlderThan time.Duration

	// Limit caps the number of uploads processed in one sweep.
	// Defaults to 1000; the cap exists so a backend with millions of
	// abandoned uploads (an unlikely but possible operator footgun)
	// does not exhaust a single cron tick.
	Limit int

	// Logger receives a slog.Info per aborted upload and a slog.Warn
	// per error. nil falls back to slog.Default.
	Logger *slog.Logger

	// Now is the time source for the cutoff calculation. nil falls
	// back to time.Now. Tests pin this for determinism.
	Now func() time.Time
}

// AbortOrphanedMultiparts sweeps the driver for multipart uploads
// older than the configured threshold and aborts each one. Used by
// the abort-orphans cron task (registered in apps/worker/cmd/worker).
//
// The driver must implement MultipartDriver; if it does not (the
// local driver doesn't, the GCS stub doesn't), the function returns
// immediately with a zero result. This keeps the cron registration
// site backend-agnostic — the cron fires on every driver but only
// does work where multipart uploads actually exist.
func AbortOrphanedMultiparts(ctx context.Context, driver Driver, opts AbortOrphansOptions) (AbortOrphansResult, error) {
	mp, ok := driver.(MultipartDriver)
	if !ok {
		// No multipart concept on this driver — nothing to do.
		return AbortOrphansResult{}, nil
	}
	if opts.OlderThan <= 0 {
		opts.OlderThan = 24 * time.Hour
	}
	if opts.Limit <= 0 {
		opts.Limit = 1000
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	cutoff := opts.Now().Add(-opts.OlderThan).UTC()
	uploads, err := mp.ListIncompleteUploads(ctx, cutoff, opts.Limit)
	if err != nil {
		return AbortOrphansResult{}, fmt.Errorf("storage: list incomplete: %w", err)
	}
	result := AbortOrphansResult{Scanned: len(uploads)}
	for _, u := range uploads {
		if err := ctx.Err(); err != nil {
			// Respect cancellation — the worker may be shutting down
			// and we don't want to hold the cron lease past the
			// drain budget.
			return result, err
		}
		if err := mp.AbortMultipart(ctx, u.Key, u.UploadID); err != nil {
			if errors.Is(err, ErrNotFound) {
				// Someone else (the operator, a manual sweep) already
				// got there. Not an error.
				continue
			}
			result.Errors++
			opts.Logger.WarnContext(ctx, "storage: abort orphan failed",
				slog.String("key", u.Key),
				slog.String("upload_id", u.UploadID),
				slog.Any("err", err),
			)
			continue
		}
		result.Aborted++
		opts.Logger.InfoContext(ctx, "storage: aborted orphan multipart",
			slog.String("key", u.Key),
			slog.String("upload_id", u.UploadID),
			slog.Duration("age", opts.Now().Sub(u.Initiated)),
		)
	}
	return result, nil
}
