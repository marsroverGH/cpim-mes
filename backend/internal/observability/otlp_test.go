package observability

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/buildinfo"
)

func TestTracerExportsOTLPHTTPJSON(t *testing.T) {
	got := make(chan map[string]any, 1)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("X-Test-Header") != "collector-secret" {
			t.Errorf("X-Test-Header missing")
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("json.Unmarshal: %v", err)
		}
		got <- payload
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer collector.Close()

	reg := NewRegistry(nil)
	tracer, err := NewTracer(TracerConfig{
		Endpoint:    collector.URL,
		Headers:     "X-Test-Header=collector-secret",
		ServiceName: "cpim-mes-backend",
		Environment: "test",
		BuildInfo: buildinfo.Info{
			Version: "v0.42.0",
			Commit:  "abc123",
		},
	}, reg)
	if err != nil {
		t.Fatalf("NewTracer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	span := tracer.StartServerSpan(req)
	span.End("/healthz", 200)

	select {
	case payload := <-got:
		encoded, _ := json.Marshal(payload)
		text := string(encoded)
		for _, want := range []string{"resourceSpans", "0123456789abcdef0123456789abcdef", "http.route", "/healthz", "service.name", "cpim-mes-backend"} {
			if !strings.Contains(text, want) {
				t.Fatalf("OTLP payload missing %q: %s", want, text)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OTLP export")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := tracer.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestTracerRejectsCredentialInEndpoint(t *testing.T) {
	_, err := NewTracer(TracerConfig{Endpoint: "https://user:pass@collector.example.com:4318"}, NewRegistry(nil))
	if err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("NewTracer error = %v", err)
	}
}

func TestParseTraceparentRejectsZeroIDs(t *testing.T) {
	traceID, parentID := parseTraceparent("00-00000000000000000000000000000000-0000000000000000-01")
	if traceID != "" || parentID != "" {
		t.Fatalf("unexpected IDs: %q %q", traceID, parentID)
	}
}
