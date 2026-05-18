package cron

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// ErrEmptyName is returned by Registry.Register when CronSpec.Name is
// empty. Schedule names are the key in the registry map and they show
// up in logs / Prometheus labels; an empty name would silently shadow
// other entries.
var ErrEmptyName = errors.New("cron: schedule name is required")

// ErrEmptySchedule is returned when CronSpec.Schedule is empty.
var ErrEmptySchedule = errors.New("cron: schedule expression is required")

// ErrEmptyTaskName is returned when CronSpec.TaskName is empty. The
// scheduler delegates the enqueue to taskspec.Enqueue keyed by this
// name — without it the spec has nowhere to fire.
var ErrEmptyTaskName = errors.New("cron: task name is required")

// ErrInvalidSchedule wraps the robfig parser error when Schedule does
// not parse. We surface the wrapped error so the caller can render a
// useful diagnostic ("at position 4: expected number") without re-
// running the parser.
var ErrInvalidSchedule = errors.New("cron: schedule expression invalid")

// ErrAlreadyRegistered is returned by Registry.Register when a spec
// with the same Name is already present. Same first-writer-wins shape
// as taskspec.ErrAlreadyRegistered: the second writer observes the
// conflict and decides whether to log, fail, or ignore.
var ErrAlreadyRegistered = errors.New("cron: schedule name already registered")

// CronSpec is the declarative descriptor for one periodic task.
//
// The producer (the cron scheduler running on the leader) reads this
// to know when to fire; the consumer side is unchanged — the handler
// it ends up invoking is whatever taskspec.TaskSpec keyed by TaskName
// declares. Decoupling the two means a cron schedule can change
// cadence without re-deploying the worker that handles the task.
//
// Field validation lives in Registry.Register: empty fields are
// rejected up front, and Schedule is run through the robfig parser
// so a typo can't sit in the registry waiting to fail at first fire.
type CronSpec struct {
	// Name is the unique identifier for this schedule entry. Used as
	// the map key in the registry, in scheduler logs, and in the
	// gonext_cron_* Prometheus labels. Conventional shape is
	// "<resource>.<action>.<cadence>" (e.g. "revisions.purge.daily"),
	// but only uniqueness is enforced.
	Name string

	// Schedule is a robfig/cron v3 expression. Standard 5-field
	// notation ("0 3 * * *") and shorthand descriptors ("@daily",
	// "@hourly", "@every 5m") both parse. The parser is the
	// "Standard" variant — no seconds field — so the smallest
	// cadence we can express is "every minute". Sub-minute fires
	// don't make sense for cron-cadenced operational tasks and would
	// stress the Redis lease.
	Schedule string

	// TaskName is the taskspec.TaskSpec.Name that the scheduler
	// enqueues each time Schedule fires. The same name must be
	// present in the taskspec registry the scheduler was constructed
	// against; missing TaskNames are logged as a warning at fire
	// time rather than at Register time, because the cron registry
	// is allowed to outlive (or boot before) the task registry in
	// host-extended deployments.
	TaskName string

	// Payload is the JSON-encodable value enqueued with each fire.
	// Most operational cron tasks need no payload (the handler reads
	// "now" and the database state); leave this nil and the
	// scheduler sends a literal JSON null. If the taskspec has a
	// PayloadSchema, the schema must accept null OR the caller must
	// set Payload to something the schema accepts.
	Payload any
}

// schedule wraps a CronSpec with its parsed robfig schedule and the
// next-fire time. We keep this in the registry alongside the spec so
// the scheduler's hot loop doesn't re-parse the expression on every
// tick — a robfig Schedule's Next() call is O(1) over the parsed AST
// but the parser is allocator-heavy.
type schedule struct {
	spec CronSpec
	cron cron.Schedule
	next time.Time
}

// Registry is the process-wide store of CronSpecs.
//
// Safe for concurrent Register / Get / Names / Snapshot. Same shape
// as taskspec.Registry: sync.RWMutex, read-heavy access pattern (the
// scheduler walks the list every tick, registrations happen at boot).
//
// Construct via NewRegistry. There is no Default() singleton here:
// cron schedules are owned by the binary's wiring (one cron registry
// per worker process), not by individual packages that register at
// init.
type Registry struct {
	mu        sync.RWMutex
	specs     map[string]CronSpec
	schedules map[string]cron.Schedule
}

// NewRegistry returns an empty Registry. The robfig parser is built
// at boot via a package-level singleton; constructing many registries
// doesn't re-allocate it.
func NewRegistry() *Registry {
	return &Registry{
		specs:     map[string]CronSpec{},
		schedules: map[string]cron.Schedule{},
	}
}

// parser is the shared robfig cron parser. We use cron.Standard
// (Minute Hour DOM Month DOW) plus the descriptor extensions
// (@daily, @hourly, @every <duration>). Seconds-resolution is
// intentionally disabled — sub-minute cron is the wrong shape for
// the kind of operational sweeps this package serves, and would
// stress the lease renewal cadence.
var parser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// Register adds spec to the registry. Returns:
//
//   - ErrEmptyName if spec.Name is empty.
//   - ErrEmptySchedule if spec.Schedule is empty.
//   - ErrEmptyTaskName if spec.TaskName is empty.
//   - ErrInvalidSchedule (wrapped with the parser detail) if the
//     schedule fails to parse.
//   - ErrAlreadyRegistered (wrapped with the offending name) if a
//     spec with the same Name already exists.
//   - nil on success.
//
// First-writer-wins: a second Register for the same Name does NOT
// overwrite. This matches taskspec.Registry; the rationale is the
// same (registries are supposed to be idempotent).
//
// Safe for concurrent use.
func (r *Registry) Register(spec CronSpec) error {
	if spec.Name == "" {
		return ErrEmptyName
	}
	if spec.Schedule == "" {
		return ErrEmptySchedule
	}
	if spec.TaskName == "" {
		return ErrEmptyTaskName
	}
	parsed, err := parser.Parse(spec.Schedule)
	if err != nil {
		return fmt.Errorf("%w: %q: %w", ErrInvalidSchedule, spec.Schedule, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.specs[spec.Name]; exists {
		return fmt.Errorf("%w: %q", ErrAlreadyRegistered, spec.Name)
	}
	r.specs[spec.Name] = spec
	r.schedules[spec.Name] = parsed
	return nil
}

// Get returns the spec for name and a bool indicating whether it was
// found. The returned CronSpec is a value copy; the Payload field is
// shared by reference (mutate at your peril, but callers don't
// usually mutate it).
//
// Safe for concurrent use.
func (r *Registry) Get(name string) (CronSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	spec, ok := r.specs[name]
	return spec, ok
}

// Names returns every registered schedule name, sorted lexicographically
// for determinism. The returned slice is a fresh copy; callers may
// mutate it freely. Useful for admin UIs and for the scheduler's
// boot-time "wired schedules" log line.
//
// Safe for concurrent use.
func (r *Registry) Names() []string {
	r.mu.RLock()
	out := make([]string, 0, len(r.specs))
	for name := range r.specs {
		out = append(out, name)
	}
	r.mu.RUnlock()
	sort.Strings(out)
	return out
}

// Has is a convenience for "is this schedule registered?". Equivalent
// to discarding the spec returned by Get; provided so call sites that
// only care about membership read more naturally.
func (r *Registry) Has(name string) bool {
	_, ok := r.Get(name)
	return ok
}

// registerWithSchedule is the test-only seam that pairs a CronSpec
// with a pre-parsed cron.Schedule. Production callers go through
// Register, which routes the spec.Schedule string through the robfig
// parser and rejects sub-second cadences. Tests need to exercise the
// scheduler's tick loop at sub-second cadences without sleeping for
// minutes; this helper bypasses the parser so a test can inject a
// 100ms ConstantDelaySchedule (which robfig itself would clamp to 1s
// when reached via its public parser).
//
// Unexported so production code physically cannot reach it — every
// CronSpec on a production registry came through the validated
// path. The exported tests in this package live under `package cron`
// (not `cron_test`) so they can call this helper.
func (r *Registry) registerWithSchedule(spec CronSpec, sched cron.Schedule) error {
	if spec.Name == "" {
		return ErrEmptyName
	}
	if spec.TaskName == "" {
		return ErrEmptyTaskName
	}
	if sched == nil {
		return ErrInvalidSchedule
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.specs[spec.Name]; exists {
		return fmt.Errorf("%w: %q", ErrAlreadyRegistered, spec.Name)
	}
	r.specs[spec.Name] = spec
	r.schedules[spec.Name] = sched
	return nil
}

// snapshot returns the registry's current contents as a slice of
// schedule values with their next-fire times computed against `now`.
// The scheduler's run loop calls this once per pass so the hot path
// doesn't hold the registry lock while iterating.
//
// The returned slice is owned by the caller; the schedule structs are
// values, so mutating the slice has no effect on the registry.
func (r *Registry) snapshot(now time.Time) []schedule {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]schedule, 0, len(r.specs))
	for name, spec := range r.specs {
		sch := r.schedules[name]
		if sch == nil {
			continue
		}
		out = append(out, schedule{
			spec: spec,
			cron: sch,
			next: sch.Next(now),
		})
	}
	return out
}
