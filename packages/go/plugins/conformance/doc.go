// Package conformance runs the contract-conformance suite that
// `gonext plugin test --suite=conformance` invokes.
//
// The suite is a series of [Scenario]s every plugin bundle must
// pass to be certifiable on the marketplace. Each Scenario combines:
//
//   - Static manifest assertions (the bundle declares a known set
//     of capabilities, hook names are recognised, no unknown
//     tokens).
//   - Behavioural assertions driven against a
//     [github.com/Singleton-Solution/GoNext/packages/go/plugins/fakehost.Host]
//     (init+teardown is idempotent, the plugin only calls ABIs
//     covered by its declared capabilities, fuel/timeout caps are
//     respected on a 1s synthetic job).
//
// # Distinction from plugintest
//
// [github.com/Singleton-Solution/GoNext/cli/gonext/internal/plugintest]
// is the contract-check runner that runs at every `gonext plugin
// test` invocation. It enforces the bundle layout and manifest
// schema — the contract every bundle MUST satisfy.
//
// Conformance is the larger suite that runs on demand under
// `--suite=conformance`. It includes plugintest's checks AS WELL AS
// behavioural scenarios that load the plugin into the fake host and
// drive it through canonical interactions.
//
// # Scenarios as data
//
// Each Scenario is a Go value (constructed by the runner) OR a YAML
// fixture loaded from `tests/conformance/fixtures/`. Authors can
// drop their own fixtures next to the built-ins and the runner
// picks them up.
//
// # Result shape
//
// Each scenario produces one [ScenarioResult]. The aggregate is a
// [Report] with a Pass bool and a slice of results. The shape
// mirrors plugintest.Report so the marketplace ingestor sees the
// same structure regardless of which mode produced it.
package conformance
