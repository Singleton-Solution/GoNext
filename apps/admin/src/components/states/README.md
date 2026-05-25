# `<states/>` — shared empty / loading / error / not-found surfaces

The five primitives in this directory are the **shared in-between
state** components for every admin surface in GoNext. They live next
to each other on purpose: empty, loading, error, and not-found are
the same family of "the page exists but the data doesn't" moments,
and the brand voice across them is the same — **calm, calibrated,
never alarming.**

## Voice rule

From `docs/design/HANDOFF.md`:

> Confident, quiet, alive. Off-brand: "Unlock seamless content
> experiences." "🚀 Supercharge your workflow!" "Hey friend!"

Apply it to state copy:

- **Empty** — invitational, not apologetic. _"An empty page is the
  best one."_, not _"You have no posts yet."_
- **Loading** — specific, not generic. _"Fetching post · 142 of
  142"_, not _"Loading..."_.
- **Error** — honest, never blame the user, mention what was
  preserved. _"Our edge in us-east-1 is having a moment. Your draft
  is saved."_, not _"Something went wrong, please try again."_.
- **Not found** — composed, not panicked. 404 isn't a system
  failure; it's a successful "no such page" outcome.

The italic-accent rule is part of every headline — one emphasised
word in Instrument Serif italic, sized 1.05em.

## When to use which

| Situation                                | Component             |
| ---------------------------------------- | --------------------- |
| List has zero items                      | `<EmptyState />`      |
| Filter / search returned nothing         | `<EmptyState variant="search" />` |
| Data is loading inside a panel           | `<LoadingState variant="card" />` or `<SkeletonCard />` |
| Data is loading inline (toolbar, button) | `<LoadingState />` (spinner) |
| Single shimmer placeholder              | `<SkeletonRow />`     |
| Paragraph-shape placeholder              | `<SkeletonText lines={n} />` |
| Async island that suspends               | `<Suspended>{children}</Suspended>` |
| Fetch / save / mutation failed           | `<ErrorState retry={…} />` |
| Resource / route doesn't exist           | `<NotFoundState />`   |

## EmptyState

```tsx
import { PenLine } from 'lucide-react';
import { EmptyState } from '@/components/states';
import { Button } from '@/components/ui/button';

<EmptyState
  icon={PenLine}
  title={<>Write your <em>first</em> post.</>}
  body="An empty page is the best one. Start with a thought, an outline, or a single sentence."
  action={
    <>
      <Button variant="primary">New post</Button>
      <Button>Import from WordPress</Button>
    </>
  }
/>;
```

Props:

- `icon` — any `LucideIcon` (component reference, not JSX).
- `title` — JSX preferred so the italic accent rule applies.
- `body` — short Geist body in `--fg-muted`.
- `action` — optional inline-flex row of `<Button>`s.
- `variant` — `"default"` (emerald-soft icon tile) or `"search"`
  (neutral paper-3 tile). Default is emerald — pick `search` when
  the moment is "your filter narrowed nothing".

## LoadingState + skeletons

```tsx
import { LoadingState, SkeletonText, SkeletonCard, SkeletonRow } from '@/components/states';

// Inline spinner with a specific label
<LoadingState label="Fetching post · 142 of 142" />

// Full panel placeholder — drop-in Suspense fallback
<SkeletonCard srLabel="Fetching dashboard" />

// Paragraph placeholder
<SkeletonText lines={3} />

// Single shimmer bar
<SkeletonRow width="mid" />
```

Variants:

- `<LoadingState />` — emerald spinner + optional Geist label.
- `<LoadingState variant="card" />` — same but renders a full
  `<SkeletonCard />` as the surface.
- `<SkeletonRow width="full|mid|short|title" />` — atomic shimmer
  bar (12px tall by default, 36px for `title`).
- `<SkeletonText lines={n} />` — n shimmer lines with the
  "first full, middle mid, last short" pattern. Clamped 1..12.
- `<SkeletonCard srLabel="…" />` — title + 3 lines + 80px tile,
  framed in a paper-2 card. Default Suspense fallback.

All four use the `gn-shimmer` keyframe (paper-3 → paper-4 → paper-3,
1.6s linear loop) defined in `app/globals.css`. The spinner uses
`gn-spin` (0.8s linear rotation). Both honour
`prefers-reduced-motion: reduce`.

## ErrorState

```tsx
import { ErrorState } from '@/components/states';

<ErrorState
  title={<>Something didn't <em>respond</em>.</>}
  body="Our edge in us-east-1 is having a moment. Your draft is saved — try again in a few seconds."
  code="err.503 · us-east-1"
  retry={refetch}
  secondaryAction={<a href="https://status.gonext.dev">Status page</a>}
/>;
```

Props:

- `title` — JSX with italic accent in `--lavender-deep`.
- `body` — honest, specific.
- `retry` — callback; renders an emerald `Retry` button.
- `secondaryAction` — optional sibling (Status page, Logs).
- `code` — optional mono error code pill ("err.503 · us-east-1").
- `retryLabel` — defaults to "Retry".

**Why lavender, not red.** Red is reserved for destructive *modal*
confirmations (Delete, Discard, Revoke). A list that failed to load
is not dangerous — it's unfinished. Lavender signals "off-nominal
but composed" — exactly the brand voice.

## NotFoundState

```tsx
import { NotFoundState } from '@/components/states';

// Default — links back to the admin home
<NotFoundState />

// Per-route customisation
<NotFoundState
  title={<>No <em>extension</em> at that slug.</>}
  body="It may have been unpublished or never existed in our catalogue."
  href="/marketplace"
  actionLabel="Browse marketplace"
  eyebrow="404 · extension"
/>

// Soft recovery via callback
<NotFoundState
  onAction={() => clearFilters()}
  actionLabel="Reset filters"
/>
```

Bigger scale (44px Archivo) than EmptyState (28px) because 404 is
typically a route-level surface, not a panel-level state.

## Suspended

```tsx
import { Suspended } from '@/components/states';
import { Suspense } from 'react';

// Default — SkeletonCard fallback
<Suspended>
  <AsyncIsland />
</Suspended>

// Spinner fallback for small islands
<Suspended fallback="spinner" srLabel="Loading filters">
  <FilterChips />
</Suspended>

// Custom fallback (one-off shapes)
<Suspended fallback={<MyCustomShape />}>
  <Thing />
</Suspended>
```

The wrapper is intentionally thin — it does not include an
ErrorBoundary. Place the brand error surface at the same nesting
level with React's `<ErrorBoundary>` (or the framework's), keeping
the two responsibilities visible.

## Composition rules

1. **One italic accent per headline.** Two if the line is long; never
   more.
2. **Don't nest brand state components.** A `<SkeletonCard />` inside
   an `<EmptyState />` is a smell — pick one or the other.
3. **Empty before error.** If a list has zero items because the
   filter narrowed everything out, that's `<EmptyState variant="search">`,
   not `<ErrorState>`. Errors are for *failure to retrieve*, not for
   "no matches".
4. **Action copy is sentence-case and verb-first.** "Add product",
   "Browse marketplace", "Reset filters" — never "Click here".
5. **No emoji in state copy.** Per the brand voice section of
   HANDOFF.md.

## Adding a new state

If you're considering a new state component:

- Can it be expressed as an `EmptyState` / `ErrorState` variant?
- Does it really live in *every* product surface, or just one?

If the answer to "every surface" is no, build it where it lives. If
yes, add it next to these — and update this README so callers know.
