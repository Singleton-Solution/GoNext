package webhooks

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is an in-process Store. Used by tests and the "no DB
// wired" fall-through in dev. Thread-safe via RWMutex.
//
// The implementation is naive: list iterates the map once per call,
// sorting by created_at DESC. That's fine for the row counts a single
// dev environment reaches; production wires the Postgres store
// instead.
type MemoryStore struct {
	mu            sync.RWMutex
	subs          map[string]*subRow
	deliveries    []Delivery
	deliverySeq   int64
	now           func() time.Time
	newID         func() string
}

type subRow struct {
	sub    Subscription
	secret []byte
}

// MemoryStoreOption lets tests pin the clock and ID generator. The
// production wiring uses defaults (time.Now + UUID v7).
type MemoryStoreOption func(*MemoryStore)

// WithClock pins the time source. Tests use this to assert on
// deterministic timestamps in audit rows.
func WithClock(now func() time.Time) MemoryStoreOption {
	return func(s *MemoryStore) { s.now = now }
}

// WithIDGen pins the ID generator. Tests use this to make assertions
// against known subscription IDs.
func WithIDGen(gen func() string) MemoryStoreOption {
	return func(s *MemoryStore) { s.newID = gen }
}

// NewMemoryStore returns an empty MemoryStore. Pass options to pin
// the clock or ID generator in tests.
func NewMemoryStore(opts ...MemoryStoreOption) *MemoryStore {
	s := &MemoryStore{
		subs:  make(map[string]*subRow),
		now:   func() time.Time { return time.Now().UTC() },
		newID: func() string { return uuid.NewString() },
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Create persists a new subscription. The caller supplies the raw
// secret bytes; the store retains them so the test endpoint can sign
// the synthetic event.
func (s *MemoryStore) Create(_ context.Context, in SubscriptionCreate, secret []byte, createdBy string) (Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	// Defensive copies — callers mutating the input slice after Create
	// shouldn't surprise a later Get.
	events := append([]string(nil), in.Events...)
	secretCopy := append([]byte(nil), secret...)
	sub := Subscription{
		ID:        s.newID(),
		Name:      in.Name,
		URL:       in.URL,
		Events:    events,
		Active:    active,
		CreatedBy: createdBy,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.subs[sub.ID] = &subRow{sub: sub, secret: secretCopy}
	return sub, nil
}

// Get returns one subscription by ID.
func (s *MemoryStore) Get(_ context.Context, id string) (Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.subs[id]
	if !ok {
		return Subscription{}, ErrNotFound
	}
	// Return a defensive copy so the caller can't mutate our state.
	sub := row.sub
	sub.Events = append([]string(nil), row.sub.Events...)
	return sub, nil
}

// List returns subscriptions in created_at DESC order. Cursor is the
// stringified index of the next page's first element. Empty cursor
// means "start at the top".
func (s *MemoryStore) List(_ context.Context, limit int, cursor string) ([]Subscription, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := make([]Subscription, 0, len(s.subs))
	for _, row := range s.subs {
		sub := row.sub
		sub.Events = append([]string(nil), row.sub.Events...)
		all = append(all, sub)
	}
	// newest first; stable by ID on tie so paging is deterministic.
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.After(all[j].CreatedAt)
		}
		return all[i].ID > all[j].ID
	})
	start := 0
	if cursor != "" {
		if n, err := strconv.Atoi(cursor); err == nil && n >= 0 {
			start = n
		}
	}
	if start >= len(all) {
		return nil, "", nil
	}
	end := start + limit
	next := ""
	if end < len(all) {
		next = strconv.Itoa(end)
	} else {
		end = len(all)
	}
	return all[start:end], next, nil
}

// Update applies the partial patch.
func (s *MemoryStore) Update(_ context.Context, id string, in SubscriptionUpdate) (Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.subs[id]
	if !ok {
		return Subscription{}, ErrNotFound
	}
	if in.Name != nil {
		row.sub.Name = *in.Name
	}
	if in.URL != nil {
		row.sub.URL = *in.URL
	}
	if in.Events != nil {
		row.sub.Events = append([]string(nil), (*in.Events)...)
	}
	if in.Active != nil {
		row.sub.Active = *in.Active
	}
	row.sub.UpdatedAt = s.now()
	sub := row.sub
	sub.Events = append([]string(nil), row.sub.Events...)
	return sub, nil
}

// Delete removes the row + any deliveries linked to it. Mirrors the
// Postgres CASCADE behavior.
func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subs[id]; !ok {
		return ErrNotFound
	}
	delete(s.subs, id)
	// Drop deliveries for this subscription.
	keep := s.deliveries[:0]
	for _, d := range s.deliveries {
		if d.SubscriptionID != id {
			keep = append(keep, d)
		}
	}
	s.deliveries = keep
	return nil
}

// RecordDelivery appends to the audit log.
func (s *MemoryStore) RecordDelivery(_ context.Context, d Delivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d.DeliveredAt.IsZero() {
		d.DeliveredAt = s.now()
	}
	if d.ID == 0 {
		s.deliverySeq++
		d.ID = s.deliverySeq
	}
	s.deliveries = append(s.deliveries, d)
	// Mirror what the Postgres trigger will do: keep the
	// subscription's last_delivery_* fields in sync. Only mutate
	// when the row exists — RecordDelivery is also called for the
	// synthetic test event, which targets a real subscription.
	if row, ok := s.subs[d.SubscriptionID]; ok && d.Status != "test" {
		row.sub.LastDeliveryAt = d.DeliveredAt
		row.sub.LastDeliveryStatus = d.Status
		row.sub.LastDeliveryResponseCode = d.ResponseCode
		if d.Status == "success" {
			row.sub.ConsecutiveFailures = 0
		} else if d.Status == "retry" || d.Status == "failed" {
			row.sub.ConsecutiveFailures++
		}
	}
	return nil
}

// ListDeliveries returns the most recent deliveries for a
// subscription, newest first.
func (s *MemoryStore) ListDeliveries(_ context.Context, subscriptionID string, limit int, cursor string) ([]Delivery, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Filter then sort. The expected row count per subscription in a
	// dev/test scenario is small enough that this is fine.
	filtered := make([]Delivery, 0)
	for _, d := range s.deliveries {
		if d.SubscriptionID == subscriptionID {
			filtered = append(filtered, d)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if !filtered[i].DeliveredAt.Equal(filtered[j].DeliveredAt) {
			return filtered[i].DeliveredAt.After(filtered[j].DeliveredAt)
		}
		return filtered[i].ID > filtered[j].ID
	})
	start := 0
	if cursor != "" {
		if n, err := strconv.Atoi(cursor); err == nil && n >= 0 {
			start = n
		}
	}
	if start >= len(filtered) {
		return nil, "", nil
	}
	end := start + limit
	next := ""
	if end < len(filtered) {
		next = strconv.Itoa(end)
	} else {
		end = len(filtered)
	}
	return filtered[start:end], next, nil
}

// Secret returns the raw HMAC bytes. Used by the test endpoint to
// sign the synthetic event; production wiring layers a
// SecretResolver over this.
func (s *MemoryStore) Secret(_ context.Context, id string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.subs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), row.secret...), nil
}
