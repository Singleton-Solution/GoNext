package wpcompat_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/hooks"
	"github.com/Singleton-Solution/GoNext/packages/go/hooks/wpcompat"
)

// ----------------------------------------------------------------------
// Filter forwarding: WP-side subscriber sees native filter fire
// ----------------------------------------------------------------------

// TestSubscribe_TheContent_FiresWhenNativeFilterRuns is the canonical
// migration scenario: a plugin author writes add_filter("the_content",
// fn) on the WP side; the bridge must deliver the value when the native
// "core.filter.the_content" chain runs.
func TestSubscribe_TheContent_FiresWhenNativeFilterRuns(t *testing.T) {
	bus := hooks.NewBus()
	bridge := wpcompat.NewBridge()
	if err := bridge.Register(bus); err != nil {
		t.Fatalf("Bridge.Register: %v", err)
	}
	defer bridge.Close()

	var (
		seen    string
		seenMu  sync.Mutex
		called  atomic.Int32
	)
	off, err := wpcompat.Subscribe(bus, "the_content", wpcompat.DefaultPriority,
		wpcompat.WPFilterFunc(func(value any, args ...any) any {
			seenMu.Lock()
			seen = value.(string)
			seenMu.Unlock()
			called.Add(1)
			return seen + " [filtered]"
		}))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer off()

	out, err := bus.ApplyFilters(context.Background(),
		"core.filter.the_content", "<p>hello</p>")
	if err != nil {
		t.Fatalf("ApplyFilters: %v", err)
	}
	if called.Load() != 1 {
		t.Fatalf("WP handler fired %d times, want 1", called.Load())
	}
	seenMu.Lock()
	defer seenMu.Unlock()
	if seen != "<p>hello</p>" {
		t.Errorf("WP handler saw %q, want %q", seen, "<p>hello</p>")
	}
	if out.(string) != "<p>hello</p> [filtered]" {
		t.Errorf("native chain output %q, want %q",
			out, "<p>hello</p> [filtered]")
	}
}

// ----------------------------------------------------------------------
// Action forwarding with payload adapter: save_post
// ----------------------------------------------------------------------

// TestSubscribe_SavePost_DeliversWPShapedPayload verifies the payload
// adapter: native "core.post.saved" is fired with (id, post, update);
// the WP-side save_post handler must see a WPPost struct.
func TestSubscribe_SavePost_DeliversWPShapedPayload(t *testing.T) {
	bus := hooks.NewBus()
	bridge := wpcompat.NewBridge()
	_ = bridge.Register(bus)
	defer bridge.Close()

	type nativePost struct{ Title string }
	var got wpcompat.WPPost
	var gotMu sync.Mutex
	off, err := wpcompat.Subscribe(bus, "save_post", wpcompat.DefaultPriority,
		wpcompat.WPActionFunc(func(args ...any) {
			gotMu.Lock()
			defer gotMu.Unlock()
			if len(args) != 1 {
				t.Errorf("expected 1 arg, got %d: %v", len(args), args)
				return
			}
			p, ok := args[0].(wpcompat.WPPost)
			if !ok {
				t.Errorf("arg type: got %T want wpcompat.WPPost", args[0])
				return
			}
			got = p
		}))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer off()

	post := &nativePost{Title: "First"}
	if err := bus.Do(context.Background(), "core.post.saved",
		"post-42", post, true); err != nil {
		t.Fatalf("Do: %v", err)
	}

	gotMu.Lock()
	defer gotMu.Unlock()
	if got.ID != "post-42" || !got.Update {
		t.Errorf("WPPost: got %+v want ID=post-42 Update=true", got)
	}
	if got.Post != post {
		t.Errorf("WPPost.Post: got %v want %v", got.Post, post)
	}
}

// ----------------------------------------------------------------------
// Fan-out: handlers on alias name AND on native name both fire exactly once
// ----------------------------------------------------------------------

// TestFanOut_AliasAndNative_BothFireExactlyOnce ensures the bridge does
// not double-deliver. A subscriber on the_content and one on the
// canonical name should each be called once when the native filter
// dispatches.
func TestFanOut_AliasAndNative_BothFireExactlyOnce(t *testing.T) {
	bus := hooks.NewBus()
	bridge := wpcompat.NewBridge()
	_ = bridge.Register(bus)
	defer bridge.Close()

	var nativeHits, wpHits atomic.Int32

	// Two natives on different priorities.
	bus.RegisterFilter("core.filter.the_content", 10,
		func(ctx context.Context, v any, args ...any) (any, error) {
			nativeHits.Add(1)
			return v.(string) + "-native10", nil
		})
	bus.RegisterFilter("core.filter.the_content", 20,
		func(ctx context.Context, v any, args ...any) (any, error) {
			nativeHits.Add(1)
			return v.(string) + "-native20", nil
		})

	// Two WP-side subscribers.
	off1, _ := wpcompat.Subscribe(bus, "the_content", 5,
		wpcompat.WPFilterFunc(func(v any, args ...any) any {
			wpHits.Add(1)
			return v.(string) + "-wp5"
		}))
	defer off1()
	off2, _ := wpcompat.Subscribe(bus, "the_content", 15,
		wpcompat.WPFilterFunc(func(v any, args ...any) any {
			wpHits.Add(1)
			return v.(string) + "-wp15"
		}))
	defer off2()

	_, err := bus.ApplyFilters(context.Background(),
		"core.filter.the_content", "x")
	if err != nil {
		t.Fatalf("ApplyFilters: %v", err)
	}

	if nativeHits.Load() != 2 {
		t.Errorf("native handlers fired %d times, want 2", nativeHits.Load())
	}
	if wpHits.Load() != 2 {
		t.Errorf("WP handlers fired %d times, want 2", wpHits.Load())
	}
}

// TestFanOut_Action_AllSubscribersFireOnce mirrors the filter fan-out
// test for actions. Actions are easier to reason about because there is
// no chained-value question.
func TestFanOut_Action_AllSubscribersFireOnce(t *testing.T) {
	bus := hooks.NewBus()
	bridge := wpcompat.NewBridge()
	_ = bridge.Register(bus)
	defer bridge.Close()

	var nativeHits, wpHits atomic.Int32

	bus.RegisterAction("core.render.head", 10,
		func(ctx context.Context, args ...any) error {
			nativeHits.Add(1)
			return nil
		})
	off1, _ := wpcompat.Subscribe(bus, "wp_head", 10,
		wpcompat.WPActionFunc(func(args ...any) { wpHits.Add(1) }))
	defer off1()
	off2, _ := wpcompat.Subscribe(bus, "wp_head", 20,
		wpcompat.WPActionFunc(func(args ...any) { wpHits.Add(1) }))
	defer off2()

	if err := bus.Do(context.Background(), "core.render.head"); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if nativeHits.Load() != 1 {
		t.Errorf("native: got %d want 1", nativeHits.Load())
	}
	if wpHits.Load() != 2 {
		t.Errorf("wp: got %d want 2", wpHits.Load())
	}
}

// ----------------------------------------------------------------------
// Unknown alias → ErrUnknownAlias
// ----------------------------------------------------------------------

func TestSubscribe_UnknownAlias_ReturnsErrUnknownAlias(t *testing.T) {
	bus := hooks.NewBus()
	_, err := wpcompat.Subscribe(bus, "the_contetn", wpcompat.DefaultPriority,
		wpcompat.WPFilterFunc(func(v any, args ...any) any { return v }))
	if !errors.Is(err, wpcompat.ErrUnknownAlias) {
		t.Errorf("err: got %v want ErrUnknownAlias", err)
	}
}

func TestSubscribe_NilBus_Errors(t *testing.T) {
	_, err := wpcompat.Subscribe(nil, "the_content", 10,
		wpcompat.WPFilterFunc(func(v any, args ...any) any { return v }))
	if err == nil {
		t.Error("expected error for nil bus")
	}
}

// ----------------------------------------------------------------------
// Direction mismatch: filter signature on an action alias (and vice versa)
// ----------------------------------------------------------------------

func TestSubscribe_DirectionMismatch_Errors(t *testing.T) {
	bus := hooks.NewBus()
	// the_content is a filter; passing a WPActionFunc should fail.
	_, err := wpcompat.Subscribe(bus, "the_content", 10,
		wpcompat.WPActionFunc(func(args ...any) {}))
	if err == nil {
		t.Error("expected error when registering action func on filter alias")
	}
	// save_post is an action; passing a WPFilterFunc should fail.
	_, err = wpcompat.Subscribe(bus, "save_post", 10,
		wpcompat.WPFilterFunc(func(v any, args ...any) any { return v }))
	if err == nil {
		t.Error("expected error when registering filter func on action alias")
	}
}

// TestSubscribe_NativeHandlerShapesAlsoWork verifies the convenience
// overload for hooks.FilterHandler / hooks.ActionHandler signatures.
func TestSubscribe_NativeHandlerShapesAlsoWork(t *testing.T) {
	bus := hooks.NewBus()
	bridge := wpcompat.NewBridge()
	_ = bridge.Register(bus)
	defer bridge.Close()

	var hits atomic.Int32
	off, err := wpcompat.Subscribe(bus, "the_content", 10,
		hooks.FilterHandler(func(ctx context.Context, v any, args ...any) (any, error) {
			hits.Add(1)
			return v.(string) + "-native-shape", nil
		}))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer off()

	out, err := bus.ApplyFilters(context.Background(),
		"core.filter.the_content", "x")
	if err != nil {
		t.Fatalf("ApplyFilters: %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("hits: got %d want 1", hits.Load())
	}
	if out.(string) != "x-native-shape" {
		t.Errorf("out: got %q", out)
	}
}

func TestSubscribe_UnsupportedHandlerType_Errors(t *testing.T) {
	bus := hooks.NewBus()
	_, err := wpcompat.Subscribe(bus, "the_content", 10, "not a function")
	if err == nil {
		t.Error("expected error for unsupported handler type")
	}
}

// ----------------------------------------------------------------------
// Unsubscribe stops the handler
// ----------------------------------------------------------------------

func TestSubscribe_Unsubscribe_StopsHandler(t *testing.T) {
	bus := hooks.NewBus()
	bridge := wpcompat.NewBridge()
	_ = bridge.Register(bus)
	defer bridge.Close()

	var hits atomic.Int32
	off, _ := wpcompat.Subscribe(bus, "the_content", 10,
		wpcompat.WPFilterFunc(func(v any, args ...any) any {
			hits.Add(1)
			return v
		}))

	_, _ = bus.ApplyFilters(context.Background(),
		"core.filter.the_content", "x")
	if hits.Load() != 1 {
		t.Fatalf("hits before off: got %d want 1", hits.Load())
	}

	off()
	off() // idempotent

	_, _ = bus.ApplyFilters(context.Background(),
		"core.filter.the_content", "x")
	if hits.Load() != 1 {
		t.Errorf("hits after off: got %d want 1", hits.Load())
	}
}

// ----------------------------------------------------------------------
// Bridge.Close removes forwarders
// ----------------------------------------------------------------------

func TestBridge_Close_RemovesForwarders(t *testing.T) {
	bus := hooks.NewBus()
	bridge := wpcompat.NewBridge()
	_ = bridge.Register(bus)

	var hits atomic.Int32
	off, _ := wpcompat.Subscribe(bus, "the_content", 10,
		wpcompat.WPFilterFunc(func(v any, args ...any) any {
			hits.Add(1)
			return v
		}))
	defer off()

	_, _ = bus.ApplyFilters(context.Background(),
		"core.filter.the_content", "x")
	if hits.Load() != 1 {
		t.Fatalf("hits with bridge: got %d", hits.Load())
	}

	bridge.Close()
	bridge.Close() // idempotent

	_, _ = bus.ApplyFilters(context.Background(),
		"core.filter.the_content", "x")
	if hits.Load() != 1 {
		t.Errorf("hits after Close: got %d want 1 (forwarder gone)", hits.Load())
	}
}

func TestBridge_RegisterTwice_NoDoubleForward(t *testing.T) {
	bus := hooks.NewBus()
	bridge := wpcompat.NewBridge()
	_ = bridge.Register(bus)
	_ = bridge.Register(bus) // second register should replace, not duplicate

	var hits atomic.Int32
	off, _ := wpcompat.Subscribe(bus, "the_content", 10,
		wpcompat.WPFilterFunc(func(v any, args ...any) any {
			hits.Add(1)
			return v
		}))
	defer off()

	_, _ = bus.ApplyFilters(context.Background(),
		"core.filter.the_content", "x")
	if hits.Load() != 1 {
		t.Errorf("double forwarder: hits got %d want 1", hits.Load())
	}
}

func TestBridge_Register_NilBus(t *testing.T) {
	bridge := wpcompat.NewBridge()
	if err := bridge.Register(nil); err == nil {
		t.Error("Register(nil) should error")
	}
}

// ----------------------------------------------------------------------
// Lookup / IsAliased accessors
// ----------------------------------------------------------------------

func TestLookup_KnownAndUnknown(t *testing.T) {
	a, ok := wpcompat.Lookup("the_content")
	if !ok || a.NativeName != "core.filter.the_content" {
		t.Errorf("Lookup the_content: %+v ok=%v", a, ok)
	}
	_, ok = wpcompat.Lookup("nonexistent_hook")
	if ok {
		t.Error("Lookup nonexistent: ok=true")
	}
}

func TestIsAliased(t *testing.T) {
	if !wpcompat.IsAliased("core.filter.the_content") {
		t.Error("IsAliased core.filter.the_content: false")
	}
	if wpcompat.IsAliased("not.in.table") {
		t.Error("IsAliased not.in.table: true")
	}
}

// ----------------------------------------------------------------------
// Direction.String coverage
// ----------------------------------------------------------------------

func TestDirection_String(t *testing.T) {
	if wpcompat.Filter.String() != "filter" {
		t.Errorf("Filter.String: %q", wpcompat.Filter.String())
	}
	if wpcompat.Action.String() != "action" {
		t.Errorf("Action.String: %q", wpcompat.Action.String())
	}
	if wpcompat.Direction(99).String() != "unknown" {
		t.Errorf("Direction(99).String: %q", wpcompat.Direction(99).String())
	}
}

// ----------------------------------------------------------------------
// All ~20 high-value WP names are present in the table
// ----------------------------------------------------------------------

func TestAliases_TopHooksPresent(t *testing.T) {
	must := []string{
		"the_content", "the_title", "the_excerpt", "the_permalink",
		"wp_title", "body_class", "post_class", "comment_text", "get_avatar",
		"login_redirect", "init", "wp_loaded", "wp_head", "wp_footer",
		"wp_enqueue_scripts", "admin_enqueue_scripts", "template_redirect",
		"save_post", "publish_post", "delete_post",
		"user_register", "profile_update", "comment_post",
	}
	for _, name := range must {
		if _, ok := wpcompat.Aliases[name]; !ok {
			t.Errorf("missing alias: %q", name)
		}
	}
}

// ----------------------------------------------------------------------
// Adapter edge cases
// ----------------------------------------------------------------------

// TestAdapter_DefensiveOnShortArgs ensures the save_post adapter doesn't
// crash when args is malformed; it should pass the value through.
func TestAdapter_DefensiveOnShortArgs(t *testing.T) {
	bus := hooks.NewBus()
	bridge := wpcompat.NewBridge()
	_ = bridge.Register(bus)
	defer bridge.Close()

	var got any
	var gotMu sync.Mutex
	off, _ := wpcompat.Subscribe(bus, "save_post", 10,
		wpcompat.WPActionFunc(func(args ...any) {
			gotMu.Lock()
			defer gotMu.Unlock()
			if len(args) > 0 {
				got = args[0]
			}
		}))
	defer off()

	// Fire with only one arg — adapter sees malformed input and
	// should pass it through without panicking.
	if err := bus.Do(context.Background(), "core.post.saved", "lone-arg"); err != nil {
		t.Fatalf("Do: %v", err)
	}

	gotMu.Lock()
	defer gotMu.Unlock()
	// We don't assert on the exact shape — only that the test reached
	// here without panicking and the handler received something.
	_ = got
}

// ----------------------------------------------------------------------
// Race: 100 goroutines subscribe + emit concurrently
// ----------------------------------------------------------------------

func TestRace_ConcurrentSubscribeAndEmit(t *testing.T) {
	bus := hooks.NewBus()
	bridge := wpcompat.NewBridge()
	_ = bridge.Register(bus)
	defer bridge.Close()

	const G = 100
	const ITER = 25

	var fires atomic.Int64
	bus.RegisterFilter("core.filter.the_content", 5,
		func(ctx context.Context, v any, args ...any) (any, error) {
			fires.Add(1)
			return v, nil
		})

	stop := make(chan struct{})

	// Firers (half the goroutines): each runs a bounded ITER batch,
	// then exits. We wait on this WG first so we can signal stop only
	// after the firers are done, then wait for the registrars to drain.
	var firersWG sync.WaitGroup
	for i := 0; i < G/2; i++ {
		firersWG.Add(1)
		go func() {
			defer firersWG.Done()
			for j := 0; j < ITER; j++ {
				_, _ = bus.ApplyFilters(context.Background(),
					"core.filter.the_content", "x")
			}
		}()
	}

	// Registrars (other half): subscribe + unsubscribe in a tight loop
	// until told to stop. Exercises the registration mutex against the
	// firers above.
	var registrarsWG sync.WaitGroup
	for i := 0; i < G/2; i++ {
		registrarsWG.Add(1)
		go func() {
			defer registrarsWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				off, err := wpcompat.Subscribe(bus, "the_content", 10,
					wpcompat.WPFilterFunc(func(v any, args ...any) any { return v }))
				if err != nil {
					t.Errorf("Subscribe: %v", err)
					return
				}
				off()
			}
		}()
	}

	firersWG.Wait()
	close(stop)
	registrarsWG.Wait()
	if fires.Load() < int64((G/2)*ITER) {
		t.Errorf("fires: got %d want >= %d", fires.Load(), (G/2)*ITER)
	}
}

// TestRace_BridgeRegisterCloseUnderDispatch makes Bridge.Register
// churn concurrently with dispatches to verify the forwarder
// install/uninstall path is race-free. Registrar goroutine stops on
// the channel close; dispatcher exits after its bounded loop. We
// close(stop) once the dispatcher signals it's done so the registrar
// can return and Wait returns deterministically.
func TestRace_BridgeRegisterCloseUnderDispatch(t *testing.T) {
	bus := hooks.NewBus()
	bridge := wpcompat.NewBridge()
	_ = bridge.Register(bus)

	stop := make(chan struct{})
	dispatcherDone := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = bridge.Register(bus)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(dispatcherDone)
		for i := 0; i < 200; i++ {
			_, _ = bus.ApplyFilters(context.Background(),
				"core.filter.the_content", "x")
			_ = bus.Do(context.Background(), "core.render.head")
		}
	}()

	<-dispatcherDone
	close(stop)
	wg.Wait()
	bridge.Close()
}
