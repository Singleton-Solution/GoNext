package conformance

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/fakehost"
)

// defaultScenarioTimeout is the per-scenario context deadline. The
// fuel-budget scenario explicitly clamps this to 1s; everything else
// inherits the outer default.
const defaultScenarioTimeout = 5 * time.Second

// Suite is the conformance run config. Construct one via [NewSuite]
// and call [Suite.Run] to produce a [Report].
type Suite struct {
	// Scenarios is the set of scenarios to execute. Defaults to
	// [BuiltinScenarios]; tests / fixtures may extend it.
	Scenarios []Scenario

	// PerScenarioTimeout overrides the default 5s context deadline.
	// Zero means use the default.
	PerScenarioTimeout time.Duration

	// HostOption is applied to every fakehost.Host the runner
	// constructs. Useful for tests that need a non-default clock
	// or a slug.
	HostOption []fakehost.Option

	// RecordFixtures, when non-empty, is a directory path where
	// the runner will dump one JSON fixture per scenario after
	// the run. Implements the --record-fixtures CLI flag.
	RecordFixtures string
}

// NewSuite returns a Suite preloaded with the built-in scenarios.
// Pass nothing to use the defaults; mutate the returned struct to
// extend.
func NewSuite() *Suite {
	return &Suite{Scenarios: BuiltinScenarios()}
}

// Run executes the suite against the bundle at path. Returns the
// aggregate Report. Does NOT return an error for "the bundle is
// bad" — those manifest as failed scenarios. The only returned
// errors are filesystem-level (cannot open the bundle, cannot
// record fixtures).
func (s *Suite) Run(ctx context.Context, path string) (Report, error) {
	r := Report{Bundle: path, Suite: "conformance"}

	rawManifest, err := readManifest(path)
	if err != nil {
		r.Add(Fail("manifest.read", fmt.Errorf("open bundle: %w", err)))
		return r, nil
	}
	m, err := ParseManifest(rawManifest)
	if err != nil {
		r.Add(Fail("manifest.parse", err))
		return r, nil
	}

	timeout := s.PerScenarioTimeout
	if timeout == 0 {
		timeout = defaultScenarioTimeout
	}

	scenarios := append([]Scenario(nil), s.Scenarios...)
	sort.SliceStable(scenarios, func(i, j int) bool {
		return scenarios[i].Name < scenarios[j].Name
	})

	for _, sc := range scenarios {
		res := s.runOne(ctx, sc, m, timeout)
		r.Add(res)
	}

	if s.RecordFixtures != "" {
		if err := r.writeFixtures(s.RecordFixtures); err != nil {
			return r, fmt.Errorf("record fixtures: %w", err)
		}
	}
	return r, nil
}

// runOne executes a single scenario, recovering panics into a
// Fail result. The host is per-scenario; scenarios cannot share
// state.
func (s *Suite) runOne(parent context.Context, sc Scenario, m *Manifest, timeout time.Duration) (res ScenarioResult) {
	host := fakehost.New(s.HostOption...)
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	start := time.Now()
	defer func() {
		if rec := recover(); rec != nil {
			res = Fail(sc.Name, fmt.Errorf("panic: %v", rec))
		}
		res.Name = sc.Name
		res.Duration = time.Since(start)
		res.Events = host.Events()
	}()
	if sc.Run == nil {
		return Fail(sc.Name, errors.New("scenario has no Run function"))
	}
	res = sc.Run(ctx, m, host)
	return res
}

// readManifest opens the bundle at p and returns the manifest
// bytes. Accepts directories, .gnplugin files (zip), and .zip
// files. Mirrors the CLI's plugintest.OpenBundle, kept separate so
// the conformance package has no import cycle with the CLI.
func readManifest(p string) ([]byte, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", p, err)
	}
	if st.IsDir() {
		path := filepath.Join(p, "manifest.json")
		return os.ReadFile(path)
	}
	ext := strings.ToLower(filepath.Ext(p))
	if ext != ".gnplugin" && ext != ".zip" {
		return nil, fmt.Errorf("unsupported bundle extension %q", ext)
	}
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == "manifest.json" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, errors.New("manifest.json not found in archive")
}

// writeFixtures dumps one JSON file per scenario under dir. The
// per-scenario filename is the dotted Name with dots turned into
// underscores. Existing files are overwritten — re-running
// --record-fixtures is idempotent.
func (r *Report) writeFixtures(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, sc := range r.Results {
		name := strings.ReplaceAll(sc.Name, ".", "_") + ".json"
		path := filepath.Join(dir, name)
		f, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("create %s: %w", path, err)
		}
		// Reuse the report's encoder so the JSON is pretty.
		single := Report{Bundle: r.Bundle, Suite: r.Suite, Pass: sc.Status == StatusPass}
		single.Results = []ScenarioResult{sc}
		if err := single.WriteJSON(f); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}

// LoadFixtureScenarios scans dir for *.json fixtures and returns
// scenarios that re-run each fixture's recorded trace as an
// assertion harness.
//
// The fixture format is the same shape writeFixtures emits: a
// Report with a single Result containing the recorded events. A
// loaded fixture becomes a Scenario named after the fixture file
// whose Run function asserts that the plugin, when driven, emits
// the SAME number of events of each kind.
//
// This is the minimum useful fixture replay: stricter
// "same-order, same-args" matching is a follow-up. The current
// shape exists so authors can write their first fixture today
// without a separate format.
func LoadFixtureScenarios(dir string) ([]Scenario, error) {
	var out []Scenario
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		out = append(out, scenarioFromFixture(path, strings.TrimSuffix(ent.Name(), ".json")))
	}
	return out, nil
}

// scenarioFromFixture builds a Scenario whose Run function replays
// the fixture and asserts the live recorded trace matches the
// fixture's count-by-kind histogram. The fixture itself is loaded
// lazily inside Run so a missing/corrupt fixture surfaces as a
// scenario fail rather than a load-time panic.
func scenarioFromFixture(path, name string) Scenario {
	return Scenario{
		Name:        "fixture." + name,
		Description: "Replay user-supplied fixture " + filepath.Base(path),
		Run: func(_ context.Context, m *Manifest, h *fakehost.Host) ScenarioResult {
			// Drive the synthetic init so the host has something
			// to record — same code path BuiltinScenarios use.
			driveSyntheticInit(h, m)
			fixture, err := readFixture(path)
			if err != nil {
				return Fail("fixture."+name, err)
			}
			liveByKind := map[string]int{}
			for _, e := range h.Events() {
				liveByKind[e.Kind]++
			}
			expectedByKind := map[string]int{}
			for _, sc := range fixture.Results {
				for _, e := range sc.Events {
					expectedByKind[e.Kind]++
				}
			}
			var diffs []string
			for k, want := range expectedByKind {
				if got := liveByKind[k]; got != want {
					diffs = append(diffs, fmt.Sprintf("%s: got=%d want=%d", k, got, want))
				}
			}
			if len(diffs) > 0 {
				sort.Strings(diffs)
				return Fail("fixture."+name,
					fmt.Errorf("event counts diverge: %s", joinStrings(diffs, "; ")))
			}
			return Pass("fixture."+name,
				fmt.Sprintf("trace shape matches fixture (%d kinds)", len(expectedByKind)))
		},
	}
}
