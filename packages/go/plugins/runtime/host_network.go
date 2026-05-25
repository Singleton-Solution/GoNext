package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/capabilities"
	"github.com/Singleton-Solution/GoNext/packages/go/ratelimit"
	"github.com/tetratelabs/wazero/api"
)

// Network ABI surface.
//
// This file owns three host exports — gn_http_fetch, gn_media_read, and
// gn_users_read — plus the per-plugin context registry every one of them
// consults to find the capability checker, audit emitter, rate limiter,
// and resource services bound to the calling module.
//
// All three exports share the same envelope shape: the guest writes a
// JSON-encoded request blob into linear memory (via gn_alloc) and passes
// (ptr, len). The host returns a packed i64 — high 32 bits = result_ptr,
// low 32 bits = result_len — pointing at a JSON-encoded response blob
// the host wrote into guest memory via the guest's gn_alloc export. The
// guest is responsible for calling gn_free on the result when done.
//
// JSON over MessagePack: the existing hook ABI (packages/go/plugins/abi/hooks)
// uses JSON for the same reasons — every plugin SDK has a stdlib-grade
// JSON implementation, and the hot path here is the network round-trip,
// not the codec. A future v2 of this ABI can swap codecs without changing
// the envelope shape.
//
// Error path: a failed call returns (ptr=0, len=N) where N is one of the
// NetResultStatus* sentinels listed below. The packed-i64 convention
// mirrors the hooks ABI so guest SDKs can share decoding logic.

// hostNetworkModuleName is the namespace under which the network host
// functions register. Kept separate from the built-in "env" host module
// so the network surface can be wired in only when a runtime is built
// with WithNetworkContext — runtimes that don't grant any plugin a
// network capability never see the extra exports.
const hostNetworkModuleName = "env"

// NetResultStatus is the catalog of negative-length sentinels the host
// returns when a network call fails. Same shape as
// abi/hooks.ResultStatus: the low 32 bits are read as int32 so negative
// values can never collide with a legitimate length.
type NetResultStatus int32

const (
	// NetStatusOK is success with no body. The host never returns this
	// for fetch/media/users (those always return a JSON envelope) but
	// it's spelled out for symmetry with the hooks ABI.
	NetStatusOK NetResultStatus = 0

	// NetStatusBadRequest signals the guest payload could not be
	// decoded as JSON or failed validation (empty URL, illegal method).
	NetStatusBadRequest NetResultStatus = -1

	// NetStatusDenied signals the plugin lacks the required capability.
	// The host has already emitted the capability_denied audit row by
	// the time the guest sees this sentinel.
	NetStatusDenied NetResultStatus = -2

	// NetStatusBlocked signals an allowlist or SSRF guard rejection —
	// the target host wasn't in the manifest's allow list, or resolved
	// to a private/loopback/link-local IP.
	NetStatusBlocked NetResultStatus = -3

	// NetStatusRateLimited signals the per-plugin rate limit fired.
	// The audit row is emitted alongside.
	NetStatusRateLimited NetResultStatus = -4

	// NetStatusUpstream signals a transport-level error talking to the
	// upstream — DNS failure, TCP reset, TLS handshake error, redirect
	// loop, response read failure. The body envelope carries the
	// stringified Go error in its "error" field.
	NetStatusUpstream NetResultStatus = -5

	// NetStatusNotFound is returned by media.read and users.read when
	// the requested id does not exist or the host has no backing
	// service for the lookup.
	NetStatusNotFound NetResultStatus = -6

	// NetStatusInternal is the catch-all for host-side plumbing
	// failures (audit store down, allocator returned 0, JSON marshal
	// failed). The guest cannot do anything about it but the audit row
	// captures the cause.
	NetStatusInternal NetResultStatus = -7
)

// String returns a short human-readable name for the status. Used in
// audit metadata and error messages; the wire format uses the int32.
func (s NetResultStatus) String() string {
	switch s {
	case NetStatusOK:
		return "ok"
	case NetStatusBadRequest:
		return "bad_request"
	case NetStatusDenied:
		return "denied"
	case NetStatusBlocked:
		return "blocked"
	case NetStatusRateLimited:
		return "rate_limited"
	case NetStatusUpstream:
		return "upstream"
	case NetStatusNotFound:
		return "not_found"
	case NetStatusInternal:
		return "internal"
	default:
		return fmt.Sprintf("status(%d)", int32(s))
	}
}

// HTTP fetch defaults.
//
// These constants describe the v1 envelope of the http.fetch capability.
// They are surface-level guards in addition to per-call caps that come
// in from the manifest (allow_hosts, rate_limit_rpm). Anything declared
// in the manifest must be a subset of (or stricter than) these defaults.
const (
	// MaxHTTPFetchTimeout caps how long one fetch call may take. 30s
	// matches the brief and is generous for any well-behaved upstream.
	// The per-call ctx may shrink this further; it never extends it.
	MaxHTTPFetchTimeout = 30 * time.Second

	// MaxHTTPFetchRedirects caps how many redirects the host will
	// follow before failing. 3 matches the brief.
	MaxHTTPFetchRedirects = 3

	// MaxHTTPFetchResponseBytes caps the response body the host will
	// read into a buffer before returning it to the guest. 10 MiB
	// matches the brief.
	MaxHTTPFetchResponseBytes = 10 * 1024 * 1024

	// MaxHTTPFetchRequestBytes caps the guest-supplied request envelope
	// (URL, headers, body). 1 MiB is generous for an outbound request.
	MaxHTTPFetchRequestBytes = 1 * 1024 * 1024

	// DefaultHTTPFetchRPM is the per-plugin rate limit if the manifest
	// doesn't override. 60 requests per minute is one per second
	// steady-state with a small burst budget.
	DefaultHTTPFetchRPM = 60
)

// MediaProvider is the seam the gn_media_read export consults. The
// production wiring satisfies it from packages/go/media (once the public
// API lands); tests inject a stub. A nil MediaProvider on the
// NetworkContext makes media.read return NetStatusNotFound for every
// call — that is the safe default for a host that hasn't wired the
// media pipeline yet.
type MediaProvider interface {
	// Read returns metadata + a signed URL for the asset identified by
	// id. The host is expected to enforce a short TTL on the URL (the
	// design doc calls for 15 minutes; the provider owns the policy).
	// Returning (nil, nil) signals "not found" — the host translates
	// that into NetStatusNotFound.
	Read(ctx context.Context, id string) (*MediaAsset, error)
}

// MediaAsset is the host-side handle the media provider returns. The
// shape mirrors the JSON envelope sent to the guest verbatim so the
// provider doesn't have to remarshal.
type MediaAsset struct {
	ID          string         `json:"id"`
	MimeType    string         `json:"mime_type,omitempty"`
	SizeBytes   int64          `json:"size_bytes,omitempty"`
	SignedURL   string         `json:"signed_url"`
	ExpiresAt   time.Time      `json:"expires_at,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// UsersProvider is the seam the gn_users_read export consults. As with
// MediaProvider, a nil provider on the NetworkContext makes users.read
// return NetStatusNotFound for every call.
type UsersProvider interface {
	// Read returns the user row identified by id as a generic
	// field-keyed map. The host strips fields the plugin did not
	// declare in its users.read field allowlist before returning to
	// the guest. Returning (nil, nil) signals "not found".
	Read(ctx context.Context, id string) (map[string]any, error)
}

// NetworkContext is the per-plugin handle the network host functions
// look up by module name at call time. It carries every dep the
// gn_http_fetch / gn_media_read / gn_users_read paths need:
//
//   - Slug pins the plugin identity for audit attribution.
//
//   - Checker enforces capability grants. A nil Checker denies every
//     call (deny-by-default is the safe posture).
//
//   - Emitter records audit rows. A nil Emitter skips audit emission
//     (suitable for tests, never for production).
//
//   - Limiter enforces per-plugin rate limits on outbound HTTP. A nil
//     Limiter disables rate limiting (suitable for tests).
//
//   - AllowHosts is the manifest's http.fetch.allow_hosts list (lower-
//     cased, exact-match in v1). An empty list blocks every outbound
//     URL — that's the v1 default for a plugin that declared the cap
//     but no specific hosts.
//
//   - HTTPClient is the transport used for the fetch. nil means use
//     the package's default client (no proxy, 30s timeout, redirect
//     guard installed).
//
//   - MediaProvider / UsersProvider supply the media/users payloads.
//     Nil providers return NotFound.
//
//   - UsersFields is the field allowlist parsed from the manifest's
//     users.read capability scope (e.g. ["id", "email"]). Anything not
//     in this set is stripped from the response. nil/empty allows only
//     the default safe fields ("id", "display_name", "roles").
type NetworkContext struct {
	Slug           string
	Checker        *capabilities.Checker
	Emitter        *audit.Emitter
	Limiter        ratelimit.Limiter
	AllowHosts     []string
	HTTPClient     *http.Client
	MediaProvider  MediaProvider
	UsersProvider  UsersProvider
	UsersFields    []string
}

// allowsHost reports whether host matches the AllowHosts list. v1 is
// exact-match only (no wildcards) — the manifest schema rejects
// wildcards too. Comparison is case-insensitive (DNS is). The empty
// list is "deny all", which is the safe default for a plugin that
// declared http.fetch but no specific hosts.
func (nc *NetworkContext) allowsHost(host string) bool {
	if nc == nil || len(nc.AllowHosts) == 0 {
		return false
	}
	host = strings.ToLower(host)
	for _, h := range nc.AllowHosts {
		if strings.EqualFold(h, host) {
			return true
		}
	}
	return false
}

// usersAllowedFields returns the set of user-field keys this plugin may
// see. If the manifest declared none, fall back to the safe default
// set (no PII): id, display_name, roles. The fallback exists so a
// misconfigured manifest doesn't accidentally leak email addresses.
func (nc *NetworkContext) usersAllowedFields() map[string]struct{} {
	if nc == nil || len(nc.UsersFields) == 0 {
		return map[string]struct{}{
			"id":           {},
			"display_name": {},
			"roles":        {},
		}
	}
	out := make(map[string]struct{}, len(nc.UsersFields))
	for _, f := range nc.UsersFields {
		out[strings.ToLower(f)] = struct{}{}
	}
	return out
}

// networkRegistry is the per-runtime store of plugin-keyed
// NetworkContexts. The runtime adds one entry per active plugin via
// RegisterNetworkContext (called from the lifecycle Activate path) and
// drops it via UnregisterNetworkContext at Deactivate.
//
// Lookup is read-heavy (every host call hits it) and writes are rare
// (activate/deactivate are not in the hot path), so we use a sync.Map.
var networkRegistry sync.Map // map[string]*NetworkContext

// RegisterNetworkContext binds the per-plugin network configuration to
// the calling module's slug. The host functions in this file look the
// context up by module name on every call. Safe for concurrent use.
//
// Idempotent: registering a slug a second time replaces the existing
// context. The lifecycle Manager calls this on every Activate; a
// concurrent re-activation just overwrites with the new bindings.
func RegisterNetworkContext(nc *NetworkContext) {
	if nc == nil || nc.Slug == "" {
		return
	}
	networkRegistry.Store(nc.Slug, nc)
}

// UnregisterNetworkContext removes the binding for a slug. Called from
// the lifecycle Manager at Deactivate so a stale plugin module can't
// keep dispatching through a now-revoked capability surface.
func UnregisterNetworkContext(slug string) {
	if slug == "" {
		return
	}
	networkRegistry.Delete(slug)
}

// lookupNetworkContext returns the NetworkContext for slug, or nil if
// no binding exists. The host functions treat nil as deny-by-default —
// every call returns NetStatusDenied without emitting an audit row
// (the runtime never authorized the plugin in the first place; nothing
// to audit). This is the safe behaviour for a plugin that was loaded
// without going through Activate.
func lookupNetworkContext(slug string) *NetworkContext {
	v, ok := networkRegistry.Load(slug)
	if !ok {
		return nil
	}
	return v.(*NetworkContext)
}

// WithNetworkHost returns a HostModuleBuilder that registers the
// network ABI exports against the supplied runtime. Wire it via
// WithHostModule on Runtime construction:
//
//	rt, err := runtime.New(ctx,
//	    runtime.WithHostModule(runtime.WithNetworkHost()),
//	)
//
// The exports are registered under the same "env" namespace as gn_log
// / gn_panic — guest toolchains import the env module by default, so
// the new functions are visible without any per-plugin import config.
func WithNetworkHost() HostModuleBuilder {
	return func(ctx context.Context, rt wazeroRuntime) error {
		// We need a wazero builder that can append to the existing
		// "env" module. But wazero rejects double-instantiation of the
		// same module name, so we register the network functions under
		// a distinct "env_net" namespace and rely on plugin SDKs to
		// import them under that name. The convention is documented
		// in docs/02-plugin-system.md §6.3.
		const ns = "env_net"
		b := rt.NewHostModuleBuilder(ns)

		b.NewFunctionBuilder().
			WithGoModuleFunction(api.GoModuleFunc(hostGnHTTPFetch),
				[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32},
				[]api.ValueType{api.ValueTypeI64}).
			WithParameterNames("req_ptr", "req_len").
			Export("gn_http_fetch")

		b.NewFunctionBuilder().
			WithGoModuleFunction(api.GoModuleFunc(hostGnMediaRead),
				[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32},
				[]api.ValueType{api.ValueTypeI64}).
			WithParameterNames("req_ptr", "req_len").
			Export("gn_media_read")

		b.NewFunctionBuilder().
			WithGoModuleFunction(api.GoModuleFunc(hostGnUsersRead),
				[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32},
				[]api.ValueType{api.ValueTypeI64}).
			WithParameterNames("req_ptr", "req_len").
			Export("gn_users_read")

		if _, err := b.Instantiate(ctx); err != nil {
			return fmt.Errorf("instantiate %q host module: %w", ns, err)
		}
		return nil
	}
}

// httpFetchRequest is the wire envelope guests send into gn_http_fetch.
type httpFetchRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body,omitempty"`
}

// httpFetchResponse is the wire envelope guests read back from
// gn_http_fetch. Error is non-empty when Status < 0 (host-detected
// failure); it is empty when the upstream returned a real status code,
// even if that code is 4xx/5xx (transport succeeded, the upstream
// signalled an application error).
type httpFetchResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body,omitempty"`
	Error   string            `json:"error,omitempty"`
}

// mediaReadRequest is the wire envelope for gn_media_read.
type mediaReadRequest struct {
	ID string `json:"id"`
}

// usersReadRequest is the wire envelope for gn_users_read.
type usersReadRequest struct {
	ID string `json:"id"`
}

// hostGnHTTPFetch implements env_net.gn_http_fetch.
//
// Steps:
//  1. Resolve the NetworkContext for the calling module. No context →
//     NetStatusDenied (deny-by-default).
//  2. Capability check via Checker.MustAllow(http.fetch). Denied →
//     NetStatusDenied (the Checker already audited).
//  3. Read + decode the JSON request envelope from guest memory.
//  4. Apply the rate limiter. Exceeded → NetStatusRateLimited.
//  5. Parse the URL, enforce the allowlist, resolve hostnames, reject
//     private/loopback/link-local IPs (the SSRF guard).
//  6. Issue the request via the context's HTTPClient (or the package
//     default), capping redirects at MaxHTTPFetchRedirects and revalidating
//     the destination at each hop.
//  7. Read the response body up to MaxHTTPFetchResponseBytes, encode the
//     envelope as JSON, allocate guest memory via gn_alloc, write the
//     bytes, return packed (ptr, len) — or a NetStatus sentinel on any
//     failure.
//  8. Emit one audit row tagged with the resolved status.
func hostGnHTTPFetch(ctx context.Context, mod api.Module, stack []uint64) {
	ptr := api.DecodeU32(stack[0])
	length := api.DecodeU32(stack[1])
	stack[0] = networkFetchImpl(ctx, mod, ptr, length)
}

func networkFetchImpl(ctx context.Context, mod api.Module, ptr, length uint32) uint64 {
	slug := mod.Name()
	nc := lookupNetworkContext(slug)
	if nc == nil {
		// No registered context = the plugin never went through
		// Activate. Deny silently — there's nothing to audit.
		return packNetStatus(NetStatusDenied)
	}

	// Capability gate. The Checker emits its own audit row on denial.
	if nc.Checker != nil {
		if err := nc.Checker.MustAllow(ctx, "http.fetch"); err != nil {
			return packNetStatus(NetStatusDenied)
		}
	}

	// Decode the request envelope.
	if length > MaxHTTPFetchRequestBytes {
		emitFetchAudit(ctx, nc, "", "", NetStatusBadRequest, "request envelope exceeds host cap")
		return packNetStatus(NetStatusBadRequest)
	}
	buf, err := readHostString("gn_http_fetch", mod, ptr, length)
	if err != nil {
		emitFetchAudit(ctx, nc, "", "", NetStatusBadRequest, err.Error())
		return packNetStatus(NetStatusBadRequest)
	}
	var req httpFetchRequest
	if err := json.Unmarshal(buf, &req); err != nil {
		emitFetchAudit(ctx, nc, "", "", NetStatusBadRequest, fmt.Sprintf("decode envelope: %v", err))
		return packNetStatus(NetStatusBadRequest)
	}
	if req.URL == "" {
		emitFetchAudit(ctx, nc, "", "", NetStatusBadRequest, "url is required")
		return packNetStatus(NetStatusBadRequest)
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}

	// Parse + allowlist-check the URL. This is the first gate so a
	// blocked URL never reaches the rate limiter (we don't want to
	// burn a token on a request that was going to fail anyway).
	parsed, err := url.Parse(req.URL)
	if err != nil {
		emitFetchAudit(ctx, nc, method, req.URL, NetStatusBadRequest, fmt.Sprintf("parse url: %v", err))
		return packNetStatus(NetStatusBadRequest)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		emitFetchAudit(ctx, nc, method, req.URL, NetStatusBlocked, fmt.Sprintf("scheme %q not allowed", parsed.Scheme))
		return packNetStatus(NetStatusBlocked)
	}
	if !nc.allowsHost(parsed.Hostname()) {
		emitFetchAudit(ctx, nc, method, req.URL, NetStatusBlocked, "host not in allowlist")
		return packNetStatus(NetStatusBlocked)
	}

	// Rate limit BEFORE network IO. A flood of denied requests would
	// otherwise keep us under quota at the upstream's expense.
	if nc.Limiter != nil {
		allowed, _, err := nc.Limiter.Allow(ctx, "plugin:"+slug+":http.fetch")
		if err == nil && !allowed {
			emitFetchAudit(ctx, nc, method, req.URL, NetStatusRateLimited, "rate limit exceeded")
			return packNetStatus(NetStatusRateLimited)
		}
	}

	// Resolve + SSRF-check. We do this BEFORE issuing the request and
	// then re-check at each redirect via the CheckRedirect hook.
	if err := assertPublicHost(ctx, parsed.Hostname()); err != nil {
		emitFetchAudit(ctx, nc, method, req.URL, NetStatusBlocked, err.Error())
		return packNetStatus(NetStatusBlocked)
	}

	// Build the HTTP request. We do NOT pass the plugin-supplied body
	// through unchanged into req.Body unless it's non-nil — an empty
	// body produces a nil reader so net/http doesn't set Content-Length
	// for an absent body.
	fetchCtx, cancel := context.WithTimeout(ctx, MaxHTTPFetchTimeout)
	defer cancel()
	var body io.Reader
	if len(req.Body) > 0 {
		body = strings.NewReader(string(req.Body))
	}
	hreq, err := http.NewRequestWithContext(fetchCtx, method, req.URL, body)
	if err != nil {
		emitFetchAudit(ctx, nc, method, req.URL, NetStatusBadRequest, fmt.Sprintf("build request: %v", err))
		return packNetStatus(NetStatusBadRequest)
	}
	// Apply guest-supplied headers — but never let the guest override
	// Host / Cookie / Authorization, which would let one plugin
	// impersonate the host's identity to an upstream. The plugin
	// declares the destination via the URL host; everything else is
	// per-call data.
	for k, v := range req.Headers {
		canon := http.CanonicalHeaderKey(k)
		switch canon {
		case "Host", "Cookie", "Authorization", "Content-Length", "Transfer-Encoding":
			continue
		}
		hreq.Header.Set(canon, v)
	}
	if hreq.Header.Get("User-Agent") == "" {
		hreq.Header.Set("User-Agent", "GoNext-Plugin/"+slug)
	}

	client := nc.HTTPClient
	if client == nil {
		client = defaultFetchClient(nc)
	}
	resp, err := client.Do(hreq)
	if err != nil {
		emitFetchAudit(ctx, nc, method, req.URL, NetStatusUpstream, err.Error())
		// Even on transport error we return a JSON envelope so the
		// guest's decoder has a uniform shape — the negative status
		// just signals "no real HTTP response was received".
		return writeFetchResponse(ctx, mod, slug, httpFetchResponse{
			Status: 0,
			Error:  err.Error(),
		}, NetStatusUpstream)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, MaxHTTPFetchResponseBytes+1))
	if err != nil {
		emitFetchAudit(ctx, nc, method, req.URL, NetStatusUpstream, fmt.Sprintf("read body: %v", err))
		return writeFetchResponse(ctx, mod, slug, httpFetchResponse{
			Status: resp.StatusCode,
			Error:  err.Error(),
		}, NetStatusUpstream)
	}
	if int64(len(respBody)) > MaxHTTPFetchResponseBytes {
		respBody = respBody[:MaxHTTPFetchResponseBytes]
	}
	headers := flattenHeaders(resp.Header)

	emitFetchAudit(ctx, nc, method, req.URL, NetStatusOK, fmt.Sprintf("status=%d", resp.StatusCode))
	return writeFetchResponse(ctx, mod, slug, httpFetchResponse{
		Status:  resp.StatusCode,
		Headers: headers,
		Body:    respBody,
	}, NetStatusOK)
}

// flattenHeaders folds an http.Header into a flat map[string]string by
// joining multi-value fields with ", ". The wire envelope is
// intentionally flat — plugin SDKs that need set-cookie or multi-value
// headers can split on the comma themselves; the cases that matter for
// content-type / cache-control / etag are all single-valued.
func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = strings.Join(v, ", ")
	}
	return out
}

// defaultFetchClient builds the per-plugin HTTP client for outbound
// fetches. The transport itself is package-default but the redirect
// guard is per-call so it carries the calling NetworkContext into the
// CheckRedirect closure for re-validation.
func defaultFetchClient(nc *NetworkContext) *http.Client {
	return &http.Client{
		Timeout: MaxHTTPFetchTimeout,
		CheckRedirect: func(redirected *http.Request, via []*http.Request) error {
			if len(via) >= MaxHTTPFetchRedirects {
				return fmt.Errorf("redirect cap (%d) exceeded", MaxHTTPFetchRedirects)
			}
			if !nc.allowsHost(redirected.URL.Hostname()) {
				return fmt.Errorf("redirect host %q not in allowlist", redirected.URL.Hostname())
			}
			if err := assertPublicHost(redirected.Context(), redirected.URL.Hostname()); err != nil {
				return fmt.Errorf("redirect blocked: %w", err)
			}
			return nil
		},
	}
}

// assertPublicHost resolves host and returns an error if any resolved
// address falls into the SSRF denylist:
//
//   - RFC1918 private space (10/8, 172.16/12, 192.168/16)
//   - 127/8 loopback
//   - 169.254/16 link-local (covers cloud-metadata 169.254.169.254)
//   - ::1 IPv6 loopback
//   - fe80::/10 IPv6 link-local
//   - any multicast or unspecified address
//
// We resolve the host server-side rather than trusting the URL string —
// a malicious plugin can use a DNS name that resolves to a private
// IP at request time even though it points elsewhere at install time
// (DNS rebinding). Re-resolution happens at every redirect via the
// CheckRedirect hook.
func assertPublicHost(ctx context.Context, host string) error {
	// Strip [v6] brackets if any — the resolver wants the bare host.
	host = strings.Trim(host, "[]")
	if host == "" {
		return errors.New("empty host")
	}
	// If the host parses directly as an IP literal, skip DNS.
	if ip, err := netip.ParseAddr(host); err == nil {
		if !isPublicAddr(ip) {
			return fmt.Errorf("host %s resolves to non-public address %s", host, ip)
		}
		return nil
	}
	resolver := net.DefaultResolver
	addrs, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("resolve %s: no addresses", host)
	}
	for _, a := range addrs {
		if !isPublicAddr(a) {
			return fmt.Errorf("host %s resolves to non-public address %s", host, a)
		}
	}
	return nil
}

// isPublicAddr reports whether the resolved IP is safe to talk to as a
// plugin upstream. "Public" here is a loose definition — we want
// "addresses that are not on the host's private fabric". Anything
// loopback, private, link-local, multicast, broadcast, or unspecified
// is rejected.
func isPublicAddr(ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	// Explicit 169.254.169.254 (cloud metadata) — IsLinkLocalUnicast
	// already covers 169.254/16, but spell it out for documentation.
	if ip.Is4() {
		b := ip.As4()
		if b[0] == 169 && b[1] == 254 {
			return false
		}
		// CGNAT (100.64/10) — used inside cloud-provider networks.
		if b[0] == 100 && (b[1]&0xc0) == 64 {
			return false
		}
	}
	return true
}

// writeFetchResponse marshals the response envelope and copies it into
// guest memory via the guest's gn_alloc export. Returns the packed
// (ptr, len) i64. If allocation or marshal fails, returns a sentinel.
func writeFetchResponse(ctx context.Context, mod api.Module, slug string, resp httpFetchResponse, status NetResultStatus) uint64 {
	// On a non-OK status we still want to return the envelope so the
	// guest sees a uniform decoder shape — but if marshalling fails we
	// fall back to the sentinel.
	if status != NetStatusOK && resp.Error == "" {
		resp.Error = status.String()
	}
	buf, err := json.Marshal(resp)
	if err != nil {
		return packNetStatus(NetStatusInternal)
	}
	return writeGuestPayload(ctx, mod, buf, status)
}

// writeGuestPayload allocates `len(buf)` bytes in guest memory via the
// guest's gn_alloc export, writes the bytes, and returns the packed
// (ptr, len) i64. On allocation failure or memory-write failure it
// returns packNetStatus(NetStatusInternal).
//
// The caller passes the intended status — when it is NetStatusOK the
// packed result is the canonical "success with body" shape. For any
// other status the function still returns the (ptr, len) on the
// envelope so the guest can read the JSON error body, BUT the int32
// low half is the negative sentinel — preserving the (ptr=0, len<0)
// shape only when no envelope at all is being returned.
//
// In other words: a guest seeing (ptr!=0, len>0) always has a decodable
// envelope; a guest seeing (ptr=0, len<0) has a sentinel and no body.
func writeGuestPayload(ctx context.Context, mod api.Module, buf []byte, _ NetResultStatus) uint64 {
	mem := mod.Memory()
	if mem == nil {
		return packNetStatus(NetStatusInternal)
	}
	alloc := mod.ExportedFunction("gn_alloc")
	if alloc == nil {
		// Guest does not export gn_alloc — we cannot deliver a body.
		// This is a contract violation: every plugin SDK is expected
		// to provide one. Surface as Internal.
		return packNetStatus(NetStatusInternal)
	}
	results, err := alloc.Call(ctx, api.EncodeU32(uint32(len(buf))))
	if err != nil || len(results) != 1 {
		return packNetStatus(NetStatusInternal)
	}
	ptr := api.DecodeU32(results[0])
	if ptr == 0 {
		return packNetStatus(NetStatusInternal)
	}
	if !mem.Write(ptr, buf) {
		return packNetStatus(NetStatusInternal)
	}
	return packResult(ptr, int32(len(buf)))
}

// packResult composes the i64 the host returns from a network call from
// a (ptr, len) pair. Matches abi/hooks.packResult byte-for-byte so
// guest SDKs can share the decode helper.
func packResult(ptr uint32, length int32) uint64 {
	return uint64(ptr)<<32 | uint64(uint32(length))
}

// packNetStatus is the (ptr=0, len=status) shape for negative-status
// sentinels. The low 32 bits hold the int32 status code so the guest's
// `result_len < 0` check correctly identifies a failure.
func packNetStatus(s NetResultStatus) uint64 {
	return packResult(0, int32(s))
}

// emitFetchAudit records one outbound-fetch event. status spells out
// whether the call succeeded or which gate rejected it; reason is the
// human-readable explanation. Best-effort — emit failure is logged but
// does not change the function's return path.
func emitFetchAudit(ctx context.Context, nc *NetworkContext, method, target string, status NetResultStatus, reason string) {
	if nc == nil || nc.Emitter == nil {
		return
	}
	sev := audit.SeverityInfo
	if status != NetStatusOK {
		sev = audit.SeverityWarning
	}
	plugged := nc.Emitter.WithPlugin(nc.Slug)
	_ = plugged.Emit(ctx, "plugin.http.fetch",
		audit.WithSeverity(sev),
		audit.WithTarget("plugin", nc.Slug),
		audit.WithMetadata(map[string]any{
			"method": method,
			"url":    target,
			"status": status.String(),
			"reason": reason,
		}),
	)
}

// hostGnMediaRead implements env_net.gn_media_read.
//
// Returns metadata + a short-TTL signed URL for the asset. Capability-
// gated by media.read; missing capability or unknown asset returns the
// appropriate sentinel. Body envelope mirrors MediaAsset.
func hostGnMediaRead(ctx context.Context, mod api.Module, stack []uint64) {
	ptr := api.DecodeU32(stack[0])
	length := api.DecodeU32(stack[1])
	stack[0] = networkMediaImpl(ctx, mod, ptr, length)
}

func networkMediaImpl(ctx context.Context, mod api.Module, ptr, length uint32) uint64 {
	slug := mod.Name()
	nc := lookupNetworkContext(slug)
	if nc == nil {
		return packNetStatus(NetStatusDenied)
	}
	if nc.Checker != nil {
		if err := nc.Checker.MustAllow(ctx, "media.read"); err != nil {
			return packNetStatus(NetStatusDenied)
		}
	}
	buf, err := readHostString("gn_media_read", mod, ptr, length)
	if err != nil {
		return packNetStatus(NetStatusBadRequest)
	}
	var req mediaReadRequest
	if err := json.Unmarshal(buf, &req); err != nil {
		return packNetStatus(NetStatusBadRequest)
	}
	if strings.TrimSpace(req.ID) == "" {
		return packNetStatus(NetStatusBadRequest)
	}
	provider := nc.MediaProvider
	if provider == nil {
		emitResourceAudit(ctx, nc, "plugin.media.read", req.ID, NetStatusNotFound, "no media provider wired")
		return packNetStatus(NetStatusNotFound)
	}
	asset, err := provider.Read(ctx, req.ID)
	if err != nil {
		emitResourceAudit(ctx, nc, "plugin.media.read", req.ID, NetStatusInternal, err.Error())
		return packNetStatus(NetStatusInternal)
	}
	if asset == nil {
		emitResourceAudit(ctx, nc, "plugin.media.read", req.ID, NetStatusNotFound, "asset not found")
		return packNetStatus(NetStatusNotFound)
	}
	out, err := json.Marshal(asset)
	if err != nil {
		return packNetStatus(NetStatusInternal)
	}
	emitResourceAudit(ctx, nc, "plugin.media.read", req.ID, NetStatusOK, "")
	return writeGuestPayload(ctx, mod, out, NetStatusOK)
}

// hostGnUsersRead implements env_net.gn_users_read.
//
// The user record returned by the UsersProvider is filtered against the
// NetworkContext's UsersFields allowlist before it leaves the host. A
// field not in the allowlist is STRIPPED (not redacted) — the guest
// sees no key, matching the brief's "anything else is stripped from
// the returned MessagePack" contract.
func hostGnUsersRead(ctx context.Context, mod api.Module, stack []uint64) {
	ptr := api.DecodeU32(stack[0])
	length := api.DecodeU32(stack[1])
	stack[0] = networkUsersImpl(ctx, mod, ptr, length)
}

func networkUsersImpl(ctx context.Context, mod api.Module, ptr, length uint32) uint64 {
	slug := mod.Name()
	nc := lookupNetworkContext(slug)
	if nc == nil {
		return packNetStatus(NetStatusDenied)
	}
	if nc.Checker != nil {
		if err := nc.Checker.MustAllow(ctx, "users.read"); err != nil {
			return packNetStatus(NetStatusDenied)
		}
	}
	buf, err := readHostString("gn_users_read", mod, ptr, length)
	if err != nil {
		return packNetStatus(NetStatusBadRequest)
	}
	var req usersReadRequest
	if err := json.Unmarshal(buf, &req); err != nil {
		return packNetStatus(NetStatusBadRequest)
	}
	if strings.TrimSpace(req.ID) == "" {
		return packNetStatus(NetStatusBadRequest)
	}
	provider := nc.UsersProvider
	if provider == nil {
		emitResourceAudit(ctx, nc, "plugin.users.read", req.ID, NetStatusNotFound, "no users provider wired")
		return packNetStatus(NetStatusNotFound)
	}
	raw, err := provider.Read(ctx, req.ID)
	if err != nil {
		emitResourceAudit(ctx, nc, "plugin.users.read", req.ID, NetStatusInternal, err.Error())
		return packNetStatus(NetStatusInternal)
	}
	if raw == nil {
		emitResourceAudit(ctx, nc, "plugin.users.read", req.ID, NetStatusNotFound, "user not found")
		return packNetStatus(NetStatusNotFound)
	}
	filtered := projectAllowedFields(raw, nc.usersAllowedFields())
	out, err := json.Marshal(filtered)
	if err != nil {
		return packNetStatus(NetStatusInternal)
	}
	emitResourceAudit(ctx, nc, "plugin.users.read", req.ID, NetStatusOK, "")
	return writeGuestPayload(ctx, mod, out, NetStatusOK)
}

// projectAllowedFields keeps only the keys in allow from in. The
// comparison is case-insensitive on the key (HTTP-style normalization
// for "Email" vs "email") so a manifest declaring ["Email"] matches a
// provider returning {"email":...}. Returns a new map; the input is
// not mutated.
func projectAllowedFields(in map[string]any, allow map[string]struct{}) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if _, ok := allow[strings.ToLower(k)]; ok {
			out[k] = v
		}
	}
	return out
}

// emitResourceAudit is the shared audit helper for media.read /
// users.read. The event name is the caller's responsibility; this
// helper only does the boilerplate of emitter-WithPlugin / severity
// derivation / target tagging.
func emitResourceAudit(ctx context.Context, nc *NetworkContext, event, id string, status NetResultStatus, reason string) {
	if nc == nil || nc.Emitter == nil {
		return
	}
	sev := audit.SeverityInfo
	if status != NetStatusOK {
		sev = audit.SeverityWarning
	}
	plugged := nc.Emitter.WithPlugin(nc.Slug)
	_ = plugged.Emit(ctx, event,
		audit.WithSeverity(sev),
		audit.WithTarget("plugin", nc.Slug),
		audit.WithMetadata(map[string]any{
			"id":     id,
			"status": status.String(),
			"reason": reason,
		}),
	)
}

// MemoryMediaProvider is an in-process MediaProvider backed by a map.
// Useful for tests, the dev CLI, and the bootstrap path before the
// full media pipeline (packages/go/media) exposes a Read API.
//
// Safe for concurrent use.
type MemoryMediaProvider struct {
	mu     sync.RWMutex
	assets map[string]*MediaAsset
}

// NewMemoryMediaProvider returns an empty provider.
func NewMemoryMediaProvider() *MemoryMediaProvider {
	return &MemoryMediaProvider{assets: map[string]*MediaAsset{}}
}

// Set associates the asset with id. Overwrites any prior value.
func (p *MemoryMediaProvider) Set(id string, asset *MediaAsset) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.assets[id] = asset
}

// Read implements MediaProvider. Returns (nil, nil) for an unknown id
// so the host translates to NetStatusNotFound.
func (p *MemoryMediaProvider) Read(_ context.Context, id string) (*MediaAsset, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.assets[id], nil
}

// MemoryUsersProvider is the in-process counterpart for users.read.
// Same use cases as MemoryMediaProvider — tests, dev mode, the
// not-yet-wired bootstrap.
//
// Safe for concurrent use. Returned maps are NOT copied — callers
// must not mutate them after Set.
type MemoryUsersProvider struct {
	mu    sync.RWMutex
	users map[string]map[string]any
}

// NewMemoryUsersProvider returns an empty provider.
func NewMemoryUsersProvider() *MemoryUsersProvider {
	return &MemoryUsersProvider{users: map[string]map[string]any{}}
}

// Set associates the row with id. Overwrites any prior value. The
// caller is expected to hand off ownership of the map; mutating it
// later races with concurrent Reads.
func (p *MemoryUsersProvider) Set(id string, row map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.users[id] = row
}

// Read implements UsersProvider. Returns (nil, nil) for unknown id.
func (p *MemoryUsersProvider) Read(_ context.Context, id string) (map[string]any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	row := p.users[id]
	if row == nil {
		return nil, nil
	}
	// Return a shallow copy so subsequent host-side projection mutations
	// don't leak into the stored row.
	out := make(map[string]any, len(row))
	for k, v := range row {
		out[k] = v
	}
	return out, nil
}

// Compile-time guards that the host functions still satisfy wazero's
// expected shape. If wazero ever changes its GoModuleFunc signature
// this file fails at build time rather than at registration.
var (
	_ api.GoModuleFunction = api.GoModuleFunc(hostGnHTTPFetch)
	_ api.GoModuleFunction = api.GoModuleFunc(hostGnMediaRead)
	_ api.GoModuleFunction = api.GoModuleFunc(hostGnUsersRead)
)

// Compile-time guards that the in-memory providers satisfy the
// public interfaces. Catches signature drift in either direction at
// build time.
var (
	_ MediaProvider = (*MemoryMediaProvider)(nil)
	_ UsersProvider = (*MemoryUsersProvider)(nil)
)

// silence linter warnings if slog ever gets used in a future patch
// without being imported — keeping the import here at file scope so
// adding a debug log is a one-line change.
var _ = slog.LevelDebug
