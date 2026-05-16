# 05 — Admin Dashboard & API Surface

> Owner: Admin/API track. Depends on `00-architecture-overview.md`. Sister docs: `01-core-cms.md` (data model), `04-block-editor.md` (the editor that the admin hosts), `06-auth-permissions.md` (capabilities/roles), `08-migration-compat.md` (WP REST shim deep-dive).
>
> Scope: the admin SPA (information architecture, list/edit screens, settings, plugin/theme admin, users, onboarding, UI framework, routing, state) and the public-facing API surface (REST, GraphQL, auth, rate limiting, webhooks, versioning, OpenAPI, CLI).
>
> Out of scope: the block editor internals (doc 04), the WASM plugin runtime (doc 02), the renderer/theme system (doc 03).

---

## Table of contents

1. Mental model and shared assumptions
2. PART A — Admin dashboard
   1. Information architecture (sidebar tree)
   2. Dashboard home and widgets
   3. List screens
   4. Edit screens
   5. Site editor (block themes)
   6. Settings registry
   7. Plugins and themes admin
   8. Users admin
   9. Notifications and inbox
   10. Onboarding wizard
   11. UI framework recommendation
   12. Routing and layout
   13. State management
   14. i18n
3. PART B — API surface
   15. REST API
   16. GraphQL API
   17. WordPress REST compatibility shim
   18. Auth (cookies, JWT, OAuth2, API keys)
   19. Rate limiting and abuse
   20. Webhooks
   21. API versioning
   22. OpenAPI and schema docs
   23. CLI (`gonext`)
4. Trade-offs and rejected alternatives
5. Open questions

---

## 1. Mental model and shared assumptions

Two things ship together in this doc because they are two views of the same data:

- The **admin** is a *consumer* of the API. There is no privileged admin-only data path. If the admin can do it, an API client with the same capabilities can do it. This forces the API to be complete.
- The **API** is *not* a thin database wrapper. It enforces the capability system from doc 06, fires hooks/filters from doc 02, validates against the content-type schemas from doc 01, and emits webhook events.

Concretely:

```
  Admin SPA  ─────► REST/GraphQL ─────► Capability check ─────► Hook bus ─────► Repo ─────► Postgres
                                            │
   Public Next.js render ─► same API ───────┘  (read-only token, same capability gate)

   CLI / 3rd-party  ─────► same API ─────────┘  (JWT or API key, same capability gate)
```

A few invariants we will keep reaching for:

- **Everything is a resource** with a stable `{id, type, slug?}` triple. Plugin CPTs are first-class.
- **All admin pages are deep-linkable.** `/admin/posts?status=draft&author=42&page=3` survives reloads.
- **No silent admin-only endpoints.** If we add an endpoint, it appears in OpenAPI and in the GraphQL schema unless explicitly internal (and internal endpoints are namespaced `/api/_internal/` and require an admin cookie).

---

## PART A — Admin Dashboard

## 2.1 Information architecture

WP admin's IA is one of its biggest assets and biggest liabilities: it is familiar to a million people but contains 20 years of barnacles (Tools → Available Tools is a museum piece). We keep the *spine* and discard the rot.

### Sidebar nav (tree)

```
Admin
├── Dashboard                    [/admin]
│   └── Home                     (default)
│
├── Content
│   ├── Posts                    [/admin/posts]
│   │   ├── All posts
│   │   ├── New post
│   │   ├── Categories           [/admin/taxonomies/category]
│   │   └── Tags                 [/admin/taxonomies/post_tag]
│   ├── Pages                    [/admin/pages]
│   ├── Media                    [/admin/media]
│   ├── Comments                 [/admin/comments]
│   └── <Custom post types>      [/admin/types/{slug}]   (plugins inject here)
│
├── Design
│   ├── Site editor              [/admin/site-editor]    (block themes only)
│   ├── Themes                   [/admin/themes]
│   ├── Customize                [/admin/customize]      (classic themes only)
│   └── Menus                    [/admin/menus]
│
├── Extend
│   ├── Plugins                  [/admin/plugins]
│   └── Marketplace              [/admin/marketplace]
│
├── People
│   ├── Users                    [/admin/users]
│   ├── Roles & capabilities     [/admin/roles]
│   └── Profile                  [/admin/profile]
│
├── Tools
│   ├── Import                   [/admin/tools/import]
│   ├── Export                   [/admin/tools/export]
│   ├── Site health              [/admin/tools/health]
│   ├── Logs                     [/admin/tools/logs]
│   └── Scheduled tasks          [/admin/tools/jobs]
│
├── Settings
│   ├── General                  [/admin/settings/general]
│   ├── Writing                  [/admin/settings/writing]
│   ├── Reading                  [/admin/settings/reading]
│   ├── Discussion               [/admin/settings/discussion]
│   ├── Media                    [/admin/settings/media]
│   ├── Permalinks               [/admin/settings/permalinks]
│   ├── Privacy                  [/admin/settings/privacy]
│   └── <Plugin settings>        [/admin/settings/{slug}]
│
└── Notifications                [/admin/inbox]          (badge in top bar)
```

### Deltas from WordPress

| WP | Us | Why |
|---|---|---|
| Appearance | **Design** | "Appearance" is a 2003 word; "Design" reads better next to "Site editor". |
| Plugins (top-level) | Under **Extend** | Groups plugins + marketplace; reduces top-level clutter. |
| Settings → Writing/Reading split | Kept | The names are bad but switching breaks every WP user's muscle memory. We keep them and rely on descriptions inside the page. |
| Tools → Available Tools (empty page) | **Removed** | It exists in WP only for plugins to inject into. We use the Tools area itself. |
| `wp-admin/options.php` | **Not exposed** | The "all settings" debug page is replaced by `gonext option` CLI. |
| Comments is a top-level menu | Demoted under Content | Most installs don't use comments; promote it only if at least one post type has comments enabled. |
| No notifications inbox | **Added** | Replaces the "admin notices" pattern (see §2.9). |

### Where plugins inject

The admin nav is a registry sourced from each installed plugin's **manifest** (doc 02 owns the manifest format and the `admin_pages` array — see doc 02 §2.2 / §7.3). On boot, and on every plugin install/activate/deactivate, the admin shell scans the manifests of all installed plugins and assembles the sidebar tree.

Concretely, each entry in a plugin's `admin_pages` array contributes:

| Manifest field | Sidebar effect |
|---|---|
| `id` | Stable nav-item id (namespaced by plugin slug). |
| `parent` | Mounts under `content` / `extend` / `tools` / `settings` / `design` / `users` / null (top-level). |
| `label`, `icon` | Display. Label is i18n-resolved against the plugin's translation bundle (see §2.14). |
| `capability` | Capability gate. The user must hold this capability or the menu item is **not rendered** (server-side filter — we don't ship a ghost item to the client). |
| `order` | Sort within parent. |
| `entry` | Path to the plugin's ES module that mounts the page; loaded into a DOM-scoped slot in the admin shell (sandboxed bundle, same as any other plugin UI surface — see §4 "Per-plugin admin routes vs sandboxed iframes" and doc 02 §7.6 for the CSP/scoping rules). |
| `children` | Nested sub-items, same shape. |

The runtime SDK helper `AdminMenu.register({...})` is **build-time sugar**: when the plugin is built, the SDK writes the registration into the plugin's `manifest.json` `admin_pages` array. There is no runtime register call — the shell never executes plugin code to discover what pages exist. The shell only loads a plugin's `entry` module when the user actually navigates to that page. (Fixed per review B6/C8 — manifest is the single source of truth; reconciles with doc 02 §2.2.)

Auth gating is **double-enforced**: the shell hides menu items the user lacks the capability for, and the API endpoint backing each page independently re-checks the same capability server-side (defence-in-depth — see doc 06 §7.4 and §3.4 of this doc).

We deliberately reject WP's `add_menu_page` callback model; everything is declarative so the nav can be SSRed and rendered without executing plugin code on every page load.

---

## 2.2 Dashboard home

The home is the first screen after login. WP's version is a graveyard of widgets; ours is intentionally sparse with strong defaults and aggressive extension points.

### Default widgets (core)

| Widget | Content | Source |
|---|---|---|
| **At a glance** | Counts of posts / pages / comments / users; site name, theme, plugin counts. | `GET /api/v1/site/at-a-glance` |
| **Activity** | Last 10 events: published, drafts, comments awaiting moderation. | `GET /api/v1/activity?limit=10` |
| **Quick draft** | Title + content, save as draft. | `POST /api/v1/posts` (status=draft) |
| **Site health** | A traffic-light summary linking to `/admin/tools/health`. | `GET /api/v1/site/health/summary` |
| **What's new** | Release notes for new core/plugin versions you've installed. RSS-style. | `GET /api/v1/updates/news` |

### Layout

```
┌──────────────────────────────────────────────────────────────────┐
│  Top bar: site switcher · search · new · notifications · profile │
├────────────┬─────────────────────────────────────────────────────┤
│            │                                                     │
│  Sidebar   │   ┌──────────────────┐  ┌──────────────────┐        │
│            │   │  At a glance     │  │  Site health     │        │
│            │   └──────────────────┘  └──────────────────┘        │
│            │   ┌──────────────────┐  ┌──────────────────┐        │
│            │   │  Activity feed   │  │  Quick draft     │        │
│            │   │  (large)         │  │                  │        │
│            │   │                  │  └──────────────────┘        │
│            │   │                  │  ┌──────────────────┐        │
│            │   │                  │  │  What's new      │        │
│            │   └──────────────────┘  └──────────────────┘        │
│            │                                                     │
└────────────┴─────────────────────────────────────────────────────┘
```

### Plugin-extensible

```ts
DashboardWidget.register({
  id: 'analytics.summary',
  title: t('Traffic (7d)'),
  size: 'md',               // sm | md | lg
  capability: 'view_analytics',
  // The component is a React lazy import; the loader pulls it from
  // the plugin's UI bundle declared in the import map.
  component: () => import('@plugins/analytics/widgets/Summary'),
  defaultEnabled: true,
});
```

Users can drag, hide, and reorder widgets; layout is persisted per-user via `PUT /api/v1/users/me/preferences`.

---

## 2.3 List screens

Every list screen (posts, pages, CPTs, media, comments, users) shares a single `<ResourceList>` shell. This is the highest-leverage component in the entire admin.

### Anatomy

```
┌──────────────────────────────────────────────────────────────────┐
│  Page title · primary action (New post)                          │
├──────────────────────────────────────────────────────────────────┤
│  Tabs: All (123) | Mine (8) | Drafts (4) | Trash (12)            │
├──────────────────────────────────────────────────────────────────┤
│  [ Search box ] [ Filter chips: status, author, date, taxonomy ] │
│  [ Saved views ▼ ] [ Bulk actions ▼ ] [ Columns ▼ ]              │
├──────────────────────────────────────────────────────────────────┤
│  ☐  Title              Author     Categories     Date    Status  │
│  ☐  Hello World        admin      Uncategorized  May 10   Pub    │
│  ☐  Draft notes        editor     —              May 09   Draft  │
│  ...                                                             │
├──────────────────────────────────────────────────────────────────┤
│  ‹ prev   1 2 3 ... 10   next ›        25 ▼ per page             │
└──────────────────────────────────────────────────────────────────┘
```

### Features

| Feature | Behaviour |
|---|---|
| **Search** | Server-side; defaults to title + excerpt; advanced syntax `author:42 status:draft` parsed into filters. |
| **Filters** | Pill chips; URL-synced (`?status=draft&author=42`). |
| **Saved views** | A filter+sort+columns combination, stored per-user; team-shared views possible (`scope: 'team'`). Backed by `view_preferences` table. |
| **Bulk actions** | Trash, restore, change status, change author, change terms; plugins register more. |
| **Sortable columns** | Click header. Multi-sort via shift-click. URL: `?sort=-date,title`. |
| **Sticky toolbar** | Survives scroll; bulk-action bar appears when rows are selected. |
| **Row density** | Comfortable / compact toggle, per-user. |
| **Empty / loading / error states** | First-class — every list screen ships all three. |
| **Real-time** | Server-Sent Events stream `resource.*` events; new posts appear with a "1 new — refresh" pill instead of jumping. |

### Plugin/theme-registered columns

```ts
ResourceColumn.register({
  resource: 'post',
  id: 'seo.score',
  label: t('SEO'),
  width: 80,
  capability: 'view_seo',
  // Cell receives the resource row.
  cell: ({ row }) => <SeoBadge score={row.meta['seo.score']} />,
  // Optional: makes the column sortable and tells the API how to sort it.
  sort: { fields: ['meta.seo.score'] },
});
```

The API supports column field selection so a plugin column can opt into fetching only its own meta key (see §3.1 sparse fieldsets).

### Bulk action contract

```ts
BulkAction.register({
  resource: 'post',
  id: 'seo.rescan',
  label: t('Rescan SEO'),
  capability: 'edit_posts',
  confirmation: ({ count }) => t('Rescan {count} posts?', { count }),
  run: async (ids) => {
    // Returns a job id; UI shows progress in the inbox.
    return api.post('/plugins/seo/jobs/rescan', { ids });
  },
});
```

Bulk operations that affect >25 rows are always backgrounded via Asynq (see doc 00) and surface in the inbox as a progress notification.

---

## 2.4 Edit screens

Four edit screen shapes:

1. **Single-post editor** — hosts the block editor (doc 04).
2. **Term editor** — for taxonomy terms (category, tag, custom).
3. **User profile** — same shell as Settings, scoped to a user.
4. **Media detail** — preview + metadata + usages.

### Single-post editor shell

The editor itself is doc 04's territory; what this doc covers is the *frame around it*: the chrome the admin provides, the sidebar panels, the publish flow.

```
┌──────────────────────────────────────────────────────────────────┐
│  ← back   Document title (auto)         Preview · Save · Publish │
├──────────────────────────────────────────────┬───────────────────┤
│                                              │  Sidebar panels:  │
│                                              │   - Status         │
│                                              │   - Permalink      │
│                                              │   - Categories     │
│   <Block editor — doc 04>                    │   - Tags           │
│                                              │   - Featured image │
│                                              │   - Excerpt        │
│                                              │   - Discussion     │
│                                              │   - Revisions      │
│                                              │   - <plugin panel> │
│                                              │   - <plugin panel> │
│                                              │                   │
├──────────────────────────────────────────────┴───────────────────┤
│  Footer: word count · last saved · validation summary            │
└──────────────────────────────────────────────────────────────────┘
```

### Sidebar panel registry

```ts
EditorPanel.register({
  id: 'seo.panel',
  resource: 'post' | ['post', 'page'],  // applies to multiple types
  title: t('SEO'),
  icon: SearchIcon,
  capability: 'edit_post',
  defaultCollapsed: false,
  order: 60,
  component: () => import('@plugins/seo/panels/Editor'),
});
```

The panel component receives a `usePostEditing()` hook giving live access to the post draft (title, content, meta) and an `update()` function. Plugins persist their data into the post's `meta` JSONB column under their slug-prefixed namespace.

### Publish flow

Publishing is a state transition, not an HTTP verb. The button in the corner is a *menu*:

- **Save draft** (default for new)
- **Submit for review** (if user lacks `publish_posts`)
- **Publish** (if user has `publish_posts`)
- **Schedule…** (date picker — sets `status=future, publish_at=…`)
- **Save & duplicate**

Pre-publish checklist (modal) — extensible:

```ts
PrePublishCheck.register({
  id: 'seo.title-length',
  resource: 'post',
  severity: 'warning' | 'blocker',
  check: (post) => post.meta['seo.title'].length <= 60 ? null
                  : t('SEO title is too long ({len} chars)', { len: ... }),
});
```

Core ships checks for: missing excerpt, missing featured image (if theme requires), no categories, broken internal links.

### Term editor

A compact two-pane: existing terms on the left as a list, "Add new" form on the right (WP's pattern, kept because it works for power users). Hierarchical taxonomies show a tree.

### User profile

Tabs: Profile · Account · Sessions · API keys · Roles · Preferences. The "Sessions" tab lists active sessions (browser, IP, last active) with a revoke button (see §3.4).

### Media detail

A slide-over drawer (not a route change) showing: large preview, alt text, caption, title, dimensions, file size, EXIF (collapsed), regenerate-thumbnails button, "Used in" — a list of posts that embed this attachment (computed by the media service, doc 07).

---

## 2.5 Site editor (block themes)

When the active theme declares `"capabilities": { "fse": true }` in its `theme.json`, the Design menu shows **Site editor** instead of **Customize**.

This screen is *similar to* the post editor but the document it edits is a **template** or **template part**, not a post. The data flow is different:

| | Post editor | Site editor |
|---|---|---|
| Document | A post row | A template or template-part row |
| Storage | `posts.content_blocks` (JSONB) | `templates.blocks` / `template_parts.blocks` |
| Save | `PUT /api/v1/posts/{id}` | `PUT /api/v1/templates/{id}` |
| Preview context | Single post view | Front-page / archive / single mock with sample data |
| Allowed blocks | All registered | All + template-specific (Query Loop, Site Title, Nav…) |

### Navigation

```
┌──────────────────────────────────────────────────────────────────┐
│  Sidebar (left, collapsible):                                    │
│    Templates                                                     │
│      ├── index                                                   │
│      ├── single                                                  │
│      ├── single-post                                             │
│      ├── archive                                                 │
│      ├── 404                                                     │
│      └── + Add new                                               │
│    Template parts                                                │
│      ├── header                                                  │
│      ├── footer                                                  │
│      └── sidebar                                                 │
│    Patterns                                                      │
│    Styles                  (global theme.json overrides)         │
│    Pages                   (editing landing pages directly)      │
│                                                                  │
│  Canvas (center): live preview with editing                      │
│  Inspector (right): selected-block settings                      │
└──────────────────────────────────────────────────────────────────┘
```

### Styles

A dedicated tab editing the *user's overrides* of the theme's `theme.json`. Saved to a `global_styles` row, merged with theme defaults at render. See doc 03 for the merge rules.

---

## 2.6 Settings

### Page structure

All settings pages share one form shell: a left rail of sections, a right column of fields, a sticky save bar. Pages load instantly because the form is rendered from a **schema**, not coded per page.

### Settings registry

Every setting is declared with JSON Schema + UI hints. Core ships the WP settings; plugins/themes extend.

```ts
Settings.register({
  page: 'general',
  section: 'identity',
  key: 'site.title',
  schema: { type: 'string', maxLength: 80, default: 'My Site' },
  ui: { widget: 'text', label: t('Site title'), description: t('Shown in the browser tab') },
  capability: 'manage_options',
});

Settings.register({
  page: 'reading',
  section: 'frontpage',
  key: 'reading.front_page',
  schema: { type: 'string', enum: ['posts', 'page'] },
  ui: { widget: 'radio', label: t('Your homepage displays') },
  capability: 'manage_options',
});
```

The Go server holds the canonical registry and exposes:

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/settings/schema?page=general` | Returns the JSON Schema + UI hints for that page. |
| `GET /api/v1/settings?page=general` | Returns current values. |
| `PUT /api/v1/settings` | Body: `{ "site.title": "...", ... }`. Validates against schema. |

### Core pages and notable fields

| Page | Sections | Sample keys |
|---|---|---|
| General | Identity, Locale, Date/time | `site.title`, `site.tagline`, `site.icon`, `site.url`, `general.timezone`, `general.date_format` |
| Writing | Defaults, Posting | `writing.default_category`, `writing.default_format`, `writing.editor` |
| Reading | Front page, Feeds, Visibility | `reading.front_page`, `reading.posts_per_page`, `reading.feed_items`, `reading.search_engine_visible` |
| Discussion | Comments, Avatars, Moderation | `discussion.allow_comments`, `discussion.require_login`, `discussion.moderation_words` |
| Media | Sizes, Organization | `media.thumbnail_size`, `media.medium_size`, `media.organize_by_date` |
| Permalinks | Structure | `permalinks.post_structure`, `permalinks.category_base`, `permalinks.tag_base` |
| Privacy | Privacy page, Data retention | `privacy.policy_page_id`, `privacy.cookie_banner` |

### Why JSON-Schema-driven

- **One renderer** for the entire admin's forms (`<SchemaForm schema={...} value={...} onChange={...} />`).
- **OpenAPI exposes** the same schemas for free.
- **The CLI** (`gonext option get site.title`) uses the same schemas to validate.
- **Plugins ship their settings page** by registering schemas — no UI code required.

---

## 2.7 Plugins & themes admin

### `/admin/plugins`

Shell similar to a list screen, but cards instead of rows (plugins have icons and screenshots).

```
┌──────────────────────────────────────────────────────────────────┐
│  Installed (8) | Active (5) | Updates (2) | All                  │
│  [ search ]                                              [+ Add] │
├──────────────────────────────────────────────────────────────────┤
│  ┌───────┐  Plugin name  v1.2.3                  ● Active        │
│  │ icon  │  Short description                                    │
│  └───────┘  by Author · 12,345 installs                          │
│            Update available → 1.3.0    [Update] [Deactivate]…    │
│  …                                                               │
└──────────────────────────────────────────────────────────────────┘
```

### Install flow

`+ Add` opens a panel with two tabs:

1. **Marketplace** — browses `/api/v1/marketplace/plugins` (federated registry, see doc 02). Cards link to detail pages with screenshots, changelog, capability list, ratings.
2. **Upload** — drop a `.zip` (or paste a registry URL). Server validates the bundle against the plugin manifest schema (doc 02).

### Capability review modal

Critical for security: WASM plugins request capabilities (DB scope, HTTP egress allowlist, KV access, hook subscriptions). Before activation we display:

```
┌──────────────────────────────────────────────────────────────────┐
│  Activate "SEO Pro" v1.3.0?                                      │
│                                                                  │
│  This plugin requests the following permissions:                 │
│                                                                  │
│   ● Read posts                                                   │
│   ● Read & write post meta (namespace: seo.*)                    │
│   ● Make HTTP requests to:                                       │
│       api.seo-pro.example                                        │
│       www.googleapis.com (sitelinks endpoint)                    │
│   ● Run on hooks: post.save, post.publish                        │
│   ● Schedule background jobs (rate limit: 10/min)                │
│                                                                  │
│  This plugin is NOT requesting:                                  │
│   ● Access to users, comments, or media bodies                   │
│   ● Filesystem or shell                                          │
│                                                                  │
│  [ Cancel ]                                  [ Review code ▾ ]   │
│                                              [ Activate ]        │
└──────────────────────────────────────────────────────────────────┘
```

Capability requests come from the plugin's signed manifest. Mismatch between manifest and runtime request = activation refused.

### Updates

Updates check is a cron job (Asynq). New version → notification in the inbox. Auto-update is opt-in per plugin and per minor/major.

### Themes admin

`/admin/themes` is similar but presents themes as a gallery. "Activate" is a one-click switch; classic theme → Site Editor is hidden, customizer is shown. Theme delete is blocked if active or a parent of the active child theme.

---

## 2.8 Users admin

### List

Same `<ResourceList>` shell. Columns: avatar, name, email, role(s), last login, posts. Filters: role, status (active, suspended, invited).

### Create / invite

Two flows:

- **Create with password** — admin sets a password.
- **Invite by email** — sends a magic-link token (one-time, 7 days). The user sets their own password.

### Roles & capabilities matrix

A separate screen at `/admin/roles`:

Six roles ship by default — slugs (lowercase) are `super_admin`, `administrator`, `editor`, `author`, `contributor`, `subscriber`. Doc 06 §6.1 is the canonical seeded role list; doc 08 §7.3 must use the same slugs when mapping imported WP users. (Fixed per review C12 — include `super_admin`; standardize on `administrator` slug.)

```
┌──────────────────────────────────────────────────────────────────────┐
│  Roles:  Super admin · Administrator · Editor · Author · Contrib ·   │
│          Subscriber       + Custom role                              │
├──────────────────────────────────────────────────────────────────────┤
│  Capability               SAdm  Admin  Edit  Auth  Contrib  Subs     │
│  ────────────────────────  ──    ──    ──    ──     ──     ─        │
│  manage_network            ✓     ·     ·     ·      ·      ·         │
│  manage_options            ✓     ✓     ·     ·      ·      ·         │
│  manage_users              ✓     ✓     ·     ·      ·      ·         │
│  manage_plugins            ✓     ✓     ·     ·      ·      ·         │
│  edit_posts                ✓     ✓     ✓     ✓      ✓      ·         │
│  publish_posts             ✓     ✓     ✓     ✓      ·      ·         │
│  edit_others_posts         ✓     ✓     ✓     ·      ·      ·         │
│  delete_others_posts       ✓     ✓     ✓     ·      ·      ·         │
│  moderate_comments         ✓     ✓     ✓     ·      ·      ·         │
│  upload_files              ✓     ✓     ✓     ✓      ·      ·         │
│  …                                                                   │
└──────────────────────────────────────────────────────────────────────┘
```

Cells are clickable when in edit mode. The full capability catalogue lives in doc 06; this screen is its UI.

Custom roles are stored in the `roles` table (`id`, `slug`, `name`, `is_builtin`, `description`), with capabilities **normalized** into a `role_capabilities` join table — see doc 06 §6 for the canonical schema. Plugins can register both new capabilities and grant defaults to existing roles via their manifest. (Fixed per review C13 — capabilities are a normalized join, not a JSONB column.)

### Profile

Fields: display name, email, bio, locale, avatar (Gravatar fallback or upload), website, social URLs, app passwords (legacy WP compat), API keys (see §3.4).

---

## 2.9 Notifications / inbox

WP's "admin notices" pattern is one of the worst things about it: plugins inject dismissable yellow boxes on every page. We replace it with a unified inbox.

### Model

```go
type Notification struct {
    ID         uuid.UUID
    UserID     *uuid.UUID  // null = broadcast to all users with capability
    Capability string      // filter recipients by capability (e.g. 'manage_plugins')
    Kind       string      // 'system' | 'plugin' | 'job' | 'security'
    Source     string      // 'core' | plugin slug
    Severity   string      // 'info' | 'success' | 'warning' | 'error'
    Title      string
    Body       string      // markdown
    Actions    []Action    // [{label, href|action}]
    CreatedAt  time.Time
    ReadAt     *time.Time
    DismissedAt *time.Time
}
```

### UI

- Bell icon in the top bar with unread badge.
- Click → popover with the 10 most recent + "View all" link to `/admin/inbox`.
- Inbox page is a `<ResourceList>` (filters: severity, source, read/unread).

### Sources

| Source | Examples |
|---|---|
| Core | Update available, low disk space, scheduled backup failed |
| Job | "Imported 1,234 of 2,000 posts (61%)" — updates in place |
| Plugin | SEO Pro: 12 posts need attention |
| Security | New login from unrecognized device |

### Plugin API

```ts
await Notifications.send({
  capability: 'manage_seo',
  severity: 'warning',
  title: 'SEO scan found 12 issues',
  body: '...',
  actions: [{ label: 'Review', href: '/admin/seo-pro/issues' }],
});
```

Rate-limited per plugin (default 20/hour) to prevent the WP-style notice spam.

---

## 2.10 Onboarding wizard

First-run flow at `/admin/setup`. The server detects "no users exist" or "setup not complete" and forces this route until done.

### Steps

1. **Welcome** — language, site title, tagline.
2. **Admin account** — email, password (or SSO connect).
3. **What is this site?** — blog · business · store · portfolio · custom. Adjusts defaults (front-page type, sample content, suggested plugins).
4. **Theme** — pick from 3 curated themes or skip (default theme). Inline previews.
5. **Sample content** — checkbox: install sample posts/pages so the empty state isn't scary. Defaults to *on*.
6. **Optional plugins** — based on Step 3 (e.g. "Store" → suggest commerce plugin).
7. **Done** — confetti, link to dashboard.

### Why this matters

WordPress's "Famous 5-minute install" is famous because every other CMS's onboarding was worse, not because it was good. The empty post list, the bewildering settings, the unconfigured permalinks — that's where most new admins bounce. We give them a working site after step 5.

The wizard is itself a Next.js route group `/admin/(setup)/` and is the only place an unauthenticated session can reach an admin path.

---

## 2.11 UI framework recommendation

### Recommendation: **shadcn/ui + Tailwind + Radix primitives**, with **React Aria Components** as fallback for any primitive Radix doesn't cover (table, grids).

### Why

| Option | Pros | Cons | Verdict |
|---|---|---|---|
| **Mantine** | Batteries-included, great DX, native CSS-in-JS, dark mode, hooks. | We don't own the components (must theme around them), the table component is heavy, Mantine v7 still settling. | Strong runner-up. |
| **shadcn/ui + Tailwind + Radix** | We own the source of every component (copied into our repo), Radix gives accessible primitives, Tailwind is the lingua franca, easy to fork. | Slightly more manual setup; no built-in form/table — we build on TanStack. | **Picked.** |
| **Custom + React Aria + Tailwind** | Maximum control, accessibility-first. | We build everything from scratch. Too much surface area for v1. | Reject for v1; revisit for v2 if shadcn becomes a constraint. |
| **Material UI** | Mature. | Looks like Google; theming away from that is a fight; bundle weight. | Reject. |
| **Chakra** | Pleasant. | Project momentum has slowed; v3 churn. | Reject. |

### Justification

The admin is the *highest-leverage* surface for design taste. We need to own every pixel without writing CSS for every box-shadow. shadcn's "copy the component into your repo" model means the framework can't deprecate us. Tailwind compiles to a small CSS file. Radix gives us accessibility for free.

### Tokens and theming

```ts
// theme.css (generated)
:root {
  --bg: 0 0% 100%;
  --fg: 240 10% 4%;
  --primary: 220 90% 56%;
  --border: 240 6% 90%;
  --radius: 0.5rem;
  ...
}
.dark {
  --bg: 240 10% 4%;
  --fg: 0 0% 98%;
  --primary: 220 90% 66%;
  --border: 240 4% 16%;
}
```

Plugins ship components via the import map and reuse the same tokens. We *do not* ship a JS-based theming API for plugins; they style with classnames/tokens or use their own CSS, scoped via Shadow DOM in heavily-stylesheet-conflicting cases.

### Dark mode

System default + per-user override. Toggle in the top bar. Persisted in `user_preferences`.

### Accessibility baseline

- WCAG 2.1 AA target.
- Keyboard nav for every action (the entire admin is operable without a mouse).
- Visible focus rings (never `outline: none`).
- Reduced-motion respected.

---

## 2.12 Routing and layout

### App structure

```
apps/admin/                       (Next.js 14, App Router)
  app/
    (setup)/
      setup/page.tsx              onboarding
    (admin)/
      layout.tsx                  shell (sidebar, top bar, providers)
      page.tsx                    dashboard home
      posts/
        page.tsx                  list
        new/page.tsx              editor (new)
        [id]/page.tsx             editor (existing)
      pages/...
      media/...
      themes/...
      site-editor/...
      plugins/...
      users/...
      settings/[page]/page.tsx
      tools/...
      inbox/page.tsx
    api/
      [...proxy]/route.ts         (optional) BFF that adds CSRF and forwards to Go
  components/
  lib/
```

### Code-splitting

Each top-level area is its own route segment, which Next code-splits by default. The block editor is a heavy chunk and is dynamic-imported only on `/admin/posts/[id]` and `/admin/site-editor`.

### Shell

The admin shell is a single layout component holding: sidebar nav, top bar (search, notifications, profile, new), and the main content area. It is *not* re-rendered on navigation; only the inner segment changes.

```
┌──────────┬──────────────────────────────────────────────────────┐
│          │  Top bar (sticky)                                    │
│ Sidebar  ├──────────────────────────────────────────────────────┤
│ (sticky) │                                                      │
│          │  <Outlet />                                          │
│          │                                                      │
└──────────┴──────────────────────────────────────────────────────┘
```

### Same app or separate?

The admin is a separate Next.js app from the public site. Reasons:

1. **Different bundle profiles.** The public site optimises for first paint (RSC, minimal client JS); the admin is a heavy SPA. Forcing one bundle to satisfy both is a permanent compromise.
2. **Different deploy cadence.** Admin can ship daily; the public render must roll forward carefully (caches, ISR).
3. **Different auth.** Admin cookies, public read-mostly.
4. **Different surface area for plugins.** Plugin UI extensions only load in admin; we shouldn't ship them to anonymous readers.

Counter-argument: dev experience is simpler with one app (one server, shared types). Mitigated by a monorepo (pnpm workspace) sharing `packages/api-types`, `packages/ui` (token + base components), and `packages/sdk`.

### URL conventions

| Path | Notes |
|---|---|
| `/admin` | Dashboard home |
| `/admin/{section}` | Section landing (list or main) |
| `/admin/{section}/{id}` | Edit |
| `/admin/{section}/new` | Create |
| `/admin/{section}?q=…` | Search/filter (URL-synced) |
| `/admin/setup` | Onboarding (auto-redirect when needed) |
| `/admin/login` | Login (only path reachable unauthenticated other than setup) |

---

## 2.13 State management

Two libraries, one rule:

> **Server state lives in TanStack Query. Ephemeral UI state lives in Zustand. Nothing else.**

### TanStack Query for server state

- One `<QueryClient>` per admin app, mounted in the shell.
- Keys: `['posts', { status, page }]`, `['post', id]`, `['settings', page]`.
- Stale-while-revalidate by default; mutations call `invalidateQueries`.
- Server-Sent Events (`GET /api/v1/events`) push invalidations: when another tab publishes a post, our cache is invalidated.

```ts
const { data, isLoading } = useQuery({
  queryKey: ['posts', { status: 'draft', page }],
  queryFn: () => api.get('/posts', { params: { status: 'draft', page } }),
  staleTime: 30_000,
});
```

### Zustand for UI state

- Per-feature stores: `useEditorUI`, `useListSelection`, `useNavCollapsed`.
- Local only, never persisted unless explicitly (sidebar width, theme).
- No global "everything" store — that's a 2018 anti-pattern.

```ts
const useEditorUI = create<EditorUIState>((set) => ({
  rightPanelOpen: true,
  setRightPanelOpen: (v) => set({ rightPanelOpen: v }),
}));
```

### Why not Redux / Jotai / Recoil

- Redux is too much ceremony for an app that mostly mirrors server state.
- Jotai is great but TanStack Query subsumes the "shared async state" use case we'd otherwise reach for atoms for.
- Zustand is the smallest thing that solves the remaining problem.

### Forms

`react-hook-form` + zod resolver, validated against zod schemas generated from the server's JSON Schema (a small build-step in `packages/api-types`).

---

## 2.14 i18n

### Library: **next-intl**

- ICU MessageFormat under the hood.
- Supports plurals, gender, nested format, dates, numbers, currencies.
- Loads message catalogues per locale; server-renders translated.

### Storage

Catalogues live in `apps/admin/messages/{locale}.json`. Each key is namespaced (`admin.posts.title`, `editor.publish.button`).

### Plugin/theme translations

Plugins ship a `translations/` folder in their bundle:

```
my-plugin/
  translations/
    en.json
    fr.json
    de.json
```

On plugin load, the admin merges the plugin's catalogue into the runtime under a namespace (`plugin.my-plugin.*`). Plugin code uses:

```ts
import { useTranslations } from '@gonext/sdk/i18n';
const t = useTranslations('plugin.my-plugin');
t('settings.label');
```

### Server-side strings

Validation errors, email templates, and CLI strings are translated on the server. Go uses `golang.org/x/text/message` with the same ICU-style catalogues. A build step (`pnpm i18n:sync`) ensures the two sides stay aligned via a shared key list.

### Locale detection

1. URL prefix `/admin/{locale}/…` — explicit (preferred).
2. User preference (per-account setting).
3. `Accept-Language` header.
4. Site default.

### RTL

Tailwind has `dir="rtl"` support via the `rtl:` variant. Admin layout uses logical properties (`ms-`, `me-`, `ps-`, `pe-`) so RTL is automatic.

---

# PART B — API SURFACE

## 3.1 REST API

### Philosophy

- **HTTP-resourceful, not RPC.** Verbs are HTTP verbs. Resources have stable URLs.
- **Predictable.** Same conventions across every resource so SDKs can be tiny.
- **Embed, don't N+1.** Clients can opt into joining related resources.
- **Versioned.** `/api/v1/...` from day one (see §3.7).
- **Boring before clever.** We adopt JSON:API-ish conventions but don't ship full JSON:API — the spec is overkill.

#### SEO meta in the post resource

The REST API exposes the **`core.seo.*` meta keys** (see doc 01 §3.3 for the canonical key list and JSON Schema) as part of every post / page / CPT resource — these include canonical title, description, canonical URL, OpenGraph fields, robots directives, schema.org overrides, and breadcrumb hints. SEO meta is **core schema**, not a plugin extension: every install gets the same `core.seo.*` field group on every public content type. The bundled `gn-seo` reference plugin (doc 02) enriches this surface — it adds analysis, sitemap generation, structured-data emission — but it does *not* own the meta keys. Removing or disabling `gn-seo` does not lose data, since the values live in `posts.meta` under the core namespace. Clients can read/write these fields with the standard sparse-fieldset projection (`?fields=meta.core.seo`). (Fixed per review gap A12.)

### URL conventions

| Pattern | Meaning |
|---|---|
| `/api/v1/{collection}` | List + create |
| `/api/v1/{collection}/{id}` | Read + update + delete |
| `/api/v1/{collection}/{id}/{sub}` | Sub-resource list |
| `/api/v1/types/{type}/items` | Generic CPT access (e.g. `/api/v1/types/product/items`) |
| `/api/v1/me` | Current user shortcut |
| `/api/plugins/{slug}/...` | Plugin-registered endpoints (sandbox, see doc 02) |
| `/api/_internal/...` | Admin-only, undocumented, cookie auth required |
| `/wp-json/wp/v2/...` | WordPress REST compatibility shim — canonical (§3.3) |
| `/api/wp-json/wp/v2/...` | WP REST shim — secondary alias for internal tooling (§3.3) |

### Core endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/posts` | List posts (filterable) |
| `POST` | `/api/v1/posts` | Create post |
| `GET` | `/api/v1/posts/{id}` | Read |
| `PUT` | `/api/v1/posts/{id}` | Replace |
| `PATCH` | `/api/v1/posts/{id}` | Partial update |
| `DELETE` | `/api/v1/posts/{id}` | Move to trash; `?force=true` to hard-delete |
| `POST` | `/api/v1/posts/{id}/revisions` | Snapshot |
| `GET` | `/api/v1/posts/{id}/revisions` | List revisions |
| `POST` | `/api/v1/posts/{id}/restore` | Restore from trash or revision |
| `GET` | `/api/v1/pages` … | (same shape) |
| `GET` | `/api/v1/types/{type}/items` | CPT list |
| `GET` | `/api/v1/taxonomies` | Registered taxonomies |
| `GET` | `/api/v1/taxonomies/{tax}/terms` | Terms |
| `POST` | `/api/v1/taxonomies/{tax}/terms` | Create term |
| `GET` | `/api/v1/media` | List media |
| `POST` | `/api/v1/media` | Upload (multipart or signed URL flow) |
| `GET` | `/api/v1/media/{id}` | Detail |
| `GET` | `/api/v1/comments` | List |
| `POST` | `/api/v1/comments` | Create |
| `PATCH` | `/api/v1/comments/{id}` | Moderate |
| `GET` | `/api/v1/users` | List |
| `POST` | `/api/v1/users` | Create / invite |
| `GET` | `/api/v1/users/{id}` | Read |
| `GET` | `/api/v1/me` | Current user |
| `PUT` | `/api/v1/me/preferences` | UI prefs |
| `GET` | `/api/v1/roles` | List roles |
| `PUT` | `/api/v1/roles/{slug}` | Update role |
| `GET` | `/api/v1/settings?page=…` | Get settings |
| `PUT` | `/api/v1/settings` | Update |
| `GET` | `/api/v1/settings/schema` | JSON Schema (all pages, by page) |
| `GET` | `/api/v1/plugins` | Installed plugins |
| `POST` | `/api/v1/plugins` | Install (multipart .zip or `{ source: 'marketplace', id }`) |
| `POST` | `/api/v1/plugins/{slug}/activate` | Activate (requires capability ack) |
| `POST` | `/api/v1/plugins/{slug}/deactivate` | Deactivate |
| `DELETE` | `/api/v1/plugins/{slug}` | Uninstall |
| `GET` | `/api/v1/themes` | Themes |
| `POST` | `/api/v1/themes/{slug}/activate` | Activate |
| `GET` | `/api/v1/templates` | Templates (FSE) |
| `PUT` | `/api/v1/templates/{id}` | Update template |
| `GET` | `/api/v1/site/at-a-glance` | Dashboard summary |
| `GET` | `/api/v1/site/health` | Health checks |
| `GET` | `/api/v1/activity` | Recent events |
| `GET` | `/api/v1/jobs` | Background jobs status |
| `GET` | `/api/v1/jobs/{id}` | One job |
| `GET` | `/api/v1/events` | SSE stream of resource events |
| `POST` | `/api/v1/webhooks` | Register a webhook |
| `GET` | `/api/v1/webhooks` | List |
| `DELETE` | `/api/v1/webhooks/{id}` | Remove |
| `POST` | `/api/v1/auth/login` | Cookie login |
| `POST` | `/api/v1/auth/logout` | |
| `POST` | `/api/v1/auth/refresh` | Refresh JWT |
| `POST` | `/api/v1/tokens` | Mint API key |
| `GET` | `/api/v1/tokens` | List own API keys |
| `DELETE` | `/api/v1/tokens/{id}` | Revoke |

### Pagination — cursor by default

```
GET /api/v1/posts?limit=25&cursor=eyJpZCI6...
```

Response:

```json
{
  "data": [ ... 25 items ... ],
  "page": {
    "limit": 25,
    "next": "eyJpZCI6...",  // opaque, decodes to (sort_field, id) tuple
    "prev": null,
    "total_estimated": 1234   // best-effort, may be omitted for large sets
  }
}
```

- Cursors avoid the offset-pagination tax (`OFFSET 10000` is slow in Postgres).
- For backwards-compat with WP REST (which uses `page=N`), the shim translates page numbers to cursor approximations.

### Filtering

URL query params, predictable shape:

```
GET /api/v1/posts?status=draft,published&author=42&category=12&date_after=2025-01-01
```

Operators (when needed): `field[op]=value`, e.g. `views[gte]=100`. We only support a small set: `eq`, `neq`, `in`, `nin`, `gte`, `lte`, `like` (server-escapes wildcards).

Free-text:

```
GET /api/v1/posts?q=hello%20world
```

### Sorting

```
GET /api/v1/posts?sort=-date,title
```

Comma-separated; `-` prefix = descending. Only fields the resource declares sortable (returned in `OPTIONS /api/v1/posts`).

### Field selection (sparse fieldsets)

```
GET /api/v1/posts?fields=id,title,date
```

Saves bytes; honoured everywhere.

### Embedding

```
GET /api/v1/posts/42?embed=author,featured_media,terms.category
```

Each embedded relation appears under `_embedded.{name}`. Capped at depth 2 to prevent abuse.

```json
{
  "id": 42,
  "title": "Hello",
  "_embedded": {
    "author": { "id": 7, "name": "Ada" },
    "featured_media": { "id": 88, "url": "..." },
    "terms.category": [{ "id": 12, "name": "Notes" }]
  }
}
```

### Errors

A single shape, RFC 7807-flavoured:

```json
{
  "error": {
    "type": "https://errors.gonext.dev/validation",
    "title": "Validation failed",
    "status": 422,
    "code": "validation_error",
    "detail": "title is required",
    "fields": {
      "title": ["required"]
    },
    "trace_id": "..."
  }
}
```

### Response envelope (or not)

We do **not** wrap successful single-resource responses; `GET /posts/42` returns the post object directly. Collection responses are wrapped (`data` + `page`). This is the JSON:API/HAL compromise: less noise for single objects, paging context kept tidy for lists.

### Content negotiation

`Accept: application/json` only for v1. Future-proofing for `application/vnd.gonext.v2+json` is possible (see §3.7).

### Conditional requests

`ETag` on every resource, `Last-Modified` where cheap. Clients can `If-None-Match` for cache; `If-Match` on writes to avoid lost updates.

### Idempotency

Mutating endpoints accept `Idempotency-Key: <uuid>`. Stored for 24h; replay returns the original response. Critical for uploads and external integrations.

---

## 3.2 GraphQL API

### Why both REST and GraphQL

| | REST | GraphQL |
|---|---|---|
| **Audience** | WP-migrators, mobile/IoT clients, simple scripts, the admin's quick endpoints | Modern frontends (Next.js public render), complex queries (block editor needs lots of joins) |
| **Caching** | HTTP-level (CDN-friendly) | Persisted queries + per-field cache |
| **Compat** | WP REST shim cheap to build on top | No equivalent in WP |
| **Tooling** | OpenAPI / curl / Postman | Codegen → typed hooks for FE |

Both layers sit on top of the same **service layer** in Go — neither is a transformation of the other. We never had to choose, so we don't.

### Library

**`gqlgen`** — schema-first, codegen-driven, mature, the dominant choice. Schema lives in `internal/graph/schema.graphql`; `gqlgen generate` produces resolvers stubs.

### Schema design principles

- **Schema is the source of truth.** Resolvers must satisfy it; no runtime schema mutation.
- **Connections for lists** (Relay-style) — uniform cursor pagination.
- **Interfaces for shared content** — `Node`, `Content`.
- **Unions where appropriate** — e.g. `MediaUsage = Post | Page | Template`.
- **Custom scalars** for `DateTime`, `JSON`, `URI`, `Slug`.
- **No mutations that bypass capabilities.** Every resolver runs the same authz the REST handlers do (shared middleware).

### Sample schema

```graphql
scalar DateTime
scalar JSON
scalar URI
scalar Slug

interface Node { id: ID! }

interface Content {
  id: ID!
  type: String!
  title: String!
  slug: Slug!
  status: ContentStatus!
  createdAt: DateTime!
  updatedAt: DateTime!
  author: User!
  meta(keys: [String!]): JSON
}

enum ContentStatus { DRAFT PENDING PUBLISHED SCHEDULED PRIVATE TRASHED }

type Post implements Node & Content {
  id: ID!
  type: String!
  title: String!
  slug: Slug!
  status: ContentStatus!
  createdAt: DateTime!
  updatedAt: DateTime!
  publishedAt: DateTime
  author: User!
  excerpt: String
  contentBlocks: JSON!        # canonical block tree
  contentHTML: String!        # rendered, cached
  featuredMedia: Media
  categories: [Term!]!
  tags: [Term!]!
  comments(first: Int, after: String, status: CommentStatus): CommentConnection!
  meta(keys: [String!]): JSON
  permalink: URI!
}

type Page implements Node & Content { ... }
type Media implements Node { ... }
type User implements Node { ... }
type Term implements Node { ... }
type Comment implements Node { ... }

type Query {
  node(id: ID!): Node
  me: User
  posts(
    first: Int = 20
    after: String
    status: [ContentStatus!]
    author: ID
    category: ID
    q: String
  ): PostConnection!
  post(id: ID, slug: Slug): Post
  pages(...): PageConnection!
  page(id: ID, slug: Slug): Page
  media(first: Int, after: String): MediaConnection!
  terms(taxonomy: String!, first: Int, after: String): TermConnection!
  settings(page: String!): JSON!
  site: Site!
}

type Mutation {
  createPost(input: CreatePostInput!): CreatePostPayload!
  updatePost(id: ID!, input: UpdatePostInput!): UpdatePostPayload!
  publishPost(id: ID!, at: DateTime): PublishPostPayload!
  trashPost(id: ID!): TrashPostPayload!
  uploadMedia(input: UploadMediaInput!): UploadMediaPayload!
  updateSettings(page: String!, values: JSON!): UpdateSettingsPayload!
  ...
}

type Subscription {
  resourceChanged(types: [String!]): ResourceChangedEvent!  # SSE-backed
  jobUpdated(id: ID!): Job!
}
```

### Codegen on the frontend

`graphql-codegen` produces TS types + TanStack Query hooks:

```ts
const { data } = usePostsQuery({ first: 20, status: ['DRAFT'] });
```

Operations are persisted at build time (`hash → query`) so the client sends `{ id: "abc123", variables: {...} }` to the server — saves bytes, enables a CDN allowlist of known queries, and makes ad-hoc queries from random clients fail (production hardening).

### REST ↔ GraphQL boundary

A simple rule:

- The **admin** uses **REST** for most CRUD (it's flat, well-cached, plugin endpoints sit there too) and **GraphQL** for the editor and dashboard widgets where a few queries replace dozens of REST calls.
- The **public renderer** is **GraphQL-first** — RSC components compose data fetches into a single query per page.
- **Third-party integrations** use REST + the WP shim.

---

## 3.3 WordPress REST compatibility shim

A subset of `/wp-json/wp/v2/...` mapped onto our API. **Canonical mount is the bare `/wp-json/wp/v2/...` path** — WordPress clients (mobile apps, headless frontends, integrations) hard-code this prefix, so the bare form is what actually works for migrating clients. A secondary `/api/wp-json/wp/v2/...` alias is also served for internal tooling consistency with the rest of our `/api/...` surface, but it is *not* the primary. Doc 08 owns the deep dive; here is the scope. (Fixed per review C9 — bare `/wp-json/...` is canonical, matching doc 08.)

### Supported endpoints (v1)

**Doc 08 §11.1 is the authoritative inventory** — the table below is a high-level summary; for the full per-route methods, status, and field-level emulation see doc 08. (Fixed per review C22 — doc 08 owns the deep dive and its broader list wins.)

| WP endpoint | Maps to | Notes |
|---|---|---|
| `GET /posts`, `POST /posts`, `GET/PUT/DELETE /posts/{id}` | `/api/v1/posts[...]` | Same filters; `page`/`per_page` → cursor. `POST` accepts WP's `content` (HTML) — converted to blocks server-side. |
| `GET/POST/PUT/DELETE /pages[/{id}]` | `/api/v1/pages[...]` | |
| `GET/POST /media`, `GET /media/{id}` | `/api/v1/media[...]` | Multipart upload supported. |
| `GET/POST/PUT/DELETE /<custom-type>` | `/api/v1/types/{type}/items` | For any CPT with `show_in_rest=true`. |
| `GET/POST/PUT/DELETE /categories`, `/tags` | `/api/v1/taxonomies/{tax}/terms` | |
| `GET/POST/PUT/DELETE /users`, `GET /users/me` | `/api/v1/users[...]`, `/api/v1/me` | Sensitive fields capability-gated. |
| `GET/POST/PUT/DELETE /comments` | `/api/v1/comments[...]` | |
| `GET/POST /settings` | `/api/v1/settings` | Whitelisted keys only. |
| `GET /types`, `GET /taxonomies`, `GET /statuses` | `/api/v1/types`, etc. | |
| `GET /menus`, `GET/POST/PUT/DELETE /menu-items` | internal menu service | WP 5.9+ menus emulation. |
| `GET/POST/PUT/DELETE /blocks` (reusable) | reusable-block service | |
| `GET /themes` | read-only single entry | Mimics WP envelope. |
| `GET /plugins` | read-only | Lists installed gonext plugins. |
| `GET /search` | search service | Subset of post fields. |
| `GET /wp-json/` | site-info envelope stub | Minimal discovery doc. |

### Not supported

- `/wp-json/wp/v2/block-renderer` (we have our own block render contract).
- Multisite endpoints.
- Plugin-registered REST namespaces — `/wp-json/<namespace>/<route>` returns 404 (the legacy plugin ran in PHP; we do not emulate). v1 final; see doc 08 §19 for open-question discussion of a future stub-mode.
- `xmlrpc.php` — 410 Gone.

### Response shape translation

The shim mimics WP's response keys (`title.rendered`, `content.rendered`, `excerpt.rendered`, `_links` HAL section). A small adapter layer lives in `internal/api/wpcompat/`.

### Auth

The shim accepts four auth mechanisms, listed canonically (must match doc 08 §11.4 exactly). (Fixed per review C10 — cookie + nonce IS supported; doc 08 is the authoritative deep dive and this list is kept in lock-step with it.)

1. **Cookie + `X-WP-Nonce` header** — for browser sessions migrating from WP-style integrations. The nonce is a short-lived token bound to the session; the `/wp-json/wp/v2/...` middleware validates it as a CSRF token against the active session cookie.
2. **Application Passwords** — `Authorization: Basic <user:apppass>` where the password is a generated **application password** (NOT the user's real password). The shim maps Application Passwords onto our personal-access-token system internally; doc 06 owns PAT storage. Importing existing WP Application-Password records during migration gives them a 30-day grace period.
3. **Session cookie + CSRF (our native admin flow)** — when the shim is called from our own admin UI, the standard `__Host-gn_session` cookie plus `X-CSRF-Token` (double-submit) is accepted as well.
4. **JWT bearer** — `Authorization: Bearer <jwt>` for our own API token system (see §3.4 and doc 06).

OAuth2 application-installed schemes are out of scope for v1 in the shim.

---

## 3.4 Auth

Three audiences, three auth methods:

| Audience | Method | Storage |
|---|---|---|
| Admin UI | Cookie session (`__Host-gn_session`, HTTPOnly, SameSite=Lax) | Redis-backed |
| 1st-party scripts, CLI | JWT access (15m) + refresh token (30d, rotated) | Refresh tokens in Postgres `refresh_tokens` |
| Programmatic / CI | API key (long-lived, named, scoped) | Hashed in Postgres `api_keys` |
| 3rd-party apps | OAuth2 (authorization code + PKCE) | Standard OAuth tables |

### Cookie session flow

```
Browser ──POST /api/v1/auth/login (email, password) ─────►  Server
                                                           verify password
                                                           create session in Redis (TTL 14d sliding)
                                                           sign session id with HMAC
                                                ◄──── Set-Cookie: __Host-gn_session=...

Browser ──GET /api/v1/posts (cookie attached) ──────────►  Server
                                                           verify cookie, look up session, attach user
                                                ◄──── 200 + CSRF header for next POST
```

- **CSRF**: double-submit cookie (`__Host-gn_csrf`) + `X-CSRF-Token` header on mutating requests. SameSite=Lax provides defense in depth.
- **MFA**: TOTP (RFC 6238) and WebAuthn supported; required for users with `manage_options` (configurable).
- **Session pinning**: bound to user-agent fingerprint (light: UA + accept-lang, not a full fingerprint).

### JWT + refresh

```
Client ──POST /auth/login (creds, ?token=true) ──────────►  Server
                                                ◄──── { access: "...", refresh: "...", exp: 900 }

Client ──Authorization: Bearer <access> ────────────────►  Server
                                                ◄──── 200

(access expires at 15m)

Client ──POST /auth/refresh { refresh } ────────────────►  Server
                                                           validate refresh token (single-use, rotated)
                                                ◄──── { access: "...", refresh: "...new..." }
```

- Access tokens are JWTs (HS256, hot rotation of secret via Redis). Claims: `sub`, `roles`, `caps_v` (version), `iat`, `exp`, `jti`.
- Refresh tokens are random 256-bit strings; rotated on every use; replay detection (using the old refresh after rotation) revokes the entire chain and emits a security notification.

### API keys

- Created from the Profile screen or the CLI.
- Shown once at creation. Stored as `bcrypt(key)`.
- Format: `wpc_<env>_<base32-12chars>_<base32-32chars>` — the prefix lets us identify and revoke leaked keys at the edge (CI logs, GitHub secret scanning).
- Scopes: `read`, `write`, `admin`, or a fine-grained capability list. Default to least privilege.
- Per-key rate limits and IP allowlists.

### OAuth2 (3rd-party apps)

Standard authorization code flow with PKCE. App registry under `/admin/settings/oauth-apps`. Refresh tokens, scopes, consent screen. We don't reinvent any of this.

### Session management UI

`/admin/profile` → Sessions tab lists active sessions (browser, IP, last seen, current); revoke individually or all-but-current.

### Plugin-registered REST endpoints — auth inheritance

Plugin routes mounted under `/api/plugins/{slug}/...` (see §3.1 URL conventions, and doc 02 §6.4 for dispatch) **inherit the standard API auth middleware** automatically. That means the same cookie / JWT / API-key acceptance applied to `/api/v1/...` runs in front of every plugin route — plugins do not, and cannot, bypass auth. CSRF (double-submit token on state-changing methods) and rate-limiting buckets (§3.5) apply identically.

**Capability gating per route is declared in the plugin's manifest.** Each `http.serve` route entry in the plugin manifest (doc 02 §2.2) carries a `capability` field:

- A capability slug (e.g. `manage_forms`, `view_analytics`) → the requester must hold that user-facing capability or the request is rejected with `403`.
- `null` (or omitted) → public route, no capability check (still subject to base rate limits and CSRF on writes; example: `/sitemap.xml`).

The middleware enforces this *before* the host dispatches `http.serve.{slug}` to the plugin's WASM `hook_handler`, so an unauthorized request never reaches plugin code. (Fixed per review C8 / gap B10.)

---

### ISR / cache revalidation endpoint (internal)

Core mutations call this contract after a transactional commit; it is *not* part of the public `/api/v1/...` surface.

```http
POST /internal/revalidate
Content-Type: application/json
X-GN-Signature: sha256=<hmac of body with the revalidate secret>
X-GN-Timestamp: <unix seconds>

{
  "tags":   ["post:{uuid}", "post-list:{type-slug}", "term:{uuid}", "global"],
  "paths":  ["/blog/hello-world"]      // optional, for direct path invalidation
}
```

- **Auth**: HMAC-SHA256 of the canonical body using a shared `INTERNAL_REVALIDATE_SECRET` (rotated via the same secret-management surface as webhook signing keys). Timestamp tolerance ±5 minutes; replay protection via short Redis nonce cache.
- **Caller**: the Go core, after committing a write that may invalidate rendered pages (post publish, term rename, menu reorder, settings change). Calls are emitted through the **transactional outbox** + Asynq invalidation-worker (doc 07 §15.2 / §16.2) — *not* synchronously in the request hot path.
- **Receiver**: the Next.js renderer maps tags to its `revalidateTag` calls; unrecognized tags are accepted and counted as a no-op.
- **Tag vocabulary**: doc 07 §16.1 is canonical. Common tags: `post:{id}`, `post-list:{slug}`, `term:{id}`, `term-tree:{taxonomy}`, `user:{id}`, `media:{id}`, `theme`, `nav:{menu-id}`, `global`.
- **Plugin invalidations** go through `host.cache.invalidate` (the WASM host-call surface — doc 02 owns that side); they end up writing the same outbox rows and reaching this endpoint.

(Fixed per review gap B4 — formal contract for ISR revalidation triggers.)

---

## 3.5 Rate limiting & abuse

Layered, Redis-backed (`go-redis/redis_rate` or a hand-rolled token bucket — leaning hand-rolled for control).

### Limits

| Bucket | Limit | Burst | Notes |
|---|---|---|---|
| Per-IP (anonymous) | 60/min | 120 | Public endpoints, login attempts |
| Per-IP (authenticated) | 600/min | 1200 | Generous; real users don't bother |
| Per-user | 6,000/min | 12,000 | Catches compromised cookies |
| Per-API-key | configurable, default 60/min | 2× | Per-key in `api_keys.rate_limit_rpm` |
| Per-OAuth-token | 600/min | 1200 | |
| Login attempts | 5/15min/IP+username | 0 | Lockout w/ exponential backoff + CAPTCHA |
| Password reset | 3/hour/account | 0 | |
| Plugin sandbox HTTP egress | per-plugin, set in manifest | — | See doc 02 |
| Webhook delivery | global 100/sec | — | Worker pool |

### Headers

Every response carries:

```
RateLimit-Limit: 600
RateLimit-Remaining: 581
RateLimit-Reset: 42
Retry-After: 42   (only when 429)
```

### Abuse signals

- Repeated 401s from one IP → temp ban + alert in `Security` inbox.
- 5xx spike from a plugin endpoint → flag in plugin admin.
- Bot detection: heuristic on UA + request shape; we don't ship a CAPTCHA out of the box but plugins can hook in `auth.pre_login`.

---

## 3.6 Webhooks

Outbound webhooks for integrations (Zapier, n8n, custom).

### Events

| Event | Payload |
|---|---|
| `post.created` | full post |
| `post.updated` | full post + `changed_fields` |
| `post.published` | full post |
| `post.trashed` | id, title |
| `post.deleted` | id |
| `comment.created` | comment + post snippet |
| `user.created` | user (no password) |
| `media.uploaded` | media |
| `plugin.activated` | slug, version |
| `theme.activated` | slug, version |
| (and any plugin-emitted event via `emit('plugin.{slug}.{name}')`) |

### Subscription model

```http
POST /api/v1/webhooks
Content-Type: application/json

{
  "url": "https://hooks.example.com/wpc",
  "events": ["post.published", "post.trashed"],
  "secret": "auto-generated if omitted",
  "active": true,
  "filters": { "post_type": ["post", "product"] }
}
```

### Delivery

```
POST https://hooks.example.com/wpc
X-GN-Event: post.published
X-GN-Delivery: 01J9...
X-GN-Signature: sha256=...   (HMAC of body with secret)
X-GN-Timestamp: 1715500000
Content-Type: application/json

{ "event": "post.published", "data": { ... }, "site": { "id": ..., "url": ... } }
```

- Workers (Asynq) deliver with retries: 1, 5, 30s, 2m, 10m, 1h, 6h (then dead-lettered).
- Each delivery logged (status, latency, response body truncated to 1KB) and viewable in the webhook detail screen.
- Replay button per delivery.
- Auto-disable after 50 consecutive failures + email to webhook owner.

---

## 3.7 API versioning

### URL versioning, single major

- `/api/v1/...` for as long as plausible.
- Additive changes go in v1 (new fields, new endpoints, new optional params). Field deprecation is announced via `Deprecation` and `Sunset` headers (RFC 8594) and the OpenAPI spec.
- Breaking changes are *very* rare and warrant `/api/v2/`. v1 lives in parallel for ≥12 months after v2 release; the admin migrates first, then the WP shim, then v1 is sunset.

### Per-endpoint feature flags

For experimental endpoints, namespace `/api/v1/experimental/...`. No SLA, may be removed in any minor.

### GraphQL versioning

GraphQL is versioned through schema evolution: fields are added freely, removed via `@deprecated` (visible in introspection) for ≥6 months. Persisted-query allowlists protect against accidental "all clients break when we delete X" — we keep the persisted query map for retired hashes pointing to a frozen executor.

### Header alternative (rejected)

`Accept: application/vnd.gonext.v2+json` is technically nicer but operationally worse: harder to debug, awkward in browser, breaks the casual `curl`. We log the rejection here and may revisit if v2 proves traumatic.

---

## 3.8 OpenAPI & GraphQL schema docs

### REST

- OpenAPI 3.1 spec auto-generated from Go handler annotations (we use `github.com/swaggo/swag`-style comments or generate from struct tags + a custom doc-gen pass — final choice in the build).
- Served at `GET /api/v1/openapi.json` and rendered as Swagger UI / Redoc at `/docs/api`.
- Stored at `docs/openapi.json` in the repo (CI fails if handlers and spec drift).

### GraphQL

- Schema published at `GET /api/v1/graphql/schema.graphql` (SDL) and `GET /api/v1/graphql/schema.json` (introspection).
- Sandbox at `/docs/graphql` (GraphiQL or Apollo Sandbox embed).

### SDK codegen

Both specs drive published client SDKs:

| Language | Generator | Output |
|---|---|---|
| TypeScript (REST) | `openapi-typescript` + a thin fetcher | `@gonext/sdk-rest` |
| TypeScript (GraphQL) | `graphql-codegen` | `@gonext/sdk-graphql` (used by admin + public Next.js) |
| Go | `oapi-codegen` | `github.com/gonext/sdk-go` |
| PHP | (planned) `openapi-generator` | for WP-migration tooling |

Released on every minor; semver-tagged.

---

## 3.9 CLI — `gonext`

A single Go binary mirroring WP-CLI in spirit. Talks to a local or remote server over the API (auth via API key from `~/.gonext/config.toml` or `GONEXT_TOKEN`). When run on the same host as the server, can also use a Unix socket for emergency local-only commands.

### Command surface

```
gonext <noun> <verb> [args] [--flags]
```

| Command | Purpose |
|---|---|
| `gonext login [--site URL]` | Interactive auth, stores API key |
| `gonext whoami` | Current user / token info |
| `gonext site info` | At-a-glance |
| `gonext site health` | Run all health checks |
| `gonext post list [--status=draft]` | |
| `gonext post create --title=... --status=draft` | |
| `gonext post get <id>` | |
| `gonext post update <id> --title=...` | |
| `gonext post publish <id>` | |
| `gonext post delete <id> [--force]` | |
| `gonext page ...` | (same shape) |
| `gonext media import <path> [--alt=...]` | |
| `gonext user create --email=... --role=editor` | |
| `gonext user delete <id>` | |
| `gonext user role-add <id> editor` | |
| `gonext role list` | |
| `gonext cap list <role>` | |
| `gonext plugin install <slug-or-zip>` | |
| `gonext plugin activate <slug> [--accept-caps]` | |
| `gonext plugin deactivate <slug>` | |
| `gonext plugin update [<slug>] [--all]` | |
| `gonext plugin delete <slug>` | |
| `gonext theme install/activate/delete <slug>` | |
| `gonext option get <key>` | |
| `gonext option set <key> <value>` | (validates against schema) |
| `gonext option list [--page=general]` | |
| `gonext db export [--file=backup.sql]` | Calls `pg_dump` (admin-machine only) |
| `gonext db import <file>` | |
| `gonext cache flush [--scope=fragment,fts]` | |
| `gonext search reindex` | |
| `gonext job list` | |
| `gonext job retry <id>` | |
| `gonext webhook list` | |
| `gonext webhook create --url=... --events=post.published,post.trashed` | |
| `gonext import wordpress <export.xml> [--map-authors=...]` | |
| `gonext export wordpress [--file=site.xml]` | |
| `gonext shell` | REPL with `await api.posts.list(...)` |
| `gonext tail [--filter=plugin:seo]` | Live log/event stream (SSE under the hood) |
| `gonext version` | |

### Output

Two formats:

- **Human** (default, tables, colors) for interactive use.
- **JSON** (`--format=json`) for scripting, also `--format=csv`, `--format=yaml`, `--format=tsv`.

### Design notes

- Implemented as a thin client of the REST API — never accesses the DB directly (except `db export`, which shells out to `pg_dump` on the local box). Eliminates an entire class of "the CLI did something the server doesn't know about" bugs.
- Bundled with the server binary for easy install (`gonext` is a subcommand of the main binary): `gonext-server post list` works on a server install.
- Distributed via Homebrew, apt, scoop, and a curl-install script.

---

## 4. Trade-offs & rejected alternatives

### Admin in the same Next.js app vs separate (recommended: separate)

| Pros of same app | Pros of separate app |
|---|---|
| One deploy, one repo route | Independent bundles (public stays tiny) |
| Shared auth context trivial | Independent deploy cadence |
| Lower infra cost | Easier ownership boundary |
| Easier theming reuse | Plugin UI bundles only ship to admin |

We pick **separate**. Cost is duplicated layout/auth wiring; mitigated by shared packages in the monorepo. The win is that the public renderer (the SEO-critical surface) is never penalised by admin code.

### REST-only vs GraphQL-only vs both (recommended: both)

- **REST-only**: simplest. But forces the modern frontend to do many round-trips, especially the editor and dashboards. The public Next.js render becomes painful.
- **GraphQL-only**: elegant. But cuts off WP-migrating tools, CLI uniformity is harder (curl + JSON is the lingua franca), and CDN caching becomes a science project.
- **Both**: more code to maintain, but each is small relative to the service layer, and they share authz/hooks. We are explicit that the service layer is the source of truth and the two HTTP layers are transports.

### Cookie sessions vs JWT for the admin (recommended: cookie sessions)

JWT in the admin means storing tokens in JS, which means XSS = full account theft. HTTPOnly cookie + Redis-backed session (revocable) is dramatically safer. JWTs are kept for programmatic clients where XSS isn't a concern.

### Mantine vs shadcn (recommended: shadcn)

Mantine would ship faster initially. shadcn pays off as soon as we want a non-default look — which is approximately week 3. Owning the components is the right long-term call for a project meant to host themes and plugins with strong design opinions.

### JSON:API spec adherence (rejected)

We borrow ideas (sparse fieldsets, included resources, pagination links) but skip the formal envelope, the `type`/`id`/`attributes` split, and the relationship objects. They are overkill for our audience and clash with the WP REST shim.

### gRPC for the API (rejected)

Plugins (WASM) talk to the host through a different ABI (doc 02), so gRPC's tooling story isn't a fit. HTTP/JSON is universal, debuggable, and what migrating WP users expect.

### Per-plugin admin routes vs sandboxed iframes (recommended: per-plugin routes, sandboxed bundles)

WP gives plugins the entire admin page. Some "admin pages" by plugins are full of jQuery soup. We give plugins React component slots loaded via ES module import maps; they get a window into the admin, not the whole window. Iframes considered but rejected: they're heavy, prevent shared theming, and clash with the inspector/sidebar patterns.

### Server-Sent Events vs WebSockets for live updates (recommended: SSE)

The admin needs server-push for events (cache invalidation, job progress, notifications). SSE is one-way, HTTP/2-friendly, trivial to terminate at a load balancer, and survives our existing auth stack untouched. WebSockets bought us nothing for these use cases and added a separate auth dance.

---

## 5. Open questions

1. **OpenAPI auto-generation tool.** swag annotations are noisy; deriving from struct tags is cleaner but requires our own pass. Decide in P0.
2. **Public marketplace API** — is it federated (we host an index, others can run mirrors) or centralised? Affects the install flow's TLS/identity story.
3. **Realtime collaboration on editor** — out of v1, but the API needs to *not foreclose* CRDT-based co-editing (block IDs are stable, content is a tree, so we're OK). Decide whether to ship the WebSocket plumbing in P2 or P3.
4. **Admin SSR or full SPA after login?** Current plan: full SPA after login (the admin shell mounts client-side). Worth a benchmark: would SSR-ing the first list screen meaningfully help TTI? Spike in P1.
5. **Capability versioning** in JWTs (`caps_v`). When an admin changes a role, do we revoke existing JWTs, or accept a stale window? Leaning revoke (bump `caps_v` for affected users; tokens with old `caps_v` fail). Confirm in doc 06.
6. **CLI bundling**: ship as part of the server binary, or separate? Separate is cleaner but adds a release artifact.
7. **Persisted GraphQL queries** — store only on the server, require codegen? Or accept ad-hoc queries from admin in dev and lock down in prod? Operational complexity vs flexibility.
8. **Webhook signing** — we use HMAC-SHA256 today; should we also offer asymmetric (Ed25519) signing for users who want to verify without sharing a secret? Probably v2.
9. **Multi-tenant admin** — we are single-site v1, but the admin code should not bake in `tenant_id = 1` everywhere. Audit during P1.
10. **Plugin admin route allocation** — `/admin/{section}/{plugin}/...` or `/admin/plugins/{plugin}/...`? First reads better, second is safer for collisions. Probably the latter, with a "promote to top-level" allow-list for trusted plugins.
