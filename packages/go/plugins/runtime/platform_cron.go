package runtime

// platform_cron.go — plugin cron registration backing gn_cron_register
// (#191). The wiring into the WASM ABI lives in host_platform.go; this
// file owns persistence (the CronStore seam) and the leader-side
// dispatch into the hook bus (the CronDispatcher seam).
//
// Lifecycle:
//
//   1. Plugin activation calls gn_cron_register(schedule, handler_id)
//      from inside its WASM init.
//   2. The runtime persists the (plugin_slug, schedule, handler_id)
//      tuple into plugin_cron_schedules (migration 000031).
//   3. The leader-elected scheduler (packages/go/jobs/cron) reads
//      plugin_cron_schedules at startup and on activation events,
//      registers each enabled row into its in-memory cron.Registry,
//      and fires them on the leader.
//   4. Each fire calls CronService.Fire, which dispatches through the
//      hook bus on the action "plugin.cron.<slug>.<handler_id>". The
//      plugin's activation code is expected to RegisterAction that
//      exact hook name.

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrCronEmpty is returned when a guest registers a schedule with an
// empty schedule expression or handler ID.
var ErrCronEmpty = errors.New("runtime: cron: schedule and handler_id are required")

// ErrCronHandlerIDShape is returned when a handler ID contains
// characters outside the allowed set [a-zA-Z0-9._-]. The handler ID
// flows into the dispatched hook name; a permissive shape would let
// plugins fabricate hook names with dots and slashes that could
// collide with platform hook conventions.
var ErrCronHandlerIDShape = errors.New("runtime: cron: handler_id contains disallowed characters")

// CronStore is the persistence seam the runtime uses to record a
// plugin-registered schedule. Each call upserts (via the
// (plugin_slug, handler_id) unique constraint) a row into
// plugin_cron_schedules. The leader-elected scheduler reads these
// rows to populate its in-memory registry.
//
// Register MUST be idempotent: a plugin re-running its activation
// hook (or the host re-instantiating after a restart) calls Register
// with the same (slug, handler_id) and expects no error.
type CronStore interface {
	Register(ctx context.Context, pluginSlug, schedule, handlerID string) error
}

// CronDispatcher is the seam between the leader-elected scheduler
// and the hook bus. We declare it as an interface so runtime tests
// don't have to pull in the real bus.
//
// Dispatch is called by the scheduler on the leader replica when a
// plugin's schedule fires.
type CronDispatcher interface {
	Dispatch(ctx context.Context, pluginSlug, handlerID string) error
}

// CronService bundles the store and dispatcher. One per process,
// wired at boot.
type CronService struct {
	store      CronStore
	dispatcher CronDispatcher

	// registered tracks the (slug, handler_id, schedule) triples
	// we've recorded during this process lifetime. Purely a cache
	// to short-circuit duplicate Register calls within a single
	// activation — the store is the source of truth.
	mu         sync.Mutex
	registered map[string]struct{}
}

// NewCronService bundles a store and dispatcher.
//
// dispatcher MAY be nil at construction — only Register is exposed
// to WASM, and the scheduler-side Fire path runs in a separate
// code path. The activation flow only requires the store; tests
// that exercise Fire pass a real dispatcher.
func NewCronService(store CronStore, dispatcher CronDispatcher) *CronService {
	if store == nil {
		panic("runtime: NewCronService: store is required")
	}
	return &CronService{
		store:      store,
		dispatcher: dispatcher,
		registered: map[string]struct{}{},
	}
}

// Register persists a (pluginSlug, schedule, handlerID) tuple. The
// schedule expression is NOT parsed here — the cron scheduler
// validates expressions when it pulls the row into its in-memory
// registry, so a bad expression surfaces at scheduler-load time
// rather than at the hot plugin-activation path.
//
// handlerID is constrained to [a-zA-Z0-9._-] so it can safely become
// part of a hook name later. Empty values are rejected before the
// shape check so the error from a bare empty string is the more
// informative ErrCronEmpty.
func (s *CronService) Register(ctx context.Context, pluginSlug, schedule, handlerID string) error {
	if pluginSlug == "" {
		return errors.New("runtime: cron: pluginSlug is required")
	}
	if schedule == "" || handlerID == "" {
		return ErrCronEmpty
	}
	if !isHandlerIDShape(handlerID) {
		return fmt.Errorf("%w: %q", ErrCronHandlerIDShape, handlerID)
	}

	// In-process dedupe: cheap short-circuit for an activation hook
	// that re-runs Register inside the same process. Store-side
	// idempotency (UNIQUE constraint + ON CONFLICT DO UPDATE) still
	// kicks in across restarts.
	cacheKey := pluginSlug + "|" + handlerID + "|" + schedule
	s.mu.Lock()
	if _, ok := s.registered[cacheKey]; ok {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	if err := s.store.Register(ctx, pluginSlug, schedule, handlerID); err != nil {
		return fmt.Errorf("runtime: cron: store register: %w", err)
	}

	s.mu.Lock()
	s.registered[cacheKey] = struct{}{}
	s.mu.Unlock()
	return nil
}

// Fire is the scheduler-side entry. The leader-elected scheduler in
// packages/go/jobs/cron calls this when a plugin schedule's
// next-fire time elapses; we delegate to the configured
// CronDispatcher (which in production wraps a hook bus Do call) so
// the plugin's WASM handler runs through the bus's panic-recovery
// and metrics envelope.
func (s *CronService) Fire(ctx context.Context, pluginSlug, handlerID string) error {
	if s.dispatcher == nil {
		return errors.New("runtime: cron: no dispatcher configured")
	}
	if pluginSlug == "" || handlerID == "" {
		return errors.New("runtime: cron: Fire requires non-empty slug and handler")
	}
	return s.dispatcher.Dispatch(ctx, pluginSlug, handlerID)
}

// HookFirer is the subset of hooks.Bus.Do the dispatcher consumes.
// hooks.Bus satisfies this; tests inject a fake.
//
// Declared as an interface so this package doesn't have a hard
// dependency on packages/go/hooks — consistent with the rest of
// runtime, which keeps its dependency footprint minimal for ease of
// testing.
type HookFirer interface {
	Do(ctx context.Context, name string, args ...any) error
}

// HookBusDispatcher is a CronDispatcher backed by the hook bus. It
// fires the action "plugin.cron.<slug>.<handlerID>". The plugin's
// activation code is expected to RegisterAction that exact name.
type HookBusDispatcher struct {
	bus HookFirer
}

// NewHookBusDispatcher wraps bus. Non-nil bus required.
func NewHookBusDispatcher(bus HookFirer) *HookBusDispatcher {
	if bus == nil {
		panic("runtime: NewHookBusDispatcher: bus is required")
	}
	return &HookBusDispatcher{bus: bus}
}

// Dispatch fires the hook bus action for this plugin/handler pair.
// The hook name shape is "plugin.cron.<slug>.<handlerID>" — fixed
// so the plugin author knows what to RegisterAction against.
//
// Slug + handler ID are passed as args so a handler hooking multiple
// cron entries can distinguish them at runtime without parsing the
// hook name.
func (d *HookBusDispatcher) Dispatch(ctx context.Context, pluginSlug, handlerID string) error {
	if pluginSlug == "" || handlerID == "" {
		return errors.New("runtime: cron dispatch: empty slug or handler")
	}
	hook := cronHookName(pluginSlug, handlerID)
	return d.bus.Do(ctx, hook, pluginSlug, handlerID)
}

// cronHookName is the canonical hook name for a plugin cron firing.
func cronHookName(pluginSlug, handlerID string) string {
	return "plugin.cron." + pluginSlug + "." + handlerID
}

// CronHookName re-exports cronHookName so callers outside this
// package can ask "what hook name will my schedule fire under?"
// without parsing the format.
func CronHookName(pluginSlug, handlerID string) string {
	return cronHookName(pluginSlug, handlerID)
}

// isHandlerIDShape reports whether s is composed only of characters
// allowed in a handler ID: ASCII letters, digits, dot, underscore,
// hyphen. The set is conservative — anything outside it could
// collide with hook-bus naming conventions or with shell-style
// metacharacters that some downstream consumer might glob.
//
// Empty s returns false (callers check empty separately for the
// more informative ErrCronEmpty).
func isHandlerIDShape(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == '-':
		default:
			return false
		}
	}
	return true
}
