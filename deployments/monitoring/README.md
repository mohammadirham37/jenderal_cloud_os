# Optional infrastructure monitoring

ITK Cloud has built-in monitoring (host, workload and GPU metrics, alerts) in
the console. These files add Prometheus + Grafana for long-term
infrastructure dashboards. They are installed with
`installer/install.sh --with-monitoring`:

| File | Installed to |
|---|---|
| `prometheus/prometheus.yml` | `/etc/prometheus/prometheus.yml` |
| `prometheus/itk-cloud-rules.yml` | `/etc/prometheus/itk-cloud-rules.yml` |
| `grafana/provisioning/datasources/itk-cloud.yml` | `/etc/grafana/provisioning/datasources/` |
| `grafana/provisioning/dashboards/itk-cloud.yml` | `/etc/grafana/provisioning/dashboards/` |
| `grafana/dashboards/itk-cloud-host.json` | `/var/lib/grafana/dashboards/itk-cloud/` |

Prometheus (`127.0.0.1:9090`), node_exporter (`127.0.0.1:9100`) and Grafana
(`127.0.0.1:3001`) listen on localhost only. Grafana is published by Caddy on
`https://<domain>:3443` (see the Caddy snippet added by the installer). Set
`PROMETHEUS_URL`/`GRAFANA_URL` in `/etc/itk-cloud/itk-cloud.env` to show links
in the console.
