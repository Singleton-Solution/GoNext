# 11. Testing Strategy & CI

> Closes gap **A3** identified in `09-review-gaps.md`. Defines the testing pyramid, tooling, gates, and CI pipeline for the WordPress-clone codebase. Reader assumed: senior engineer who has set up CI for non-trivial systems. Builds on [`00-architecture-overview.md`](00-architecture-overview.md) — the system under test is the Go API + WASM plugin host, Next.js public + admin (React), Postgres, Redis, Asynq workers, plugin sandbox, theme system, block editor, and migration importer.

---

## 1. Testing Philosophy

### 1.1 Trust boundaries are where we test hardest

This codebase has a small number of trust boundaries, and almost every nasty bug we will ship in our careers lives on one. We bias test investment toward those boundaries:

| Boundary | Why it matters | Primary test type |
|---|---|---|
| **HTTP edge → Go API** | Untrusted JSON, auth, permission checks. | Contract + integration. |
| **Go host → WASM plugin** | Sandbox correctness, capability enforcement, fuel/memory caps. | Contract + integration + fuzz. |
| **WASM plugin → Go host (ABI)** | Hook bus dispatch, ABI stability across versions. | Contract. |
| **Theme template ↔ block tree** | Theme renders any block tree without crashing. | Contract (theme test suite). |
| **Block schema ↔ stored JSON** | Round-trip stability; deprecation migration. | Property + snapshot. |
| **Importer → site state** | A migrated WP site must be equivalent. | Integration (corpus replay). |
| **Domain logic ↔ database** | Transactional correctness, constraints, race. | Integration (real Postgres). |
| **React component → user** | Visual + interaction correctness. | Unit (RTL) + e2e (Playwright). |

### 1.2 Principles

1. **Fast feedback first, comprehensive feedback eventually.** Unit on every save; integration on every commit; e2e on every PR; load nightly.
2. **Mock at trust boundaries, not at module boundaries.** Mock the network at the edge of our system; do not mock our own packages against themselves. In particular: use real Postgres in tests, not an in-memory fake.
3. **Contract over implementation.** API endpoints, plugin ABI, theme manifest, block schema — these are user-facing surfaces. Their tests assert behaviour authors depend on, not internal structure.
4. **Tests are documentation that runs.** A failing test should describe a contract that a human would write down.
5. **Flakes are bugs.** A flaky test is removed or fixed within one week, not retried.
6. **No test is owned by no one.** Every suite has a `CODEOWNERS` entry.

### 1.3 What each level proves

- **Unit** — "this function returns the right value for these inputs."
- **Integration** — "this package wired to real dependencies produces the right state."
- **Contract** — "the surface we publish behaves as documented, regardless of how it's implemented."
- **End-to-end** — "a user can complete this journey."
- **Performance** — "we have not regressed against the published budgets."

---

## 2. Test Pyramid for This Codebase

```
                    ┌───────────────────┐
                    │   E2E (Playwright)│   ~50 tests, run on PR
                    │  critical journeys│   ~10 min wall clock
                    └───────────────────┘
                  ┌───────────────────────┐
                  │       Contract        │   ~500 tests
                  │ API / Plugin / Theme  │   ~5 min
                  └───────────────────────┘
              ┌───────────────────────────────┐
              │         Integration            │   ~2,000 tests
              │  Postgres/Redis/MinIO/WASM     │   ~8 min sharded
              └───────────────────────────────┘
          ┌──────────────────────────────────────┐
          │                Unit                  │   ~10,000 tests
          │   Go pure logic + React components   │   ~90 s sharded
          └──────────────────────────────────────┘
```

Rough ratio target: **70/20/8/2** (unit/integration/contract/e2e) by count. We expect contract tests to grow disproportionately as the plugin and theme ecosystems form, since that surface is where third parties depend on us.

| Type | Tooling | Trigger | Wall clock budget (full suite) |
|---|---|---|---|
| Unit (Go) | `go test`, `testify/require` | every commit, watch mode | 60 s sharded |
| Unit (React) | Vitest, React Testing Library | every commit, watch mode | 30 s sharded |
| Integration (Go) | `testcontainers-go` (Postgres, Redis, MinIO), real `wazero` | every commit | 8 min sharded |
| Contract (API) | OpenAPI test client, GraphQL schema diff, WP REST corpus | every PR | 3 min |
| Contract (Plugin) | `gonext plugin test` CLI | every PR (publish gate in marketplace CI) | 2 min |
| Contract (Theme) | `gonext theme test` CLI | every PR | 2 min |
| E2E | Playwright vs docker-compose stack | every PR | 10 min |
| Load | k6 | nightly + on `perf/*` branches | 30 min |
| Security (SAST) | gosec, govulncheck, semgrep, npm audit | every PR | 1 min |
| Security (DAST) | OWASP ZAP baseline | weekly vs staging | 20 min |
| A11y | axe-core in Playwright + theme suite | every PR | covered by e2e + contract |
| Mutation | `go-mutesting`, Stryker | nightly, informational | 60 min |

---

## 3. Go Unit & Integration

### 3.1 Tooling — recommendation

- **`testing`** stdlib for runners. Table-driven tests by convention.
- **`testify/require`** for assertions. We use `require` (not `assert`) — a failing assertion in setup makes subsequent assertions meaningless and noisy. We **do not** use `testify/suite` — it hides test boundaries and confuses parallelism.
- **`testcontainers-go`** for ephemeral Postgres / Redis / MinIO / Mailpit. The CI runner already has Docker; the cost is launch time, not engineering effort.
- **`go-cmp`** for deep diff on structured comparisons; better error messages than `reflect.DeepEqual`.
- **`gotestsum`** for CI output (JUnit XML + human-readable summary).
- **`golangci-lint`** at the lint stage (not in test pipeline).

We deliberately reject:

- **sqlmock / in-memory DB fakes.** They drift from real Postgres: missing constraints, wrong null semantics, no FTS, no JSONB operators. The class of bug they hide is exactly the class we ship to users. Cost of real Postgres is ~200 ms per suite, paid once via shared container.
- **`Ginkgo`/`Gomega`.** BDD DSL adds nothing over table-driven `testing` and obscures failures from CI parsers.

### 3.2 DB test pattern: transaction-rollback per test

Two patterns are common — schema-per-test and txn-rollback per test. We choose **txn-rollback** as the default with a documented escape hatch:

**Txn-rollback (default):**
```
Begin tx → run test → Rollback
```
- Pros: fast (~5 ms per test), no schema duplication, container reused across the whole package.
- Cons: cannot test code that itself starts transactions; cannot test code under real `COMMIT` semantics (deferred constraints, triggers firing after commit).

**Schema-per-test (escape hatch):**
```
CREATE SCHEMA test_<uuid> → apply migrations → run test → DROP SCHEMA
```
- Used in `internal/store/...` integration tests that exercise transactional code paths, the migration system itself, and the row-level security policies.
- ~150 ms setup cost per test; sharded.

A single shared `*pgxpool.Pool` is constructed once per package via `TestMain`. Tests opt into one of two helpers: `withTx(t, func(tx))` or `withSchema(t, func(db))`.

### 3.3 Concrete integration test setup

```go
// internal/store/posts/posts_integration_test.go
package posts_test

import (
    "context"
    "database/sql"
    "os"
    "testing"

    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/stretchr/testify/require"
    "github.com/testcontainers/testcontainers-go"
    tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

    "github.com/example/gonext/internal/migrations"
    "github.com/example/gonext/internal/store/posts"
    "github.com/example/gonext/internal/testkit"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
    ctx := context.Background()

    container, err := tcpg.Run(ctx,
        "postgres:15-alpine",
        tcpg.WithDatabase("gonext_test"),
        tcpg.WithUsername("test"),
        tcpg.WithPassword("test"),
        testcontainers.WithWaitStrategy(tcpg.DefaultWaitStrategy()),
    )
    if err != nil {
        panic(err)
    }
    defer container.Terminate(ctx)

    dsn, _ := container.ConnectionString(ctx, "sslmode=disable")
    pool, err = pgxpool.New(ctx, dsn)
    if err != nil {
        panic(err)
    }
    defer pool.Close()

    if err := migrations.ApplyAll(ctx, pool); err != nil {
        panic(err)
    }

    os.Exit(m.Run())
}

func TestPosts_CreateAndPublish(t *testing.T) {
    t.Parallel()
    testkit.WithTx(t, pool, func(tx pgx.Tx) {
        repo := posts.NewRepo(tx)
        ctx := context.Background()

        author := testkit.SeedUser(t, tx, testkit.UserOpts{Role: "author"})

        draft, err := repo.Create(ctx, posts.NewPost{
            AuthorID: author.ID,
            Title:    "Hello",
            Status:   posts.StatusDraft,
            Content:  testkit.MustParseBlocks(`[{"type":"core/paragraph","attributes":{"text":"hi"}}]`),
        })
        require.NoError(t, err)
        require.Equal(t, posts.StatusDraft, draft.Status)

        published, err := repo.Transition(ctx, draft.ID, posts.StatusPublished)
        require.NoError(t, err)
        require.NotNil(t, published.PublishedAt)
        require.WithinDuration(t, time.Now(), *published.PublishedAt, 5*time.Second)

        // Constraint: cannot publish a post with no slug.
        bad, err := repo.Create(ctx, posts.NewPost{AuthorID: author.ID, Status: posts.StatusPublished, Title: ""})
        require.Error(t, err)
        require.ErrorIs(t, err, posts.ErrSlugRequired)
        require.Nil(t, bad)
    })
}
```

`testkit.WithTx` rolls back unconditionally on test exit, including on panic. `testkit.SeedUser` and friends operate on the passed `tx` so seed data also rolls back.

### 3.4 Fixtures

- **Golden files** (`testdata/golden/`) for expected outputs: HTML renderings, JSON serializations, OpenAPI responses. Updated with `go test -update`.
- All JSON fixtures validated against their JSON Schema in `TestMain` of the fixtures package — a malformed fixture is itself a test failure.
- **Builder helpers** in `internal/testkit/` (`testkit.NewPost().WithBlocks(...).WithStatus(...).Build()`) — avoid `make(map[string]interface{})` smell.

### 3.5 Coverage targets

| Package class | Target | Enforced? |
|---|---|---|
| `internal/domain/*` (pure logic) | 80% lines | gate fails PR |
| `internal/store/*` | 75% lines | gate fails PR |
| `internal/plugin/*` (sandbox, ABI) | 85% lines + 100% capability matrix | gate fails PR |
| Everything else | 60% lines | informational |
| Overall | 60% lines | gate fails PR |

We measure coverage with `go test -coverprofile`. Branch coverage is **not** enforced — Go's branch coverage tooling is immature and the metric is noisy.

---

## 4. React Unit Tests (Admin + Theme Components)

### 4.1 Tooling

- **Vitest** as the runner. Faster than Jest, native ESM, compatible with most Jest APIs.
- **React Testing Library** for component rendering.
- **`@testing-library/user-event`** for interactions (typing, clicking, drag) — never fire `fireEvent.click` directly.
- **MSW (Mock Service Worker)** for API stubbing. Intercepts at the network layer; tests do not know whether the call is REST or GraphQL.
- **happy-dom** as the DOM environment (faster than jsdom for our shape).
- Snapshot testing: **inline snapshots only**. File-based snapshots rot; inline snapshots fail in code review.

### 4.2 Patterns

- **Test by user-visible role, not by selector.** `getByRole('button', { name: /publish/i })`, never `getByTestId('pub-btn')`. The exceptions are blocks identified by `data-block-id`, which is a real product contract.
- **One render per test.** Re-renders to test state changes are hard to read; prefer firing a user event that causes the re-render.
- **No `act` wrapping in test bodies.** If you need `act`, you're testing implementation. `user-event` and RTL queries already handle it.
- **Provider wrappers** are factories in `src/test/render.tsx` — `renderWithAdmin(ui, { user, capabilities })`.

```tsx
// admin/src/components/PostList.test.tsx
import { renderWithAdmin } from '@/test/render';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { server } from '@/test/msw';
import { PostList } from './PostList';

test('non-editor cannot see Bulk Delete', async () => {
  renderWithAdmin(<PostList />, { capabilities: ['edit_posts'] });
  expect(await screen.findByRole('row', { name: /first post/i })).toBeVisible();
  expect(screen.queryByRole('button', { name: /bulk delete/i })).not.toBeInTheDocument();
});

test('editor publishing a draft updates the row status', async () => {
  server.use(
    http.post('/api/posts/:id/transition', () =>
      HttpResponse.json({ id: '42', status: 'published', publishedAt: '2026-01-01T00:00:00Z' }),
    ),
  );
  renderWithAdmin(<PostList />, { capabilities: ['edit_posts', 'publish_posts'] });
  const row = await screen.findByRole('row', { name: /draft post/i });
  await userEvent.click(within(row).getByRole('button', { name: /publish/i }));
  expect(await within(row).findByText(/published/i)).toBeVisible();
});
```

### 4.3 What we don't test at this layer

- Routing (Next.js owns it). Covered in e2e.
- Server-side rendering output (covered by integration tests on the Next.js render endpoint).
- Network error messages from the real backend (covered by contract tests).

---

## 5. Block Editor Tests

The block editor has more unique testing requirements than any other subsystem because the data is structured, lossless serialization is non-negotiable, and we have to support deprecations forever.

### 5.1 Block snapshot tests

For each registered block: given a set of attributes, the server `render` output is asserted against an inline snapshot.

```ts
// blocks/core-paragraph/render.test.ts
import { renderBlock } from '@/render/server';

test('core/paragraph renders text with alignment', () => {
  expect(
    renderBlock({
      type: 'core/paragraph',
      attributes: { text: 'Hello world', align: 'center' },
    }),
  ).toMatchInlineSnapshot(`"<p class=\"has-text-align-center\">Hello world</p>"`);
});
```

A change in HTML output is a contract change and will appear in code review. Reviewers reject changes that silently alter user-visible output.

### 5.2 Block schema round-trip

```ts
// blocks/contract.test.ts
import { validate, parse, serialize } from '@/blocks/json';
import { coreBlocks } from '@/blocks/core';

for (const block of coreBlocks) {
  test(`${block.name}: round-trips through parse/serialize`, () => {
    const tree = sampleTreeFor(block);
    const json = serialize(tree);
    expect(validate(json)).toEqual({ ok: true });
    const reparsed = parse(json);
    expect(reparsed).toEqual(tree);
  });
}
```

### 5.3 Block deprecation migration tests

Every block that ships a `deprecated` array gets a test per migration:

```ts
test('core/quote v1 → v2: cite moved out of innerHTML', () => {
  const v1 = { type: 'core/quote', attributes: { value: '<p>q</p><cite>x</cite>' } };
  const v2 = migrate('core/quote', 1, v1);
  expect(v2).toEqual({
    type: 'core/quote',
    attributes: { value: '<p>q</p>', citation: 'x' },
  });
});
```

The migration runner refuses to load a block whose `deprecated[N]` doesn't have a corresponding test in the package — enforced by a CI lint step.

### 5.4 Editor interaction tests

Headless editor rendered in JSDOM via the same `BlockEditor` React component that ships in admin. Asserts the JSON tree produced by user actions.

```ts
test('inserting a heading and typing produces the expected tree', async () => {
  const { getTree } = renderEditor();
  await userEvent.click(screen.getByRole('button', { name: /add block/i }));
  await userEvent.click(screen.getByRole('option', { name: /heading/i }));
  await userEvent.type(screen.getByRole('textbox', { name: /heading/i }), 'Hi');
  expect(getTree()).toEqual([
    { type: 'core/heading', attributes: { level: 2, text: 'Hi' }, innerBlocks: [] },
  ]);
});
```

---

## 6. Theme Contract Test Suite

Themes are third-party code. We ship a **CLI test runner** as part of the SDK so theme authors get the same gate before they publish.

```
gonext theme test ./my-theme
```

### 6.1 What it asserts

For each theme package:

1. **Manifest valid** — `theme.json` validates against the published schema.
2. **Template hierarchy fallback resolves** — for every route class (`single-post-slug`, `single-post`, `single`, `index`, `archive-tag-slug`, etc.), the resolver finds a template; no fallback ends at "no template".
3. **theme.json semantics** — declared colour and spacing tokens parse; declared block style variations refer to known blocks.
4. **Block style variations apply** — for each `styles.blocks.<name>.variations.<v>`, render a sample of that block with the variation; no React warnings/errors in console; output non-empty.
5. **A11y scan** — render canonical templates (home, single post, archive, 404, search results) against a synthetic site (`testkit.KitchenSink`); run axe-core; **zero serious or critical** violations.
6. **Bundle size** — built JS for each template within the per-template budget from doc 07 §21 (warn at 80%, fail at 100%).
7. **Server-render parity** — for each template the SSR output is deterministic across two renders with the same inputs.

### 6.2 CI integration

- In the GoNext monorepo: a `themes-contract` job runs the suite against each reference theme.
- In a theme author's repo: ship a GitHub Action `gonext/theme-test-action@v1` that wraps the CLI.
- In the marketplace publish flow: the gate runs server-side before a theme can be listed.

---

## 7. Plugin Contract Tests

Same model as themes — a CLI runner, used by both authors and the marketplace.

```
gonext plugin test ./my-plugin.gnplugin
```

### 7.1 What it asserts

For each bundle:

1. **Manifest schema valid** — `plugin.json` validates against the published schema.
2. **WASM loads** within configured fuel/memory caps (no infinite init loop, no startup OOM).
3. **Declared hooks register** — for each `hooks` entry in the manifest, the host can register a handler; the symbol is exported and callable.
4. **Declared capabilities recognized** — every entry in `capabilities` is a known capability string in the current API version; unknown caps fail.
5. **Admin pages resolve** — every `adminPages` entry has a frontend ES module that can be import-resolved and exports the required `default` component.
6. **Activation migrations** apply against an empty plugin schema, and the recorded rollback returns the schema to empty.
7. **Determinism** — the manifest hash, WASM hash, and asset hashes match the values declared in the bundle's signature.
8. **Sample dispatch** — for each declared hook, call it with a generated argument matching its declared schema; assert no panic, returns within the declared time budget.

### 7.2 Runner sketch

```go
// cmd/gonext-plugintest/main.go
func runContract(ctx context.Context, bundlePath string) (Report, error) {
    bundle, err := plugin.OpenBundle(bundlePath)
    if err != nil { return Report{}, fmt.Errorf("open: %w", err) }

    var report Report
    report.Bundle = bundle.Manifest

    // 1. Manifest schema
    if err := manifest.Validate(bundle.Manifest); err != nil {
        return report.fail("manifest.invalid", err)
    }

    // 2. WASM instantiation under caps
    host := pluginhost.NewTestHost(pluginhost.Caps{
        FuelInit:     bundle.Manifest.Limits.FuelInit,
        MemoryBytes:  bundle.Manifest.Limits.MemoryBytes,
        WallClock:    2 * time.Second,
        Capabilities: bundle.Manifest.Capabilities,
    })
    inst, err := host.Instantiate(ctx, bundle.WASM)
    if err != nil {
        return report.fail("wasm.instantiate", err)
    }
    defer inst.Close()

    // 3. Hook registration
    for _, h := range bundle.Manifest.Hooks {
        if err := inst.RegisterHook(ctx, h.Name); err != nil {
            return report.fail("hook.register:"+h.Name, err)
        }
    }

    // 4. Capabilities recognized
    for _, cap := range bundle.Manifest.Capabilities {
        if !capabilities.IsKnown(cap) {
            return report.fail("capability.unknown:"+cap, nil)
        }
    }

    // 5. Admin page module resolution
    for _, p := range bundle.Manifest.AdminPages {
        if err := frontend.ResolveModule(bundle, p.Module); err != nil {
            return report.fail("admin.module:"+p.Path, err)
        }
    }

    // 6. Activation migrations + rollback
    if err := migrations.DryRunRoundTrip(ctx, bundle); err != nil {
        return report.fail("migrations.roundtrip", err)
    }

    // 7. Hashes
    if err := bundle.VerifyHashes(); err != nil {
        return report.fail("bundle.hashes", err)
    }

    // 8. Sample dispatch per hook
    for _, h := range bundle.Manifest.Hooks {
        in := samples.For(h.ArgSchema)
        if _, err := inst.Dispatch(ctx, h.Name, in); err != nil {
            return report.fail("hook.dispatch:"+h.Name, err)
        }
    }

    return report.pass(), nil
}
```

The runner returns a structured `Report{Pass bool; Checks []Check}`. The marketplace publishing API requires `Pass == true` and stores the report. CI failures link to the failing check.

---

## 8. WASM Host Tests

These are integration tests against the real `wazero` runtime, not the test host above. They prove the sandbox is real, not aspirational.

| Test | Setup | Assertion |
|---|---|---|
| **Capability gating: http.fetch denied** | Instantiate a plugin compiled to call `host.http_fetch` with **no** `http.fetch` capability. | Call returns `ErrCapabilityDenied`; no outbound socket opened (asserted by hooking the dialer). |
| **Capability gating: db scope** | Plugin with `db.kv` but not `db.posts` reads `posts` table via host. | Returns `ErrCapabilityDenied`. |
| **Fuel cap** | Plugin runs `while(true) {}` in an exported function. | Execution aborts with `ErrFuelExhausted` within fuel budget × 1.1 wall clock. |
| **Memory cap** | Plugin attempts to `memory.grow` beyond cap. | Grow returns -1; subsequent allocation traps; host returns `ErrMemoryLimit`. |
| **Wall clock cap** | Plugin sleeps via a host call (allowed) past wall-clock budget. | Host cancels context; plugin call returns `ErrDeadline`. |
| **Cross-plugin KV isolation** | Plugin A writes `kv["x"] = "secret"`. Plugin B reads `kv["x"]`. | Plugin B's read returns "not found" (KV is scoped by plugin slug + namespace). |
| **Cross-plugin hook arg isolation** | Plugin A's filter mutates a passed-by-pointer struct. | Plugin B receives the *original* value, not Plugin A's mutation, unless A's return was used as B's input (filter chain semantics). |
| **Reload safety** | Hot-reload plugin with active in-flight calls. | In-flight calls complete on old instance; new calls hit new instance; no panics. |
| **Panic containment** | Plugin's exported function calls `unreachable`. | Host returns `ErrPluginTrap` with stack; host process continues; other plugins unaffected. |

A **capability matrix test** generates one test per `(capability, host_function)` pair — must reach 100% coverage of the matrix. Adding a new host function is gated on adding its matrix entry.

---

## 9. API Contract Tests

### 9.1 OpenAPI is the source of truth

Doc 05 §3.8 already establishes that `openapi.yaml` (committed) is canonical. Tests consume it:

1. **Schema validation on responses** — at the end of each integration test, the response body is validated against the OpenAPI schema for that operation. A response field not declared in OpenAPI fails the test.
2. **Authorization matrix** — `internal/api/authz_matrix.yaml` lists every endpoint × role → expected status (200/403/404). A generated test calls each combination.
3. **Pagination boundaries** — for every list endpoint: empty result, single page, exactly-page-size, page-size + 1, very large offset, malformed cursor. Single parametrized test per endpoint via reflection over the spec.
4. **Idempotency** — endpoints declared idempotent (`POST` with `Idempotency-Key`) get a test asserting double-submit returns 200 with the original response.

### 9.2 GraphQL schema diff

PRs that change the GraphQL schema run `graphql-inspector diff` against `main`. Breaking changes (field removal, type rename, non-null relaxation in argument or strengthening in return) **fail the PR** unless the PR has the `graphql-breaking-ok` label and a linked deprecation plan.

### 9.3 WP REST shim corpus

The migration story (doc 08) commits us to mimicking WP REST. We test this by replay:

- A corpus of recorded WP REST responses (gathered from 20 real WP installs, anonymized) lives in `testdata/wp-rest-corpus/`.
- Each entry has: request line + headers, response body + headers, WP version.
- The contract test replays each request against our shim against an equivalent seeded state, and diffs the response field-by-field.
- Fields we do not promise to mirror are listed in `internal/wpcompat/shim_exceptions.yaml`; the diff ignores those keys. The exception list itself is reviewed.

---

## 10. Migration Tests

The migration story stakes our credibility. Test investment is heavy.

### 10.1 Corpus replay

Doc 08 §16 names a 10-site corpus. Each site is a tarball: WXR export + media + a `truth.json` describing expected post-import state (counts, slug → checksum map, redirect map, sample HTML similarity scores).

For each site, on every PR:

```
1. Spin up clean DB.
2. Run importer against the WXR.
3. Run the verification gate:
   - exact counts: posts, pages, taxonomies, users, media
   - sampled HTML similarity per post (>= 0.95 cosine on token set)
   - redirect coverage (every old URL has a redirect or matches new URL)
   - no orphan media, no broken internal links
4. Diff result against truth.json; differences are surfaced.
```

The truth files are golden — updated only with explicit `--update-truth` flag, which triggers a CODEOWNERS-restricted review.

### 10.2 Fuzzing

The WXR parser is fuzzed (`go test -fuzz`) against:

- Deeply nested shortcodes
- Broken HTML (unclosed tags, mismatched quotes)
- Mixed encodings within one file
- Unicode edge cases (right-to-left override, surrogate pairs)
- Pathologically large attachments

Crashes are gated; the fuzz corpus from past failures is committed under `testdata/fuzz/` so each regression has a permanent test.

---

## 11. End-to-End (Playwright)

E2E is the gate that says "no, really, a human could do this."

### 11.1 Critical user journeys

We deliberately keep this list small. Adding a journey requires arguing the value in PR review.

| Journey | Steps |
|---|---|
| **First-run** | Fresh install → signup admin → set site title → land on dashboard. |
| **Create & publish a post** | Login → New Post → add 5 blocks → save draft → preview → publish → see on public site. |
| **Install a plugin** | Admin → plugins → browse → install reference plugin → activate → assert new admin nav entry → assert hook fires. |
| **Install a theme** | Admin → themes → install → activate → assert public site uses new theme. |
| **Run an import** | Admin → tools → import → upload sample WXR → run → verify counts on dashboard. |
| **Comment on a post** | Logged-out reader → comment form → submit → moderation queue → admin approve → public visible. |
| **Password reset** | Logout → forgot password → click emailed link (Mailpit) → set new password → login. |
| **Block edit save** | Login → edit post → reorder blocks via drag → save → reload → order preserved. |
| **Permission negative** | Logged in as author → attempt to access `/admin/users` → 403. |
| **Site search** | Public site search → results page → click result → land on post. |

### 11.2 Stack

E2E runs against a `docker-compose.test.yml` stack: Postgres, Redis, MinIO, Mailpit, Go API, Next.js public, Next.js admin, an Asynq worker. Seeded by a shared `testkit.KitchenSink` fixture.

```ts
// e2e/post-publish.spec.ts
import { test, expect } from '@playwright/test';
import { signin, seed } from './support';

test('editor publishes a post end-to-end', async ({ page, request }) => {
  await seed(request, { user: { role: 'editor', email: 'ed@example.com' } });
  await signin(page, 'ed@example.com');

  await page.getByRole('link', { name: 'Posts' }).click();
  await page.getByRole('button', { name: 'New post' }).click();
  await page.getByRole('textbox', { name: 'Title' }).fill('Hello E2E');
  await page.getByRole('button', { name: 'Add block' }).click();
  await page.getByRole('option', { name: 'Paragraph' }).click();
  await page.getByRole('textbox', { name: 'Paragraph block' }).fill('Body text.');
  await page.getByRole('button', { name: 'Publish' }).click();
  await page.getByRole('button', { name: 'Confirm publish' }).click();

  await expect(page.getByText(/published/i)).toBeVisible();

  const slug = await page.getByTestId('post-permalink').getAttribute('data-slug');
  const publicPage = await page.context().newPage();
  await publicPage.goto(`/posts/${slug}`);
  await expect(publicPage.getByRole('heading', { name: 'Hello E2E' })).toBeVisible();
  await expect(publicPage.getByText('Body text.')).toBeVisible();
});
```

### 11.3 Determinism

- Every test seeds its own data via the `seed` helper, never relies on cross-test state.
- Times are mocked via a `?fakeTime=` query param in test mode, served by the API.
- Tests parallelize across workers; each worker gets a separate site (via `tenant` header) on the shared stack to avoid step-on-toes.
- Retries: `retries: 1` on CI, **0 locally**. A retry that passes on CI marks the test as flaky in the test report.

### 11.4 Visual regression

Optional, opt-in via tag `@visual`. Uses Playwright's screenshot diff against committed baselines. Limited to ~10 admin pages because baseline churn is high.

---

## 12. Load & Performance Gates

### 12.1 k6 scripts

Routes and budgets (initial, refined per release):

| Route | Scenario | p50 | p95 | p99 |
|---|---|---|---|---|
| `GET /` (homepage, ISR hot) | 100 vus, 1 min | 50 ms | 120 ms | 300 ms |
| `GET /posts/<slug>` (ISR cold) | 50 vus ramp, 30 s | 200 ms | 500 ms | 1000 ms |
| `GET /posts/<slug>` (ISR hot) | 100 vus, 1 min | 30 ms | 80 ms | 200 ms |
| `POST /admin/login` | 20 vus, 30 s | 100 ms | 250 ms | 500 ms |
| `POST /api/posts` (block editor save) | 30 vus, 1 min | 80 ms | 200 ms | 400 ms |
| `GET /api/wp/v2/posts` (REST shim) | 50 vus, 30 s | 60 ms | 150 ms | 350 ms |
| Plugin REST endpoint (with N=10 plugins) | 30 vus, 30 s | 100 ms | 250 ms | 500 ms |

Gate logic: nightly run records numbers; PR check compares the PR branch against `main`'s last nightly. Regressions > 15% on a p95 of any **hot path** route fail the PR (informational on others). Hot-path list is explicit in `perf/budgets.yaml`.

### 12.2 Bundle budgets

Per doc 07 §21 — each Next.js route has a JS bundle budget. CI runs `next-bundle-analyzer` and fails if any route exceeds 100%, warns at 90%.

### 12.3 Plugin perf regression

A dedicated k6 script measures hook bus dispatch latency with N plugins registered (N ∈ {0, 1, 10, 50}). p50 and p95 thresholds enforced; growth past linear in N fails.

---

## 13. Security Tests

### 13.1 Static

| Tool | Stage | Behaviour |
|---|---|---|
| **gosec** | lint | High/critical findings fail; medium informational. |
| **govulncheck** | lint | Any vulnerable Go stdlib/dep import fails. |
| **npm audit** | lint | High severity in `dependencies` (not `devDependencies`) fails. |
| **semgrep** with our ruleset | lint | Findings rated `error` fail; `warning` informational. |
| **trivy** for container images | build | High/critical OS CVEs fail. |

### 13.2 Dynamic

- **OWASP ZAP baseline** scan against staging weekly. Findings filed automatically.
- **`gitleaks`** scans every commit for secrets; pre-commit hook plus CI gate.
- **Dependency pinning check** — `go.sum` and `package-lock.json` must be present and consistent with `go.mod` / `package.json`. `npm ci`, not `npm install`, in CI.

### 13.3 Forward reference

Doc 13 — Security Baseline (TBW) — defines the full threat model and STRIDE per subsystem. This section enforces test coverage of items called out there.

---

## 14. Accessibility Tests

| Layer | Tool | Trigger |
|---|---|---|
| Admin pages | `@axe-core/playwright` injected into e2e | every PR |
| Theme contract suite | axe-core run server-side via JSDOM | every PR for reference themes; in CLI for authors |
| Block editor unit | `jest-axe` on rendered fixtures | every PR |

Gate: **zero serious or critical** axe violations on the admin canonical pages (dashboard, post list, post editor, plugin list, theme list, user list, settings). Moderate violations are tracked but not gating.

---

## 15. CI Pipeline Structure

### 15.1 Stages

```
                    ┌──────────────────────────────────────────────────────┐
                    │                  GitHub Actions                       │
                    │                                                       │
   PR opened ──►  ┌─┴─┐    ┌────┐    ┌─────────────┐    ┌────┐   ┌──────┐  │
                  │lint│ ─► │unit│ ─► │ integration │ ─► │e2e │ ─►│publish│ │
                  └─┬─┘    └─┬──┘    └──────┬──────┘    └─┬──┘   └──────┘  │
                    │        │              │              │               │
                    │        │              ▼              │               │
                    │        │       ┌─────────────┐       │               │
                    │        │       │  contract   │       │               │
                    │        │       │ (api/plugin/│       │               │
                    │        │       │   theme)    │       │               │
                    │        │       └─────────────┘       │               │
                    │        │                             │               │
                    │        └──────► perf (informational, nightly on main)│
                    │                                                       │
                    └─────► security (parallel to all)                      │
                    └─────► a11y (inside e2e)                               │
                    └──────────────────────────────────────────────────────┘
```

Stages execute sequentially **per category**, but categories run in parallel. A failure in `unit` short-circuits later stages for the same category. `lint` is fast (~30 s) and always runs first as a gate.

### 15.2 Required vs informational

| Stage | Required to merge? | Notes |
|---|---|---|
| lint | yes | |
| unit | yes | |
| integration | yes | sharded |
| contract (api, plugin, theme) | yes | |
| e2e | yes | |
| security (static) | yes | |
| security (dynamic) | no | weekly job |
| a11y | yes | inside e2e |
| perf | no on PR; yes on `perf/*` branches; nightly on `main` | regression triggers issue |
| mutation | no | informational |
| visual regression | no | informational |

### 15.3 Parallelization

- **Sharding**: `unit` and `integration` shard by Go package using `gotestsum --packages=...` distributed across 4 runners. React unit shards by Vitest's `--shard=k/n`.
- **E2E**: 4 Playwright workers × 2 machines.
- **Test selection on PR**: a `paths-filter` action computes the change set; the build resolver maps changed files → affected packages. Affected packages run their full suite; unaffected run only smoke tests. On `main` (post-merge) the full suite always runs.

The selection rule is conservative: any change to `internal/plugin/...`, `internal/policy/...`, `internal/migrations/...`, OpenAPI spec, or the GraphQL schema triggers the **full** suite regardless of paths-filter.

### 15.4 Artifact retention

| Artifact | Retention | Where |
|---|---|---|
| JUnit XML | 30 days | GHA artifacts |
| Coverage HTML | 30 days, latest pinned for `main` | GHA artifacts + S3 bucket `coverage.gonext.dev/<sha>` |
| Playwright traces | 14 days for failed runs only | GHA artifacts |
| k6 reports | 90 days | S3 bucket `perf.gonext.dev/<date>/<sha>` |
| Bundle analyser reports | 30 days | GHA artifacts |
| SBOM (CycloneDX) | per release tag, indefinite | release assets |

### 15.5 GitHub Actions vs alternatives

We standardize on **GitHub Actions** because the codebase lives on GitHub, the team already has runners, and the marketplace coverage saves us writing custom integrations.

We deliberately keep CI logic **tool-portable**. All test invocations go through `make` targets (`make test-unit`, `make test-integration-go`, `make test-e2e`, `make load`). The GHA workflow is a thin shell calling those targets. Migrating to Buildkite, CircleCI, or self-hosted Drone requires only rewriting the YAML — not the test invocations themselves.

Self-hosted runners on AWS are used for **e2e and load** because the docker-compose stack is too large for GHA-hosted runners' 7 GB memory. They are autoscaled with `actions-runner-controller` on a small EKS cluster.

---

## 16. Test Data

### 16.1 Seed fixtures

`testkit.KitchenSink` builds a site representative of mid-sized real-world use:

| Resource | Count | Notes |
|---|---|---|
| Posts | 100 | Mixed status: 60 published, 30 draft, 10 scheduled. |
| Pages | 10 | Including a homepage. |
| CPTs | 5 types × ~20 entries | "product", "event", "team_member", "case_study", "doc_page". |
| Taxonomies | 2 (tag, category) × ~30 terms | Hierarchical category tree depth 3. |
| Users | 20 | One of each role; rest authors/subscribers. |
| Plugins | 10 active | 3 reference (SEO, contact form, analytics), 7 stub plugins exercising different capability sets. |
| Themes | 2 installed, 1 active | One block theme, one classic. |
| Comments | 200 | Mixed approved/spam/pending; ~5 per post on average. |
| Media | 50 items | Mix of image/video/PDF; some attached, some unattached. |

`testkit.KitchenSink(t)` builds this state inside a transaction (or a fresh schema). Used by integration, contract, and e2e tests. The data is deterministic — fixed seeds, fixed UUIDs.

### 16.2 Staging data

Staging gets a nightly anonymized snapshot of production (when production exists). Anonymization pipeline:

1. Replace user emails with `<id>@anon.local`.
2. Replace user names with `Faker(seed=user_id)`.
3. Replace IPs with `0.0.0.0`.
4. Strip raw analytics blobs.
5. Re-hash sessions.

The pipeline itself is a job with its own integration tests asserting that all PII fields are scrubbed.

---

## 17. Mutation Testing

Informational only. We run **`go-mutesting`** nightly against:

- `internal/policy/...` (auth, role enforcement)
- `internal/plugin/...` (sandbox, ABI)
- `internal/migrations/...` (data correctness)

For React/TypeScript, **Stryker** runs nightly against `admin/src/components/Permission*` and `editor/src/blocks/*`.

Reports are published to `coverage.gonext.dev/mutation/<sha>`. The mutation score is **not** a gate — chasing it has well-known anti-patterns (testing equivalent mutants, brittle assertions). It is a signal to the maintainer of a critical package.

---

## 18. Local Developer Experience

### 18.1 Make targets

```
make test           # unit + small integration; the "save and look at it" target. ~2 min.
make test-unit      # unit only. ~90 s.
make test-int       # integration with testcontainers. ~5 min.
make test-contract  # all contract suites. ~3 min.
make test-e2e       # full Playwright against docker-compose. ~10 min.
make test-all       # everything except load. ~15 min.
make test-load      # k6 against local stack. ~30 min.
make test-update    # update golden files / inline snapshots.
```

### 18.2 Watch mode

`gonext dev` boots the dev stack and starts a watcher. On file save:

1. Computed the affected package set (same rule as CI's paths-filter).
2. Runs unit tests for those packages (Go and React).
3. Streams pass/fail to the terminal and the admin's dev toolbar.

Integration tests are not re-run on save (too slow); developer runs them explicitly before pushing.

### 18.3 Pre-commit hooks

A minimal `lefthook` config runs:

- `gofmt`, `goimports`
- `golangci-lint run --new-from-rev origin/main` (only changed files)
- `prettier --check`
- `eslint` (only changed files)
- `gitleaks protect --staged`

No tests run pre-commit — too slow. CI is the gate.

---

## 19. Trade-offs & Rejected Alternatives

### 19.1 testcontainers vs in-memory fakes (`sqlite`, `sqlmock`, `redismock`)

**Chosen: testcontainers.** Real Postgres, Redis, MinIO.

- **Pro real:** finds bugs that production has — column types, constraint races, JSONB operator subtleties, FTS quirks, Redis pipeline ordering. We will write code that uses Postgres-specific features (`tsvector`, `LATERAL`, `INSERT ... ON CONFLICT`). An sqlite fake fails immediately on the first such query.
- **Con real:** slower startup. Mitigated by per-package shared container + per-test transactions (5 ms overhead).
- **Pro fake:** very fast, no Docker.
- **Con fake:** every divergence between fake and real is a future production bug we lulled ourselves into thinking we tested for.

We have lost too many hours to "passed in CI, failed in prod, turned out the fake didn't model `ON DELETE CASCADE`". Not paying that tax again.

### 19.2 Playwright vs Cypress

**Chosen: Playwright.**

- **Pro Playwright:** multi-browser (Chromium/Firefox/WebKit) in-process; parallelization is first-class; tracing UI is excellent; auto-wait is robust; no proxy architecture.
- **Pro Cypress:** more mature plugin ecosystem; nicer time-travel debugger.
- **Decisive:** Playwright's parallelization across processes saves us ~3 minutes of CI per run. Cypress's worker model parallelizes test files but each worker is heavier. Webkit support also matters — a non-trivial slice of editor users are on Safari.

### 19.3 GitHub Actions vs self-hosted runners (Drone, Buildkite, Tekton)

**Chosen: GHA with self-hosted runners for heavy jobs.**

- **Pro GHA:** zero infrastructure, mature, on-call cost = 0, marketplace.
- **Con GHA:** memory/CPU caps on hosted runners are tight for our docker-compose stack; per-minute pricing on private repos.
- **Pro fully self-hosted:** cheaper at scale, no caps.
- **Con fully self-hosted:** on-call burden, secret management, runner version drift.

The compromise — GHA-hosted for unit/contract/lint, ARC-managed self-hosted for integration/e2e/load — captures the cost split (small jobs are free or trivial; large jobs are the ones that benefit from beefier hardware).

### 19.4 BDD frameworks (Cucumber, Ginkgo, godog)

**Rejected.** BDD frameworks make the test description nice and the failure trace awful. Our test reviewers and on-callers parse Go and TypeScript fluently; they do not need Gherkin between them and the assertion. The product has no non-engineer test author.

### 19.5 Property-based testing (gopter, fast-check)

**Selective.** We use property-based testing for:

- Block tree round-trip (`parse ∘ serialize == id`).
- Markdown / WXR shortcode parser.
- Permission resolver associativity (granting and revoking are commutative within a role).

We do not adopt it project-wide. It is hard to write good properties for CRUD code; the cost-benefit is bad outside parsers and pure transforms.

### 19.6 Snapshot testing as default

**Avoided as default.** Inline snapshots are fine for small, stable outputs (block render to HTML). File-based snapshots over large objects rot quickly — reviewers stop reading them and "update snapshots to fix CI" becomes muscle memory. We use inline only.

### 19.7 Test coverage as merge gate

**Adopted with caveats.** Coverage is gated only on packages where it produces reasonable behaviour (`internal/domain/*`, `internal/store/*`, `internal/plugin/*`). For packages where coverage is misleading (cmd entry points, transport adapters), it is not gated. Treating coverage as a global gate produces dishonest tests (`if err != nil { return err }` lines tested just to clear the bar).

### 19.8 Synthetic monitoring as e2e

**Separate layer.** Production has synthetic monitors (uptime checks, scripted journeys against prod). Those are not e2e tests — they are alerting infrastructure. Conflating them leads to flaky alerts because e2e tests use mocked time, anonymous users, etc. Doc 13 (security baseline) and a future doc 14 (observability) will own the synthetics.

---

## 20. Open Questions

1. **Plugin marketplace CI compute.** Running the plugin contract suite on every third-party upload is unbounded cost. Do we rate-limit per author? Charge for repeated failed submissions? Time-box per check?
2. **WP REST corpus refresh cadence.** WP itself ships updates. How often do we re-record the corpus, and from which WP versions? Suggest pinning to the last two WP minor releases.
3. **Visual regression baseline ownership.** Who reviews visual diffs — design or engineering? Today: opt-in only; if we widen this, we need an explicit owner.
4. **Theme contract suite stability vs theme author velocity.** Each new rule is a breaking change to the ecosystem. Should the suite be versioned, and themes opt into a specific suite version per their manifest's `apiVersion`?
5. **Migration corpus IP.** The 10-site corpus contains real WP content (even anonymized). Licensing? We probably need explicit author permission or fully synthetic sites.
6. **Mutation testing in CI.** If signal proves valuable on the three critical packages, do we promote it to required on those? Risk: brittle tests written to chase score.
7. **Property-based testing for plugin host.** Generating random plugin manifests + WASM-ish modules to fuzz the host instantiator might find capability-matrix gaps faster than the enumerated matrix test — worth a spike in P4.
8. **Performance budget enforcement on hot-path list.** The hot-path list is editorial. Who can edit it (PR-only? release notes only?)? We don't want PRs that loosen budgets just to land features.
9. **Local stack memory footprint.** Docker-compose with Postgres + Redis + MinIO + Mailpit + Go API + 2× Next.js + worker is ~3 GB. Developers on 8 GB laptops feel this. Worth an investigation into a "lite" profile (sqlite + in-process Asynq) for casual dev — at the cost of yet another mode to maintain.
10. **Test ownership of cross-cutting failures.** When a flaky e2e fails because the auth refactor + the import refactor + the theme refactor all landed on the same day, who triages? Suggest a weekly rotation, but only viable once headcount supports it.

---

## 21. Cross-References

- **Architecture overview**: [`00-architecture-overview.md`](00-architecture-overview.md) — system topology, stack choices.
- **Core CMS data model**: [`01-core-cms.md`](01-core-cms.md) — what `internal/store/*` tests target.
- **Plugin system**: [`02-plugin-system.md`](02-plugin-system.md) — ABI under contract test in §7.
- **Theme system**: [`03-theme-system.md`](03-theme-system.md) — contract suite in §6.
- **Block editor**: [`04-block-editor.md`](04-block-editor.md) — block tests in §5.
- **Admin & API**: [`05-admin-api.md`](05-admin-api.md) — OpenAPI source-of-truth used in §9.
- **Auth & permissions**: [`06-auth-permissions.md`](06-auth-permissions.md) — authz matrix in §9.1.
- **Media & performance**: [`07-media-performance.md`](07-media-performance.md) — bundle budgets in §12.2, perf budgets in §12.1.
- **Migration & compat**: [`08-migration-compat.md`](08-migration-compat.md) — corpus in §10.
- **Gap review**: [`09-review-gaps.md`](09-review-gaps.md) §A3 — the gap this doc closes.
- **Security baseline**: doc 13 (to be written) — referenced from §13.
