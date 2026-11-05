# CPIM-MES Observability — v0.41.1 Step 10

Step 10 adds production-safe observability without changing the planning semantics, database schema, or execution behavior introduced through migration 0041.

## 1. Prometheus metrics

The backend serves:

```text
GET /metrics
```

using Prometheus text exposition format 0.0.4. In the production Compose topology, this endpoint is intentionally **not** proxied by the public Nginx frontend. Prometheus should scrape:

```text
http://backend:8080/metrics
```

from the private application network.

The public frontend explicitly returns HTTP 404 for `/metrics`, preventing the SPA fallback from accidentally publishing internal telemetry.

### Core HTTP and DB metrics

```text
http_requests_total{method,route,status}
http_request_duration_seconds{method,route}

db_open_connections
db_in_use_connections
db_idle_connections
db_max_open_connections
process_start_time_seconds
```

`route` is the bounded Chi route pattern such as:

```text
/api/recovery-scenarios/{id}/simulate
```

not the concrete URL containing a UUID. Usernames, customer IDs, order IDs, scenario IDs, JWTs, cookies and other user-controlled values are never metric labels. This is deliberate cardinality control.

### Planning and recovery metrics

```text
mrp_runs_total{result}
mrp_run_duration_seconds{result}

detailed_schedule_runs_total{result}
detailed_schedule_run_duration_seconds{result}

pegging_runs_total{result}
pegging_run_duration_seconds{result}

dynamic_reschedule_runs_total{result}
dynamic_reschedule_run_duration_seconds{result}

control_tower_refresh_total{result}
control_tower_refresh_duration_seconds{result}

recovery_simulation_runs_total{result}
recovery_simulation_failed_total
recovery_simulation_duration_seconds{result}
```

`result` is limited to `success` or `error` and is derived from the HTTP result of the run endpoint. Metrics are observational only; no service transaction changes are made by the recorder.

### Optional exporter self-metrics

When OTLP tracing is enabled, the backend also exposes:

```text
otel_spans_exported_total
otel_spans_dropped_total
otel_export_failures_total
```

Collector failures never fail an MES request. A full exporter queue drops only the span and increments `otel_spans_dropped_total`.

## 2. Prometheus example

A future Prometheus service on the same private Compose/network plane can use:

```yaml
scrape_configs:
  - job_name: cpim-mes-backend
    scrape_interval: 15s
    static_configs:
      - targets:
          - backend:8080
```

Do not expose `/metrics` to the public Internet solely to make scraping easier. Prefer a private network, service discovery, VPN, or an authenticated telemetry gateway.

## 3. Optional OpenTelemetry traces

Tracing is disabled by default. It is enabled only when:

```text
OTEL_EXPORTER_OTLP_ENDPOINT
```

is configured.

The Step 10 exporter uses **OTLP/HTTP JSON** and appends `/v1/traces` to a base collector URL. Example:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318
OTEL_SERVICE_NAME=cpim-mes-backend
OTEL_EXPORT_TIMEOUT=5s
OTEL_BSP_MAX_QUEUE_SIZE=256
```

Optional collector headers may be supplied as comma-separated `key=value` pairs:

```bash
OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer-example
```

Do not embed credentials in `OTEL_EXPORTER_OTLP_ENDPOINT`; startup validation rejects URLs containing user information.

### Trace behavior

Each HTTP request can produce one server span containing bounded attributes:

```text
service.name
service.version
deployment.environment.name
service.instance.id
http.request.method
http.route
http.response.status_code
http.request_id
```

The backend accepts and continues a valid W3C `traceparent` header. It does not place request bodies, Authorization headers, JWTs, cookies, CSRF tokens, usernames or concrete route IDs into spans.

The exporter uses a bounded asynchronous queue. Requests never wait for collector delivery. On shutdown, the backend attempts to drain the queue within the normal server shutdown timeout.

## 4. Environment variables

```text
OTEL_EXPORTER_OTLP_ENDPOINT=
OTEL_EXPORTER_OTLP_HEADERS=
OTEL_SERVICE_NAME=cpim-mes-backend
OTEL_EXPORT_TIMEOUT=5s
OTEL_BSP_MAX_QUEUE_SIZE=256
```

An empty endpoint means tracing is disabled. Metrics remain available regardless of tracing configuration.

## 5. Security boundary

Production topology:

```text
Internet / Browser
       |
       v
Nginx :8080
  |        \
  |         \-- /metrics -> 404
  |
  +-- /api, /version -> backend:8080

Private telemetry network
       |
       +-- Prometheus -> backend:8080/metrics
       +-- backend -> OTLP Collector:4318/v1/traces (optional)
```

`/metrics` is intentionally unauthenticated **on the backend private network** so a standard Prometheus scraper does not require an application session. The production reverse proxy must continue to keep it private.

## 6. Operational checks

Backend private scrape:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml \
  exec -T backend wget -qO- http://127.0.0.1:8080/metrics
```

Public boundary check:

```bash
curl -i http://localhost:8080/metrics
# expected: HTTP 404
```

Expected families include:

```text
# HELP http_requests_total ...
# HELP db_open_connections ...
# HELP mrp_runs_total ...
# HELP recovery_simulation_failed_total ...
```

## 7. Release invariants

Step 10 must preserve:

```text
Migration count = 41
latest migration = 0041
no migration 0042
0041 Recovery Simulation/Publish remains side-effect free
Step 8 FAILED run persistence remains intact
existing planning semantics remain unchanged
```

No Prometheus client library or OpenTelemetry SDK dependency is added in Step 10. The metrics registry and optional OTLP/HTTP JSON exporter are implemented with the Go standard library plus dependencies already present in CPIM-MES, keeping dependency-lock state stable.
