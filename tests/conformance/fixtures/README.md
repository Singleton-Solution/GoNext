# Conformance suite fixtures

This directory holds user-supplied JSON fixtures the conformance runner
can replay via
[`conformance.LoadFixtureScenarios`](../../packages/go/plugins/conformance/runner.go).

## Format

Each `*.json` file is a one-scenario [`Report`](../../packages/go/plugins/conformance/scenario.go):

```json
{
  "bundle": "examples/plugins/seo",
  "suite": "conformance",
  "pass": true,
  "results": [
    {
      "name": "init.expected-trace",
      "status": "pass",
      "message": "expected init produces this trace",
      "events": [
        { "kind": "gn_cron_register", "args": { "...": "..." } },
        { "kind": "gn_audit_emit", "args": { "...": "..." } }
      ]
    }
  ]
}
```

The replay scenario constructed from a fixture loads the file, drives
the canonical synthetic init through `fakehost.Host`, and asserts the
recorded trace's per-kind event counts match the fixture's. Stricter
matching (per-arg, ordered) is a follow-up — the current shape is the
minimum useful fixture for catching regressions like "the plugin
stopped emitting its audit event after refactor X".

## Generating a fixture

Rather than hand-write the events, run:

```bash
gonext plugin test --suite=conformance \
  --record-fixtures=tests/conformance/fixtures \
  path/to/your/plugin
```

That writes one fixture per built-in scenario into this directory.
Trim the file down to the scenarios you care about, commit it next to
your plugin's tests, and the next conformance run will replay it.
