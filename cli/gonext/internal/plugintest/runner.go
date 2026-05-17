package plugintest

import (
	"errors"
	"fmt"
	"strings"
)

// Check name constants. Keep these stable — the marketplace ingest pipeline
// (docs/11-testing-ci.md §7.2) keys on these strings.
const (
	CheckBundleLayout    = "bundle.layout"
	CheckManifestSchema  = "manifest.schema"
	CheckCapabilities    = "manifest.capabilities"
	CheckWASMModule      = "wasm.module"
	CheckWASMInstantiate = "wasm.instantiate"
	CheckHookRegister    = "hooks.register"
	CheckMigrations      = "migrations.roundtrip"
	CheckHashes          = "bundle.hashes"
	CheckDispatchBudget  = "hooks.dispatch"
)

// runtimeNotAvailable is the canonical skip reason for contract checks that
// require the WASM host. The host has not landed yet — those checks emit a
// row with this reason so the report shape stays stable.
const runtimeNotAvailable = "runtime-not-available"

// Run executes the contract checks against the bundle at p and returns the
// resulting [Report]. The report's Pass flag is true iff no check failed —
// skipped checks (those that need the WASM host) do not count against it.
//
// Run never returns an error for "the bundle is bad" — those land as failed
// rows in the report. It does return an error if the bundle path itself
// cannot be opened (the CLI surfaces that as an exit code distinct from
// "checks ran but some failed").
func Run(p string) (Report, error) {
	report := Report{Bundle: p}

	bundle, err := OpenBundle(p)
	if err != nil {
		return report, err
	}
	defer bundle.Close()

	// 1. Manifest schema — read and parse first because subsequent checks
	//    consume manifest-declared paths (server.wasm).
	rawManifest, err := bundle.ReadManifest()
	if err != nil {
		report.Add(Fail(CheckManifestSchema, fmt.Errorf("read: %w", err)))
		report.Add(Skip(CheckBundleLayout, runtimeNotAvailable))
		report.Add(Skip(CheckCapabilities, runtimeNotAvailable))
		report.Add(Skip(CheckWASMModule, runtimeNotAvailable))
		addReservedChecks(&report)
		return report, nil
	}

	manifest, err := ParseManifest(rawManifest)
	if err != nil {
		report.Add(Fail(CheckManifestSchema, err))
		// Layout we can still check (the path may still be valid). But we
		// don't know the WASM path so we'll fall back to the default.
		addLayoutCheck(&report, bundle, "")
		report.Add(Skip(CheckCapabilities, runtimeNotAvailable))
		addWASMCheck(&report, bundle, "")
		addReservedChecks(&report)
		return report, nil
	}

	if problems := ValidateManifest(manifest); len(problems) > 0 {
		report.Add(Fail(CheckManifestSchema, errors.New(strings.Join(problems, "; "))))
	} else {
		report.Add(Pass(CheckManifestSchema, "manifest validates against v1 schema"))
	}

	// 2. Bundle layout — manifest + WASM are both required entries.
	addLayoutCheck(&report, bundle, manifest.Server.WASM)

	// 3. Capability vocabulary — separate row so dashboards can break it
	//    out from the rest of the manifest schema.
	if unknown := unknownCapabilities(manifest); len(unknown) > 0 {
		report.Add(Fail(CheckCapabilities, fmt.Errorf("unknown capabilities: %s", strings.Join(unknown, ", "))))
	} else {
		report.Add(Pass(CheckCapabilities, fmt.Sprintf("%d capabilities declared, all known", len(manifest.Capabilities))))
	}

	// 4. WASM module — read-only header check until the host lands.
	addWASMCheck(&report, bundle, manifest.Server.WASM)

	// 5. Runtime-dependent checks — scaffolded.
	addReservedChecks(&report)

	return report, nil
}

// addLayoutCheck adds the bundle.layout row.
func addLayoutCheck(report *Report, bundle *Bundle, wasmPath string) {
	if err := bundle.CheckLayout(wasmPath); err != nil {
		report.Add(Fail(CheckBundleLayout, err))
		return
	}
	report.Add(Pass(CheckBundleLayout, "manifest and WASM module present at expected paths"))
}

// addWASMCheck adds the wasm.module row by reading the bytes and running
// [ValidateWASMHeader] over them.
func addWASMCheck(report *Report, bundle *Bundle, wasmPath string) {
	wasm, err := bundle.ReadWASM(wasmPath)
	if err != nil {
		report.Add(Fail(CheckWASMModule, err))
		return
	}
	if err := ValidateWASMHeader(wasm); err != nil {
		report.Add(Fail(CheckWASMModule, err))
		return
	}
	report.Add(Pass(CheckWASMModule, fmt.Sprintf("%d-byte WebAssembly v1 module", len(wasm))))
}

// addReservedChecks emits the rows for contract checks that require the
// WASM host. They share the [runtimeNotAvailable] skip reason so dashboards
// can group them.
func addReservedChecks(report *Report) {
	report.Add(Skip(CheckWASMInstantiate, runtimeNotAvailable))
	report.Add(Skip(CheckHookRegister, runtimeNotAvailable))
	report.Add(Skip(CheckMigrations, runtimeNotAvailable))
	report.Add(Skip(CheckHashes, runtimeNotAvailable))
	report.Add(Skip(CheckDispatchBudget, runtimeNotAvailable))
}

// unknownCapabilities returns the set of capability tokens in m that are
// not in the v1 vocabulary.
func unknownCapabilities(m *Manifest) []string {
	var unknown []string
	for cap := range m.Capabilities {
		if !IsKnownCapability(cap) {
			unknown = append(unknown, cap)
		}
	}
	return unknown
}
