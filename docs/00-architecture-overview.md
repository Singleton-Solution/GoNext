# WordPress Clone — Architecture Overview

> Codename: TBD. A modern, modular CMS inspired by WordPress, built on Next.js (frontend) and Go (backend). This document is the **shared foundation** all subsystem designs build on. Read this first.

---

## 1. Goals & Non-Goals

### Goals
- **Familiar mental model** for WordPress users: posts, pages, custom types, taxonomies, plugins, themes, roles.
- **Modern stack**: Go backend (single binary, low memory, fast), Next.js frontend (SSR/SSG/ISR), PostgreSQL.
- **Plugin ecosystem viability**: a hook/filter system as expressive as WordPress's, but secure and sandboxed.
- **Theme ecosystem viability**: developers can ship themes as npm packages or zip bundles.
- **Migration story from WordPress**: a working importer is non-negotiable.
- **Headless-first**: the site renderer is one frontend among many — clients can build their own.

### Non-Goals (v1)
- PHP compatibility. We are not running WordPress plugins. We provide migration tools.
- Feature parity with every WP feature (multisite, XML-RPC). Defer to v2+.
- Beating WP on plugin count. We win on quality, not quantity.

---

## 2. Stack Decisions (shared across all subsystems)

| Layer | Choice | Reason |
|---|---|---|
| Backend language | **Go** | Single binary deploy, low memory footprint, excellent concurrency, mature WASM host (`wazero`), great DB drivers. |
| Public site frontend | **Next.js (App Router)** | SSR/SSG/ISR, React Server Components for fast first paint, ecosystem. |
| Admin dashboard | **Next.js (separate app) OR Vite SPA** | Admin is interactive — SPA-style is fine. Could share the Next.js app via route groups. |
| Database | **PostgreSQL 15+** | JSONB for flexible metadata (better than WP's `wp_postmeta` EAV), built-in full-text search, mature, scales. |
| Cache | **Redis** | Sessions, fragment cache, plugin KV store, rate limiting. |
| Media storage | **S3-compatible** (filesystem fallback for dev) | Standard. |
| Search | **Postgres FTS** v1 → **Meilisearch/Typesense** v2 | Avoid premature complexity. |
| Background jobs | **Asynq** (Go, Redis-backed) | Cron, email, image processing, plugin tasks. |
| Auth | **Sessions in Redis + JWT for API** | Cookie sessions for the admin/site, JWT for plugin/3rd-party API access. |

---

## 3. The Three Hard Problems

Most of the architectural complexity reduces to three problems. Each subsystem doc references back to these.

### 3.1 Plugins — How does untrusted, third-party code extend the system?

**The decision: WebAssembly (WASM) via `wazero`, with a stable host ABI.**

- Plugins compile from any language (Go, Rust, AssemblyScript, JS via Javy, C/C++) to a `.wasm` module.
- The Go host loads modules, exposes a curated API (hooks, scoped DB access, HTTP, KV, logger).
- Memory-isolated, no filesystem access by default, capability-based permissions.
- Admin/editor UI extensions ship as **ES modules** loaded via import maps in the frontend (separate from server-side WASM logic).

**Why not alternatives:**
- *Native Go plugins (`plugin` pkg)*: only works on Linux, no isolation, ABI fragile.
- *Hashicorp go-plugin (gRPC subprocess)*: works, but heavy per plugin, no in-process speed.
- *JS-only plugins (V8)*: limits authors to JS, harder to reason about resource limits.
- *PHP compat layer*: massive scope, defeats the purpose of moving off PHP.

**Hook model:** `actions` (fire-and-forget side effects) + `filters` (transform-a-value chains), just like WP. Host dispatches these to all registered plugin handlers via WASM calls.

See [`02-plugin-system.md`](02-plugin-system.md) for the full design.

### 3.2 Themes — How do non-developers customize the look?

**The decision: themes are npm packages (or zip bundles containing the same) shipping React components + a manifest.**

- A theme exports a set of template components matched by a **template hierarchy** (similar to WP: `single-{type}-{slug}.tsx` → `single-{type}.tsx` → `single.tsx` → `index.tsx`).
- Themes can declare **theme.json**: colors, typography, spacing, supported features (like WP's theme.json).
- **Block themes** (full-site editing): the whole site is composed of blocks; templates are block compositions stored in the DB and editable in the admin.
- **Classic themes** (code-defined templates): for developers who want full control.
- The Next.js renderer resolves the request → picks the right template → renders with the post data.

See [`03-theme-system.md`](03-theme-system.md) for the full design.

### 3.3 Block Editor — How does content authoring work?

**The decision: a React-based block editor (Gutenberg-equivalent), but with a cleaner data model.**

- Content is stored as a structured JSON tree of blocks (not HTML with comment delimiters like WP).
- Each block has: `type`, `attributes`, `innerBlocks`, optional `clientId`.
- Blocks are registered by core or plugins; each block ships a React `edit` component, a `save` component (or a server render function), and a JSON schema for attributes.
- Renderer (Next.js) walks the tree and renders each block server-side; interactive blocks hydrate.

See [`04-block-editor.md`](04-block-editor.md) for the full design.

---

## 4. System Topology

```
                ┌──────────────────────┐         ┌──────────────────────┐
                │   Next.js (public)   │         │   Next.js (admin)    │
                │  SSR/SSG/ISR site    │         │   wp-admin equiv     │
                │  Theme renderer      │         │   Block editor       │
                └──────────┬───────────┘         └──────────┬───────────┘
                           │                                │
                           │  REST + GraphQL                │  REST + GraphQL
                           ▼                                ▼
                ┌─────────────────────────────────────────────────────┐
                │                    Go API Server                   │
                │  ┌────────┐ ┌─────────┐ ┌───────┐ ┌──────────────┐  │
                │  │  HTTP  │ │  Auth   │ │ Hooks │ │ Plugin Mgr   │  │
                │  │ router │ │  RBAC   │ │  bus  │ │ (WASM host)  │  │
                │  └────────┘ └─────────┘ └───────┘ └──────────────┘  │
                │  ┌────────┐ ┌─────────┐ ┌───────┐ ┌──────────────┐  │
                │  │ Domain │ │ Content │ │ Media │ │ Background   │  │
                │  │ models │ │ store   │ │  svc  │ │ jobs (Asynq) │  │
                │  └────────┘ └─────────┘ └───────┘ └──────────────┘  │
                └────┬──────────────┬─────────────┬──────────┬───────┘
                     │              │             │          │
                     ▼              ▼             ▼          ▼
                ┌────────┐   ┌─────────────┐  ┌──────┐  ┌──────────┐
                │Postgres│   │   Redis     │  │  S3  │  │  Plugin  │
                │        │   │ cache/jobs/ │  │media │  │ registry │
                │        │   │ sessions    │  │      │  │ (later)  │
                └────────┘   └─────────────┘  └──────┘  └──────────┘
```

---

## 5. Subsystem Documents

Each one is owned by an agent. They depend on this overview.

| # | Doc | Owns |
|---|---|---|
| 01 | [Core CMS & Data Model](01-core-cms.md) | Content types, taxonomies, metadata, DB schema |
| 02 | [Plugin System](02-plugin-system.md) | WASM runtime, hooks/filters, plugin ABI, capabilities |
| 03 | [Theme System](03-theme-system.md) | Template hierarchy, theme manifest, customizer, FSE |
| 04 | [Block Editor](04-block-editor.md) | Block model, editor UX, serialization, server render |
| 05 | [Admin Dashboard & API](05-admin-api.md) | Admin UI structure, REST + GraphQL surface |
| 06 | [Auth & Permissions](06-auth-permissions.md) | Users, roles, capabilities, sessions, API tokens |
| 07 | [Media & Performance](07-media-performance.md) | Uploads, image pipeline, cache, ISR strategy |
| 08 | [Migration & Compatibility](08-migration-compat.md) | WP importer, WP REST API compat shim |

---

## 6. Phasing (rough)

| Phase | Months | Scope |
|---|---|---|
| **P0 — Skeleton** | 0–2 | Go server, Postgres schema, basic auth, REST API, Next.js render with hardcoded theme. |
| **P1 — CMS core** | 2–5 | Posts/pages/CPTs, taxonomies, media, admin UI (CRUD), users + roles. |
| **P2 — Editor** | 5–9 | Block editor with ~20 core blocks, server render, theme.json. |
| **P3 — Themes** | 9–12 | Template hierarchy, theme installer, customizer, 1–2 reference themes. |
| **P4 — Plugins** | 12–18 | WASM runtime, hook system, plugin SDK, 3 reference plugins (SEO, contact form, analytics). |
| **P5 — Migration** | 18–21 | WP XML importer, REST API compat shim. |
| **P6 — Polish** | 21–24 | Performance, docs, marketplace, launch. |

Two engineers: ~24 months to a credible v1. A solo dev: double that.

---

## 7. Open Questions (resolve before serious build)

1. **Admin in same Next.js app or separate?** Separate is cleaner; same simplifies auth/deploy.
2. **WASM plugin DX**: do we ship an SDK per language, or one canonical (Go/Rust) and let JS authors use AssemblyScript?
3. **Multi-tenancy**: is multisite v2 or never? Affects schema (tenant_id columns).
4. **Licensing**: GPL (like WP, friendly to ecosystem) or Apache/MIT (friendlier to commercial)?
5. **Hosted offering**: SaaS from day 1, or self-host only? Affects priorities heavily.
