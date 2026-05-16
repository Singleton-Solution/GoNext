# 13 — Security Baseline

> Owns: project-wide security posture beyond authentication. HTTP headers, CSP, XSS pipeline, secret management, supply chain, SSRF, sandboxing tradeoffs, incident response. Reader: a senior security engineer evaluating whether to deploy this stack.
>
> Authentication, authorization, sessions, passkeys, password hashing, admin CSRF: owned by [`06-auth-permissions.md`](06-auth-permissions.md). This doc references but does not re-spec those concerns.
>
> Plugin sandbox (WASM) details: owned by [`02-plugin-system.md`](02-plugin-system.md). Theme trust model is shared between this doc and [`03-theme-system.md`](03-theme-system.md).

---

## 0. Reading order & scope summary

| Concern | Owned by | This doc says |
|---|---|---|
| Password hashing, sessions, MFA, RBAC | 06 | references; pepper is a system secret managed here |
| Plugin sandbox, WASM limits, per-plugin caps | 02 | references; adds infra-level (signing, dependency hygiene) |
| Theme runtime model | 03 | references; adds signing + the explicit "themes are not sandboxed" trust statement |
| Media upload validation, content sniffing | 07 | references |
| Migration importer threat surface | 08 | references; importer is a privileged surgery tool |
| HTTP headers, CSP, SSRF, secret store, supply chain, IR | **13 (this doc)** | the canonical source |
| Testing of security controls | 11 | forward-references — security tests live here |

Open the gap review (`09-review-gaps.md` §A7) for the original prompt that scoped this document.

---

## 1. Threat model

This is the project-level threat model. Subsystem docs scope to their own surfaces; this is the union.

### 1.1 Attacker classes

| # | Class | Position | What they want | Primary mitigations | Doc |
|---|---|---|---|---|---|
| T1 | Unauthenticated internet | Public HTTP, no creds | Web app vulns (XSS/CSRF/SSRF/RCE), credential stuffing, DoS, content scraping at abusive rates | Headers, CSP, input validation, rate limits, WAF rules, bot defense | this doc + 06 §rate-limit |
| T2 | Authenticated low-privilege (subscriber, commenter) | Valid session, low caps | Privilege escalation, stored XSS via comments, SSRF via profile fields, abuse of UGC primitives | Sanitization pipeline, capability checks, comment moderation defaults | 06 + this doc §4 |
| T3 | Authenticated author/editor | Valid session, content caps | Stored XSS in posts, abuse of `unfiltered_html`, embedding malicious media, plugin/theme misconfig via admin UI | Default-strict sanitization, `unfiltered_html` is super-admin gated, audit log | this doc §4 + 06 |
| T4 | Malicious plugin author | Code installed by site admin | Escape sandbox, exfil data via host ABI, lateral DB access, SSRF via outbound HTTP cap, supply-chain push | WASM isolation (02), capability gates, signing, outbound HTTP guard, secrets opaqueness | 02 (primary) + this doc §3.3, §7, §9 |
| T5 | Malicious theme author | Code installed by admin, runs in Next.js Node process | Arbitrary JS in render path, leak server env, SSR-side SSRF, supply-chain via npm | Signing + install-time review + strict CSP for themes' bundles + bounded Next.js process role | this doc §3.3, §14 + 03 |
| T6 | Insider with admin access | Legitimate super-admin / staff | Data exfil, impersonation, audit erasure | Append-only audit log, 4-eyes on dangerous ops, secret-store ACLs, tamper-evident logs | 06 (primary) + this doc §12 |
| T7 | Supply chain | Compromised dependency or build pipeline | Implanted code in core/plugin/theme/dep | Signing, SBOMs, vuln scanning, pinned digests, separate build identity, reproducible builds (aspirational) | this doc §7 |
| T8 | Network adversary | On-path or DNS | Session theft, downgrade, rebinding, exfil | HSTS preload, TLS 1.3, OCSP-must-staple (aspirational), DNS-rebinding guard in egress | this doc §2, §9 |
| T9 | Compromised browser/extension on author | Authenticated browser is hostile | Token theft, action on behalf | Short cookie lifetimes, step-up for dangerous ops, no localStorage tokens for admin | 06 |

### 1.2 Trust boundaries (one-page)

```
   Browser  ─────┐
                 │   TLS, HSTS preload
                 ▼
       ┌──────────────────┐
       │  Edge / CDN      │  rate limits, TLS termination, ACL,
       │  (out of scope   │  bot rules; not relied on for AuthZ
       │   of source)     │
       └────────┬─────────┘
                │ HTTPS (mTLS optional in §2)
                ▼
   ┌──────────────────────────────────────────────────────┐
   │                Next.js Node (public + admin)         │
   │  Untrusted code in this process:                     │
   │    - Themes (TRUST: signed + install review)         │
   │    - Block editor 3rd-party blocks (signed)          │
   │  Sanitized output before send                         │
   └────────┬─────────────────────────────────────────────┘
            │ REST / GraphQL (token auth)
            ▼
   ┌──────────────────────────────────────────────────────┐
   │                Go API server                         │
   │  Trust boundary: untrusted code in WASM (plugins)    │
   │  Sandboxed via wazero with capability ABI            │
   │  Hardened outbound HTTP (§9) for any net I/O         │
   └────────┬─────────────────────────────────────────────┘
            │
            ▼
   ┌─────────────┐  ┌─────────────┐  ┌─────────────┐
   │  Postgres   │  │  Redis      │  │  S3 / media │
   │  TLS req    │  │  TLS req    │  │  signed URLs│
   └─────────────┘  └─────────────┘  └─────────────┘
```

### 1.3 What is explicitly *out of scope*

- DDoS at L3/L4 — delegated to edge provider.
- Physical access to hosts — operator responsibility.
- Chosen-side-channel attacks on the host kernel — not in v1 threat model.
- 100% PII-free logs — we redact aggressively but assume logs are a sensitive store.

---

## 2. HTTP security headers

The canonical header set, applied by a single middleware (`pkg/security/headers`). All responses get the **base set**; per-route-class overrides extend it.

### 2.1 Header matrix

Route classes:

- **Public** — `/`, `/posts/...`, theme-rendered pages.
- **Admin** — `/admin/...` (block editor, settings).
- **REST** — `/api/v1/...`, machine-consumed JSON.
- **Plugin frontend** — `/plugin-assets/{pluginId}/...` (plugin-shipped ES modules / CSS / images).
- **Media** — `/media/...` (uploaded user content; usually CDN-fronted).
- **Reporting** — `/_/csp-report`, `/_/nel`.

| Header | Public | Admin | REST | Plugin frontend | Media |
|---|---|---|---|---|---|
| `Strict-Transport-Security` | `max-age=63072000; includeSubDomains; preload` | same | same | same | same |
| `Content-Security-Policy` | site policy (§3.1) | admin policy (§3.2) | `default-src 'none'; frame-ancestors 'none'` | plugin policy (§3.3) | `default-src 'none'; sandbox` |
| `X-Content-Type-Options` | `nosniff` | `nosniff` | `nosniff` | `nosniff` | `nosniff` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | `same-origin` | `no-referrer` | `same-origin` | `no-referrer` |
| `Permissions-Policy` | deny-by-default (§2.2) | deny-by-default minus admin needs | `interest-cohort=()` | extra-strict | `interest-cohort=()` |
| `Cross-Origin-Opener-Policy` | `same-origin` | `same-origin` | n/a | `same-origin` | n/a |
| `Cross-Origin-Embedder-Policy` | `credentialless` | `require-corp` | n/a | `require-corp` | n/a |
| `Cross-Origin-Resource-Policy` | `same-site` (override `cross-origin` for media URLs) | `same-origin` | `same-origin` | `same-site` | `cross-origin` |
| `X-Frame-Options` | `SAMEORIGIN` | `DENY` | n/a | `DENY` | `DENY` |
| `Cache-Control` | route-defined | `private, no-store` | route-defined | `public, max-age=31536000, immutable` (hashed paths) | route-defined |
| `Server` | omitted | omitted | omitted | omitted | omitted |
| `X-Permitted-Cross-Domain-Policies` | `none` | `none` | `none` | `none` | `none` |
| `Origin-Agent-Cluster` | `?1` | `?1` | n/a | `?1` | n/a |

#### Notes per header

- **HSTS preload**: 2-year `max-age` with `preload`. Submission to the Chromium preload list is a deployment gate. Subdomain coverage means `wildcard` cert + every staging/dev subdomain must be HTTPS.
- **COEP**: `require-corp` breaks many third-party embeds (oEmbed, ad networks). On the public site we ship `credentialless` so cross-origin resources load without credentials but still need CORP — see §2.4 tradeoff.
- **CORP for media**: only `/media/...` is `cross-origin` because we want third-party sites (and our own CDN-fronted domain) to fetch images. The signed-URL system in doc 07 enforces auth.
- **`Server`**: we strip it; no fingerprintable banner.

### 2.2 Permissions-Policy default

A deny-by-default policy applied to public/admin/plugin routes:

```
accelerometer=(), ambient-light-sensor=(), autoplay=(), battery=(),
camera=(), cross-origin-isolated=(), display-capture=(), document-domain=(),
encrypted-media=(), execution-while-not-rendered=(),
execution-while-out-of-viewport=(), fullscreen=(self), geolocation=(),
gyroscope=(), keyboard-map=(), magnetometer=(), microphone=(),
midi=(), navigation-override=(), payment=(), picture-in-picture=(),
publickey-credentials-get=(self), screen-wake-lock=(), sync-xhr=(),
usb=(), web-share=(), xr-spatial-tracking=(), clipboard-read=(self),
clipboard-write=(self), interest-cohort=()
```

A theme may request `geolocation`, `fullscreen`, or `camera` via `theme.json` `permissions[]`; admin sees a warning before activation. Plugin frontends *cannot* extend this policy.

### 2.3 Go middleware sketch

```go
// pkg/security/headers/middleware.go
package headers

import (
	"net/http"
	"strings"
)

type RouteClass int

const (
	ClassPublic RouteClass = iota
	ClassAdmin
	ClassREST
	ClassPluginFrontend
	ClassMedia
	ClassReporting
)

type Policy struct {
	HSTS               string
	CSP                string // built per-request to bind a nonce
	XContentType       string
	ReferrerPolicy     string
	PermissionsPolicy  string
	COOP, COEP, CORP   string
	XFO                string
	CacheControl       string
}

// NoncedCSP receives the per-request nonce (see §3) and substitutes it.
type CSPBuilder func(nonce string) string

type Middleware struct {
	base   Policy
	byClass map[RouteClass]Policy
	csp     map[RouteClass]CSPBuilder
	classify func(*http.Request) RouteClass
	nonceFn  func() string // crypto/rand 16 bytes, base64 url-safe
}

func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		class := m.classify(r)
		nonce := m.nonceFn()
		// expose nonce to handler chain (Next.js reads via X-Script-Nonce trailer)
		r = r.WithContext(WithNonce(r.Context(), nonce))

		h := w.Header()
		set(h, "Strict-Transport-Security", m.base.HSTS)
		set(h, "X-Content-Type-Options", "nosniff")
		setIf(h, "Cross-Origin-Opener-Policy", m.byClass[class].COOP)
		setIf(h, "Cross-Origin-Embedder-Policy", m.byClass[class].COEP)
		setIf(h, "Cross-Origin-Resource-Policy", m.byClass[class].CORP)
		setIf(h, "Referrer-Policy", m.byClass[class].ReferrerPolicy)
		setIf(h, "Permissions-Policy", m.byClass[class].PermissionsPolicy)
		setIf(h, "X-Frame-Options", m.byClass[class].XFO)
		if b, ok := m.csp[class]; ok {
			set(h, "Content-Security-Policy", b(nonce))
		}
		// pass nonce to upstream Next.js for inline tags in SSR HTML
		set(h, "X-Script-Nonce", nonce)

		// strip fingerprints
		h.Del("Server")
		h.Del("X-Powered-By")

		next.ServeHTTP(w, r)
	})
}

func set(h http.Header, k, v string)   { h.Set(k, v) }
func setIf(h http.Header, k, v string) { if strings.TrimSpace(v) != "" { h.Set(k, v) } }
```

The Next.js public/admin servers run *behind* this middleware via reverse proxy. The nonce is delivered to Next.js via `X-Script-Nonce` request header (Go → Next as a proxy header) and Next.js threads it into `<script nonce>` / `<style nonce>` attributes during render. Next.js *does not* generate the nonce itself — single source of truth in Go.

### 2.4 Tradeoffs & rejected alternatives (headers)

- **`COEP: require-corp` on public site**: rejected for v1. Many oEmbed / video / ad providers don't ship CORP headers and would silently break. `credentialless` keeps cross-origin isolation off but blocks credentialed cross-origin loads — acceptable for blogs/sites. Admin gets `require-corp` because we control all loaded resources there. Revisit when ecosystem catches up.
- **`Clear-Site-Data` on logout**: enabled — see 06. Listed here for completeness.
- **NEL (Network Error Logging) / Report-To**: enabled in `report-only` mode for 90 days post-launch to collect baseline; only collected on first-party origin. See §12.
- **`X-Frame-Options: DENY` *and* `frame-ancestors`**: we set both. XFO is legacy but still parsed by old proxies; CSP `frame-ancestors` is authoritative.
- **Removing `Server`**: rejected the alternative of setting `Server: nginx` as decoy — defenders shouldn't lie because operators end up debugging it.

---

## 3. CSP for themes and plugins

CSP is the single most load-bearing header. We split it into three policies because the trust model differs by route class.

### 3.1 Public site (theme-rendered) CSP

```
default-src 'self';
script-src 'self' 'nonce-{NONCE}' 'strict-dynamic';
style-src 'self' 'nonce-{NONCE}';
img-src 'self' data: https://media.{site};
font-src 'self' data:;
connect-src 'self' https://media.{site};
frame-src 'self' {oembed_hosts};
media-src 'self' https://media.{site};
object-src 'none';
base-uri 'self';
form-action 'self';
frame-ancestors 'self';
worker-src 'self';
manifest-src 'self';
upgrade-insecure-requests;
report-uri /_/csp-report;
report-to default;
```

Key choices:

- **`'strict-dynamic'`**: combined with nonce-based `script-src`. Means once a nonced script loads, it can load more — modern best practice, eliminates the `'unsafe-inline'` need.
- **No `'unsafe-inline'`, no `'unsafe-eval'`**. Themes have to ship inline-free. Next.js generates inline hydration JSON via nonced `<script type="application/json">` blocks.
- **`{oembed_hosts}`**: a curated allowlist of providers (YouTube, Vimeo, Twitter, etc.). Plugins can request additions via manifest, admin reviews on install.
- **`upgrade-insecure-requests`**: mixed-content auto-upgrade.

#### Themes adding sources via `theme.json`

Themes may declare extra hosts:

```jsonc
// theme.json (excerpt)
{
  "csp": {
    "scriptSrc": [],                          // empty: theme can't add JS hosts
    "styleSrc":  ["https://fonts.googleapis.com"],
    "fontSrc":   ["https://fonts.gstatic.com"],
    "imgSrc":    ["https://cdn.example.com"],
    "connectSrc":["https://api.example.com"]
  }
}
```

Rules (enforced by theme validator at install time):

- `scriptSrc` from themes is **rejected** — themes must use the Next.js bundles we generate. (A theme that needs a third-party `<script>` must request it via a plugin instead, which goes through a more formal review.)
- Other directives: an admin warning lists each requested host, asks explicit confirmation.
- Wildcards (`*.example.com`) require super-admin.
- Schemes other than `https:` require super-admin.

### 3.2 Admin CSP (block editor / settings)

```
default-src 'self';
script-src 'self' 'nonce-{NONCE}';
style-src 'self' 'nonce-{NONCE}' 'sha256-{KNOWN_HASHES}';
img-src 'self' data: blob: https://media.{site};
font-src 'self' data:;
connect-src 'self';
frame-src 'self';
media-src 'self' blob: https://media.{site};
object-src 'none';
base-uri 'self';
form-action 'self';
frame-ancestors 'none';
worker-src 'self' blob:;
require-trusted-types-for 'script';
trusted-types default nextjs#bundler dompurify;
report-uri /_/csp-report;
```

Key differences from public:

- **No `'strict-dynamic'`** — admin has a fixed set of known scripts.
- **`worker-src 'self' blob:`** — block editor uses workers for syntax highlighting / large transforms.
- **`require-trusted-types-for 'script'`** + **`trusted-types`** policies. This is the v1 Trusted Types rollout for admin only; we ship pre-named policies (`default`, `nextjs#bundler`, `dompurify`) and any other sink injection fails closed.
- **`frame-ancestors 'none'`** — admin is never framed (clickjacking).
- **`connect-src 'self'`** — admin's only network is back to our API.

### 3.3 Plugin frontend bundle CSP

Plugins that ship admin/editor UI extensions or block frontends serve assets via `/plugin-assets/{pluginId}/...`. The CSP for these responses:

```
default-src 'none';
script-src 'self' 'sha256-{KNOWN}'  ;       // SRI; nonce only if approved
style-src  'self' 'nonce-{NONCE}';
img-src    'self' data: https://media.{site};
font-src   'self';
connect-src 'self';
sandbox allow-scripts allow-same-origin allow-forms;
```

Plus, separately, **each module's `<script>` tag in the host page gets an `integrity=` (SRI) hash** — the plugin manifest declares `bundle.sri` and we set it. If a plugin ships a non-matching bundle (registry drift, MITM), the browser refuses execution.

- **Inline allowed only via nonce**. The host generates the nonce; the plugin's bundle declares it needs inline by listing `inline: true` in manifest, and the host emits `<script nonce="...">` wrappers. Plugins that don't declare this can't inject inline.
- **`sandbox`** directive in the document response is unused (since the plugin's UI hydrates inside the host page); but for *iframed* plugin previews (block previews in editor) we attach an HTML `<iframe sandbox>` — see §14.

### 3.4 Nonce delivery: end-to-end

| Step | Component | Action |
|---|---|---|
| 1 | Edge / Go middleware | generate 16 random bytes, base64url-encode → `nonce` |
| 2 | Go middleware | set `Content-Security-Policy` with `'nonce-{nonce}'`, set request header `X-Script-Nonce` forwarded to Next.js |
| 3 | Next.js render | read `X-Script-Nonce` via `headers()` in App Router; pass to `<Script nonce={nonce}>` and `<style nonce={nonce}>` |
| 4 | Next.js render | pass nonce through React context so any inline-emitting block uses it |
| 5 | Response | flushed with both the CSP header and matching `nonce=` attributes |

Sketch (Next.js `app/layout.tsx`):

```tsx
import { headers } from 'next/headers';
import Script from 'next/script';

export default function RootLayout({ children }: { children: React.ReactNode }) {
  const nonce = headers().get('x-script-nonce') ?? '';
  return (
    <html>
      <head>
        <Script nonce={nonce} src="/_next/static/chunks/main.js" strategy="beforeInteractive" />
      </head>
      <body data-nonce={nonce /* exposed for client-side scripts that emit DOM */}>
        {children}
      </body>
    </html>
  );
}
```

`data-nonce` exposure: required because client-side React occasionally has to inject inline (e.g., a code-syntax-highlight library mounting a `<style>`). Trade-off accepted: leaking the nonce *to same-origin scripts* is fine — the nonce only matters to a cross-origin attacker, who cannot read DOM attributes.

### 3.5 CSP report endpoint

`POST /_/csp-report` (no auth, rate-limited 100/min/IP). Accepts standard report bodies (both legacy and Reporting API). Drops payload to a sampled stream (`metric:csp.violation`); top-10 violators per week feed a weekly digest for the security team.

Cardinality controls: bucket by `effective-directive` × `blocked-uri-host` × `route-class`. Drop the rest. Reports older than 30 days are deleted.

---

## 4. XSS sanitization pipeline

Goal: every untrusted string is sanitized at the boundary where it transitions from data → markup. The pipeline is layered; each layer is owned by a clear component.

### 4.1 Layer responsibilities

| Layer | Where | Owner | Sanitizer |
|---|---|---|---|
| L1 storage | DB write of post/comment content | core CMS | minimal: charset normalize, control-char strip; no HTML stripping |
| L2 server render | block render in Go OR Next.js | block render | `bluemonday` (Go) for server-rendered blocks; trust block contract for typed attrs |
| L3 client render | React in browser | block client | default React escaping; `dangerouslySetInnerHTML` only after `DOMPurify` |
| L4 admin output | admin UI showing user-supplied strings | admin | React default escaping; lint flags `dangerouslySetInnerHTML` |
| L5 plugin output | host injection of plugin-rendered HTML | plugin host | `bluemonday` (Go) before injection |

Sanitize on render (not on store). Keep raw input; render through a sanitizer. This way policy changes don't require re-saving old content.

Exception: comments — sanitized on save **and** on render (defense in depth + the on-save pass lets us reject obviously-toxic input synchronously for moderation UX).

### 4.2 Bluemonday profiles

We use four configured profiles:

```go
// pkg/security/sanitize/policies.go
package sanitize

import "github.com/microcosm-cc/bluemonday"

// CommentsUGC: extremely tight. p, br, em, strong, code, blockquote, lists, a (with rel-nofollow).
func CommentsUGC() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowStandardURLs()
	p.AllowAttrs("href").OnElements("a")
	p.RequireParseableURLs(true)
	p.RequireNoReferrerOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	p.AllowElements("p", "br", "em", "strong", "code", "blockquote", "ul", "ol", "li", "a")
	return p
}

// PostContent: richer — typical authoring HTML allowed.
func PostContent() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowImages()
	p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("p", "div", "span", "h1", "h2", "h3", "h4", "h5", "h6")
	p.AllowAttrs("loading").Matching(bluemonday.SpaceSeparatedTokens).OnElements("img")
	p.AllowDataURIImages()
	return p
}

// BlockOutput: very strict — block render is *supposed* to be typed; this is a backstop.
func BlockOutput() *bluemonday.Policy { return PostContent() }

// PluginOutput: same as BlockOutput; plugins cannot exceed.
func PluginOutput() *bluemonday.Policy { return BlockOutput() }
```

`unfiltered_html` capability (super-admin by default, ungrantable to regular roles via UI without explicit super-admin override) bypasses sanitization on post content. It is **never** bypassed for comments.

### 4.3 DOMPurify in the editor

Editor surfaces that accept paste / HTML-import use `DOMPurify` with a config matching `PostContent`:

```ts
// frontend/admin/sanitize.ts
import DOMPurify from 'dompurify';

export const sanitizePasted = (html: string) =>
  DOMPurify.sanitize(html, {
    USE_PROFILES: { html: true },
    FORBID_TAGS: ['style', 'form', 'input', 'iframe', 'frame', 'object', 'embed'],
    FORBID_ATTR: ['onerror', 'onload', 'onclick', 'onmouseover', 'onfocus', 'onblur'],
    ALLOW_DATA_ATTR: false,
    RETURN_TRUSTED_TYPE: true,            // Trusted Types in admin
  });
```

Trusted Types: `RETURN_TRUSTED_TYPE` returns a `TrustedHTML` object. The block editor's `innerHTML` sinks accept only `TrustedHTML`, enforced by the admin CSP's `require-trusted-types-for 'script'`.

### 4.4 SVG handling

SVG is uniquely XSS-prone (script in `<svg>`, foreignObject, event handlers).

- Uploaded SVGs go through a server-side sanitizer (`bluemonday` SVG policy + a separate XML walker stripping `<script>`, event attrs, `xlink:href` with `javascript:`).
- In renders, SVGs uploaded by non-`unfiltered_html` users are served as `Content-Disposition: inline; filename=...` but with `Content-Security-Policy: sandbox` so even if a payload slips, it's executed in a sandboxed context.
- Inline SVG (theme bundles, block icons): allowed; they're trusted code.

### 4.5 MathML, embedded media, iframes

- MathML: sanitize via DOMPurify config that permits the MathML subset.
- Embedded `<iframe>`: only via the `embed` block, which goes through an **oEmbed allowlist**. Free-form iframes require `unfiltered_html`.
- Allowed iframes get `sandbox="allow-scripts allow-same-origin allow-popups"` (no `allow-top-navigation`, no `allow-forms` by default).

### 4.6 Plugin-rendered HTML

Plugins return HTML strings from filters (e.g., `the_content`). The host runs `PluginOutput().Sanitize(...)` before inserting into the rendered page. Plugins cannot opt out **except** by claiming `unfiltered_html`, which:

- Requires super-admin grant via admin UI per plugin instance.
- Logs an audit event on each invocation.
- Is rate-limited (max N invocations/min — beyond that, sanitization re-applies and logs).

### 4.7 Lints / static checks

Forward-reference to doc 11. Required CI lints:

- `no-danger` rule for React: `dangerouslySetInnerHTML` only inside files whitelisted via `// sanitized: ...` pragma referencing the sanitizer.
- `forbid-element` for `<script>` inline in TSX outside `app/layout.tsx`.
- Go vet rule `unsafehtml`: any `template.HTML`-typed value crossing the sanitizer boundary needs an explicit `// nosanitize: ...` justification.

---

## 5. Secret management

Three tiers, each with a different storage class, access pattern, and rotation story.

### 5.1 The tiers

| Tier | Examples | Storage | Access at runtime | Rotation |
|---|---|---|---|---|
| **System** | DB password, S3 keys, SMTP creds, OAuth client secrets, **pepper**, signing keys, encryption keys | secret backend (Vault / AWS SM / GCP SM / K8s Secrets / Doppler) via adapter | Go boots, fetches once, holds in memory; refresh on signal or TTL | manual or backend-driven; rotation playbook §6 |
| **Per-plugin** | plugin's API keys to external services | Postgres column, encrypted with system key | Plugin reads via host ABI `host.secrets.get(key)` | admin sets value; plugin can't write |
| **Per-user** | passkey credentials, recovery codes | covered by 06 (passkey credentials in DB, recovery codes Argon2id-hashed) | n/a — owned by 06 | n/a |

### 5.2 System secret adapter

```go
// pkg/security/secrets/store.go
package secrets

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("secret not found")

type Store interface {
	Get(ctx context.Context, key string) (string, error)
	// GetBinary used for pepper, encryption keys.
	GetBinary(ctx context.Context, key string) ([]byte, error)
	// Watch optional: backend notifies on rotation; nil means "no live rotation".
	Watch(ctx context.Context, key string) (<-chan struct{}, error)
}

// LoadRequired fails the process if any required secret is missing.
// Called from main before HTTP starts.
func LoadRequired(ctx context.Context, s Store, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		v, err := s.Get(ctx, k)
		if err != nil { return nil, errKeyf("secret %q: %w", k, err) }
		if v == "" { return nil, errKeyf("secret %q is empty", k) }
		out[k] = v
	}
	return out, nil
}
```

Adapters implemented:

- `EnvStore` — dev only; pulls from environment.
- `VaultStore` — Vault KV v2; supports `Watch` via Vault's KV subscription.
- `AWSSMStore` — Secrets Manager; polled with TTL.
- `K8sSecretStore` — projected volume; file-watcher on `/secrets/...`.
- `DopplerStore` — REST API.

Choice is config-driven: `secrets.backend = "vault" | "aws-sm" | "k8s" | "doppler" | "env"`.

### 5.3 Required system secrets

| Key | Purpose | Format |
|---|---|---|
| `database.dsn` | Postgres connection | URL |
| `redis.url` | Redis connection | URL |
| `s3.access_key` / `s3.secret_key` | media bucket | string |
| `smtp.password` | mail | string |
| `auth.pepper` | password hashing pepper (06) | 32 random bytes, base64 |
| `auth.session_signing_key` | session cookie HMAC | 64 random bytes |
| `auth.oauth.{provider}.client_secret` | each OAuth provider | string |
| `plugin.signing.trusted_keys` | cosign public keys, PEM | bundle |
| `theme.signing.trusted_keys` | cosign public keys | bundle |
| `dek.master` | data-encryption-key for per-plugin secrets at rest | 32 random bytes |

Boot validation refuses to start if any required key is missing or empty.

### 5.4 Per-plugin secrets

Schema:

```sql
CREATE TABLE plugin_secrets (
  plugin_id    TEXT      NOT NULL,
  key          TEXT      NOT NULL,
  ciphertext   BYTEA     NOT NULL,  -- AES-256-GCM ciphertext
  nonce        BYTEA     NOT NULL,  -- GCM nonce
  dek_version  SMALLINT  NOT NULL,  -- which DEK encrypted it (for rotation)
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (plugin_id, key)
);
```

Plugin manifest declares its keys:

```yaml
secrets:
  - key: STRIPE_API_KEY
    description: "Stripe live secret key (sk_live_...)"
    required: true
  - key: WEBHOOK_SIGNING_SECRET
    description: "Stripe webhook signing secret"
    required: true
```

Admin sees these in the plugin settings UI, types values; the values are encrypted with the DEK and stored. The plugin reads via host ABI:

```
// inside plugin WASM
host.secrets.get("STRIPE_API_KEY") -> returns string (opaque to plugin; not loggable)
```

- The string is marked tainted in the host's plugin runtime: any attempt by the plugin to log it via `host.log.*` redacts to `***REDACTED***`.
- The string is *not* in the WASM linear memory persistently — fetched on call, scrubbed when control returns.

### 5.5 Encryption at rest for per-plugin secrets

- AES-256-GCM.
- DEK from `secrets.dek.master`. Rotation: bump `dek_version`; old DEKs kept in a versioned map until grace period elapses; background job re-encrypts.
- Optional envelope encryption: if backend is AWS-SM/Vault, the DEK itself can be KMS-wrapped. Adapter exposes `Wrap(ctx, dek)` / `Unwrap(ctx, wrapped)`.

### 5.6 Redaction rules (universal)

- Never log secret values, ever. Use sentinel `***REDACTED***`.
- Error wrapping must strip values: standard helper `errors.RedactedWrap(err, "fetching secret %q", key)`.
- Stack traces never include local variable values from the secret-fetch functions (the Go stack only includes function names by default; ensure we don't add `%v` formatting that leaks).
- Audit log writers run all messages through a `redactSecretsLike` regex: long-base64, `sk_live_`, `Bearer ...`, AWS access-key patterns. False positives okay.

### 5.7 Tradeoffs (secrets)

- **Vault vs cloud-native**: rejected single-backend mandate. Adapter pattern keeps small deploys (just env vars + K8s Secrets) viable while letting enterprises pick Vault. Cost: adapter complexity, fewer features (Vault's lease auto-renew is harder to abstract).
- **Storing per-plugin secrets in DB vs secret backend**: DB chosen because admin UI needs to *set* values, and uniform admin write to multiple secret backends is a nightmare. DEK protects at rest; backend has only the DEK.
- **Per-process pepper cache vs re-fetch every login**: cache in memory; the secret backend is allowed to be slow on rotation events but never on hot path.

---

## 6. Pepper (cross-reference 06)

Pepper is a system secret. The hashing scheme lives in 06; here's what's specific to this doc:

- Sourced from `auth.pepper` at boot. Held in memory only. Never persisted in the app process.
- Versioned: `auth.pepper.v2`, `auth.pepper.v3`, etc. Stored hashes carry a small `pep_v` field.
- Rotation: on the rotation event, the runtime begins double-checking: try `argon2id(pwd || pepper_v_current)`, fall back `argon2id(pwd || pepper_v_old)`; on successful old-version verify, re-hash with current pepper and persist.
- Grace period: 12 months default before old pepper version is removed; affected users are flagged for forced password reset at the end of grace.
- Pepper rotation triggers an audit-log event `auth.pepper.rotated` with version numbers, no secret material.

---

## 7. Supply chain

Everything that enters the running system: core code, plugins, themes, container images, deps. Each gets a signing and verification story.

### 7.1 Core releases

- Built in a hermetic CI environment (one-shot ephemeral runner, no persistent state, no shell access).
- Output artifacts: Linux/macOS/Windows binaries, container images, npm packages for SDKs, signed zip of admin static assets.
- **Signing**: `cosign sign` for binaries and OCI images, using Sigstore keyless (OIDC-bound identity) — no long-lived signing key.
- **Verification**: download mechanism (CLI / installer / Helm chart) runs `cosign verify` against expected identity `*@releases.ourorg.example`. Mismatch = refuse to install.
- **Checksums**: `SHA256SUMS` file alongside; signed by the same identity.

### 7.2 Plugins (cross-ref 02)

Doc 02 owns the plugin signing model. Here's the security baseline that intersects:

- Plugin `.wasm` modules are signed at publish time with the publisher's cosign identity (Sigstore keyless or KMS-backed key).
- The host's `plugin.signing.trusted_keys` system secret is a bundle of accepted publisher identities (regex or exact match).
- A plugin from an unknown publisher: admin sees a hard warning + must type publisher name to confirm. Audit-logged.
- A plugin whose signature doesn't verify: refused to load.

### 7.3 Themes

Themes ship code that runs in the Next.js process (see §14). Therefore themes are subject to **the same signing requirement** as plugins.

- Theme package format: npm tarball (or zipped equivalent) + `theme.json` manifest + `signature.sig` (cosign bundle).
- Trusted theme-publisher identities live in `theme.signing.trusted_keys`.
- An unsigned theme: blocked by default. Admin can override with `--allow-unsigned` flag (operator-level, not UI), with prominent banner and audit event for any unsigned theme activated.
- The signature covers the package tarball checksum — file-level tamper is detected.

### 7.4 Dependency pinning

- **Go**: `go.mod` + `go.sum` mandatory; `GOFLAGS=-mod=readonly` in CI; `gosum` verified on every build.
- **Node**: `package-lock.json` committed; `npm ci` (not `npm install`) in CI; `npm audit signatures` for packages from npmjs (verifies registry-side signatures where available).
- **Container images**: pin by digest in Dockerfile / Helm values (`postgres@sha256:...`, never `postgres:15`).
- **Renovate-bot** for managed updates, but pinning is preserved across PRs.

### 7.5 SBOM

- Each release ships an SBOM in CycloneDX JSON format.
- Generated by `syft packages dir:./ -o cyclonedx-json` for Go + npm; container SBOM via `syft <image-ref>`.
- Stored alongside release artifacts; cosign-signed.
- Useful for downstream CVE reconciliation (`grype sbom:...`).

### 7.6 Dependency vulnerability scanning

- `govulncheck ./...` on every PR. Failure on any high/critical hits. Medium below CVSS 7 = warning.
- `osv-scanner` on lockfiles for Node + Go.
- `trivy image` against any container image we publish.
- Weekly cron scan over the deployed running version's SBOM.
- Doc 11 owns the test plumbing; here we declare the policy.

### 7.7 Reproducible builds (aspirational)

We document this as a known gap.

- Go builds with `-trimpath -buildvcs=false` and identical `go.mod` should be reproducible across hermetic runners; we'll verify periodically.
- Node builds are not reproducible by default (bundler determinism varies); v1 ships with best-effort.
- Goal: by v2, two independent CI runs should produce byte-identical artifacts for both Go and Node.

### 7.8 Build identity & provenance

- CI runner uses an OIDC-issued short-lived token to authenticate to artifact registries — no static credentials.
- Build provenance (SLSA Level 3 target): the build step emits a signed provenance attestation (cosign attest with `slsaprovenance` predicate).
- Downstream verification: any deployment pipeline can require provenance to match expected source repo + workflow.

### 7.9 Tradeoffs (supply chain)

- **Sigstore keyless vs KMS keys**: keyless chosen for project-published plugins/themes because it removes long-lived secret management. KMS-backed identities allowed for org plugin authors who already have KMS infra. Both verify via the same `cosign verify` against an identity regex.
- **Mandatory signed themes**: rejected the option to allow unsigned themes by default — themes run in the Next.js process and the risk is too high. Operator override path exists for advanced users.
- **`npm audit signatures`**: deceptively named — relies on npm registry's signatures, which were rolled out gradually; not all packages have them. We treat as a *signal*, not a gate.

---

## 8. Input validation

The principle: every API input is validated against a declared schema at the boundary. Beyond the boundary, only typed structs.

### 8.1 REST

- OpenAPI 3.1 schema is the source of truth. `oapi-codegen` generates Go server-side request types with strict validators.
- Unknown fields rejected by default (`additionalProperties: false` everywhere). Endpoints that genuinely need extensibility opt-in via `x-allow-additional`.
- Numeric ranges, string lengths, enum values: enforced before the handler runs.

### 8.2 GraphQL

- Schema-first; resolvers receive typed inputs from `gqlgen`.
- Variables validated by the GraphQL runtime; no `JSON` scalar except for explicit `BlockAttributes` (whose shape is validated downstream by the block registry).
- Introspection: disabled in production by default; super-admin opt-in. (Modern argument for keeping it on is debatable; we err on the side of off.)

### 8.3 File uploads

Owned by doc 07. Here's the security-relevant subset:

- Content-sniffing (`net/http.DetectContentType` + libmagic for richer detection), not extension.
- MIME allowlist, not denylist.
- Reject anomalous: e.g., a file labeled `image/jpeg` whose sniffed type is `text/html`.
- File size hard caps per route.
- Storage path is `{uuid}.{ext}` — never user-controlled.

### 8.4 URL-bearing fields

Any field that takes a URL (plugin manifest's `allow_hosts`, webhook URLs, OAuth callback URLs, oEmbed URLs, image-import URLs):

1. Canonicalize: parse with `url.Parse`, normalize host, lowercase scheme.
2. Scheme must be in `{https}` (or `{https, http}` for dev only — env-flagged).
3. Host: not bare IP unless explicitly configured (no `http://192.168.1.1`).
4. Reject userinfo (`https://user:pass@host`).
5. Resolve DNS at validation time and reject if any A/AAAA resolves to a private/loopback/link-local/multicast range — this is partial SSRF guard, with full guard at egress time §9.
6. Reject fragments and trailing whitespace.

### 8.5 JSON depth and size

Decoder limits:

- Max body size per route (REST: 1 MiB default, content-creation: 16 MiB, media upload: per doc 07).
- Max JSON depth: 100 levels. Bluemonday in front of any HTML field.
- Max array length: 10k per array unless the route declares otherwise.

---

## 9. SSRF mitigation

The Server-Side Request Forgery story is critical because the system makes outbound HTTP from multiple components:

- **Plugins** with the `http.fetch` capability.
- **Webhooks** (event-driven outbound POSTs).
- **Image proxy** (when fetching remote URLs to generate thumbnails).
- **OAuth callbacks** (token-endpoint POSTs).
- **oEmbed discovery** (HEAD/GET to embed providers).

We funnel **all** outbound HTTP through a single shared `pkg/safehttp` package. No goroutine anywhere calls `http.Get` directly on user-influenced URLs.

### 9.1 Hardened HTTP client

```go
// pkg/safehttp/client.go
package safehttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

var (
	ErrPrivateIP   = errors.New("destination resolves to a private/internal address")
	ErrSchemeBlocked = errors.New("scheme not allowed")
	ErrTooLarge    = errors.New("response exceeds max size")
	ErrTimeout     = errors.New("request timed out")
)

type Options struct {
	MaxResponseBytes int64
	Timeout          time.Duration
	AllowedSchemes   []string // ["https"]; ["https","http"] only via opt-in
	MaxRedirects     int      // default 5
	UserAgent        string
}

func New(opts Options) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		// DialContext is the SSRF guard: re-resolve, validate every IP, then dial.
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil { return nil, err }
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil { return nil, err }
			for _, ip := range ips {
				a, ok := netip.AddrFromSlice(ip.IP)
				if !ok { return nil, errors.New("invalid IP") }
				if isBlocked(a) { return nil, ErrPrivateIP }
			}
			// Dial against the *resolved* IP. The crucial point: we do NOT
			// trust a later DNS lookup. We pin to the IP we just validated.
			ip := ips[0].IP.String()
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
		},
		TLSHandshakeTimeout: 5 * time.Second,
		MaxIdleConns:        20,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  false,
	}
	c := &http.Client{
		Transport: roundTripperWithLimit(transport, opts),
		Timeout:   nonZero(opts.Timeout, 30*time.Second),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > nonZeroInt(opts.MaxRedirects, 5) {
				return errors.New("too many redirects")
			}
			// Re-validate on redirect; URL must pass scheme + host check again.
			return validateURL(req.URL, opts)
		},
	}
	return c
}

// isBlocked returns true for RFC1918, loopback, link-local, multicast, CGNAT,
// unique-local IPv6, etc. We err on the side of denying.
func isBlocked(a netip.Addr) bool {
	return a.IsLoopback() ||
		a.IsLinkLocalUnicast() ||
		a.IsLinkLocalMulticast() ||
		a.IsMulticast() ||
		a.IsPrivate() ||
		a.IsUnspecified() ||
		isCGNAT(a) ||                // 100.64.0.0/10
		isReserved(a)                // 0.0.0.0/8, 192.0.0.0/24, 192.0.2.0/24, ...
}

func validateURL(u *url.URL, opts Options) error {
	allowed := opts.AllowedSchemes
	if len(allowed) == 0 { allowed = []string{"https"} }
	ok := false
	for _, s := range allowed { if u.Scheme == s { ok = true; break } }
	if !ok { return ErrSchemeBlocked }
	if u.User != nil { return errors.New("userinfo in URL not allowed") }
	return nil
}

// roundTripperWithLimit wraps Transport to cap response size.
func roundTripperWithLimit(t http.RoundTripper, opts Options) http.RoundTripper {
	return rtFunc(func(req *http.Request) (*http.Response, error) {
		if err := validateURL(req.URL, opts); err != nil { return nil, err }
		req.Header.Set("User-Agent", nonZeroStr(opts.UserAgent, "gonext/1.0 (+safehttp)"))
		resp, err := t.RoundTrip(req)
		if err != nil { return nil, err }
		if opts.MaxResponseBytes > 0 {
			resp.Body = newLimitedBody(resp.Body, opts.MaxResponseBytes)
		}
		return resp, nil
	})
}

type rtFunc func(*http.Request) (*http.Response, error)
func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
```

Notes:

- **DNS rebinding defense** is the trickiest part. The naive approach (resolve → check → reconnect → resolver returns different IP) is vulnerable. We solve it by **pinning the connection to the resolved IP we just validated** — `DialContext` takes the IP, not the hostname.
- For TLS, the Go transport still does SNI from the original Host header, so cert validation works against the hostname. (We use `transport.TLSClientConfig.ServerName` if we need to be explicit.)
- Per-caller knobs (`MaxResponseBytes`, allowed schemes, timeout) let plugins have tighter limits than core's image proxy.

### 9.2 Per-caller defaults

| Caller | Timeout | Max bytes | Schemes |
|---|---|---|---|
| Plugin `http.fetch` | 10s | 1 MiB | `https` only (override: super-admin per plugin) |
| Webhooks (outbound) | 15s | 4 KiB request, 4 KiB response | `https` only |
| Image proxy | 20s | 25 MiB | `https` |
| oEmbed discovery | 5s | 64 KiB | `https` (allowlisted hosts) |
| OAuth token endpoint | 10s | 128 KiB | `https` |
| Internal-allowed (search index sync) | 30s | unlimited | `https` (and `http` if allowed in config) |

### 9.3 Metrics

- `safehttp.requests_total{caller, result}` — result in `{ok, blocked_private, blocked_scheme, timeout, too_large, error}`.
- Alert: spike in `blocked_private` from a single plugin → triage signal.

---

## 10. CSRF

Admin/API CSRF is fully owned by 06 (synchronizer-token pattern + `SameSite=Lax` session cookies + Origin/Referer checks for state-changing requests).

This doc adds the **public-form** case:

### 10.1 Public-form CSRF tokens

Comments, plugin-provided contact forms, search-tracking POSTs: each form renders with a token bound to the visitor's anon-session.

- Anon-session: opaque cookie `__site_anon` set on first visit (HttpOnly, SameSite=Lax, Secure, Path=/, max-age 30d). Just a random UUID; not authenticated.
- Token: HMAC of (anon-id || form-id || timestamp) using `auth.session_signing_key`, valid for 12h.
- On submit, the server verifies the token and the cookie's HMAC.
- A `Origin` or `Referer` header must match the site origin for state-changing POSTs.
- Tokens are single-use for high-stakes forms (e.g., password recovery — replays prevented).

### 10.2 GraphQL mutations

POST-only; same `Origin`/`Referer` enforcement; admin uses the admin CSRF token from 06; public/anon GraphQL mutations either don't exist or require the public-form token.

---

## 11. Rate limiting and abuse

Cross-cutting. The unified middleware (`pkg/security/ratelimit`) reads policy from a registry; each route declares one or more buckets.

### 11.1 Buckets

| Bucket key | Used by | Default |
|---|---|---|
| `auth.*` | login, MFA, recovery (06) | 06's policy |
| `api.unauth.{ip}` | unauth REST/GraphQL | 60 req/min, burst 100 |
| `api.user.{userId}` | auth REST/GraphQL | 600 req/min |
| `plugin.{pluginId}.cpu` | plugin compute budget | 02 |
| `plugin.{pluginId}.http` | plugin outbound HTTP | 100/min |
| `media.upload.{userId}` | uploads | 30/hour authors; 200/hour editors |
| `comments.{ip}` | comment posting | 10/hour anon, 60/hour authed |
| `search.{ip}` | search endpoint | 30/min |
| `graphql.cost.{role}` | cost analyzer | see §11.2 |
| `csp-report.{ip}` | CSP endpoint | 100/min |

Storage: Redis (token bucket). Distributed across replicas naturally.

### 11.2 GraphQL query cost analyzer

A per-query static cost is computed by walking the AST:

- Each scalar field: cost 1.
- Each object field: cost 2.
- Connection field (list): cost = 5 + (requested page size).
- `first/last` arguments multiply contained field cost by page size.
- `@include`/`@skip`: do not reduce static cost (worst-case).

Budgets:

| Role | Cost per query | Cost per minute |
|---|---|---|
| Anonymous | 1,000 | 10,000 |
| Subscriber | 2,500 | 50,000 |
| Author/Editor | 10,000 | 250,000 |
| Admin / API token (super-admin) | 50,000 | uncapped (logged) |

Queries that exceed per-query cost fail with `RATE_LIMIT_QUERY_COST`. The minute-bucket overflows reject with `RATE_LIMIT_BUDGET`.

### 11.3 Enforcement

`pkg/security/ratelimit.Middleware` runs *after* auth (so user-scoped buckets are correct). Per-route declarations chain multiple buckets; first to exceed denies.

### 11.4 Penalty escalation

A bucket that has been exceeded in the last 60s gets a 2× tighter limit for the next 5 min. This dampens scripted bursts without permabanning. Hard bans require an operator action.

### 11.5 Bot defense (light)

We don't ship a CAPTCHA in v1, but:

- The CSP-report endpoint, search, and comment POSTs check for trivial bot signals (no `Accept-Language`, no `Accept`, no UA, UA on a known-bad list) and apply 10× tighter limits.
- Operator can drop in Turnstile / hCaptcha via a plugin in v1.

---

## 12. Logging and monitoring of security events

A separate stream from app logs.

### 12.1 Event categories

| Category | Examples | Sink | Retention |
|---|---|---|---|
| Auth | login, logout, MFA, password change, passkey events (06) | audit | 1y (06) |
| WAF | header check rejection, SSRF block, malformed input, oversized body | metric + sampled log | 90d |
| CSP | violation reports | metric + sampled log | 30d |
| Plugin | unsigned plugin activation, sandbox violation, capability denial | audit + log | 1y |
| Secret | secret access (only "this key was read", not the value), rotation, store unreachable | audit | 1y |
| Admin actions | role change, plugin install, theme activation, config change | audit (06) | 1y |
| Rate-limit | bucket exhaustion (sampled) | metric + log | 30d |

### 12.2 Log schema

```jsonc
{
  "ts": "2026-05-13T10:00:00.000Z",
  "level": "warn",
  "category": "waf",
  "event": "ssrf.blocked",
  "actor": { "type": "plugin", "id": "stripe-checkout", "version": "1.4.2" },
  "request_id": "01J5...",
  "target": "https://example.com/x",
  "reason": "private_ip:10.0.0.5",
  "ip": "203.0.113.5",   // anonymized after 90d to 203.0.113.0/24
  "user_id": null
}
```

- IPs anonymized to /24 (IPv4) or /48 (IPv6) at 90 days (matches 06 audit retention).
- Tamper-evidence: audit log is append-only Postgres + every 5 min the latest row's hash is signed with an HMAC chain (a tamper introduces a chain mismatch detectable on audit replay).

### 12.3 CSP reporting endpoint

`POST /_/csp-report` — accepts both the legacy `application/csp-report` body shape and the modern Reporting API JSON array. No auth; rate-limited per §11. Reports normalized into the WAF metric stream.

### 12.4 NEL / Network Error Logging

Public site emits a `NEL` and `Report-To` header pointing to `/_/nel`. Sampling rate: 0.1 (10%). Useful to detect upstream-of-us breakage we wouldn't otherwise see.

### 12.5 Alerting baseline

- `csp.violation` rate spike > 5× 7-day baseline → page.
- `safehttp.blocked_private` from one caller > 10/min → page.
- `auth.lockouts` from one user > 5/hour → page.
- Audit log chain mismatch detected → page (this is the loudest possible alert).

---

## 13. Vulnerability disclosure and patching

`SECURITY.md` (separate file, not in this doc — referenced from the repo root) declares the process. Key points:

### 13.1 Reporting

- Channel: `security@<domain>` (PGP key published) + a Sigstore-signed `.well-known/security.txt`.
- A formal HackerOne (or equivalent) program is an open question (§19).

### 13.2 SLAs

| Severity | Initial ack | Triage | Fix | Public disclosure |
|---|---|---|---|---|
| Critical (RCE, auth bypass, data exfil) | 24h | 48h | 7d | 30d post-fix or 90d max |
| High (privilege escalation, stored XSS in admin) | 48h | 7d | 30d | 60d post-fix or 90d max |
| Medium (XSS in public site, info disclosure) | 5d | 14d | 60d | 90d post-fix |
| Low | best-effort | best-effort | next release | with release notes |

### 13.3 CVE-style numbering

We self-issue advisories with format `GN-YYYY-NNNN`. Each gets a CVE if MITRE accepts.

### 13.4 Security release channel

- Patch-only releases distinct from feature releases. Even-minor.zero `.0` versioning reserved for emergency.
- Update mechanism (admin "check for updates") fetches a signed manifest from `releases.<domain>` with cosign verification.

---

## 14. Sandboxing recap

### 14.1 Plugins (sandboxed via WASM)

Owned by 02. Summary: WASM via `wazero`, capability-based ABI, CPU/memory/syscall caps. Untrusted code is acceptable inside the plugin sandbox.

### 14.2 Themes (NOT sandboxed)

**Explicit known limitation.** Themes are TypeScript/TSX that runs inside the Next.js Node process. There is no v1 sandbox.

The trust boundary is:

1. **Install-time review**: super-admin must approve theme install, sees a manifest of what the theme imports.
2. **Signing**: themes are signed; an unsigned theme requires explicit operator override (§7.3).
3. **CSP**: theme-emitted JS goes through Next.js bundling, which strips raw `<script>` and emits nonced bundles; themes cannot easily inject runtime JS.
4. **No server credentials in Next.js process**: the Next.js Node process must not have direct DB access or system-secret-store access. It only holds a session-signing-key for cookie verification and short-lived bearer tokens to the Go API. A malicious theme can exfil what's in this process; it cannot dump the DB.
5. **Network egress**: the Next.js process's outbound HTTP goes through the same `safehttp` rules (we ship a Node-side equivalent: a `fetch` wrapper with the same private-IP guard).
6. **Filesystem**: container-level read-only FS where the theme runs. Tmp is `tmpfs`.

Future direction (v2): explore isolated-vm or worker-thread sandbox for theme render — but this is deferred.

### 14.3 User-supplied SVG / MathML / embedded media

- SVG: server-side sanitized; if from a low-trust user, served with `CSP: sandbox`.
- MathML: DOMPurify-sanitized.
- Iframes: `sandbox="allow-scripts allow-same-origin allow-popups"` by default; `<iframe>` outside the oEmbed allowlist requires `unfiltered_html`.

### 14.4 Block previews in editor

The editor renders block previews inside a sandboxed iframe (`sandbox="allow-scripts allow-same-origin" srcdoc="..."`). This prevents a malformed block from breaking out into the editor context.

---

## 15. Privacy and data minimization

### 15.1 Cookies

- Essential cookies only by default:
  - `__site_anon` (anon session, §10.1).
  - Admin/auth cookies (06).
- Analytics / third-party tracking cookies: disabled by default; admin opt-in per cookie category.
- Cookie banner: shown only if a non-essential cookie is enabled.
- Cookie attributes: `HttpOnly` where applicable, `Secure`, `SameSite=Lax` (or `Strict` for admin).

### 15.2 IP handling

- Stored for abuse mitigation only.
- Anonymized to /24 (IPv4) / /48 (IPv6) after 90 days, matching 06.
- Never logged in public-side request logs by default (operator can opt in; banner shown).

### 15.3 PII export & deletion

Owned by 06. Documented here only that the surface exists and that it integrates with the secret store (per-user secrets like passkey credentials must be wiped on delete).

### 15.4 Third-party scripts

- Disabled by default. No analytics, no fonts-from-Google.com, no remote anything in the base ship.
- Admin enables explicit categories. Each category's hosts get added to CSP.

### 15.5 Pseudonymous comments

Commenter email is stored hashed (Argon2id) with a separate pepper for matching repeat commenters without retaining the raw email. Display name and the gravatar hash (if enabled) are stored separately.

---

## 16. Penetration testing and red-team

### 16.1 External pentest

- Annual third-party pentest covering: auth flows, admin, plugin API, REST/GraphQL, the WASM sandbox, the Next.js render path with malicious themes.
- Pre-launch (P6, doc 00) pentest is a hard gate.

### 16.2 Bug bounty

- Open question — see §19. Decision pre-launch.
- If yes: HackerOne or self-hosted; scope explicit; safe-harbor language; payment tiers by severity.

### 16.3 Internal red-team

- Each major release (every 6 months): a 1-week red-team exercise with rotating internal team. Targets: a deployed staging clone of production.
- Findings tracked in a private security board, time-boxed remediation.

### 16.4 Continuous security testing

- Fuzz the input boundaries: HTTP request parser, the block JSON schema validator, the WASM ABI marshaling, the bluemonday/DOMPurify configs.
- Doc 11 owns the test harnesses.

---

## 17. Compliance posture

### 17.1 GDPR/CCPA

- Data export and right-to-delete: owned by 06.
- DPA template: provided.
- Subprocessors list maintained at `/privacy/subprocessors`.
- Data residency: deployment-config dependent; the system runs in one region by default; multi-region is operator's choice.

### 17.2 HIPAA / PCI

Out of scope by default.

- HIPAA: the system does not encrypt every field at rest individually; logs may contain PHI if site stores it. Operators wanting HIPAA need a BAA with the operator infra and additional config.
- PCI: do not store cardholder data in this system. Plugins (e.g., Stripe) handle PCI scope by tokenizing at the third party.

### 17.3 SOC 2

Aspirational for the hosted offering (open question §19). Self-host has no SOC 2 commitment.

---

## 18. Incident response

### 18.1 Playbook structure

Every incident follows:

1. **Detection** — alert fires (§12.5) or external report received.
2. **Containment** — kill switch (per-plugin disable, feature-flag rollback, edge block IP); does not delete evidence.
3. **Eradication** — fix the vulnerability, rotate any potentially-leaked secrets, push patch.
4. **Recovery** — restore service to nominal, monitor for re-occurrence.
5. **Lessons** — post-mortem within 14 days; published internally and, where applicable, externally as an advisory.

### 18.2 Runbooks (must exist by P5)

- **Compromised plugin**: revoke publisher key in `plugin.signing.trusted_keys`, force-disable plugin globally, audit users who had it active, push security advisory.
- **Credential stuffing surge**: tighten auth rate limits, enable global CAPTCHA on login, audit lockouts.
- **DB breach suspected**: rotate `auth.pepper`, force password reset for affected users, rotate session-signing key (invalidates all sessions), audit DB logs for the breach window.
- **Secret store unreachable**: failover behavior — app uses last-known-good cached values, refuses any operation that requires un-cached secrets, alerts loudly. Boot is blocked.
- **Theme compromise discovered**: disable theme, revert site to default theme, audit-log all sessions that hit theme-served pages during compromise window.

### 18.3 Communications templates

- **Security advisory** (public): a short markdown template — summary, CVSS, affected versions, fix version, workaround, credit, timeline.
- **User-facing breach notification** (regulated): jurisdiction-specific; legal counsel involved.
- **Status page** integration: an incident automatically pushes a status-page event with severity.

### 18.4 War-room playbook

- Single incident channel.
- Roles: incident commander, scribe, comms lead, technical lead.
- 30-min status updates internally.
- External comms timing: depends on severity; legal counsel sign-off mandatory for breach notifications.

---

## 19. Security review checklist (for PRs)

A short checklist the reviewer walks through for any PR that touches auth, sessions, plugin ABI, file I/O, network I/O, crypto, parsing, or HTML rendering. Lives as `.github/PULL_REQUEST_TEMPLATE/security.md`.

### 19.1 The checklist

```
- [ ] Does this PR introduce new HTTP endpoints? If yes:
      [ ] route registered in headers middleware classifier
      [ ] CSP/headers policy chosen and documented
      [ ] rate-limit bucket declared
      [ ] CSRF/Origin requirement matches state-change semantics
      [ ] input validation schema present (OpenAPI / GraphQL / JSON schema)
- [ ] Does this PR add a new outbound HTTP call? If yes:
      [ ] uses pkg/safehttp (not net/http directly)
      [ ] caller registered in safehttp options table
- [ ] Does this PR touch HTML rendering (server or client)? If yes:
      [ ] sanitizer applied at the boundary, profile chosen
      [ ] no dangerouslySetInnerHTML without `// sanitized:` pragma
      [ ] no template.HTML conversion without `// nosanitize:` justification
- [ ] Does this PR read/write secrets? If yes:
      [ ] uses pkg/security/secrets adapter
      [ ] redaction verified in error paths
      [ ] no secrets in tests/fixtures (gitleaks scanned)
- [ ] Does this PR change auth/AuthZ? If yes:
      [ ] capabilities are explicit (no implicit grant)
      [ ] audit event emitted
      [ ] tests cover the new authority boundary
- [ ] Does this PR change the plugin ABI?
      [ ] capability gate added
      [ ] resource quota updated if applicable
      [ ] ABI version bumped per 02's compat policy
- [ ] Does this PR add a new third-party dependency?
      [ ] license compatible
      [ ] no known CVEs (govulncheck/osv-scanner clean)
      [ ] supply-chain review attached (maintainer activity, repo health)
- [ ] Does this PR change CSP or headers?
      [ ] reviewed in report-only mode in staging for 7 days first
      [ ] tested against the smoke-test matrix
- [ ] Does this PR change crypto?
      [ ] uses the std lib or an audited library; no rolled-from-scratch
      [ ] key handling reviewed by another security-tagged reviewer
- [ ] Does this PR change parsing of untrusted input?
      [ ] fuzz target added or updated
      [ ] resource caps in place (depth, length, byte size)
```

The reviewer ticks the boxes that apply or notes N/A. PRs touching auth/crypto/plugin-ABI require a second reviewer with the `security` GitHub team membership.

### 19.2 Definition of "security-sensitive"

A PR touching any of: `pkg/security/...`, `pkg/safehttp/...`, `pkg/auth/...`, `pkg/plugin/abi/...`, `pkg/media/upload/...`, `cmd/`, `Dockerfile`, `helm/...`, dependencies (`go.mod`, `package*.json`), CSP/headers config — requires the security checklist.

---

## 20. Tradeoffs and rejected alternatives (summary)

| Decision | Chosen | Rejected | Rationale |
|---|---|---|---|
| COEP on public site | `credentialless` | `require-corp` | Too much breakage with oEmbed providers in v1 |
| CSP inline policy | nonce + `strict-dynamic` | hash-only | hash-only is brittle with dynamic SSR content |
| Secret backend | adapter (Vault / AWS-SM / K8s / Doppler / Env) | single backend mandate | small deploys don't have Vault budget |
| Theme sandbox | none in v1, signing + review instead | per-theme isolated-vm | engineering cost prohibitive for v1; revisit v2 |
| Plugin signing | Sigstore keyless | publisher-issued long-lived keys | no long-lived secret management for OSS authors |
| SSRF allowlist | egress-denylist (private IPs blocked) | egress-allowlist | unrealistic for plugin use cases |
| GraphQL introspection | off in prod by default | always on | unnecessary fingerprinting |
| Mandatory MFA for super-admin | yes (06) | optional | acceptable UX cost for the blast-radius |
| HSTS preload | yes | opt-in | site downtime cost during certificate misconfig is acceptable |
| Trusted Types | admin only in v1 | site-wide | site-wide blocks legacy themes; revisit v2 |

---

## 21. Open questions

1. **Bug bounty program** — pre-launch yes/no? Cost vs signal-to-noise.
2. **Site-wide Trusted Types** — when to extend from admin to public site? Requires theme cooperation; could be opt-in via theme-manifest flag in v1.5.
3. **Subresource Integrity for the public site's third-party scripts** — once plugins opt in to declaring SRI, can we require it for any cross-origin script the site loads? Depends on whether oEmbed providers' embeds are scriptful (many are).
4. **Cosign keyless OIDC issuer** — GitHub OIDC vs Anchore Fulcio public vs our-own Sigstore instance. Cost of self-hosted Sigstore vs trust-rooted-in-GitHub.
5. **Reproducible builds** — what's the v1 commitment? Best-effort or hard goal?
6. **CSP `report-only` rollout** — start with `report-only` for 30 days post-launch, then flip to enforcing? Risk of unanticipated breakage in long tail of themes.
7. **Outbound HTTP allowlist for plugins** — should we ship a default allowlist of "known-good" provider hosts (Stripe, Twilio, etc.) so admins don't have to type them every time? Risk: encourages laziness; reward: better UX.
8. **WAF placement** — do we ship our own L7 WAF rules (OWASP CRS subset) inside the Go server, or rely entirely on edge? Edge is fragmented per operator deployment; an in-app baseline is portable but slower.
9. **Anti-bot integration** — first-class plugin slot for Turnstile / hCaptcha / Friendly Captcha? Affects 06's login flow and §11.5.
10. **Cookie-free anon sessions for public site** — feasible? Cleaner privacy story but breaks comment-form CSRF and search rate-limit accuracy.
11. **`Cross-Origin-Opener-Policy: same-origin-allow-popups`** for admin to support OAuth popup flows — is the popups exception worth the slightly weaker isolation?
12. **Secret rotation automation** — should the system auto-trigger pepper rotation every N days, or operator-only? Auto risks rotation storms; manual risks neglect.
13. **PII redaction in logs at write-time vs read-time** — write-time is safer but loses fidelity for debugging; read-time leaves a window of risk.
14. **SOC 2** — committed for hosted offering? Affects vendor selection (logging, secret store).
15. **CSP nonce in proxy headers** — `X-Script-Nonce` traveling Go → Next.js: any path where this header is logged into a request trace? Audit needed.

---

## 22. Appendix — quick reference

### 22.1 Files mentioned

- `pkg/security/headers/` — middleware, policies.
- `pkg/security/sanitize/` — bluemonday policies.
- `pkg/security/secrets/` — adapter interface and adapters.
- `pkg/safehttp/` — hardened outbound HTTP.
- `pkg/security/ratelimit/` — unified rate limiter.
- `pkg/security/audit/` — audit log writer (06 owns the storage; this writes events).
- `.github/PULL_REQUEST_TEMPLATE/security.md` — review checklist.
- `SECURITY.md` (repo root) — disclosure policy.
- `theme.json` / plugin manifest extensions for `csp`, `secrets`, `permissions`.

### 22.2 External libraries

- `github.com/sigstore/cosign` — signing.
- `github.com/microcosm-cc/bluemonday` — Go HTML sanitizer.
- `dompurify` — JS HTML sanitizer.
- `github.com/tetratelabs/wazero` — WASM runtime (02).
- `github.com/anchore/syft` and `grype` — SBOM and CVE.
- `govulncheck`, `osv-scanner` — vuln scanners.

### 22.3 Metrics (security namespace)

- `security.headers.misconfigured_total{header}`
- `security.csp.violation_total{directive,blocked_host}`
- `security.safehttp.requests_total{caller,result}`
- `security.sanitize.dropped_total{policy,reason}`
- `security.secrets.access_total{tier,key_hash,result}` (key_hash is short hash, not key)
- `security.ratelimit.exceeded_total{bucket}`
- `security.audit.chain_breaks_total` (must be 0 forever)

---

End of doc 13.
