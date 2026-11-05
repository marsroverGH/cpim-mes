package api

import "net/http"

func (s *server) version(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.buildInfo)
}
