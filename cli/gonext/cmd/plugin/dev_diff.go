package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// readManifestCapabilities reads <projectDir>/manifest.json and returns
// the sorted, deduplicated list of capability slugs declared by the
// plugin. It tolerates a missing or malformed manifest by returning an
// error — the dev loop calls this opportunistically and prints no diff
// on failure rather than aborting.
//
// We deliberately decode into a minimal struct rather than calling
// into packages/go/plugins/manifest. The CLI doesn't need full schema
// validation here (`gonext plugin test` is the validator); we just
// need the capability list, and a non-strict decode lets the dev loop
// show useful output even while the author's manifest is still
// in-flight.
func readManifestCapabilities(projectDir string) ([]string, error) {
	path := filepath.Join(projectDir, "manifest.json")
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var stub struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(body, &stub); err != nil {
		return nil, err
	}
	return dedupSorted(stub.Capabilities), nil
}

// readManifestName returns the "name" field of the project's
// manifest.json. Used by the --logs flag to identify which per-plugin
// log stream to subscribe to. Errors propagate so the caller can
// disable log tailing rather than connecting to a wrong endpoint.
func readManifestName(projectDir string) (string, error) {
	path := filepath.Join(projectDir, "manifest.json")
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var stub struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &stub); err != nil {
		return "", err
	}
	if stub.Name == "" {
		return "", fmt.Errorf("manifest %q has no name field", path)
	}
	return stub.Name, nil
}

// dedupSorted returns in sorted with duplicates collapsed. We return a
// fresh slice so the caller can store it as the "previous" snapshot
// without worrying about backing-array aliasing.
func dedupSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// writeCapDiff prints the difference between two capability snapshots
// in a +/-/= style. On the very first build, `prev` is nil and we list
// every entry as "=" so the operator can see what the host is being
// asked to grant. On subsequent builds, an added capability is "+ "
// and a removed one is "- ".
func writeCapDiff(w io.Writer, prev, next []string) {
	if len(prev) == 0 && len(next) == 0 {
		return
	}
	if prev == nil {
		// First build: print the initial set so the operator has a
		// reference for later diffs.
		fmt.Fprintln(w, "capabilities:")
		for _, c := range next {
			fmt.Fprintf(w, "  = %s\n", c)
		}
		return
	}
	added, removed := diffSorted(prev, next)
	if len(added) == 0 && len(removed) == 0 {
		return
	}
	fmt.Fprintln(w, "capabilities changed:")
	for _, c := range added {
		fmt.Fprintf(w, "  + %s\n", c)
	}
	for _, c := range removed {
		fmt.Fprintf(w, "  - %s\n", c)
	}
}

// diffSorted returns (added, removed) given two sorted slices. Both
// inputs are assumed to be deduplicated and sorted; [dedupSorted] is
// the canonical producer.
func diffSorted(prev, next []string) (added, removed []string) {
	i, j := 0, 0
	for i < len(prev) && j < len(next) {
		switch {
		case prev[i] == next[j]:
			i++
			j++
		case prev[i] < next[j]:
			removed = append(removed, prev[i])
			i++
		default:
			added = append(added, next[j])
			j++
		}
	}
	removed = append(removed, prev[i:]...)
	added = append(added, next[j:]...)
	return added, removed
}
