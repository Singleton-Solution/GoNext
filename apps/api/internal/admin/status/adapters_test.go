package status

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/lifecycle"
	"github.com/Singleton-Solution/GoNext/packages/go/theme"
)

// TestParseRedisVersion covers the INFO server reply parser. We don't
// stand up Redis here — the parsing logic is a small string scanner
// and the fixture is what a real INFO reply looks like (trimmed to the
// fields the function cares about).
func TestParseRedisVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "real fixture",
			in: "# Server\r\nredis_version:7.2.4\r\nredis_git_sha1:00000000\r\nos:Linux\r\n",
			want: "7.2.4",
		},
		{
			name: "no trailing CR",
			in:   "redis_version:8.0.0\n",
			want: "8.0.0",
		},
		{
			name: "missing field",
			in:   "# Server\nos:Darwin\n",
			want: "",
		},
		{
			name: "empty",
			in:   "",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRedisVersion(tc.in); got != tc.want {
				t.Errorf("parseRedisVersion(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestMigrationDirSource_CountsUpSQL builds a directory with a mix of
// migration files and asserts only the *.up.sql files count toward
// TotalCount. Directories and other extensions are ignored.
func TestMigrationDirSource_CountsUpSQL(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{
		"001_init.up.sql",
		"001_init.down.sql",
		"002_users.up.sql",
		"002_users.down.sql",
		"003_posts.up.sql",
		"README.md",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("--"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// A subdirectory must NOT be counted as a file.
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	src := MigrationDirSource{
		Dir: dir,
		StatusFn: func(context.Context) (uint, bool, error) {
			return 3, false, nil
		},
	}
	got := src.Snapshot(context.Background())
	if got.TotalCount != 3 {
		t.Errorf("total = %d, want 3", got.TotalCount)
	}
	if got.CurrentVersion != 3 || got.Dirty {
		t.Errorf("current = %d, dirty = %v; want (3, false)", got.CurrentVersion, got.Dirty)
	}
	if got.Error != "" {
		t.Errorf("error = %q, want empty", got.Error)
	}
}

// TestMigrationDirSource_StatusFnError shows that a StatusFn failure is
// surfaced in the Error field without erasing the on-disk TotalCount.
func TestMigrationDirSource_StatusFnError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "001_init.up.sql"), []byte("--"), 0o600)

	src := MigrationDirSource{
		Dir: dir,
		StatusFn: func(context.Context) (uint, bool, error) {
			return 0, false, errors.New("db unreachable")
		},
	}
	got := src.Snapshot(context.Background())
	if got.TotalCount != 1 {
		t.Errorf("total = %d, want 1", got.TotalCount)
	}
	if !strings.Contains(got.Error, "db unreachable") {
		t.Errorf("error = %q, want substring %q", got.Error, "db unreachable")
	}
}

// TestThemeFnSource covers the active-theme adapter against a fake
// ActiveThemeFn that returns a manually constructed ThemeJSON.
func TestThemeFnSource(t *testing.T) {
	t.Parallel()

	manifest := &theme.ThemeJSON{
		Version: theme.CurrentVersion,
		Title:   "gn-pro",
		TemplateParts: []theme.TemplatePartDef{
			{Name: "header"}, {Name: "footer"},
		},
		CustomTemplates: []theme.TemplateDef{
			{Name: "page-landing"},
		},
	}

	src := ThemeFnSource{Fn: func() (string, *theme.ThemeJSON, error) {
		return "gn-pro", manifest, nil
	}}
	got := src.Snapshot(context.Background())
	if got.ActiveName != "gn-pro" || got.PartsCount != 2 || got.TemplatesCount != 1 {
		t.Errorf("snapshot = %+v", got)
	}
	if got.Version != "v1" {
		t.Errorf("version = %q, want v1", got.Version)
	}
	if got.Error != "" {
		t.Errorf("error = %q, want empty", got.Error)
	}
}

// TestThemeFnSource_NoActiveTheme asserts a nil manifest is surfaced
// distinctly from a Fn error — operators want to know "no theme is
// active" without misreading it as a parse failure.
func TestThemeFnSource_NoActiveTheme(t *testing.T) {
	t.Parallel()

	src := ThemeFnSource{Fn: func() (string, *theme.ThemeJSON, error) {
		return "", nil, nil
	}}
	got := src.Snapshot(context.Background())
	if got.Error != "no active theme" {
		t.Errorf("error = %q, want %q", got.Error, "no active theme")
	}
}

// TestLifecycleStoreSource_TalliesByState builds an in-memory plugin
// store with rows in every state we care about and asserts the tally
// + LastInstall.
func TestLifecycleStoreSource_TalliesByState(t *testing.T) {
	t.Parallel()

	store := lifecycle.NewMemoryStorage()
	ctx := context.Background()

	t1 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)

	mustInsert(t, store, lifecycle.Plugin{
		Slug: "a-active", Version: "1.0.0", State: lifecycle.StateActive, InstalledAt: t1,
	})
	mustInsert(t, store, lifecycle.Plugin{
		Slug: "b-inactive", Version: "1.0.0", State: lifecycle.StateInactive, InstalledAt: t2,
	})
	mustInsert(t, store, lifecycle.Plugin{
		Slug: "c-errored", Version: "1.0.0", State: lifecycle.StateErrored, InstalledAt: t3,
	})

	src := LifecycleStoreSource{Store: store}
	got := src.Snapshot(ctx)

	if got.Installed != 3 {
		t.Errorf("installed = %d, want 3", got.Installed)
	}
	if got.Active != 1 {
		t.Errorf("active = %d, want 1", got.Active)
	}
	if got.Errored != 1 {
		t.Errorf("errored = %d, want 1", got.Errored)
	}
	if got.LastInstall != t2.UTC().Format(time.RFC3339) {
		t.Errorf("last_install = %q, want %q (most recent)", got.LastInstall, t2.UTC().Format(time.RFC3339))
	}
}

func mustInsert(t *testing.T, s lifecycle.Storage, p lifecycle.Plugin) {
	t.Helper()
	if err := s.Insert(context.Background(), p); err != nil {
		t.Fatalf("insert %s: %v", p.Slug, err)
	}
}

// TestFilesystemDiskSource_WalksAndSumsBytes seeds a temp dir with
// known file sizes and asserts the walker returns the correct total.
func TestFilesystemDiskSource_WalksAndSumsBytes(t *testing.T) {
	t.Parallel()

	themeDir := t.TempDir()
	mediaDir := t.TempDir()

	// theme: one file of 100 bytes in a subdir + one file of 25 bytes
	// at the root.
	if err := os.Mkdir(filepath.Join(themeDir, "parts"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "parts", "header.html"), make([]byte, 100), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "theme.json"), make([]byte, 25), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// media: 1000 bytes.
	if err := os.WriteFile(filepath.Join(mediaDir, "img.png"), make([]byte, 1000), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	src := FilesystemDiskSource{ThemeDir: themeDir, MediaDir: mediaDir}
	got := src.Snapshot(context.Background())

	if got.ThemeDirBytes != 125 {
		t.Errorf("theme_dir_bytes = %d, want 125", got.ThemeDirBytes)
	}
	if got.MediaDirBytes != 1000 {
		t.Errorf("media_dir_bytes = %d, want 1000", got.MediaDirBytes)
	}
	if got.Error != "" {
		t.Errorf("error = %q, want empty", got.Error)
	}
}

// TestFilesystemDiskSource_MissingDirIsZero asserts a non-existent
// directory yields zero, not an error — a brand-new install has no
// uploaded media yet, and the page should not flag that as a fault.
func TestFilesystemDiskSource_MissingDirIsZero(t *testing.T) {
	t.Parallel()

	src := FilesystemDiskSource{
		ThemeDir: filepath.Join(t.TempDir(), "does-not-exist"),
		MediaDir: "",
	}
	got := src.Snapshot(context.Background())
	if got.ThemeDirBytes != 0 || got.MediaDirBytes != 0 {
		t.Errorf("snapshot = %+v, want zero", got)
	}
	if got.Error != "" {
		t.Errorf("error = %q, want empty for missing dir", got.Error)
	}
}

// fakeInspector implements QueueInspector with fixed per-queue replies.
type fakeInspector struct {
	infos map[string]*asynq.QueueInfo
	errs  map[string]error
}

func (f *fakeInspector) GetQueueInfo(queue string) (*asynq.QueueInfo, error) {
	if err, ok := f.errs[queue]; ok {
		return nil, err
	}
	return f.infos[queue], nil
}

// TestAsynqInspectorSource_TalliesAndIsolatesErrors covers the happy
// path plus an error on one queue: the row's Error is set and other
// queues remain intact.
func TestAsynqInspectorSource_TalliesAndIsolatesErrors(t *testing.T) {
	t.Parallel()

	insp := &fakeInspector{
		infos: map[string]*asynq.QueueInfo{
			"critical": {Pending: 2, Active: 1, Processed: 100, Failed: 0},
			"default":  {Pending: 0, Active: 0, Processed: 50, Failed: 0},
		},
		errs: map[string]error{
			"missing": errors.New("queue not found"),
		},
	}

	src := AsynqInspectorSource{
		Inspector: insp,
		Queues:    []string{"critical", "missing", "default"},
	}
	got := src.Snapshot(context.Background())

	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Name != "critical" || got[0].Pending != 2 || got[0].Active != 1 || got[0].Processed24H != 100 {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Name != "missing" || got[1].Error == "" {
		t.Errorf("got[1] = %+v; want Error set", got[1])
	}
	if got[2].Name != "default" || got[2].Processed24H != 50 {
		t.Errorf("got[2] = %+v", got[2])
	}
}
