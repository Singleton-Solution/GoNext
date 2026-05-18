// Package main is the dashboard smoke validator.
//
// It walks every *.json file in the given directory and asserts a
// minimum shape:
//
//   - top-level "schemaVersion" is a positive integer;
//   - at least one panel exists under "panels";
//   - every panel has a non-empty "datasource" — either a string
//     ("Prometheus") or an object ({"type": "...", "uid": "..."}).
//
// The validator is deliberately stdlib-only so CI can run it with a
// bare `go run` and no module fetches. It is its own Go module so it
// does not participate in the workspace's `go vet` / `go test` sweeps.
//
// Usage:
//
//	go run ./tools/dashboards/validate.go deploy/grafana/dashboards
//
// Exit codes: 0 on success, 1 on any validation failure.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// dashboard is the minimal subset of the Grafana dashboard schema the
// validator cares about. Everything else in the JSON is ignored.
type dashboard struct {
	// SchemaVersion is decoded into json.Number so we can accept both
	// integer and float forms (Grafana exports as integer, but some
	// editors round-trip via float). We assert positivity below.
	SchemaVersion json.Number       `json:"schemaVersion"`
	Title         string            `json:"title"`
	Panels        []json.RawMessage `json:"panels"`
}

// panel decodes only the datasource field — we don't care about the
// rest of the panel for this smoke check.
type panel struct {
	Datasource json.RawMessage `json:"datasource"`
	Title      string          `json:"title"`
	Type       string          `json:"type"`
	ID         json.Number     `json:"id"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: validate <dir>")
		os.Exit(2)
	}
	dir := os.Args[1]

	files, err := collect(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "validate: %v\n", err)
		os.Exit(1)
	}
	if len(files) == 0 {
		// An empty directory is treated as a soft pass: the CI job is
		// skipped by path-filter when no dashboards change, but a
		// manual run against an empty tree should also succeed rather
		// than fail confusingly.
		fmt.Println("validate: no dashboards found, nothing to check")
		return
	}

	var failures []string
	for _, f := range files {
		if errs := validateFile(f); len(errs) > 0 {
			for _, e := range errs {
				failures = append(failures, fmt.Sprintf("%s: %s", f, e))
			}
			continue
		}
		fmt.Printf("ok  %s\n", f)
	}

	if len(failures) > 0 {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "validate: failures:")
		for _, f := range failures {
			fmt.Fprintf(os.Stderr, "  - %s\n", f)
		}
		os.Exit(1)
	}
	fmt.Printf("validate: %d dashboard(s) ok\n", len(files))
}

// collect returns every *.json file under dir, sorted for stable
// output across machines.
func collect(dir string) ([]string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}

	var out []string
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// validateFile returns the list of validation errors for a single
// dashboard file. An empty slice means the file passes.
func validateFile(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("read: %v", err)}
	}

	var d dashboard
	if err := json.Unmarshal(raw, &d); err != nil {
		return []string{fmt.Sprintf("parse: %v", err)}
	}

	var errs []string

	// schemaVersion must be present and look like a positive integer.
	// We accept any positive number — Grafana has been on
	// schemaVersion 30+ for years; specifying a hard floor here just
	// invites churn whenever a release bumps it.
	sv := strings.TrimSpace(d.SchemaVersion.String())
	if sv == "" {
		errs = append(errs, "missing schemaVersion")
	} else if n, err := d.SchemaVersion.Int64(); err != nil || n <= 0 {
		errs = append(errs, fmt.Sprintf("schemaVersion is not a positive integer (%q)", sv))
	}

	if len(d.Panels) == 0 {
		errs = append(errs, "no panels")
	}

	for i, raw := range d.Panels {
		var p panel
		if err := json.Unmarshal(raw, &p); err != nil {
			errs = append(errs, fmt.Sprintf("panel[%d]: parse: %v", i, err))
			continue
		}
		if err := checkDatasource(p.Datasource); err != nil {
			label := panelLabel(p, i)
			errs = append(errs, fmt.Sprintf("panel[%s]: %v", label, err))
		}
	}

	return errs
}

// checkDatasource asserts the panel has a non-empty datasource. The
// field can be a string (legacy Grafana, e.g. "Prometheus") or an
// object ({"type": "prometheus", "uid": "${datasource}"}).
//
// We accept any non-empty value because dashboards in this repo
// standardise on the templated `${datasource}` UID — pinning to a
// hard-coded UID would defeat the cross-environment portability the
// template variable buys.
func checkDatasource(raw json.RawMessage) error {
	if len(raw) == 0 {
		return errors.New("missing datasource")
	}
	// A literal JSON null counts as missing.
	if string(raw) == "null" {
		return errors.New("datasource is null")
	}

	// Try string form first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			return errors.New("datasource is empty string")
		}
		return nil
	}

	// Otherwise expect an object with a non-empty uid OR type.
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("datasource is neither string nor object: %v", err)
	}
	uid, _ := obj["uid"].(string)
	typ, _ := obj["type"].(string)
	if strings.TrimSpace(uid) == "" && strings.TrimSpace(typ) == "" {
		return errors.New("datasource object has no uid or type")
	}
	return nil
}

// panelLabel builds a human-readable identifier for error messages so
// a failure points at "panel[id=3, title='Latency']" rather than just
// the array index.
func panelLabel(p panel, idx int) string {
	parts := []string{fmt.Sprintf("%d", idx)}
	if id := strings.TrimSpace(p.ID.String()); id != "" {
		parts = append(parts, "id="+id)
	}
	if p.Title != "" {
		parts = append(parts, "title="+strconv.Quote(p.Title))
	}
	return strings.Join(parts, ", ")
}
