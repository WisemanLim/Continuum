# Grafana dashboard

`continuum-dashboard.json` — import in Grafana (Dashboards → New → Import), pick the Prometheus data source
that scrapes the Controller's `GET /metrics` (FR-O01). Panels: disks online / not online, queue depth, open
conflicts, failed files, SMB access point, volume capacity and used %, effective free per disk (ordinal),
disk state table, jobs by state, transfer throughput, committed / failed files per second. `$volume` filters by volume.

Prometheus scrape example (bearer = `CONTINUUM_TOKEN`, or a viewer session token):

```yaml
scrape_configs:
  - job_name: continuum
    metrics_path: /metrics
    authorization: { credentials_file: /etc/prometheus/continuum.token }
    static_configs: [{ targets: ["controller:8080"] }]
```

`internal/ops` test `TestGrafanaDashboard` checks that every metric the dashboard queries is exported.
