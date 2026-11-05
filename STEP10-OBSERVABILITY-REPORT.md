# CPIM-MES v0.41.1 Step 10 — Observability Implementation Report

- Base bundle: `cpim-mes-v0.41.1-step9-version-build-metadata-complete.zip`
- Target: v0.41.1 Production Hardening / Step 10 Observability
- Migration range: 0001–0041 / 41 migrations
- Migration 0042: not created
- Scope: metrics, private scrape boundary, optional tracing, tests, CI and documentation
- Planning/domain semantics: unchanged

## 1. Objective

Step 10 implements operations-ready observability without changing MRP, Detailed Scheduling, Pegging, Dynamic Rescheduling, Control Tower, Recovery Planning, Shop Floor, Inventory or other business semantics.

The implementation targets:

```text
/metrics
http_requests_total
http_request_duration_seconds
db_open_connections
db_in_use_connections
mrp_runs_total
mrp_run_duration_seconds
detailed_schedule_runs_total
pegging_runs_total
dynamic_reschedule_runs_total
control_tower_refresh_total
recovery_simulation_runs_total
recovery_simulation_failed_total
recovery_simulation_duration_seconds
```

Optional tracing is enabled only when `OTEL_EXPORTER_OTLP_ENDPOINT` is configured.

## 2. Prometheus-compatible metrics

New package:

```text
backend/internal/observability/metrics.go
```

The registry is dependency-free and emits Prometheus text exposition format 0.0.4.

### HTTP metrics

```text
http_requests_total{method,route,status}
http_request_duration_seconds{method,route}
```

The `route` label uses the Chi route pattern after routing, for example:

```text
/api/recovery-scenarios/{id}/simulate
```

Concrete UUIDs, usernames, customer IDs, order IDs and other user-controlled values are never used as metric labels. This bounds time-series cardinality.

Duration histograms use fixed buckets from milliseconds through 60 seconds.

### Database pool metrics

Metrics are read from `database/sql.DB.Stats()` at scrape time:

```text
db_open_connections
db_in_use_connections
db_idle_connections
db_max_open_connections
```

No additional database query is required to scrape the pool state.

### Planning metrics

The HTTP observability middleware recognizes the stable Chi route pattern for each planning operation and records success/error and duration without changing service transactions:

```text
POST /api/mrp/run
POST /api/detailed-scheduling/run
POST /api/sales-orders/{id}/pegging/run
POST /api/dynamic-rescheduling/run
POST /api/control-tower/refresh
POST /api/recovery-scenarios/{id}/simulate
```

Exposed families:

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

`result` has only two values: `success` and `error`.

## 3. Private `/metrics` boundary

Backend route:

```text
GET /metrics
```

Properties:

```text
Content-Type: text/plain; version=0.0.4; charset=utf-8
Cache-Control: no-store
```

The production Nginx frontend does **not** proxy this endpoint. It explicitly returns:

```text
GET /metrics -> 404
```

Therefore Prometheus is expected to scrape the private application network:

```text
backend:8080/metrics
```

This keeps operational telemetry separate from the public Browser/API reverse proxy.

## 4. Optional OTLP/HTTP JSON tracing

New package:

```text
backend/internal/observability/otlp.go
```

Tracing remains disabled unless:

```text
OTEL_EXPORTER_OTLP_ENDPOINT
```

is non-empty.

The built-in exporter uses OTLP/HTTP JSON and appends:

```text
/v1/traces
```

to the configured base endpoint.

The implementation follows OTLP/HTTP JSON conventions used by the OpenTelemetry specification: HTTP POST, `application/json`, Protobuf JSON field mapping and decimal-string nanosecond timestamps.

### Supported configuration

```text
OTEL_EXPORTER_OTLP_ENDPOINT=
OTEL_EXPORTER_OTLP_HEADERS=
OTEL_SERVICE_NAME=cpim-mes-backend
OTEL_EXPORT_TIMEOUT=5s
OTEL_BSP_MAX_QUEUE_SIZE=256
```

Embedded URL credentials are rejected. Collector authentication belongs in `OTEL_EXPORTER_OTLP_HEADERS` or the network/collector layer.

### Server spans

A valid incoming W3C `traceparent` is continued. Otherwise a fresh trace ID is generated.

Bounded attributes include:

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

The tracer does not record request bodies, password fields, Authorization headers, JWTs, session cookies, CSRF tokens or usernames.

### Failure isolation

The exporter is asynchronous with a bounded queue.

```text
HTTP request
  -> server span
  -> non-blocking enqueue
  -> exporter worker
  -> collector
```

If the queue is full, the span is dropped without affecting the MES request. If the collector is unavailable, export failure is logged and counted; the application response is unchanged.

Self-metrics:

```text
otel_spans_exported_total
otel_spans_dropped_total
otel_export_failures_total
```

On application shutdown, the backend attempts to drain the exporter queue within the existing shutdown timeout.

## 5. Configuration and deployment changes

Updated:

```text
backend/internal/config/config.go
backend/internal/config/config_test.go
backend/cmd/server/main.go
backend/internal/api/router.go
backend/internal/api/metrics.go
.env.example
.env.production.example
docker-compose.yml
docker-compose.prod.yml
frontend/nginx.conf
```

Production defaults keep tracing disabled. Prometheus metrics remain available on the internal backend listener.

## 6. CI and E2E

New Static Guard:

```text
backend/scripts/check_observability.py
```

Result:

```text
Step 10 Observability Static Guard: 96/96 PASS
```

GitHub Actions now:

1. Runs the Step 10 Static Guard.
2. Starts the existing production topology.
3. Executes an internal scrape from the backend container.
4. Verifies required metric families exist.
5. Verifies the public Nginx `/metrics` endpoint returns 404.
6. Continues with the Step 8 forced-recovery-failure gate and Playwright suite.

New E2E definition:

```text
e2e/tests/observability.spec.ts
```

It verifies that the public reverse proxy does not expose backend telemetry.

Playwright test definitions are now:

```text
25
```

## 7. Validation performed in this environment

### Static guards

All `backend/scripts/check_*.py` guards pass.

Important retained results:

```text
0039 Real-Time Dispatch / Dynamic Rescheduling: 106 PASS
0040 Production Control Tower:                 95/95 PASS
0041 Scenario-Based Recovery Planning:         98/98 PASS
Step 8 FAILED Run Persistence:                 57/57 PASS
Step 9 Version / Build Metadata:               57/57 PASS
Step 10 Observability:                         96/96 PASS
```

Runtime/dependency policy:

```text
Runtime version policy: PASS
Dependency lock check: PASS
CI/dependency policy: PASS
```

### Syntax/configuration validation

```text
All Go files: go/parser PASS
All Go files: gofmt clean
Config isolated unit tests: PASS
Observability package type-check with dependency stubs: PASS
JSON parse: PASS
YAML parse: PASS
Python compile: PASS
Nginx syntax: PASS
```

The isolated Config tests include OTLP defaults, valid configuration and invalid endpoint/timeout/queue rejection.

The observability package was type-checked against minimal local stubs for Chi/sqlx because external Go modules are not available in the execution environment.

### Full Go/Docker limitation

The provided execution environment has Go 1.23.2 while the project requires Go 1.26.x, and external module/toolchain download is unavailable. Docker daemon access is also unavailable.

Therefore the following full runtime gates remain for GitHub Actions or the development Mac:

```text
go mod verify
go vet ./...
go test -race -count=1 ./...
frontend npm ci / typecheck / build
production Docker build
private live /metrics scrape
Playwright 25/25
```

These gates are already wired into CI.

## 8. Migration and business-logic invariants

Step 9 baseline comparison:

```text
backend/migrations          UNCHANGED
backend/internal/domain     UNCHANGED
backend/internal/service    UNCHANGED
backend/internal/repository UNCHANGED
frontend/src                UNCHANGED
```

Migration state:

```text
count  = 41
latest = 0041
0042   = absent
```

0041 SHA-256 remains:

```text
325c558c86bd247439e7e7ea765c02b3e2e9bd718a82831aef89960f09ecdf18
```

Step 10 therefore does not change Recovery Planning persistence, side-effect boundaries, MRP/CRP/Detailed Scheduling/Pegging logic, or database schema.

## 9. Dependency impact

No new Go dependency was added.

Specifically absent:

```text
github.com/prometheus/client_golang
go.opentelemetry.io/otel
```

This preserves the existing `go.mod` / `go.sum` dependency lock state while still providing the required Prometheus exposition and optional OTLP/HTTP JSON trace export.

## 10. Result

Step 10 establishes an operations-ready observability boundary:

```text
MES workload
   |
   +--> private Prometheus metrics
   |
   +--> optional OTLP traces
   |
   +--> existing application/audit logs
```

with bounded-cardinality telemetry, private scrape exposure, failure isolation and no schema/business-semantic change.

The next planned hardening step is:

```text
Step 11 — Backup / Restore / Disaster Recovery
```
