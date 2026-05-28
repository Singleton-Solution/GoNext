package jobs

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

// stubInspector is the test seam for the Inspector interface.
type stubInspector struct {
	queues   []string
	infos    map[string]*asynq.QueueInfo
	archived map[string][]*asynq.TaskInfo
	deleted  map[string]int
	closed   bool
}

func newStub() *stubInspector {
	return &stubInspector{
		queues:   []string{"critical", "default", "low"},
		infos:    map[string]*asynq.QueueInfo{},
		archived: map[string][]*asynq.TaskInfo{},
		deleted:  map[string]int{},
	}
}

func (s *stubInspector) Queues() ([]string, error)                  { return s.queues, nil }
func (s *stubInspector) GetQueueInfo(q string) (*asynq.QueueInfo, error) {
	if info, ok := s.infos[q]; ok {
		return info, nil
	}
	return &asynq.QueueInfo{Queue: q}, nil
}
func (s *stubInspector) ListArchivedTasks(q string, _ ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	return s.archived[q], nil
}
func (s *stubInspector) DeleteAllArchivedTasks(q string) (int, error) {
	n := len(s.archived[q])
	s.archived[q] = nil
	s.deleted[q] = n
	return n, nil
}
func (s *stubInspector) Close() error { s.closed = true; return nil }

func withStub(t *testing.T, stub *stubInspector) func() {
	t.Helper()
	prev := inspectorFactory
	inspectorFactory = func() (Inspector, error) { return stub, nil }
	return func() { inspectorFactory = prev }
}

// Tests that swap `inspectorFactory` or `cronRegistryFactory` must NOT
// run in parallel — those vars are package-level globals and the race
// detector (CI runs with -race) flags concurrent reads/writes. The
// suite is fast enough that serialization costs nothing material.

func TestQueue_Table(t *testing.T) {
	stub := newStub()
	stub.infos["default"] = &asynq.QueueInfo{Queue: "default", Size: 7, Pending: 5}
	cleanup := withStub(t, stub)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	code := runQueue(nil, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "default") {
		t.Errorf("missing queue row: %s", stdout.String())
	}
}

func TestFailed_FiltersByQueue(t *testing.T) {
	stub := newStub()
	stub.archived["default"] = []*asynq.TaskInfo{
		{ID: "t1", Type: "post.publish", LastErr: "boom", Retried: 3, LastFailedAt: time.Now()},
	}
	cleanup := withStub(t, stub)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	code := runFailed([]string{"--queue", "default"}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "post.publish") {
		t.Errorf("missing task row: %s", stdout.String())
	}
}

func TestDrain_RequiresConfirmation(t *testing.T) {
	stub := newStub()
	stub.archived["default"] = []*asynq.TaskInfo{{ID: "t1"}}
	cleanup := withStub(t, stub)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	code := runDrainWithStdin([]string{"--queue", "default"}, &stdout, &stderr,
		strings.NewReader("no\n"))
	if code != ExitOK {
		t.Errorf("exit = %d (declined drain should be a clean exit)", code)
	}
	if stub.deleted["default"] != 0 {
		t.Errorf("declined drain deleted rows: %d", stub.deleted["default"])
	}
}

func TestDrain_HappyPath(t *testing.T) {
	stub := newStub()
	stub.archived["default"] = []*asynq.TaskInfo{{ID: "t1"}, {ID: "t2"}}
	cleanup := withStub(t, stub)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	code := runDrainWithStdin([]string{"--queue", "default", "--yes"}, &stdout, &stderr,
		strings.NewReader(""))
	if code != ExitOK {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	if stub.deleted["default"] != 2 {
		t.Errorf("deleted = %d, want 2", stub.deleted["default"])
	}
}

func TestCron_EmptySnapshot(t *testing.T) {
	prev := cronRegistryFactory
	cronRegistryFactory = func() (CronRegistry, error) {
		return &staticCronRegistry{entries: []CronEntry{
			{Name: "revisions.purge.daily", Schedule: "@daily", TaskName: "revisions.purge"},
		}}, nil
	}
	defer func() { cronRegistryFactory = prev }()

	var stdout, stderr bytes.Buffer
	code := runCron(nil, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout.String(), "revisions.purge.daily") {
		t.Errorf("missing entry: %s", stdout.String())
	}
}

func TestPluginPrefix(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":                          "core",
		"plain":                     "core",
		"core.task":                 "core",
		"myseo.sitemap.regenerate":  "myseo",
		"akismet.scan":              "akismet",
	}
	for in, want := range cases {
		if got := pluginPrefix(in); got != want {
			t.Errorf("pluginPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}
