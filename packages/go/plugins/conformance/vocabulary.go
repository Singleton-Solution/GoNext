package conformance

// vocabulary tracks the v1 capability + hook tokens conformance
// knows about. Mirrors the CLI's plugintest vocabulary plus the
// legacy posts.read/posts.write/hooks.subscribe/jobs.enqueue set
// that the seo example (and other early plugins) declare.
//
// We accept BOTH the v1 and the legacy vocabularies so plugins
// authored before the v1 manifest schema landed continue to pass
// conformance. The CLI's strict plugintest already enforces the v1
// schema for marketplace submission; conformance is the broader
// "does the plugin play nicely with the host?" check.

var v1Capabilities = map[string]struct{}{
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
	"posts.read":       {},
	"posts.write":      {},
}

var legacyCapabilities = map[string]struct{}{
	"hooks.subscribe": {},
	"jobs.enqueue":    {},
}

// IsKnownCapability reports whether name is in either the v1 or
// the legacy vocabulary.
func IsKnownCapability(name string) bool {
	if _, ok := v1Capabilities[name]; ok {
		return true
	}
	if _, ok := legacyCapabilities[name]; ok {
		return true
	}
	return false
}

// knownHooks is a non-exhaustive list of core hook names the host
// understands. Plugins MAY register custom hooks (other plugins
// fire them), so we do NOT fail on unknown hook names — but we do
// flag hooks that look like typos of known ones (a future
// enhancement). For now the suite only asserts hook NAMES are
// non-empty strings.
var knownHooks = map[string]struct{}{
	"the_content":  {},
	"the_title":    {},
	"wp_head":      {},
	"wp_footer":    {},
	"save_post":    {},
	"publish_post": {},
	"delete_post":  {},
	"the_excerpt":  {},
}

// IsKnownHook reports whether name is a recognised core hook. The
// hooks-vocabulary scenario tolerates unknown hook names (plugins
// may register their own); this helper is informational.
func IsKnownHook(name string) bool {
	_, ok := knownHooks[name]
	return ok
}
