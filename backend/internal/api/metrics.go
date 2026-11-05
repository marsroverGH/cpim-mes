package api

import "net/http"

func (s *server) prometheusMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.metrics.WritePrometheus(w); err != nil {
		// The response may already be partially written. Do not expose internal
		// details to the scrape client; the next scrape can retry.
		return
	}
}
