package jobs

import (
	"errors"
	"fmt"
	"os"

	"github.com/hibiken/asynq"
)

// Inspector is the subset of *asynq.Inspector the jobs subcommands
// depend on. Defined as an interface so tests can swap in a fake
// without standing up Redis. Production wiring constructs an
// *asynq.Inspector from REDIS_URL and passes it directly.
type Inspector interface {
	// Queues lists the configured queue names. asynq.Inspector
	// returns them ordered alphabetically; our subcommands honour
	// the same ordering.
	Queues() ([]string, error)

	// GetQueueInfo returns depth + counts for one queue.
	GetQueueInfo(queue string) (*asynq.QueueInfo, error)

	// ListArchivedTasks pages the dead-letter / archived list.
	ListArchivedTasks(queue string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error)

	// DeleteAllArchivedTasks deletes every archived row in queue.
	// Returns the count deleted.
	DeleteAllArchivedTasks(queue string) (int, error)

	// Close releases the underlying connection.
	Close() error
}

// openInspector is the production wiring: REDIS_URL -> *asynq.Inspector.
// Tests inject a stub via the per-subcommand `inspector` factory
// (each runXxx function takes one as a default-argumented parameter).
func openInspector() (Inspector, error) {
	dsn := os.Getenv("REDIS_URL")
	if dsn == "" {
		return nil, errors.New("REDIS_URL is required")
	}
	opt, err := asynq.ParseRedisURI(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	return asynq.NewInspector(opt), nil
}

// inspectorFactory is replaced in tests so the run functions can
// build the Inspector through a single seam.
var inspectorFactory = openInspector
