package settings

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeRow implements pgx.Row for the single-row read path.
type fakeRow struct {
	value []byte
	err   error
}

func (r *fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 1 {
		return errors.New("fakeRow: wrong dest count")
	}
	switch d := dest[0].(type) {
	case *[]byte:
		*d = r.value
	default:
		return errors.New("fakeRow: unsupported dest type")
	}
	return nil
}

// fakeMultiRow implements pgx.Rows over a list of (key, value) pairs.
type fakeMultiRow struct {
	keys   []string
	values [][]byte
	idx    int
	closed bool
	err    error
}

func newFakeMultiRow(pairs map[string][]byte) *fakeMultiRow {
	keys := make([]string, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vals := make([][]byte, len(keys))
	for i, k := range keys {
		vals[i] = pairs[k]
	}
	return &fakeMultiRow{keys: keys, values: vals, idx: -1}
}

func (r *fakeMultiRow) Next() bool {
	if r.closed || r.err != nil {
		return false
	}
	r.idx++
	return r.idx < len(r.keys)
}
func (r *fakeMultiRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.idx < 0 || r.idx >= len(r.keys) {
		return errors.New("fakeMultiRow: out of range")
	}
	if len(dest) != 2 {
		return errors.New("fakeMultiRow: wrong dest count")
	}
	*(dest[0].(*string)) = r.keys[r.idx]
	*(dest[1].(*[]byte)) = r.values[r.idx]
	return nil
}
func (r *fakeMultiRow) Close()                                     { r.closed = true }
func (r *fakeMultiRow) Err() error                                 { return r.err }
func (*fakeMultiRow) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (*fakeMultiRow) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (*fakeMultiRow) Values() ([]any, error)                       { return nil, nil }
func (*fakeMultiRow) RawValues() [][]byte                          { return nil }
func (*fakeMultiRow) Conn() *pgx.Conn                              { return nil }

// fakeTag is a minimal pgxCommandTag for Exec.
type fakeTag struct{ rows int64 }

func (t fakeTag) RowsAffected() int64 { return t.rows }

// fakeQuerier captures invocations and serves canned responses. Each
// SQL statement is keyed off the first 32 chars so we don't have to
// repeat the full string in every test.
type fakeQuerier struct {
	mu sync.Mutex

	queryRowRows map[string]*fakeRow            // keyed by first 16 chars
	queryRows    map[string]pgx.Rows            // keyed by first 16 chars
	queryErr     map[string]error               // keyed by first 16 chars
	execTags     map[string]pgxCommandTag       // keyed by first 16 chars
	execErrs     map[string]error               // keyed by first 16 chars
	calls        []fakeQuerierCall              // chronological log
	rowsByKey    map[string][]byte              // for read-by-key
	rowReadErr   map[string]error               // for read-by-key
	bulkResp     map[string][]byte              // for ANY($1) BulkRead
	autoloadResp map[string][]byte              // for autoloadSQL
	execCalled   map[string]map[string]struct{} // sql-prefix -> first arg seen
}

type fakeQuerierCall struct {
	sql  string
	args []any
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{
		queryRowRows: map[string]*fakeRow{},
		queryRows:    map[string]pgx.Rows{},
		queryErr:     map[string]error{},
		execTags:     map[string]pgxCommandTag{},
		execErrs:     map[string]error{},
		rowsByKey:    map[string][]byte{},
		rowReadErr:   map[string]error{},
		bulkResp:     map[string][]byte{},
		autoloadResp: map[string][]byte{},
		execCalled:   map[string]map[string]struct{}{},
	}
}

func sqlTag(sql string) string {
	s := strings.TrimSpace(sql)
	if len(s) < 32 {
		return s
	}
	return s[:32]
}

func (q *fakeQuerier) record(sql string, args []any) {
	q.mu.Lock()
	q.calls = append(q.calls, fakeQuerierCall{sql: sqlTag(sql), args: args})
	q.mu.Unlock()
}

func (q *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.record(sql, args)
	// We expect QueryRow only for the single-key readSQL.
	if !strings.HasPrefix(strings.TrimSpace(sql), "SELECT value FROM options") {
		return &fakeRow{err: errors.New("unexpected QueryRow: " + sql)}
	}
	if len(args) == 0 {
		return &fakeRow{err: errors.New("readSQL needs key arg")}
	}
	key, _ := args[0].(string)
	q.mu.Lock()
	defer q.mu.Unlock()
	if err, ok := q.rowReadErr[key]; ok {
		return &fakeRow{err: err}
	}
	if v, ok := q.rowsByKey[key]; ok {
		// Copy to avoid retaining a reference into the shared map.
		buf := make([]byte, len(v))
		copy(buf, v)
		return &fakeRow{value: buf}
	}
	return &fakeRow{err: pgx.ErrNoRows}
}

func (q *fakeQuerier) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.record(sql, args)
	trimmed := strings.TrimSpace(sql)
	switch {
	case strings.HasPrefix(trimmed, "SELECT key, value FROM options WHERE key = ANY"):
		// BulkRead. args[0] is the []string of misses.
		req, _ := args[0].([]string)
		q.mu.Lock()
		hits := make(map[string][]byte, len(req))
		for _, k := range req {
			if v, ok := q.bulkResp[k]; ok {
				buf := make([]byte, len(v))
				copy(buf, v)
				hits[k] = buf
			}
		}
		q.mu.Unlock()
		return newFakeMultiRow(hits), nil
	case strings.HasPrefix(trimmed, "SELECT key, value FROM options WHERE autoload"):
		q.mu.Lock()
		snapshot := make(map[string][]byte, len(q.autoloadResp))
		for k, v := range q.autoloadResp {
			buf := make([]byte, len(v))
			copy(buf, v)
			snapshot[k] = buf
		}
		q.mu.Unlock()
		return newFakeMultiRow(snapshot), nil
	}
	return nil, errors.New("unexpected Query: " + sql)
}

func (q *fakeQuerier) Exec(_ context.Context, sql string, args ...any) (pgxCommandTag, error) {
	q.record(sql, args)
	trimmed := strings.TrimSpace(sql)
	if strings.HasPrefix(trimmed, "INSERT INTO options") {
		q.mu.Lock()
		defer q.mu.Unlock()
		// Record by key so tests can assert exec was called.
		key, _ := args[0].(string)
		if errs, ok := q.execErrs["upsert"]; ok && errs != nil {
			return nil, errs
		}
		m, ok := q.execCalled["upsert"]
		if !ok {
			m = map[string]struct{}{}
			q.execCalled["upsert"] = m
		}
		m[key] = struct{}{}
		// Mirror into rowsByKey so a subsequent Read sees it.
		raw, _ := args[1].([]byte)
		q.rowsByKey[key] = raw
		return fakeTag{rows: 1}, nil
	}
	return nil, errors.New("unexpected Exec: " + sql)
}

// callsFor returns the calls whose SQL prefix matches tag.
func (q *fakeQuerier) callsFor(tag string) []fakeQuerierCall {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]fakeQuerierCall, 0)
	for _, c := range q.calls {
		if strings.HasPrefix(c.sql, tag) {
			out = append(out, c)
		}
	}
	return out
}

// pgRegistry returns the same Registry used by memory tests.
func pgRegistry(t *testing.T) *Registry { return testRegistry(t) }

func TestPostgresStore_ReadFallsBackToDefault(t *testing.T) {
	q := newFakeQuerier()
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)

	v, err := store.Read(context.Background(), "core.site.name")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if v != "My GoNext Site" {
		t.Errorf("Read default: got %v want %q", v, "My GoNext Site")
	}

	// Cache should now hold a tombstone for the unset key.
	cached, ok := store.cacheGet("core.site.name")
	if !ok {
		t.Error("cache should hold tombstone after miss")
	}
	if _, isTombstone := cached.(pgCacheTombstone); !isTombstone {
		t.Errorf("expected tombstone, got %T %v", cached, cached)
	}
}

func TestPostgresStore_ReadHitsRowFromDatabase(t *testing.T) {
	q := newFakeQuerier()
	q.rowsByKey["core.site.name"] = []byte(`"Stored Value"`)
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)

	v, err := store.Read(context.Background(), "core.site.name")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if v != "Stored Value" {
		t.Errorf("Read: got %v want %q", v, "Stored Value")
	}

	// Second Read should hit the cache (no second SQL call).
	q.rowsByKey["core.site.name"] = []byte(`"Different Value"`) // would only be seen with cache miss
	v2, _ := store.Read(context.Background(), "core.site.name")
	if v2 != "Stored Value" {
		t.Errorf("second Read should hit cache: got %v", v2)
	}
	if calls := q.callsFor("SELECT value FROM options"); len(calls) != 1 {
		t.Errorf("expected 1 SQL Read call (cached second time), got %d", len(calls))
	}
}

func TestPostgresStore_ReadUnknownKey(t *testing.T) {
	q := newFakeQuerier()
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)

	_, err := store.Read(context.Background(), "no.such.key")
	if !errors.Is(err, ErrUnknownKey) {
		t.Errorf("expected ErrUnknownKey, got %v", err)
	}
	// No SQL should have been issued.
	if got := len(q.callsFor("SELECT")); got != 0 {
		t.Errorf("unknown key should short-circuit before SQL, got %d calls", got)
	}
}

func TestPostgresStore_WriteValidatesAndUpserts(t *testing.T) {
	q := newFakeQuerier()
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)
	ctx := context.Background()

	if err := store.Write(ctx, "core.site.name", "Test Site"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	upserts := q.callsFor("INSERT INTO options")
	if len(upserts) != 1 {
		t.Fatalf("expected 1 INSERT, got %d", len(upserts))
	}
	args := upserts[0].args
	if args[0].(string) != "core.site.name" {
		t.Errorf("upsert key arg: got %v", args[0])
	}
	if !reflect.DeepEqual(args[1], []byte(`"Test Site"`)) {
		t.Errorf("upsert value arg: got %v", args[1])
	}
	if args[2].(bool) != true {
		t.Errorf("upsert autoload arg: got %v want true", args[2])
	}
	if args[3].(string) != "core" {
		t.Errorf("upsert namespace arg: got %v want core", args[3])
	}
}

func TestPostgresStore_WriteRejectsInvalidValue(t *testing.T) {
	q := newFakeQuerier()
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)
	ctx := context.Background()

	// site.name has minLength: 1.
	err := store.Write(ctx, "core.site.name", "")
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation, got %v", err)
	}
	// No INSERT should have been issued.
	if got := len(q.callsFor("INSERT")); got != 0 {
		t.Errorf("validation failure should not issue INSERT, got %d", got)
	}
}

func TestPostgresStore_WriteInvalidatesAndSeedsCache(t *testing.T) {
	q := newFakeQuerier()
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)
	ctx := context.Background()

	// Prime cache with a tombstone via Read.
	if _, err := store.Read(ctx, "core.site.name"); err != nil {
		t.Fatalf("priming Read: %v", err)
	}
	if cached, _ := store.cacheGet("core.site.name"); cached == nil {
		t.Fatal("cache should be primed with tombstone")
	}
	if _, ok := func() (pgCacheTombstone, bool) {
		v, _ := store.cacheGet("core.site.name")
		t, ok := v.(pgCacheTombstone)
		return t, ok
	}(); !ok {
		t.Fatal("cache entry should be a tombstone before Write")
	}

	// Write the setting — cache entry must be replaced with the new value.
	if err := store.Write(ctx, "core.site.name", "New Name"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	cached, _ := store.cacheGet("core.site.name")
	if cached != "New Name" {
		t.Errorf("post-Write cache: got %v want %q", cached, "New Name")
	}

	// Subsequent Read goes through the cache, not SQL.
	preReadCalls := len(q.callsFor("SELECT value"))
	v, _ := store.Read(ctx, "core.site.name")
	if v != "New Name" {
		t.Errorf("post-Write Read: got %v", v)
	}
	if got := len(q.callsFor("SELECT value")); got != preReadCalls {
		t.Errorf("post-Write Read should hit cache, got extra SQL calls")
	}
}

func TestPostgresStore_WriteUnknownKey(t *testing.T) {
	q := newFakeQuerier()
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)

	err := store.Write(context.Background(), "no.such.key", "x")
	if !errors.Is(err, ErrUnknownKey) {
		t.Errorf("expected ErrUnknownKey, got %v", err)
	}
}

func TestPostgresStore_InvalidateClearsCache(t *testing.T) {
	q := newFakeQuerier()
	q.rowsByKey["core.site.name"] = []byte(`"v1"`)
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)
	ctx := context.Background()

	// Prime cache.
	if _, err := store.Read(ctx, "core.site.name"); err != nil {
		t.Fatalf("priming Read: %v", err)
	}
	if cached, ok := store.cacheGet("core.site.name"); !ok || cached != "v1" {
		t.Fatalf("cache primed wrong: %v ok=%v", cached, ok)
	}

	// Out-of-band SQL change + Invalidate → next Read picks up new value.
	q.rowsByKey["core.site.name"] = []byte(`"v2"`)
	store.Invalidate("core.site.name")
	if _, ok := store.cacheGet("core.site.name"); ok {
		t.Errorf("Invalidate should drop the entry")
	}

	v, _ := store.Read(ctx, "core.site.name")
	if v != "v2" {
		t.Errorf("post-Invalidate Read: got %v want v2", v)
	}
}

func TestPostgresStore_InvalidateAllClearsCache(t *testing.T) {
	q := newFakeQuerier()
	q.rowsByKey["core.site.name"] = []byte(`"v1"`)
	q.rowsByKey["core.comments.enabled"] = []byte(`true`)
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)
	ctx := context.Background()

	_, _ = store.Read(ctx, "core.site.name")
	_, _ = store.Read(ctx, "core.comments.enabled")
	if store.cacheLen() < 2 {
		t.Fatalf("cache len pre-InvalidateAll: got %d want >= 2", store.cacheLen())
	}

	store.InvalidateAll()
	if store.cacheLen() != 0 {
		t.Errorf("cache len post-InvalidateAll: got %d want 0", store.cacheLen())
	}
}

func TestPostgresStore_BulkReadMixesCacheAndSQL(t *testing.T) {
	q := newFakeQuerier()
	q.rowsByKey["core.site.name"] = []byte(`"From SQL"`)
	q.bulkResp["core.posts.per_page"] = []byte(`25`)
	// comments.enabled deliberately absent from store → default fallback.

	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)
	ctx := context.Background()

	// Prime cache with site.name via a single Read.
	if _, err := store.Read(ctx, "core.site.name"); err != nil {
		t.Fatalf("priming Read: %v", err)
	}

	got, err := store.BulkRead(ctx, []string{
		"core.site.name", "core.posts.per_page", "core.comments.enabled", "no.such.key",
	})
	if err != nil {
		t.Fatalf("BulkRead: %v", err)
	}
	if got["core.site.name"] != "From SQL" {
		t.Errorf("cached: got %v", got["core.site.name"])
	}
	if got["core.posts.per_page"] != float64(25) {
		t.Errorf("sql: got %v want 25", got["core.posts.per_page"])
	}
	if got["core.comments.enabled"] != true {
		t.Errorf("default: got %v want true", got["core.comments.enabled"])
	}
	if _, present := got["no.such.key"]; present {
		t.Errorf("unknown key should be skipped: %v", got["no.such.key"])
	}
	if len(got) != 3 {
		t.Errorf("expected 3 results, got %d: %+v", len(got), got)
	}
}

func TestPostgresStore_BulkReadCachesResults(t *testing.T) {
	q := newFakeQuerier()
	q.bulkResp["core.site.name"] = []byte(`"BulkValue"`)
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)
	ctx := context.Background()

	_, _ = store.BulkRead(ctx, []string{"core.site.name"})
	v, ok := store.cacheGet("core.site.name")
	if !ok || v != "BulkValue" {
		t.Errorf("BulkRead should cache result: got %v ok=%v", v, ok)
	}
}

func TestPostgresStore_LoadAutoloadOnlyAutoload(t *testing.T) {
	q := newFakeQuerier()
	// Autoload SQL response includes a value for site.name; comments.enabled
	// stays absent → default expected.
	q.autoloadResp["core.site.name"] = []byte(`"From Autoload"`)
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)

	got, err := store.LoadAutoload(context.Background())
	if err != nil {
		t.Fatalf("LoadAutoload: %v", err)
	}

	// Expected keys: site.name + comments.enabled (both Autoload=true).
	wantKeys := []string{"core.site.name", "core.comments.enabled"}
	gotKeys := make([]string, 0, len(got))
	for k := range got {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(wantKeys)
	sort.Strings(gotKeys)
	if !reflect.DeepEqual(wantKeys, gotKeys) {
		t.Errorf("autoload keys: got %v want %v", gotKeys, wantKeys)
	}
	if got["core.site.name"] != "From Autoload" {
		t.Errorf("autoload value: got %v", got["core.site.name"])
	}
	if got["core.comments.enabled"] != true {
		t.Errorf("default: got %v want true", got["core.comments.enabled"])
	}
}

func TestPostgresStore_LoadAutoloadIgnoresUnregisteredRows(t *testing.T) {
	q := newFakeQuerier()
	// Stale row from an uninstalled plugin shows up in the autoload SQL
	// result. LoadAutoload must skip it rather than leak it.
	q.autoloadResp["core.site.name"] = []byte(`"Real"`)
	q.autoloadResp["unregistered.ghost"] = []byte(`"Stale"`)
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)

	got, _ := store.LoadAutoload(context.Background())
	if _, present := got["unregistered.ghost"]; present {
		t.Errorf("unregistered key should be filtered out: %+v", got)
	}
}

func TestPostgresStore_LoadAutoloadCachesValues(t *testing.T) {
	q := newFakeQuerier()
	q.autoloadResp["core.site.name"] = []byte(`"FromBoot"`)
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)
	ctx := context.Background()

	_, _ = store.LoadAutoload(ctx)
	cached, ok := store.cacheGet("core.site.name")
	if !ok || cached != "FromBoot" {
		t.Errorf("autoload should warm the cache: got %v ok=%v", cached, ok)
	}

	// Subsequent Read hits cache, not SQL.
	pre := len(q.callsFor("SELECT value FROM options"))
	v, _ := store.Read(ctx, "core.site.name")
	if v != "FromBoot" {
		t.Errorf("post-autoload Read: got %v", v)
	}
	if got := len(q.callsFor("SELECT value FROM options")); got != pre {
		t.Errorf("autoload-warmed cache should serve Read, got extra SQL")
	}
}

func TestPostgresStore_WriteErrorClearsCache(t *testing.T) {
	q := newFakeQuerier()
	q.execErrs["upsert"] = errors.New("db write fail")
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)
	ctx := context.Background()

	// Prime cache.
	q.rowsByKey["core.site.name"] = []byte(`"v1"`)
	_, _ = store.Read(ctx, "core.site.name")
	if cached, _ := store.cacheGet("core.site.name"); cached != "v1" {
		t.Fatalf("cache priming: got %v want v1", cached)
	}

	// Write fails — cache must be invalidated so the next read is authoritative.
	err := store.Write(ctx, "core.site.name", "newvalue")
	if err == nil {
		t.Fatal("expected write error")
	}
	if _, ok := store.cacheGet("core.site.name"); ok {
		t.Errorf("write failure should drop the cache entry")
	}
}

func TestPostgresStore_ReadRowReadError(t *testing.T) {
	q := newFakeQuerier()
	q.rowReadErr["core.site.name"] = errors.New("db unreachable")
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)

	_, err := store.Read(context.Background(), "core.site.name")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "db unreachable") {
		t.Errorf("error should bubble underlying message: %v", err)
	}
}

func TestPostgresStore_ReadMalformedJSON(t *testing.T) {
	q := newFakeQuerier()
	q.rowsByKey["core.site.name"] = []byte(`{not json}`)
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)

	_, err := store.Read(context.Background(), "core.site.name")
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestPostgresStore_BulkReadAllCachedShortCircuits(t *testing.T) {
	q := newFakeQuerier()
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)
	ctx := context.Background()

	// Warm cache for both keys.
	if err := store.Write(ctx, "core.site.name", "A"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := store.Write(ctx, "core.comments.enabled", true); err != nil {
		t.Fatalf("Write: %v", err)
	}

	preQuery := len(q.callsFor("SELECT key, value FROM options WHERE key = ANY"))
	got, err := store.BulkRead(ctx, []string{"core.site.name", "core.comments.enabled"})
	if err != nil {
		t.Fatalf("BulkRead: %v", err)
	}
	if got["core.site.name"] != "A" {
		t.Errorf("site.name: got %v", got["core.site.name"])
	}
	if got["core.comments.enabled"] != true {
		t.Errorf("comments.enabled: got %v", got["core.comments.enabled"])
	}
	if got := len(q.callsFor("SELECT key, value FROM options WHERE key = ANY")); got != preQuery {
		t.Errorf("fully-cached BulkRead should not issue SQL: got %d new calls", got-preQuery)
	}
}

// namespaceFor unit tests — the helper is small but load-bearing for
// the namespace column, so cover the cases explicitly.
func TestNamespaceFor(t *testing.T) {
	cases := []struct {
		key, want string
	}{
		{"core.site.name", "core"},
		{"core.timezone", "core"},
		{"plugin:foo.bar", "plugin:foo"},
		{"plugin:multi.dotted.path", "plugin:multi"},
		{"plugin:lone", "plugin:lone"},
		{"myplugin.feature.x", "plugin:myplugin"},
		{"singleword", "core"},
		{"", "core"},
	}
	for _, c := range cases {
		if got := namespaceFor(c.key); got != c.want {
			t.Errorf("namespaceFor(%q) = %q want %q", c.key, got, c.want)
		}
	}
}

// Concurrency smoke test: Read/Write under -race should not data-race.
func TestPostgresStore_ConcurrentReadWrite(t *testing.T) {
	q := newFakeQuerier()
	reg := pgRegistry(t)
	store := NewPostgresStoreWithQuerier(q, reg)
	ctx := context.Background()

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, _ = store.Read(ctx, "core.site.name")
		}()
		go func() {
			defer wg.Done()
			_ = store.Write(ctx, "core.site.name", "Concurrent")
		}()
	}
	wg.Wait()
}

// Sanity: the core seed Registers cleanly via RegisterCore.
func TestRegisterCore(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterCore(reg); err != nil {
		t.Fatalf("RegisterCore: %v", err)
	}
	// Every key from CoreSettings should be Get-able.
	for _, s := range CoreSettings() {
		got, err := reg.Get(s.Key)
		if err != nil {
			t.Errorf("Get(%q): %v", s.Key, err)
			continue
		}
		if got.Key != s.Key {
			t.Errorf("seed key drift: got %q want %q", got.Key, s.Key)
		}
	}
}

// Sanity: every CoreSetting carries the manage_options capability.
func TestCoreSettings_CapabilityGated(t *testing.T) {
	for _, s := range CoreSettings() {
		if s.RequiresCapability == "" {
			t.Errorf("core setting %q missing RequiresCapability", s.Key)
		}
	}
}

// Sanity: every CoreSetting has a non-empty Group.
func TestCoreSettings_GroupAssigned(t *testing.T) {
	for _, s := range CoreSettings() {
		if s.Group == "" {
			t.Errorf("core setting %q missing Group", s.Key)
		}
	}
}

// Plugin-extensibility smoke test mirroring the docs §2.6 example:
// a plugin registers a setting at activation, and a subsequent
// Write+Read through the same Store sees it.
func TestPluginExtensibility_RegisterAndUse(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterCore(reg); err != nil {
		t.Fatalf("RegisterCore: %v", err)
	}

	// "Plugin" activation: register a setting.
	pluginSetting := Setting{
		Key:         "plugin:seo.title_separator",
		Description: "Character used to separate title segments",
		Type:        SettingTypeEnum,
		Schema:      json.RawMessage(`{"type":"string","enum":["-","|","::"]}`),
		Default:     "-",
		Autoload:    false,
		Group:       "seo",
	}
	if err := reg.Register(pluginSetting); err != nil {
		t.Fatalf("plugin Register: %v", err)
	}

	store := NewMemoryStore(reg)
	ctx := context.Background()

	// Default applies.
	v, _ := store.Read(ctx, "plugin:seo.title_separator")
	if v != "-" {
		t.Errorf("plugin default: got %v want -", v)
	}

	// Schema enforced.
	if err := store.Write(ctx, "plugin:seo.title_separator", "@"); !errors.Is(err, ErrValidation) {
		t.Errorf("plugin schema should reject '@': %v", err)
	}

	// Happy path.
	if err := store.Write(ctx, "plugin:seo.title_separator", "|"); err != nil {
		t.Fatalf("plugin Write: %v", err)
	}
	v, _ = store.Read(ctx, "plugin:seo.title_separator")
	if v != "|" {
		t.Errorf("plugin write+read: got %v want |", v)
	}
}
