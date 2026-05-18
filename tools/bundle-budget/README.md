# @gonext/bundle-budget

Bundle-size enforcement for GoNext's Next.js apps. Reads the `.next/`
build manifest, computes gzipped sizes per route, and gates CI against
budgets declared in [`budgets.yaml`](./budgets.yaml). Issue #126.

## Why

The admin (`apps/admin`) is a richer surface every release — block editor,
media library, plugin UIs. Bundle creep is the kind of regression you only
notice in aggregate, and by then the fix is "remove a feature." This tool
makes the budget explicit, surfaces the size on every PR, and refuses to
merge changes that blow through declared ceilings.

## Usage

```sh
# After a Next.js build:
node tools/bundle-budget/bin/check.js \
  --app admin \
  --next apps/admin/.next \
  --summary "$GITHUB_STEP_SUMMARY"
```

The exit code is `0` on success, `1` on a budget violation, and `2` on a
usage or manifest-parse error. The Markdown table is written to stdout in
all cases and additionally appended to `--summary` when given (the
`$GITHUB_STEP_SUMMARY` mechanism for the rendered job summary).

## Budget format

See [`budgets.yaml`](./budgets.yaml). All sizes are gzipped kilobytes.
Routes not listed are reported as warnings, not errors — adding a route
to the budget is a one-line review afterwards.

## Tests

```sh
pnpm --filter @gonext/bundle-budget test
pnpm --filter @gonext/bundle-budget typecheck
```

## Status

Advisory on CI (`continue-on-error: true`) until the first few PRs settle
the noise. A follow-up issue will flip the job to required on the branch
protection rule.
