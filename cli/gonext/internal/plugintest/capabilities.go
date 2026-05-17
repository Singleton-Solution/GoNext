package plugintest

// knownCapabilities is the v1 capability vocabulary the plugin manifest can
// declare. Mirrors the names in docs/02-plugin-system.md §2.2 and §6.
// Update this list when a new capability lands in the host ABI.
//
// The map is keyed by the dotted capability token. Values are unused — the
// map is used as a set for O(1) lookup.
var knownCapabilities = map[string]struct{}{
	"db":               {},
	"kv":               {},
	"queue":            {},
	"cron":             {},
	"http":             {},
	"http.serve":       {},
	"media.read":       {},
	"media.write":      {},
	"users.read":       {},
	"users.write":      {},
	"cache.invalidate": {},
	"audit.emit":       {},
	"email":            {},
	"secrets":          {},
}

// IsKnownCapability reports whether the given capability token is part of
// the v1 host ABI vocabulary. See [knownCapabilities].
func IsKnownCapability(name string) bool {
	_, ok := knownCapabilities[name]
	return ok
}

// KnownCapabilities returns a slice copy of the v1 capability vocabulary.
// Intended for `--list-capabilities` style introspection and tests.
func KnownCapabilities() []string {
	out := make([]string, 0, len(knownCapabilities))
	for k := range knownCapabilities {
		out = append(out, k)
	}
	return out
}
