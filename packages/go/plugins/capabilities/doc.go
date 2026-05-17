// Package capabilities is the host-side registry of plugin capabilities.
//
// Plugin capabilities are distinct from user/admin capabilities (those live
// in packages/go/policy). A user capability answers "is this human allowed
// to publish posts?"; a plugin capability answers "is this WASM module
// permitted to call posts.write?". The two systems share the conceptual
// shape — named slugs that gate access to a resource/action — but they
// have separate registries, separate enforcement paths, and separate audit
// vocabulary.
//
// What's in this package:
//
//   - CapabilityDef is the descriptor for one host capability: ID,
//     Description, Resource, Action, and a Sensitive flag for caps that
//     warrant extra scrutiny in the install UX (e.g. http.fetch reaching
//     outbound, email.send able to dispatch to arbitrary recipients).
//
//   - Registry is the process-wide store of CapabilityDefs. Plugins do NOT
//     register caps themselves — only the host does. A plugin DECLARES
//     which already-registered caps it needs in its manifest; the lifecycle
//     install path resolves those names against the Registry and rejects
//     anything unknown. This keeps the trust boundary clean: a malicious
//     plugin cannot mint a new capability slug to escape sandboxing.
//
//   - Checker is the per-plugin enforcement point. It carries the set of
//     caps granted to one plugin (the subset of registered caps the
//     operator approved at install time, intersected with what the
//     manifest declared). The WASM ABI layer holds a Checker per
//     instantiation and calls MustAllow on every host-call entry. A
//     denial returns ErrCapabilityDenied wrapping the cap ID, so callers
//     can use errors.Is to switch on the failure mode without parsing the
//     message string.
//
//   - Audit emission on denial: every MustAllow that returns
//     ErrCapabilityDenied also emits a `capability_denied` audit event
//     through the host's audit.Emitter, tagged with the plugin slug, the
//     cap ID, and SeverityWarning. The denial path is intentionally
//     noisy: a plugin reaching for a capability it doesn't hold is
//     usually a bug, but it can also be the signature of an exploit
//     attempt — operators want to see it.
//
// Built-in capabilities (registered at init):
//
//   - posts.read         — read post rows
//   - posts.write        — create / update / delete posts
//   - users.read         — read user rows (no PII fields)
//   - email.send         — outbound transactional email (Sensitive)
//   - http.fetch         — outbound HTTP request (Sensitive)
//   - kv.read            — read from the plugin KV namespace
//   - kv.write           — write to the plugin KV namespace
//   - hooks.subscribe    — register a hook listener
//   - jobs.enqueue       — enqueue a background job
//
// The set is intentionally small in P0. Follow-up issues will expand it
// (media.*, taxonomies.*, comments.*) as the corresponding host APIs
// land. The seam is stable: new entries go through the Registry's
// init-time Register, and the lifecycle install path picks them up
// automatically.
//
// Typical wiring:
//
//	// At process boot: built-ins are already in the global registry.
//	reg := capabilities.Default()
//
//	// At plugin install: validate the manifest's declared caps.
//	for _, slug := range manifest.Capabilities {
//	    if _, ok := reg.Get(slug); !ok {
//	        return fmt.Errorf("unknown capability %q", slug)
//	    }
//	}
//
//	// At WASM instantiation: bind the granted set to a Checker.
//	granted := capabilities.NewGrantSet(plugin.Capabilities...)
//	chk := capabilities.NewChecker(reg, granted,
//	    capabilities.WithAuditEmitter(emitter.WithPlugin(slug)),
//	)
//
//	// On every host-call entry point:
//	if err := chk.MustAllow(ctx, "posts.write"); err != nil {
//	    return err  // ErrCapabilityDenied bubbles to the WASM caller as a trap
//	}
//
// See docs/02-plugin-system.md §4 (capability grammar) and §6 (host
// runtime) for the broader picture once those docs land.
package capabilities
