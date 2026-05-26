package scheduler

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/cron"
	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
)

// SeedOptions bundles the dependencies SeedDefaults needs to build
// both task specs.
type SeedOptions struct {
	// Pool is the pgx connection pool both tasks read/write
	// against. Required.
	Pool *pgxpool.Pool

	// Logger is the structured logger handed to both handlers.
	Logger *slog.Logger
}

// SeedDefaults registers the publisher and GC tasks against the
// supplied task + cron registries. Idempotent: callers that re-seed
// at runtime get a wrapped ErrAlreadyRegistered, which they can
// ignore if the duplicate is benign.
//
// This is the single entry point worker main.go calls — it pairs
// the taskspec and cron registrations in one place so a future task
// addition doesn't require touching two files.
func SeedDefaults(
	taskReg *taskspec.Registry,
	cronReg *cron.Registry,
	opts SeedOptions,
) error {
	if taskReg == nil {
		return errors.New("scheduler: SeedDefaults: task registry is required")
	}
	if cronReg == nil {
		return errors.New("scheduler: SeedDefaults: cron registry is required")
	}
	if opts.Pool == nil {
		return errors.New("scheduler: SeedDefaults: pool is required")
	}

	pubSpec, err := NewPublisherSpec(PublisherSpecOptions{
		Pool:   opts.Pool,
		Logger: opts.Logger,
	})
	if err != nil {
		return fmt.Errorf("publisher spec: %w", err)
	}
	if err := taskReg.Register(pubSpec); err != nil &&
		!errors.Is(err, taskspec.ErrAlreadyRegistered) {
		return fmt.Errorf("register publisher task: %w", err)
	}
	if err := cronReg.Register(NewPublisherCron()); err != nil &&
		!errors.Is(err, cron.ErrAlreadyRegistered) {
		return fmt.Errorf("register publisher cron: %w", err)
	}

	gcSpec, err := NewGCSpec(GCSpecOptions{
		Pool:   opts.Pool,
		Logger: opts.Logger,
	})
	if err != nil {
		return fmt.Errorf("gc spec: %w", err)
	}
	if err := taskReg.Register(gcSpec); err != nil &&
		!errors.Is(err, taskspec.ErrAlreadyRegistered) {
		return fmt.Errorf("register gc task: %w", err)
	}
	if err := cronReg.Register(NewGCCron()); err != nil &&
		!errors.Is(err, cron.ErrAlreadyRegistered) {
		return fmt.Errorf("register gc cron: %w", err)
	}
	return nil
}
