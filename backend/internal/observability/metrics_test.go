package observability

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestRegistryMiddlewareUsesRoutePatternsAndPlanningMetrics(t *testing.T) {
	reg := NewRegistry(nil)
	r := chi.NewRouter()
	r.Use(reg.Middleware(nil))
	r.Post("/api/recovery-scenarios/{id}/simulate", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forced", http.StatusInternalServerError)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/recovery-scenarios/2ca68196-6f0e-4c57-9a5b-5639fc75a561/simulate", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	var out bytes.Buffer
	if err := reg.WritePrometheus(&out); err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	text := out.String()
	for _, want := range []string{
		`http_requests_total{method="POST",route="/api/recovery-scenarios/{id}/simulate",status="500"} 1`,
		`recovery_simulation_runs_total{result="error"} 1`,
		`recovery_simulation_failed_total 1`,
		`recovery_simulation_duration_seconds_count{result="error"} 1`,
		`db_open_connections 0`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "2ca68196-6f0e-4c57-9a5b-5639fc75a561") {
		t.Fatal("metrics leaked a concrete scenario ID instead of the bounded route pattern")
	}
}

func TestRegistryExposesRequiredFamiliesBeforeFirstObservation(t *testing.T) {
	reg := NewRegistry(nil)
	var out bytes.Buffer
	if err := reg.WritePrometheus(&out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, name := range []string{
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
	} {
		if !strings.Contains(text, "# HELP "+name+" ") {
			t.Fatalf("missing HELP for %s", name)
		}
	}
}
