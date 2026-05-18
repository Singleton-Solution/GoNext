# Grafana dashboards

Five JSON dashboards covering the standard Prometheus metric families
emitted by GoNext. Drop them into any Grafana instance with a
Prometheus datasource — the dashboards are parameterised so a single
copy works across dev, staging, and prod.

## Files

| File             | What it covers                                                        |
| ---------------- | --------------------------------------------------------------------- |
| `overview.json`  | Single-pane: request rate, p95 latency, error rate, jobs in/out, version banner. |
| `http.json`      | Per-route latency heatmap, status-code split, inflight by route, top error routes. |
| `jobs.json`      | Asynq-native: processed/failed/inflight/unknown, backpressure shed, retry rate, DLQ size, queue depth. |
| `plugins.json`   | Per-plugin invocations, latency, trap reasons, capability denials, pool checkout/recycle. |
| `db.json`        | Pool size, utilization, wait events, query duration percentiles, error kinds, replication lag. |

Every dashboard ships with two template variables:

- `$datasource` — picks the Prometheus datasource to query.
- `$env` — filters by the `env` label (e.g. `dev` / `staging` / `prod`).
  Set to `$__all` by default; if your scrape config doesn't add an
  `env` label, the include-all wildcard makes the filter a no-op.

A few dashboards add a third level (`$route`, `$queue`, `$plugin`,
`$db`) for drill-down.

## Naming conventions

All metrics use the `gonext_*` prefix and the standard Prometheus
suffixes (`_total` for counters, `_seconds` for time, `_bytes` for
bytes). The canonical catalogue lives in
[`docs/10-observability.md` §5.3](../../docs/10-observability.md).
Renaming a metric is a breaking change for these dashboards.

Today the JSON references both the metrics emitted in code (HTTP
middleware, plugin health/pool, Asynq jobs, backpressure, hooks) and a
handful of metrics that are documented but not yet wired (the
`gonext_db_*`, `gonext_asynq_queue_depth`, `gonext_asynq_dlq_size`
families). Panels for unwired metrics render empty until the emitters
ship — they're included so the dashboards don't need a re-import as
each emitter lands.

## Import

### 1. Grafana UI

1. Sidebar → **Dashboards** → **New** → **Import**.
2. Paste the file contents into the JSON box, or upload the file.
3. On the import screen, pick the Prometheus datasource for the
   `${datasource}` variable.
4. Save.

### 2. Grafana HTTP API

```bash
GRAFANA_URL=https://grafana.example.com
GRAFANA_TOKEN=eyJrIjo...

for f in deploy/grafana/dashboards/*.json; do
  jq -n --slurpfile dash "$f" \
    '{dashboard: $dash[0], overwrite: true, folderUid: ""}' \
  | curl -fsSL -X POST "$GRAFANA_URL/api/dashboards/db" \
      -H "Authorization: Bearer $GRAFANA_TOKEN" \
      -H "Content-Type: application/json" \
      --data-binary @-
done
```

### 3. Kubernetes ConfigMap (sidecar)

If you run the Grafana Helm chart with the dashboard sidecar enabled,
mount each JSON as a labelled ConfigMap and the sidecar will pick them
up automatically.

```bash
kubectl create configmap gonext-dashboards \
  --namespace monitoring \
  --from-file=deploy/grafana/dashboards/ \
  --dry-run=client -o yaml \
  | kubectl label --local -f - \
      grafana_dashboard=1 \
      -o yaml --dry-run=client \
  | kubectl apply -f -
```

Adjust the `grafana_dashboard` label key/value to match your sidecar's
`dashboards.label` / `dashboards.labelValue` settings.

### 4. Provisioning file (sidecar-less)

If you bake Grafana with file-provisioned dashboards, copy
`deploy/grafana/dashboards/*.json` into `/var/lib/grafana/dashboards/`
and add a `dashboards.yaml` provider that points there.

## Validation

A smoke validator lives at `tools/dashboards/validate.go`. It parses
every JSON file in `deploy/grafana/dashboards/` and asserts:

- top-level `schemaVersion` is an integer;
- at least one panel exists;
- every panel sets `datasource` to a non-empty value.

Run locally with:

```bash
go run ./tools/dashboards/validate.go deploy/grafana/dashboards
```

CI runs the same check via the `lint-dashboards` job whenever a file
under `deploy/grafana/` or `tools/dashboards/` changes.

## Adding a new dashboard

1. Drop `<name>.json` into `deploy/grafana/dashboards/`.
2. Re-use the `$datasource` and `$env` template variables so the
   dashboard stays portable.
3. Run the validator (`go run ./tools/dashboards/validate.go
   deploy/grafana/dashboards`).
4. Open a PR — the `lint-dashboards` CI job re-runs the validator on
   every push that touches this directory.
