package observability

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jmoiron/sqlx"
)

// Histogram buckets intentionally cover normal API latency through long-running
// planning operations without generating high-cardinality series.
var defaultDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

type histogramValue struct {
	Count   uint64
	Sum     float64
	Buckets []uint64
}

type metricFamily struct {
	Help string
	Type string
}

// Registry is a small, dependency-free Prometheus exposition registry for the
// fixed CPIM-MES metrics required by the production-hardening release. Labels
// are deliberately bounded to route patterns, HTTP methods/statuses and fixed
// operation/result values; user IDs, document IDs and usernames are never used.
type Registry struct {
	mu        sync.RWMutex
	startedAt time.Time
	db        *sqlx.DB

	counters   map[string]map[string]uint64
	histograms map[string]map[string]*histogramValue
	families   map[string]metricFamily
}

func NewRegistry(db *sqlx.DB) *Registry {
	r := &Registry{
		startedAt:  time.Now().UTC(),
		db:         db,
		counters:   make(map[string]map[string]uint64),
		histograms: make(map[string]map[string]*histogramValue),
		families:   make(map[string]metricFamily),
	}

	r.registerCounter("http_requests_total", "Total HTTP requests handled by the backend.")
	r.registerHistogram("http_request_duration_seconds", "HTTP request duration in seconds.")
	r.registerGauge("db_open_connections", "Current number of open database connections.")
	r.registerGauge("db_in_use_connections", "Current number of database connections in use.")
	r.registerGauge("db_idle_connections", "Current number of idle database connections.")
	r.registerGauge("db_max_open_connections", "Configured maximum number of open database connections.")
	r.registerGauge("process_start_time_seconds", "Backend process start time as Unix seconds.")

	r.registerCounter("mrp_runs_total", "MRP run requests by result.")
	r.registerHistogram("mrp_run_duration_seconds", "MRP run request duration in seconds by result.")
	r.registerCounter("detailed_schedule_runs_total", "Detailed scheduling run requests by result.")
	r.registerHistogram("detailed_schedule_run_duration_seconds", "Detailed scheduling run request duration in seconds by result.")
	r.registerCounter("pegging_runs_total", "Full pegging run requests by result.")
	r.registerHistogram("pegging_run_duration_seconds", "Full pegging run request duration in seconds by result.")
	r.registerCounter("dynamic_reschedule_runs_total", "Dynamic rescheduling run requests by result.")
	r.registerHistogram("dynamic_reschedule_run_duration_seconds", "Dynamic rescheduling run request duration in seconds by result.")
	r.registerCounter("control_tower_refresh_total", "Production Control Tower refresh requests by result.")
	r.registerHistogram("control_tower_refresh_duration_seconds", "Production Control Tower refresh request duration in seconds by result.")
	r.registerCounter("recovery_simulation_runs_total", "Recovery simulation requests by result.")
	r.registerCounter("recovery_simulation_failed_total", "Recovery simulation requests that returned an error result.")
	r.registerHistogram("recovery_simulation_duration_seconds", "Recovery simulation request duration in seconds by result.")

	r.registerCounter("otel_spans_exported_total", "OTLP spans successfully exported by the optional exporter.")
	r.registerCounter("otel_spans_dropped_total", "OTLP spans dropped because the exporter queue was full.")
	r.registerCounter("otel_export_failures_total", "OTLP export attempts that failed.")

	return r
}

func (r *Registry) registerCounter(name, help string) {
	r.families[name] = metricFamily{Help: help, Type: "counter"}
}
func (r *Registry) registerGauge(name, help string) {
	r.families[name] = metricFamily{Help: help, Type: "gauge"}
}
func (r *Registry) registerHistogram(name, help string) {
	r.families[name] = metricFamily{Help: help, Type: "histogram"}
}

func labelsKey(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(labels[k]))
	}
	return b.String()
}

func (r *Registry) IncCounter(name string, labels map[string]string) {
	if r == nil {
		return
	}
	key := labelsKey(labels)
	r.mu.Lock()
	defer r.mu.Unlock()
	series := r.counters[name]
	if series == nil {
		series = make(map[string]uint64)
		r.counters[name] = series
	}
	series[key]++
}

func (r *Registry) ObserveDuration(name string, duration time.Duration, labels map[string]string) {
	if r == nil {
		return
	}
	seconds := duration.Seconds()
	key := labelsKey(labels)
	r.mu.Lock()
	defer r.mu.Unlock()
	series := r.histograms[name]
	if series == nil {
		series = make(map[string]*histogramValue)
		r.histograms[name] = series
	}
	v := series[key]
	if v == nil {
		v = &histogramValue{Buckets: make([]uint64, len(defaultDurationBuckets))}
		series[key] = v
	}
	v.Count++
	v.Sum += seconds
	for i, upper := range defaultDurationBuckets {
		if seconds <= upper {
			v.Buckets[i]++
		}
	}
}

func statusResult(status int) string {
	if status >= 200 && status < 400 {
		return "success"
	}
	return "error"
}

func canonicalRoute(r *http.Request) string {
	pattern := chi.RouteContext(r.Context()).RoutePattern()
	if pattern == "" {
		return "unmatched"
	}
	return pattern
}

// Middleware records generic HTTP metrics and the fixed set of planning run
// metrics using Chi route patterns, never concrete IDs. It also creates an OTLP
// server span when an optional Tracer is configured.
func (reg *Registry) Middleware(tracer *Tracer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			span := tracer.StartServerSpan(r)

			next.ServeHTTP(ww, r)

			duration := time.Since(start)
			route := canonicalRoute(r)
			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			reg.IncCounter("http_requests_total", map[string]string{
				"method": r.Method,
				"route":  route,
				"status": strconv.Itoa(status),
			})
			reg.ObserveDuration("http_request_duration_seconds", duration, map[string]string{
				"method": r.Method,
				"route":  route,
			})
			reg.observePlanningOperation(r.Method, route, status, duration)
			span.End(route, status)
		})
	}
}

func (r *Registry) observePlanningOperation(method, route string, status int, duration time.Duration) {
	if method != http.MethodPost {
		return
	}
	result := statusResult(status)
	labels := map[string]string{"result": result}

	switch route {
	case "/api/mrp/run":
		r.IncCounter("mrp_runs_total", labels)
		r.ObserveDuration("mrp_run_duration_seconds", duration, labels)
	case "/api/detailed-scheduling/run":
		r.IncCounter("detailed_schedule_runs_total", labels)
		r.ObserveDuration("detailed_schedule_run_duration_seconds", duration, labels)
	case "/api/sales-orders/{id}/pegging/run":
		r.IncCounter("pegging_runs_total", labels)
		r.ObserveDuration("pegging_run_duration_seconds", duration, labels)
	case "/api/dynamic-rescheduling/run":
		r.IncCounter("dynamic_reschedule_runs_total", labels)
		r.ObserveDuration("dynamic_reschedule_run_duration_seconds", duration, labels)
	case "/api/control-tower/refresh":
		r.IncCounter("control_tower_refresh_total", labels)
		r.ObserveDuration("control_tower_refresh_duration_seconds", duration, labels)
	case "/api/recovery-scenarios/{id}/simulate":
		r.IncCounter("recovery_simulation_runs_total", labels)
		if result == "error" {
			r.IncCounter("recovery_simulation_failed_total", nil)
		}
		r.ObserveDuration("recovery_simulation_duration_seconds", duration, labels)
	}
}

func prometheusLabels(key string) string {
	if key == "" {
		return ""
	}
	return "{" + key + "}"
}

func escapeHelp(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, "\n", `\n`)
}

func appendLabel(key, name, value string) string {
	if key == "" {
		return name + "=" + strconv.Quote(value)
	}
	return key + "," + name + "=" + strconv.Quote(value)
}

// WritePrometheus writes Prometheus text exposition format 0.0.4.
func (r *Registry) WritePrometheus(w io.Writer) error {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	counters := make(map[string]map[string]uint64, len(r.counters))
	for name, values := range r.counters {
		copyValues := make(map[string]uint64, len(values))
		for k, v := range values {
			copyValues[k] = v
		}
		counters[name] = copyValues
	}
	histograms := make(map[string]map[string]histogramValue, len(r.histograms))
	for name, values := range r.histograms {
		copyValues := make(map[string]histogramValue, len(values))
		for k, v := range values {
			copyBuckets := append([]uint64(nil), v.Buckets...)
			copyValues[k] = histogramValue{Count: v.Count, Sum: v.Sum, Buckets: copyBuckets}
		}
		histograms[name] = copyValues
	}
	families := make(map[string]metricFamily, len(r.families))
	for name, family := range r.families {
		families[name] = family
	}
	r.mu.RUnlock()

	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	sort.Strings(names)

	stats := sql.DBStats{}
	if r.db != nil {
		stats = r.db.Stats()
	}
	for _, name := range names {
		family := families[name]
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, escapeHelp(family.Help), name, family.Type); err != nil {
			return err
		}
		switch family.Type {
		case "counter":
			values := counters[name]
			keys := sortedSeriesKeys(values)
			for _, key := range keys {
				if _, err := fmt.Fprintf(w, "%s%s %d\n", name, prometheusLabels(key), values[key]); err != nil {
					return err
				}
			}
		case "gauge":
			var value float64
			switch name {
			case "db_open_connections":
				value = float64(stats.OpenConnections)
			case "db_in_use_connections":
				value = float64(stats.InUse)
			case "db_idle_connections":
				value = float64(stats.Idle)
			case "db_max_open_connections":
				value = float64(stats.MaxOpenConnections)
			case "process_start_time_seconds":
				value = float64(r.startedAt.Unix())
			}
			if _, err := fmt.Fprintf(w, "%s %g\n", name, value); err != nil {
				return err
			}
		case "histogram":
			values := histograms[name]
			keys := make([]string, 0, len(values))
			for key := range values {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				v := values[key]
				for i, upper := range defaultDurationBuckets {
					bucketLabels := appendLabel(key, "le", strconv.FormatFloat(upper, 'g', -1, 64))
					if _, err := fmt.Fprintf(w, "%s_bucket{%s} %d\n", name, bucketLabels, v.Buckets[i]); err != nil {
						return err
					}
				}
				infLabels := appendLabel(key, "le", "+Inf")
				if _, err := fmt.Fprintf(w, "%s_bucket{%s} %d\n", name, infLabels, v.Count); err != nil {
					return err
				}
				if _, err := fmt.Fprintf(w, "%s_sum%s %g\n", name, prometheusLabels(key), v.Sum); err != nil {
					return err
				}
				if _, err := fmt.Fprintf(w, "%s_count%s %d\n", name, prometheusLabels(key), v.Count); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func sortedSeriesKeys(values map[string]uint64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
