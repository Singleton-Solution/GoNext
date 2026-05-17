//go:build !short

package asynq_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	upstream "github.com/hibiken/asynq"

	jobsasynq "github.com/Singleton-Solution/GoNext/packages/go/jobs/asynq"
	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

// TestIntegrationEnqueueDequeue stands up a real Redis container,
// enqueues a task with an Asynq Client, and waits for the chassis's
// server to dequeue and dispatch it through the registered handler.
//
// This is the one integration-level test the chassis owns. Everything
// else can use miniredis — but the enqueue→dequeue contract sits
// across two processes (publisher Client and consumer Server) talking
// through Redis, and miniredis's MULTI/EXEC/Lua coverage isn't
// guaranteed to match every Asynq script. A real Redis pins down the
// contract once; downstream packages get to trust it.
//
// Skipped automatically when Docker isn't reachable (laptops without
// Docker, restricted CI shards). The containers.Redis helper handles
// the skip; we don't need a manual guard here.
func TestIntegrationEnqueueDequeue(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test; skipped in -short mode")
	}

	url := containers.Redis(t)
	if url == "" {
		// containers.Redis already called t.Skip; this guard is for
		// readability and to make the rest of the test obviously
		// dead-code when Docker isn't present.
		return
	}

	connOpt, err := upstream.ParseRedisURI(url)
	if err != nil {
		t.Fatalf("ParseRedisURI: %v", err)
	}

	srv, mux, err := jobsasynq.New(connOpt, jobsasynq.Config{
		Logger: quiet(),
		// Tight timings so the test finishes in seconds: a 1s
		// shutdown timeout is plenty when the handler does no work,
		// and a 200ms health check makes Healthy() reflect reality
		// quickly enough for the post-stop assertion below.
		ShutdownTimeout:     1 * time.Second,
		HealthCheckInterval: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const taskType = "integration:hello"
	var (
		mu       sync.Mutex
		received []*upstream.Task
		done     = make(chan struct{}, 1)
	)
	mux.HandleFunc(taskType, func(_ context.Context, task *upstream.Task) error {
		mu.Lock()
		received = append(received, task)
		mu.Unlock()
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	})

	// Run the server in the background. Run blocks until Shutdown is
	// called; we trigger that via srv.Close in the test cleanup.
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run() }()
	t.Cleanup(func() {
		_ = srv.Close(context.Background())
		select {
		case <-runErr:
		case <-time.After(5 * time.Second):
			t.Log("integration: Run did not return within 5s of Close")
		}
	})

	// Wait for the health check to fire at least once so we know the
	// server is up and pulling from Redis. Healthy() starts true (boot
	// grace), so we can't poll on the boolean alone; one
	// HealthCheckInterval is enough to guarantee the ping has run.
	time.Sleep(400 * time.Millisecond)
	if !srv.Healthy() {
		t.Fatal("Healthy(): server is not healthy after first ping window; cannot proceed")
	}

	client := upstream.NewClient(connOpt)
	defer client.Close()

	payload := []byte(`{"msg":"hello"}`)
	if _, err := client.EnqueueContext(context.Background(),
		upstream.NewTask(taskType, payload),
		upstream.Queue("default"),
	); err != nil {
		t.Fatalf("EnqueueContext: %v", err)
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handler never received the enqueued task within 10s")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("received %d tasks, want 1", len(received))
	}
	if got := string(received[0].Payload()); !strings.Contains(got, "hello") {
		t.Errorf("payload: got %q, want substring 'hello'", got)
	}
}
