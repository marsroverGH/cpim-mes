#!/usr/bin/env python3
"""Static guard for v0.41.1 Step 10 — Observability."""
from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
checks: list[tuple[str, bool]] = []


def text(path: str) -> str:
    p = ROOT / path
    return p.read_text(encoding="utf-8") if p.exists() else ""


def check(name: str, ok: bool) -> None:
    checks.append((name, bool(ok)))


metrics = text("backend/internal/observability/metrics.go")
otlp = text("backend/internal/observability/otlp.go")
metrics_test = text("backend/internal/observability/metrics_test.go")
otlp_test = text("backend/internal/observability/otlp_test.go")
router = text("backend/internal/api/router.go")
handler = text("backend/internal/api/metrics.go")
main = text("backend/cmd/server/main.go")
config = text("backend/internal/config/config.go")
config_test = text("backend/internal/config/config_test.go")
nginx = text("frontend/nginx.conf")
compose = text("docker-compose.yml")
prod = text("docker-compose.prod.yml")
env = text(".env.example")
prod_env = text(".env.production.example")
ci = text(".github/workflows/ci.yml")
make = text("Makefile")
doc = text("docs/OBSERVABILITY.md")
e2e = text("e2e/tests/observability.spec.ts")

check("observability metrics package exists", "package observability" in metrics)
check("observability OTLP package exists", "package observability" in otlp)
check("metrics use fixed histogram buckets", "defaultDurationBuckets" in metrics and "60" in metrics)
check("metrics registry accepts DB pool", "NewRegistry(db *sqlx.DB)" in metrics)
check("DB open gauge uses sql.DB stats", 'case "db_open_connections"' in metrics and "stats.OpenConnections" in metrics)
check("DB in-use gauge uses sql.DB stats", 'case "db_in_use_connections"' in metrics and "stats.InUse" in metrics)
check("DB idle gauge exists", 'db_idle_connections' in metrics)
check("DB max-open gauge exists", 'db_max_open_connections' in metrics)
check("process start time gauge exists", 'process_start_time_seconds' in metrics)

required_metrics = [
    "http_requests_total",
    "http_request_duration_seconds",
    "db_open_connections",
    "db_in_use_connections",
    "mrp_runs_total",
    "mrp_run_duration_seconds",
    "detailed_schedule_runs_total",
    "pegging_runs_total",
    "dynamic_reschedule_runs_total",
    "control_tower_refresh_total",
    "recovery_simulation_runs_total",
    "recovery_simulation_failed_total",
    "recovery_simulation_duration_seconds",
]
for name in required_metrics:
    check(f"required metric {name}", name in metrics)

check("HTTP labels use Chi route pattern", "RoutePattern()" in metrics)
check("HTTP metric route label is bounded pattern", '"route":  route' in metrics)
check("HTTP metric does not label username", '"username"' not in metrics)
check("HTTP metric does not label user ID", '"user_id"' not in metrics.lower())
check("recovery simulation failed counter increments", 'IncCounter("recovery_simulation_failed_total"' in metrics)
check("MRP route mapped", 'case "/api/mrp/run"' in metrics)
check("detailed scheduling route mapped", 'case "/api/detailed-scheduling/run"' in metrics)
check("pegging route mapped", 'case "/api/sales-orders/{id}/pegging/run"' in metrics)
check("dynamic reschedule route mapped", 'case "/api/dynamic-rescheduling/run"' in metrics)
check("control tower route mapped", 'case "/api/control-tower/refresh"' in metrics)
check("recovery simulation route mapped", 'case "/api/recovery-scenarios/{id}/simulate"' in metrics)
check("Prometheus exposition includes HELP", '"# HELP %s %s\\n# TYPE %s %s\\n"' in metrics)
check("Prometheus histogram +Inf bucket", '"+Inf"' in metrics)

check("metrics endpoint is root /metrics", 'r.Get("/metrics", srv.prometheusMetrics)' in router)
check("metrics endpoint stays outside API auth group", router.find('r.Get("/metrics", srv.prometheusMetrics)') < router.find('r.Route("/api"'))
check("metrics response content type is Prometheus", 'text/plain; version=0.0.4' in handler)
check("metrics response is no-store", 'Set("Cache-Control", "no-store")' in handler)
check("metrics middleware wired", "cfg.Metrics.Middleware(cfg.Tracer)" in router)
check("main creates metrics registry", "observability.NewRegistry(db)" in main)
check("main passes metrics to router", "Metrics:" in main and "metrics" in main)

check("OTLP is disabled on empty endpoint", 'if endpoint == ""' in otlp and "return nil, nil" in otlp)
check("OTLP endpoint appends /v1/traces", '"/v1/traces"' in otlp)
check("OTLP exporter sends JSON", 'Content-Type", "application/json"' in otlp)
check("OTLP exporter queue is bounded", "make(chan exportedSpan, queueSize)" in otlp)
check("OTLP queue full is non-blocking", "default:" in otlp and "otel_spans_dropped_total" in otlp)
check("OTLP failures do not fail requests", "otel_export_failures_total" in otlp and "worker()" in otlp)
check("OTLP exporter validates URL", "absolute http(s) URL" in otlp)
check("OTLP endpoint rejects embedded credentials", "must not contain embedded credentials" in otlp)
check("OTLP exporter supports headers", "OTEL_EXPORTER_OTLP_HEADERS" in otlp)
check("OTLP uses W3C traceparent", 'r.Header.Get("traceparent")' in otlp and "parseTraceparent" in otlp)
check("OTLP uses server span kind", '"kind":              2' in otlp)
check("OTLP uses route not concrete URL path", 'stringAttribute("http.route", span.Route)' in otlp)
check("OTLP trace attributes include status", 'http.response.status_code' in otlp)
check("OTLP shutdown flush exists", "func (t *Tracer) Close" in otlp)
check("OTLP export metrics exist", "otel_spans_exported_total" in metrics and "otel_spans_dropped_total" in metrics and "otel_export_failures_total" in metrics)

for var in [
    "OTEL_EXPORTER_OTLP_ENDPOINT",
    "OTEL_EXPORTER_OTLP_HEADERS",
    "OTEL_SERVICE_NAME",
    "OTEL_EXPORT_TIMEOUT",
    "OTEL_BSP_MAX_QUEUE_SIZE",
]:
    check(f"config includes {var}", var in config)
    check(f"compose includes {var}", var in compose)
    check(f"production compose includes {var}", var in prod)

check("OTEL endpoint validated before DB startup", "OTEL_EXPORTER_OTLP_ENDPOINT must be an absolute http(s) URL" in config)
check("OTEL default service name", '"cpim-mes-backend"' in config)
check("OTEL config tests exist", "TestLoadObservabilityDefaults" in config_test and "TestLoadRejectsInvalidOTLPConfiguration" in config_test)
check("metrics unit test exists", "TestRegistryMiddlewareUsesRoutePatternsAndPlanningMetrics" in metrics_test)
check("metrics test forbids concrete ID labels", "leaked a concrete scenario ID" in metrics_test)
check("OTLP unit test exists", "TestTracerExportsOTLPHTTPJSON" in otlp_test)
check("OTLP unit test verifies traceparent propagation", "0123456789abcdef0123456789abcdef" in otlp_test)

check("Nginx explicitly blocks public /metrics", "location = /metrics" in nginx and "return 404;" in nginx)
check("Nginx does not proxy /metrics", "proxy_pass http://backend:8080/metrics" not in nginx)
check("production docs say private scrape", "private backend network" in doc.lower() or "private application network" in doc.lower())
check("environment example documents OTLP", "OTEL_EXPORTER_OTLP_ENDPOINT" in env)
check("production environment documents private metrics", "Prometheus should scrape backend:8080/metrics" in prod_env)
check("observability documentation exists", "GET /metrics" in doc and "OTLP/HTTP JSON" in doc)
check("documentation warns about label cardinality", "cardinality" in doc.lower())
check("Makefile observability guard target", "check-observability" in make)
check("CI runs observability static guard", "check_observability.py" in ci)
check("CI live-scrapes backend metrics", "backend:8080/metrics" in ci or "127.0.0.1:8080/metrics" in ci)
check("CI checks metrics are not public", "metrics_public_status" in ci)
check("E2E checks public metrics are blocked", "request.get('/metrics')" in e2e and "toBe(404)" in e2e)

migrations = sorted((ROOT / "backend/migrations").glob("[0-9][0-9][0-9][0-9]_*.sql"))
check("migration count is 45", len(migrations) == 45)
check("latest migration is 0045", bool(migrations) and migrations[-1].name.startswith("0045_"))
check("migration 0042 present", any(p.name.startswith("0042_") for p in migrations))

# Dependency policy: Step 10 deliberately adds no Go dependency for metrics/OTLP.
go_mod = text("backend/go.mod")
check("no Prometheus client dependency added", "prometheus/client_golang" not in go_mod)
check("no OpenTelemetry Go SDK dependency added", "go.opentelemetry.io/otel" not in go_mod)

failed = [name for name, ok in checks if not ok]
for name, ok in checks:
    print(("PASS" if ok else "FAIL") + ": " + name)
if failed:
    print(f"\nStep 10 Observability Static Guard: {len(checks)-len(failed)}/{len(checks)} PASS")
    print("FAILED: " + ", ".join(failed), file=sys.stderr)
    sys.exit(1)
print(f"\nStep 10 Observability Static Guard: {len(checks)}/{len(checks)} PASS")
