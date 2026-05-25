package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// fakeCronStore records every Register call.
type fakeCronStore struct {
	mu       sync.Mutex
	calls    []cronCall
	registerErr error
}

type cronCall struct {
	slug      string
	schedule  string
	handlerID string
}

func (s *fakeCronStore) Register(_ context.Context, slug, schedule, handlerID string) error {
	if s.registerErr != nil {
		return s.registerErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, cronCall{slug: slug, schedule: schedule, handlerID: handlerID})
	return nil
}

// fakeBus is a HookFirer double.
type fakeBus struct {
	mu     sync.Mutex
	fires  []fakeFire
	doErr  error
}

type fakeFire struct {
	name string
	args []any
}

func (b *fakeBus) Do(_ context.Context, name string, args ...any) error {
	if b.doErr != nil {
		return b.doErr
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fires = append(b.fires, fakeFire{name: name, args: args})
	return nil
}

func TestCronService_Register_HappyPath(t *testing.T) {
	store := &fakeCronStore{}
	svc := NewCronService(store, nil)
	if err := svc.Register(context.Background(), "seo", "@hourly", "sitemap-regen"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(store.calls) != 1 {
		t.Fatalf("expected 1 store call, got %d", len(store.calls))
	}
	got := store.calls[0]
	if got.slug != "seo" || got.schedule != "@hourly" || got.handlerID != "sitemap-regen" {
		t.Errorf("call = %+v", got)
	}
}

func TestCronService_Register_EmptyArgs(t *testing.T) {
	store := &fakeCronStore{}
	svc := NewCronService(store, nil)
	cases := []struct {
		slug, sched, handler string
	}{
		{"", "@hourly", "h"},
		{"seo", "", "h"},
		{"seo", "@hourly", ""},
	}
	for _, c := range cases {
		err := svc.Register(context.Background(), c.slug, c.sched, c.handler)
		if err == nil {
			t.Errorf("expected error for %+v", c)
		}
	}
}

func TestCronService_Register_BadHandlerIDShape(t *testing.T) {
	store := &fakeCronStore{}
	svc := NewCronService(store, nil)
	// Allowed: alnum, dot, underscore, hyphen.
	for _, h := range []string{"sitemap.regen", "sitemap-regen", "sitemap_regen", "s123"} {
		if err := svc.Register(context.Background(), "seo", "@hourly", h); err != nil {
			t.Errorf("allowed handler %q rejected: %v", h, err)
		}
	}
	// Rejected: slash, space, colon, special chars.
	for _, h := range []string{"sitemap/regen", "site map", "sitemap:regen", "sitemap*"} {
		err := svc.Register(context.Background(), "seo", "@hourly", h)
		if !errors.Is(err, ErrCronHandlerIDShape) {
			t.Errorf("handler %q: want ErrCronHandlerIDShape, got %v", h, err)
		}
	}
}

func TestCronService_Register_Idempotent(t *testing.T) {
	store := &fakeCronStore{}
	svc := NewCronService(store, nil)
	for i := 0; i < 3; i++ {
		if err := svc.Register(context.Background(), "seo", "@hourly", "h"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	// In-process cache short-circuits — only one store call.
	if len(store.calls) != 1 {
		t.Errorf("expected 1 store call (dedup), got %d", len(store.calls))
	}
}

func TestCronService_Register_StoreError(t *testing.T) {
	store := &fakeCronStore{registerErr: errors.New("connection refused")}
	svc := NewCronService(store, nil)
	err := svc.Register(context.Background(), "seo", "@hourly", "h")
	if err == nil {
		t.Errorf("expected error")
	}
}

func TestCronService_Fire_NoDispatcher(t *testing.T) {
	svc := NewCronService(&fakeCronStore{}, nil)
	if err := svc.Fire(context.Background(), "seo", "h"); err == nil {
		t.Errorf("expected error for nil dispatcher")
	}
}

func TestHookBusDispatcher_Dispatch(t *testing.T) {
	bus := &fakeBus{}
	d := NewHookBusDispatcher(bus)
	if err := d.Dispatch(context.Background(), "seo", "sitemap-regen"); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(bus.fires) != 1 {
		t.Fatalf("expected 1 fire, got %d", len(bus.fires))
	}
	got := bus.fires[0]
	if got.name != "plugin.cron.seo.sitemap-regen" {
		t.Errorf("hook name = %q", got.name)
	}
	if len(got.args) != 2 || got.args[0] != "seo" || got.args[1] != "sitemap-regen" {
		t.Errorf("args = %+v", got.args)
	}
}

func TestHookBusDispatcher_EmptyArgs(t *testing.T) {
	bus := &fakeBus{}
	d := NewHookBusDispatcher(bus)
	if err := d.Dispatch(context.Background(), "", "h"); err == nil {
		t.Errorf("empty slug should error")
	}
	if err := d.Dispatch(context.Background(), "seo", ""); err == nil {
		t.Errorf("empty handler should error")
	}
}

func TestCronHookName(t *testing.T) {
	if got := CronHookName("seo", "h"); got != "plugin.cron.seo.h" {
		t.Errorf("CronHookName = %q", got)
	}
}

func TestNewCronService_NilStore(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic")
		}
	}()
	NewCronService(nil, nil)
}

func TestNewHookBusDispatcher_NilBus(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic")
		}
	}()
	NewHookBusDispatcher(nil)
}

func TestCronService_Fire_DispatchesThroughBus(t *testing.T) {
	bus := &fakeBus{}
	svc := NewCronService(&fakeCronStore{}, NewHookBusDispatcher(bus))
	if err := svc.Fire(context.Background(), "seo", "h"); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if len(bus.fires) != 1 || bus.fires[0].name != "plugin.cron.seo.h" {
		t.Errorf("bus fire wrong: %+v", bus.fires)
	}
}
