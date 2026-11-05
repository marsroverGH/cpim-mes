package observability

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cpim-mes/backend/internal/buildinfo"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

const defaultOTLPQueueSize = 256

type TracerConfig struct {
	Endpoint      string
	Headers       string
	ServiceName   string
	Environment   string
	BuildInfo     buildinfo.Info
	ExportTimeout time.Duration
	QueueSize     int
}

type Tracer struct {
	endpoint    string
	headers     http.Header
	serviceName string
	environment string
	buildInfo   buildinfo.Info
	client      *http.Client
	registry    *Registry
	queue       chan exportedSpan
	done        chan struct{}
	closeOnce   sync.Once
}

type Span struct {
	tracer       *Tracer
	traceID      string
	spanID       string
	parentSpanID string
	name         string
	method       string
	requestID    string
	start        time.Time
}

type exportedSpan struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Name         string
	Method       string
	Route        string
	RequestID    string
	Status       int
	Start        time.Time
	End          time.Time
}

func NewTracer(cfg TracerConfig, registry *Registry) (*Tracer, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("OTEL_EXPORTER_OTLP_ENDPOINT must be an absolute http(s) URL")
	}
	if u.User != nil {
		return nil, errors.New("OTEL_EXPORTER_OTLP_ENDPOINT must not contain embedded credentials; use OTEL_EXPORTER_OTLP_HEADERS")
	}
	if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/v1/traces") {
		u.Path = strings.TrimRight(u.Path, "/") + "/v1/traces"
	}

	headers, err := parseOTLPHeaders(cfg.Headers)
	if err != nil {
		return nil, err
	}
	serviceName := strings.TrimSpace(cfg.ServiceName)
	if serviceName == "" {
		serviceName = "cpim-mes-backend"
	}
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = defaultOTLPQueueSize
	}
	timeout := cfg.ExportTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	t := &Tracer{
		endpoint:    u.String(),
		headers:     headers,
		serviceName: serviceName,
		environment: cfg.Environment,
		buildInfo:   cfg.BuildInfo,
		client:      &http.Client{Timeout: timeout},
		registry:    registry,
		queue:       make(chan exportedSpan, queueSize),
		done:        make(chan struct{}),
	}
	go t.worker()
	return t, nil
}

func parseOTLPHeaders(raw string) (http.Header, error) {
	h := make(http.Header)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return h, nil
	}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, value, ok := strings.Cut(pair, "=")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if !ok || name == "" || strings.ContainsAny(name, "\r\n") || strings.ContainsAny(value, "\r\n") {
			return nil, errors.New("OTEL_EXPORTER_OTLP_HEADERS must be comma-separated key=value pairs without CR/LF")
		}
		h.Set(name, value)
	}
	return h, nil
}

func (t *Tracer) Enabled() bool { return t != nil }

func (t *Tracer) StartServerSpan(r *http.Request) *Span {
	if t == nil {
		return &Span{}
	}
	traceID, parentSpanID := parseTraceparent(r.Header.Get("traceparent"))
	if traceID == "" {
		traceID = randomHex(16)
	}
	return &Span{
		tracer:       t,
		traceID:      traceID,
		spanID:       randomHex(8),
		parentSpanID: parentSpanID,
		name:         r.Method + " HTTP",
		method:       r.Method,
		requestID:    chimiddleware.GetReqID(r.Context()),
		start:        time.Now().UTC(),
	}
}

func (s *Span) End(route string, status int) {
	if s == nil || s.tracer == nil || s.traceID == "" || s.spanID == "" {
		return
	}
	name := s.method + " " + route
	if route == "" || route == "unmatched" {
		name = s.name
	}
	item := exportedSpan{
		TraceID:      s.traceID,
		SpanID:       s.spanID,
		ParentSpanID: s.parentSpanID,
		Name:         name,
		Method:       s.method,
		Route:        route,
		RequestID:    s.requestID,
		Status:       status,
		Start:        s.start,
		End:          time.Now().UTC(),
	}
	select {
	case s.tracer.queue <- item:
	default:
		s.tracer.registry.IncCounter("otel_spans_dropped_total", nil)
	}
}

func (t *Tracer) Close(ctx context.Context) error {
	if t == nil {
		return nil
	}
	t.closeOnce.Do(func() { close(t.queue) })
	select {
	case <-t.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *Tracer) worker() {
	defer close(t.done)
	for span := range t.queue {
		if err := t.export(span); err != nil {
			t.registry.IncCounter("otel_export_failures_total", nil)
			log.Printf("[observability] OTLP trace export failed: %v", err)
			continue
		}
		t.registry.IncCounter("otel_spans_exported_total", nil)
	}
}

func (t *Tracer) export(span exportedSpan) error {
	payload := t.tracePayload(span)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for name, values := range t.headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("collector returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (t *Tracer) tracePayload(span exportedSpan) map[string]any {
	resourceAttrs := []map[string]any{
		stringAttribute("service.name", t.serviceName),
		stringAttribute("service.version", t.buildInfo.Version),
		stringAttribute("deployment.environment.name", t.environment),
	}
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		resourceAttrs = append(resourceAttrs, stringAttribute("service.instance.id", hostname))
	}

	attrs := []map[string]any{
		stringAttribute("http.request.method", span.Method),
		stringAttribute("http.route", span.Route),
		intAttribute("http.response.status_code", int64(span.Status)),
	}
	if span.RequestID != "" {
		attrs = append(attrs, stringAttribute("http.request_id", span.RequestID))
	}
	statusCode := 0
	if span.Status >= 500 {
		statusCode = 2 // STATUS_CODE_ERROR
	}
	spanJSON := map[string]any{
		"traceId":           span.TraceID,
		"spanId":            span.SpanID,
		"name":              span.Name,
		"kind":              2, // SPAN_KIND_SERVER
		"startTimeUnixNano": fmt.Sprintf("%d", span.Start.UnixNano()),
		"endTimeUnixNano":   fmt.Sprintf("%d", span.End.UnixNano()),
		"attributes":        attrs,
		"status":            map[string]any{"code": statusCode},
	}
	if span.ParentSpanID != "" {
		spanJSON["parentSpanId"] = span.ParentSpanID
	}

	return map[string]any{
		"resourceSpans": []any{
			map[string]any{
				"resource": map[string]any{"attributes": resourceAttrs},
				"scopeSpans": []any{
					map[string]any{
						"scope": map[string]any{"name": "cpim-mes/backend/http", "version": t.buildInfo.Version},
						"spans": []any{spanJSON},
					},
				},
			},
		},
	}
}

func stringAttribute(key, value string) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
}

func intAttribute(key string, value int64) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"intValue": fmt.Sprintf("%d", value)}}
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

func parseTraceparent(value string) (traceID, parentSpanID string) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 4 || len(parts[0]) != 2 || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return "", ""
	}
	if parts[0] == "ff" || allZeroHex(parts[1]) || allZeroHex(parts[2]) {
		return "", ""
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return "", ""
	}
	if _, err := hex.DecodeString(parts[2]); err != nil {
		return "", ""
	}
	if _, err := hex.DecodeString(parts[3]); err != nil {
		return "", ""
	}
	return strings.ToLower(parts[1]), strings.ToLower(parts[2])
}

func allZeroHex(value string) bool {
	for _, r := range value {
		if r != '0' {
			return false
		}
	}
	return true
}
