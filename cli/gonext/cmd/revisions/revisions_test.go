package revisions

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	pkgrev "github.com/Singleton-Solution/GoNext/packages/go/revisions"
)

// fakePool satisfies poolish without holding a real connection.
type fakePool struct{ closed bool }

func (p *fakePool) Close() { p.closed = true }

// fakePruner satisfies prunerish with canned stats. Tests inject
// expected Policy + PrunerOptions so we can verify the CLI maps flags
// to fields correctly.
type fakePruner struct {
	gotPolicy pkgrev.Policy
	gotOpts   pkgrev.PrunerOptions
	stats     pkgrev.Stats
	err       error
}

func (p *fakePruner) Run(_ context.Context, policy pkgrev.Policy, opts pkgrev.PrunerOptions) (pkgrev.Stats, error) {
	p.gotPolicy = policy
	p.gotOpts = opts
	// Mirror the production Pruner: DryRun on opts propagates to stats
	// so the formatter renders the "dry-run" prefix.
	out := p.stats
	out.DryRun = opts.DryRun
	return out, p.err
}

// newTestRunner wires the fake pool and pruner into pruneRunner.
func newTestRunner(p *fakePruner) (pruneRunner, *fakePool) {
	pool := &fakePool{}
	return pruneRunner{
		open: func(_ context.Context, _ string) (poolish, error) {
			return pool, nil
		},
		makePruner: func(_ poolish) prunerish { return p },
	}, pool
}

func TestRun_NoArgs_PrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("usage missing: %q", stderr.String())
	}
}

func TestRun_Help(t *testing.T) {
	for _, arg := range []string{"help", "--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run([]string{arg}, &stdout, &stderr)
			if code != ExitOK {
				t.Errorf("exit: got %d want 0", code)
			}
			if !strings.Contains(stdout.String(), "Usage:") {
				t.Errorf("usage missing: %q", stdout.String())
			}
		})
	}
}

func TestRun_UnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"snorgle"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "unknown subcommand") {
		t.Errorf("expected unknown subcommand on stderr: %q", stderr.String())
	}
}

func TestRunPrune_MissingDSN(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	var stdout, stderr bytes.Buffer
	code := runPruneWith(nil, &stdout, &stderr, pruneRunner{})
	if code != ExitUsage {
		t.Errorf("exit: got %d want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "DATABASE_URL") {
		t.Errorf("expected DATABASE_URL error: %q", stderr.String())
	}
}

func TestRunPrune_DefaultsMapToPolicy(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	fp := &fakePruner{stats: pkgrev.Stats{PostsScanned: 3, Deleted: 7}}
	runner, pool := newTestRunner(fp)
	var stdout, stderr bytes.Buffer
	code := runPruneWith(nil, &stdout, &stderr, runner)
	if code != ExitOK {
		t.Fatalf("exit: got %d want 0 (stderr=%q)", code, stderr.String())
	}
	if fp.gotPolicy.KeepLast != 30 {
		t.Errorf("KeepLast: got %d want 30", fp.gotPolicy.KeepLast)
	}
	if fp.gotPolicy.KeepWithin != 7*24*time.Hour {
		t.Errorf("KeepWithin: got %v want 168h", fp.gotPolicy.KeepWithin)
	}
	if !pool.closed {
		t.Errorf("pool should be closed on success")
	}
	if !strings.Contains(stdout.String(), "deleted=7") {
		t.Errorf("expected stats line on stdout, got %q", stdout.String())
	}
}

func TestRunPrune_FlagsOverrideDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	fp := &fakePruner{}
	runner, _ := newTestRunner(fp)
	var stdout, stderr bytes.Buffer
	code := runPruneWith([]string{
		"--keep-last", "5",
		"--keep-within", "48h",
		"--dry-run",
		"--batch", "100",
	}, &stdout, &stderr, runner)
	if code != ExitOK {
		t.Fatalf("exit: got %d want 0 (stderr=%q)", code, stderr.String())
	}
	if fp.gotPolicy.KeepLast != 5 {
		t.Errorf("KeepLast: got %d want 5", fp.gotPolicy.KeepLast)
	}
	if fp.gotPolicy.KeepWithin != 48*time.Hour {
		t.Errorf("KeepWithin: got %v want 48h", fp.gotPolicy.KeepWithin)
	}
	if !fp.gotOpts.DryRun {
		t.Errorf("DryRun flag did not stick")
	}
	if fp.gotOpts.BatchSize != 100 {
		t.Errorf("BatchSize: got %d want 100", fp.gotOpts.BatchSize)
	}
	if !strings.Contains(stdout.String(), "dry-run") {
		t.Errorf("expected dry-run note on stdout, got %q", stdout.String())
	}
}

func TestRunPrune_RejectsNegativeFlags(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	for _, args := range [][]string{
		{"--keep-last", "-1"},
		{"--keep-within", "-1h"},
	} {
		var stdout, stderr bytes.Buffer
		code := runPruneWith(args, &stdout, &stderr, pruneRunner{})
		if code != ExitUsage {
			t.Errorf("args=%v: exit %d want %d", args, code, ExitUsage)
		}
	}
}

func TestRunPrune_TrailingArgsRejected(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	var stdout, stderr bytes.Buffer
	code := runPruneWith([]string{"--keep-last", "10", "extra"}, &stdout, &stderr, pruneRunner{})
	if code != ExitUsage {
		t.Errorf("exit: got %d want %d", code, ExitUsage)
	}
}

func TestRunPrune_PrunerErrorIsExitFail(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	fp := &fakePruner{
		stats: pkgrev.Stats{PostsScanned: 1, Deleted: 2},
		err:   errors.New("disk on fire"),
	}
	runner, _ := newTestRunner(fp)
	var stdout, stderr bytes.Buffer
	code := runPruneWith(nil, &stdout, &stderr, runner)
	if code != ExitFail {
		t.Errorf("exit: got %d want %d", code, ExitFail)
	}
	// Stats are printed even on partial failure.
	if !strings.Contains(stderr.String(), "deleted=2") {
		t.Errorf("expected stats on stderr: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "disk on fire") {
		t.Errorf("expected wrapped error: %q", stderr.String())
	}
}

func TestFormatStats(t *testing.T) {
	cases := []struct {
		name string
		s    pkgrev.Stats
		want string
	}{
		{
			name: "non-dry",
			s:    pkgrev.Stats{PostsScanned: 2, Scanned: 10, Deleted: 4, Skipped: 1, Duration: 17 * time.Millisecond},
			want: "revisions prune: posts=2 scanned=10 deleted=4 skipped=1 duration=17ms",
		},
		{
			name: "dry-run",
			s:    pkgrev.Stats{PostsScanned: 1, Deleted: 3, DryRun: true},
			want: "revisions prune (dry-run): posts=1 scanned=0 deleted=3 skipped=0 duration=0s",
		},
		{
			name: "with-note",
			s:    pkgrev.Stats{Notes: []string{"policy disabled"}},
			want: "revisions prune: posts=0 scanned=0 deleted=0 skipped=0 duration=0s note=policy_disabled",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatStats(c.s)
			if got != c.want {
				t.Errorf("got %q\nwant %q", got, c.want)
			}
		})
	}
}
