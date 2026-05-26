package audit

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
)

// Emitter is the ergonomic wrapper around Store. It carries the
// per-request context (actor, IP, user-agent, plugin slug) so handlers
// don't have to re-thread those into every Emit call.
//
// Lifecycle: build one Emitter at process start that points at the
// chosen Store. Per request, call WithRequest to bind the actor and
// HTTP context — that returns a derived Emitter whose Emit calls
// auto-populate ActorUserID, IP, UserAgent. Handlers then call
// derived.Emit(ctx, "post.published", ...opts).
//
// A zero Emitter is unusable; always go through NewEmitter.
type Emitter struct {
	store Store

	// Captured per-request context. Empty on the root Emitter.
	actorUserID string
	pluginSlug  string
	ip          string
	userAgent   string

	// trustedProxies is the set of CIDR ranges whose addresses we trust
	// to set X-Forwarded-For. See the doc on clientIP for the exact
	// trust-chain semantics; an empty list means no proxies are trusted
	// and XFF is ignored (the safe default).
	trustedProxies []netip.Prefix

	// chain carries the HMAC key + previous-row fetcher. nil disables
	// the chain (prev_hash stays whatever the caller set, typically
	// nil). See ChainConfig.Valid.
	chain *ChainConfig

	// chainMu is a process-global lock that serializes the
	// "fetch prev row, hash it, insert" sequence. Stored as a
	// pointer because Emitter is copied by value in the With* path;
	// every derived Emitter must contend on the same lock instance.
	chainMu *sync.Mutex
}

// NewEmitter builds the root Emitter for a process. store is required.
//
// The returned Emitter trusts no proxies by default: X-Forwarded-For is
// ignored entirely, and clientIP is read from r.RemoteAddr. To enable
// XFF handling when running behind one or more reverse proxies, use
// WithTrustedProxies to install the proxy CIDR allowlist.
func NewEmitter(store Store) *Emitter {
	if store == nil {
		// We could return an error, but Emitter is constructed once at
		// boot — a nil store is a wiring bug that should crash early.
		panic("audit.NewEmitter: store is required")
	}
	return &Emitter{store: store, chainMu: &sync.Mutex{}}
}

// Store returns the underlying Store. Useful for admin endpoints that
// want to List directly without an extra dependency.
func (e *Emitter) Store() Store { return e.store }

// WithChain enables the tamper-evidence chain. The receiver is
// mutated in place because the chain is process-global state —
// every derived Emitter (via WithActor, WithHTTP, etc) shares the
// same chain config. Returns the receiver for chainable construction.
//
// Pass nil (or a ChainConfig whose Key is too short / PrevFetcher is
// nil) to disable the chain. ChainConfig.Valid() is consulted before
// each Emit; an invalid config silently disables chaining without
// returning an error from Emit, because audit emission is best-effort
// and an invalid chain config is a startup-time bug.
func (e *Emitter) WithChain(c *ChainConfig) *Emitter {
	if e == nil {
		return nil
	}
	e.chain = c
	return e
}

// WithTrustedProxies returns a derived Emitter that trusts the given
// CIDR ranges to set X-Forwarded-For. The receiver is not mutated.
//
// The trust model is the standard "trust chain": when an inbound
// connection's RemoteAddr is itself a trusted proxy, the emitter walks
// the X-Forwarded-For list from rightmost (closest hop) to leftmost
// (original client) and stops at the first address NOT in the trusted
// set. That address is reported as the client IP. If RemoteAddr is not
// trusted, XFF is ignored entirely and RemoteAddr is used verbatim.
//
// The default — zero trusted proxies — is the safe choice when the
// server is exposed directly: an attacker cannot forge their source IP
// by setting X-Forwarded-For, because the header is never consulted.
func (e *Emitter) WithTrustedProxies(proxies []netip.Prefix) *Emitter {
	cp := *e
	if len(proxies) == 0 {
		cp.trustedProxies = nil
		return &cp
	}
	cp.trustedProxies = make([]netip.Prefix, len(proxies))
	copy(cp.trustedProxies, proxies)
	return &cp
}

// TrustedProxies returns the configured proxy CIDR allowlist. Returned
// for inspection only; the slice is a copy and mutating it has no
// effect on the Emitter.
func (e *Emitter) TrustedProxies() []netip.Prefix {
	if len(e.trustedProxies) == 0 {
		return nil
	}
	out := make([]netip.Prefix, len(e.trustedProxies))
	copy(out, e.trustedProxies)
	return out
}

// WithActor returns a derived Emitter that auto-populates ActorUserID.
// The receiver is not mutated.
func (e *Emitter) WithActor(userID string) *Emitter {
	cp := *e
	cp.actorUserID = userID
	return &cp
}

// WithPlugin returns a derived Emitter that auto-populates the plugin
// slug. Used by the plugin runtime when proxying audit.emit calls.
func (e *Emitter) WithPlugin(slug string) *Emitter {
	cp := *e
	cp.pluginSlug = slug
	return &cp
}

// WithHTTP returns a derived Emitter that captures the IP and User-Agent
// from r. Use this from middleware so handler-level Emit calls don't
// need to inspect the request.
//
// IP resolution honors the Emitter's trusted-proxies allowlist; see the
// godoc on WithTrustedProxies for the trust-chain semantics. Without
// a trusted-proxies list, X-Forwarded-For is ignored.
func (e *Emitter) WithHTTP(r *http.Request) *Emitter {
	cp := *e
	cp.ip = e.clientIP(r)
	cp.userAgent = r.UserAgent()
	return &cp
}

// WithRequest is a convenience that combines WithActor and WithHTTP.
// Typical usage in a handler: `e := emitter.WithRequest(r, currentUser.ID)`.
func WithRequest(base *Emitter, r *http.Request, actorUserID string) *Emitter {
	return base.WithActor(actorUserID).WithHTTP(r)
}

// EmitOption is a functional option for Emit. Options compose; later
// options win when they overlap (e.g. two WithMetadata calls merge,
// with the later call's keys taking precedence).
type EmitOption func(*Event)

// WithTarget sets the ResourceType / ResourceID. Use the empty string
// for events without a specific target (e.g. failed login).
func WithTarget(resourceType, resourceID string) EmitOption {
	return func(e *Event) {
		e.ResourceType = resourceType
		e.ResourceID = resourceID
	}
}

// WithSeverity overrides the default SeverityInfo for this event.
func WithSeverity(s Severity) EmitOption {
	return func(e *Event) { e.Severity = s }
}

// WithMetadata merges m into the event's Metadata map. Subsequent
// WithMetadata calls merge in order, with later keys winning.
func WithMetadata(m map[string]any) EmitOption {
	return func(e *Event) {
		if e.Metadata == nil {
			e.Metadata = make(map[string]any, len(m))
		}
		for k, v := range m {
			e.Metadata[k] = v
		}
	}
}

// WithActorOverride overrides the captured actor for one Emit call.
// Rarely needed — the normal pattern is WithActor on the Emitter.
// Useful when one handler emits an event on behalf of a different
// user (e.g. admin impersonation start, where the actor is the admin
// but the event also has a target user).
func WithActorOverride(userID string) EmitOption {
	return func(e *Event) { e.ActorUserID = userID }
}

// WithIP overrides the captured IP for one Emit call. Use sparingly.
func WithIP(ip string) EmitOption {
	return func(e *Event) { e.IP = ip }
}

// Emit records an audit event. The captured Emitter fields fill in
// defaults; EmitOptions override them.
//
// Returns the Store's error wrapped with %w. Callers that treat audit
// emission as best-effort (the common case for hot-path actions)
// should log-and-continue rather than failing the user-facing request.
//
// Chain semantics: if the Emitter has been wired with WithChain, the
// emitter fetches the most recent stored event, HMACs its canonical
// bytes with the chain key, and writes the result into evt.PrevHash
// before forwarding to the store. The chainMu mutex serializes this
// fetch-and-emit so two concurrent calls don't chain off the same
// predecessor. A chain-fetch failure is logged-and-ignored — we'd
// rather emit a chain-break than drop the audit row.
func (e *Emitter) Emit(ctx context.Context, eventType string, opts ...EmitOption) error {
	if e == nil || e.store == nil {
		return errors.New("audit: Emit called on nil Emitter")
	}
	if eventType == "" {
		return errors.Join(ErrInvalidEvent, errors.New("eventType is required"))
	}
	evt := Event{
		EventType:       eventType,
		ActorUserID:     e.actorUserID,
		ActorPluginSlug: e.pluginSlug,
		IP:              e.ip,
		UserAgent:       e.userAgent,
	}
	for _, opt := range opts {
		opt(&evt)
	}

	if e.chain.Valid() {
		if e.chainMu != nil {
			e.chainMu.Lock()
			defer e.chainMu.Unlock()
		}
		prev, err := e.chain.PrevFetcher()
		if err == nil {
			evt.PrevHash = ChainHash(e.chain.Key, prev)
		}
		// On err we deliberately do NOT bail: emit with a nil PrevHash
		// is preferable to dropping the row, and the verifier flags
		// the chain break.
	}

	return e.store.Emit(ctx, evt)
}

// clientIP returns a best-effort client IP using the standard
// trust-chain pattern.
//
// If the immediate peer (r.RemoteAddr) is NOT in the Emitter's
// trusted-proxies allowlist, X-Forwarded-For is ignored and we report
// the immediate peer. This is the safe path: it prevents a directly
// connecting client from forging their source IP by setting an
// X-Forwarded-For header — the header is only consulted when we
// already trust the immediate hop.
//
// If the immediate peer IS trusted, we walk the X-Forwarded-For list
// from rightmost (most recent hop, set by the proxy in front of us)
// to leftmost (the proxy chain's claim about the original client).
// We stop at the first address that is NOT in the trusted set and
// report that address — that's the leftmost untrusted hop, which is
// the best claim we have about the real client.
//
// On any parsing failure we fall back to r.RemoteAddr (or, ultimately,
// its raw string form) — the audit row is best-effort, but the
// fallback never honors an unverified XFF claim.
func (e *Emitter) clientIP(r *http.Request) string {
	remoteAddr := r.RemoteAddr
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	// No trusted proxies configured: never consult XFF. This is the
	// default and the only safe choice when the server is exposed
	// directly without a reverse proxy in front of it.
	if len(e.trustedProxies) == 0 {
		return host
	}

	peerAddr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}

	// If the immediate peer is not a trusted proxy, ignore XFF and
	// report the peer. A direct client cannot forge their IP by
	// setting the header.
	if !addrInPrefixes(peerAddr, e.trustedProxies) {
		return host
	}

	// The peer is a trusted proxy. Walk XFF from rightmost (closest
	// upstream hop) to leftmost (the chain's claim about the original
	// client) and stop at the first untrusted address.
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return host
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		if candidate == "" {
			continue
		}
		addr, err := netip.ParseAddr(candidate)
		if err != nil {
			// Unparseable hop — treat it as untrusted and return it
			// verbatim. The audit row is best-effort and we'd rather
			// log a fuzzy value than silently fall back to the proxy.
			return candidate
		}
		if !addrInPrefixes(addr, e.trustedProxies) {
			return candidate
		}
	}
	// Every XFF hop is a trusted proxy. Report the immediate peer —
	// it's the best identifier we have left.
	return host
}

// addrInPrefixes reports whether addr falls within any of the given
// CIDR prefixes. An empty prefixes slice always reports false.
func addrInPrefixes(addr netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
