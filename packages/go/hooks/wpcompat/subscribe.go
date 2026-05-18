package wpcompat

import (
	"context"
	"fmt"

	"github.com/Singleton-Solution/GoNext/packages/go/hooks"
)

// DefaultPriority is what Subscribe uses when the caller does not specify
// one. It matches WordPress's default add_action / add_filter priority
// (10) so that ported plugins keep the same relative ordering they had
// on the WP side without any code changes.
const DefaultPriority = 10

// WPFilterFunc is the WP-flavored filter handler signature. Compared to
// hooks.FilterHandler:
//
//   - No context.Context — WP plugin code is sync and rarely threads ctx
//     through; the bridge supplies the bus's ctx behind the scenes so
//     handler authors don't have to.
//   - args are variadic any, same as on the WP side.
//
// We deliberately keep the value typed as `any` rather than introducing
// a generic per-alias signature: the WP API itself is dynamically typed,
// and forcing generics here would mean a different Subscribe per alias.
// Handler authors `value.(string)` (or whichever type the appendix
// promises) once, the same as they would in PHP.
type WPFilterFunc func(value any, args ...any) any

// WPActionFunc is the WP-flavored action handler signature. WordPress
// action callbacks return nothing; this signature matches.
type WPActionFunc func(args ...any)

// Subscribe registers fn as a handler for the WP-named hook on bus.
//
// fn must be one of WPFilterFunc or WPActionFunc — passing any other
// function shape returns an error rather than panicking. The choice of
// "tagged union via concrete function types" rather than reflect-based
// signature matching is deliberate: a typed assertion keeps the hot
// path allocation-free, and the per-call cost of "did the caller pass
// a filter or an action?" is one type switch.
//
// Priority follows the same convention as hooks.Bus.Register*: lower
// runs first, ties keep registration order. Use DefaultPriority (10)
// to match WordPress.
//
// Returns ErrUnknownAlias if wpName is not in the Aliases table; this
// surfaces typos at registration time rather than handing back an
// unsubscribe closure that does nothing useful.
//
// The returned unsubscribe function is the same one Bus.Register*
// returns: idempotent, safe to call from any goroutine.
//
// Subscribe registers on the WP-name chain (not the native name). The
// Bridge installed by Register is what forwards native events into that
// chain. Subscribing without an installed Bridge means the handler will
// only fire when something explicitly fires the WP name — which is
// rarely what a plugin author wants. Installing a Bridge once at startup
// is the expected pattern; this package does not enforce it because tests
// occasionally want to fire WP names directly.
func Subscribe(bus *hooks.Bus, wpName string, priority int, fn any) (func(), error) {
	if bus == nil {
		return nil, fmt.Errorf("wpcompat: Subscribe(%q): bus is nil", wpName)
	}
	alias, ok := Aliases[wpName]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAlias, wpName)
	}

	switch h := fn.(type) {
	case WPFilterFunc:
		if alias.Direction != Filter {
			return nil, fmt.Errorf(
				"wpcompat: Subscribe(%q): handler is WPFilterFunc but alias is an %s",
				wpName, alias.Direction)
		}
		off := bus.RegisterFilter(wpName, priority,
			func(ctx context.Context, value any, args ...any) (any, error) {
				return h(value, args...), nil
			})
		return off, nil

	case WPActionFunc:
		if alias.Direction != Action {
			return nil, fmt.Errorf(
				"wpcompat: Subscribe(%q): handler is WPActionFunc but alias is a %s",
				wpName, alias.Direction)
		}
		off := bus.RegisterAction(wpName, priority,
			func(ctx context.Context, args ...any) error {
				h(args...)
				return nil
			})
		return off, nil

	case hooks.FilterHandler:
		// Native-shape filter handler is also accepted — useful for
		// Go-side code that already speaks the FilterHandler signature
		// but wants to subscribe by WP name (e.g. a test asserting on
		// ctx propagation through the bridge).
		if alias.Direction != Filter {
			return nil, fmt.Errorf(
				"wpcompat: Subscribe(%q): handler is FilterHandler but alias is an %s",
				wpName, alias.Direction)
		}
		return bus.RegisterFilter(wpName, priority, h), nil

	case hooks.ActionHandler:
		if alias.Direction != Action {
			return nil, fmt.Errorf(
				"wpcompat: Subscribe(%q): handler is ActionHandler but alias is a %s",
				wpName, alias.Direction)
		}
		return bus.RegisterAction(wpName, priority, h), nil

	default:
		return nil, fmt.Errorf(
			"wpcompat: Subscribe(%q): unsupported handler type %T (want WPFilterFunc, WPActionFunc, hooks.FilterHandler, or hooks.ActionHandler)",
			wpName, fn)
	}
}

// Lookup returns the alias entry for the given WP name, plus an ok flag
// indicating whether the name is recognized. The returned Alias is a
// copy — the global table is not mutable through this accessor.
//
// This is helpful for callers that want to inspect the mapping without
// importing the table directly (or for code generators producing docs).
func Lookup(wpName string) (Alias, bool) {
	a, ok := Aliases[wpName]
	return a, ok
}

// IsAliased reports whether the given GoNext canonical name has at
// least one WP alias pointing at it. Callers can use this to decide
// whether to log "this dispatch will fan out to WP-compat".
func IsAliased(nativeName string) bool {
	_, ok := nativeIndex[nativeName]
	return ok
}
