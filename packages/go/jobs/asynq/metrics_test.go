package asynq

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakeInspector is a goroutine-safe stand-in for *asynq.Inspector. The
// per-queue info map and the Queues slice are seeded by the test;
// GetQueueInfo errors for the listed names so we can exercise the
// failure path without standing up Redis.
type fakeInspector struct {
	mu        sync.Mutex
	queues    []string
	info      map[string]*asynq.QueueInfo
	errs      map[string]error
	queuesErr error
}

func (f *fakeInspector) Queues() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.queuesErr != nil {
		return nil, f.queuesErr
	}
	out := make([]string, len(f.queues))
	copy(out, f.queues)
	return out, nil
}

func (f *fakeInspector) GetQueueInfo(name string) (*asynq.QueueInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.errs[name]; ok {
		return nil, err
	}
	if i, ok := f.info[name]; ok {
		return i, nil
	}
	return nil, errors.New("unknown queue")
}

func (f *fakeInspector) Close() error { return nil }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// TestNewInspectorCollector_PanicsOnNilInspector verifies the
// documented precondition.
func TestNewInspectorCollector_PanicsOnNilInspector(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on nil inspector")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "inspector is required") {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	_ = NewInspectorCollector(nil, InspectorCollectorOptions{})
}

// TestInspectorCollector_Collect_EmitsPerQueueGauges verifies the
// gauges are emitted with the right values for every configured queue.
func TestInspectorCollector_Collect_EmitsPerQueueGauges(t *testing.T) {
	insp := &fakeInspector{
		info: map[string]*asynq.QueueInfo{
			QueueCritical: {
				Queue:    QueueCritical,
				Pending:  12,
				Active:   3,
				Retry:    1,
				Archived: 2,
				Latency:  15 * time.Second,
				Paused:   false,
			},
			QueueDefault: {
				Queue:    QueueDefault,
				Pending:  0,
				Active:   0,
				Retry:    0,
				Archived: 0,
				Paused:   true,
			},
		},
	}
	c := NewInspectorCollector(insp, InspectorCollectorOptions{
		Queues: []string{QueueCritical, QueueDefault},
		Logger: discardLogger(),
	})

	expected := strings.NewReader(`
# HELP gonext_jobs_dlq_size Number of tasks in the dead-letter queue (archived after exhausting retries), by queue.
# TYPE gonext_jobs_dlq_size gauge
gonext_jobs_dlq_size{queue="critical"} 2
gonext_jobs_dlq_size{queue="default"} 0
# HELP gonext_jobs_queue_active Number of tasks currently being processed across all workers, by queue.
# TYPE gonext_jobs_queue_active gauge
gonext_jobs_queue_active{queue="critical"} 3
gonext_jobs_queue_active{queue="default"} 0
# HELP gonext_jobs_queue_depth Number of pending tasks in the queue (cluster-wide).
# TYPE gonext_jobs_queue_depth gauge
gonext_jobs_queue_depth{queue="critical"} 12
gonext_jobs_queue_depth{queue="default"} 0
# HELP gonext_jobs_queue_lag_seconds Age in seconds of the oldest pending task in the queue (processing lag).
# TYPE gonext_jobs_queue_lag_seconds gauge
gonext_jobs_queue_lag_seconds{queue="critical"} 15
gonext_jobs_queue_lag_seconds{queue="default"} 0
# HELP gonext_jobs_queue_paused 1 when the queue is paused (operators stopped dispatch); 0 otherwise.
# TYPE gonext_jobs_queue_paused gauge
gonext_jobs_queue_paused{queue="critical"} 0
gonext_jobs_queue_paused{queue="default"} 1
# HELP gonext_jobs_retries Number of tasks scheduled for retry after a handler failure, by queue.
# TYPE gonext_jobs_retries gauge
gonext_jobs_retries{queue="critical"} 1
gonext_jobs_retries{queue="default"} 0
`)
	if err := testutil.CollectAndCompare(c, expected,
		metricQueueDepth,
		metricQueueActive,
		metricQueueLagSecs,
		metricRetries,
		metricDLQSize,
		metricQueuePaused,
	); err != nil {
		t.Fatalf("CollectAndCompare: %v", err)
	}
}

// TestInspectorCollector_Collect_RecordsProbeFailureOnGetQueueInfoError
// exercises the per-queue failure path: a probe error is logged + the
// failure counter increments + the queue is skipped (rather than crashing).
func TestInspectorCollector_Collect_RecordsProbeFailureOnGetQueueInfoError(t *testing.T) {
	insp := &fakeInspector{
		info: map[string]*asynq.QueueInfo{
			QueueEmail: {Queue: QueueEmail, Pending: 5},
		},
		errs: map[string]error{
			QueueWebhook: errors.New("redis down"),
		},
	}
	c := NewInspectorCollector(insp, InspectorCollectorOptions{
		Queues: []string{QueueWebhook, QueueEmail},
		Logger: discardLogger(),
	})

	// QueueEmail is healthy; QueueWebhook errors. CollectAndCount calls
	// Collect once and tells us how many series came through for the
	// metric name we ask about. After one scrape we should see exactly
	// one series (Email) and the failure counter should sit at 1.
	if got := testutil.CollectAndCount(c, metricQueueDepth); got != 1 {
		t.Errorf("queue depth series: got %d want 1 (only email survived)", got)
	}
	if got := c.failureCount.Load(); got != 1 {
		t.Errorf("failure count: got %d want 1", got)
	}
}

// TestInspectorCollector_Collect_DiscoversQueuesWhenUnset exercises the
// default-queues path: when opts.Queues is nil, Inspector.Queues() is
// consulted on every scrape.
func TestInspectorCollector_Collect_DiscoversQueuesWhenUnset(t *testing.T) {
	insp := &fakeInspector{
		queues: []string{QueueMedia},
		info: map[string]*asynq.QueueInfo{
			QueueMedia: {Queue: QueueMedia, Pending: 9, Active: 1},
		},
	}
	c := NewInspectorCollector(insp, InspectorCollectorOptions{
		Logger: discardLogger(),
	})

	if got := testutil.CollectAndCount(c, metricQueueDepth); got != 1 {
		t.Errorf("queue depth series: got %d want 1", got)
	}
}

// TestInspectorCollector_Collect_QueuesDiscoveryFailureLogged covers
// the "Inspector.Queues() failed at scrape time" branch.
func TestInspectorCollector_Collect_QueuesDiscoveryFailureLogged(t *testing.T) {
	insp := &fakeInspector{queuesErr: errors.New("queues down")}
	c := NewInspectorCollector(insp, InspectorCollectorOptions{
		Logger: discardLogger(),
	})

	reg := prometheus.NewRegistry()
	reg.MustRegister(c)
	if _, err := reg.Gather(); err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if got := c.failureCount.Load(); got != 1 {
		t.Errorf("failure count: got %d want 1", got)
	}
}

// TestInspectorCollector_ProbeTimeout_Default ensures a zero-valued
// option still produces a sensible probe budget.
func TestInspectorCollector_ProbeTimeout_Default(t *testing.T) {
	c := NewInspectorCollector(&fakeInspector{}, InspectorCollectorOptions{})
	if c.opts.ProbeTimeout != defaultInspectorProbeTimeout {
		t.Errorf("default probe timeout: got %v want %v", c.opts.ProbeTimeout, defaultInspectorProbeTimeout)
	}
}
