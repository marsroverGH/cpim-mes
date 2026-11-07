package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cpim-mes/backend/internal/service"

	"github.com/cpim-mes/backend/internal/buildinfo"
)

func TestVersionIsPublicAndNoStore(t *testing.T) {
	want := buildinfo.Info{
		Version:         "v0.43.0",
		Commit:          "0123456789abcdef",
		BuildTime:       "2026-11-05T12:34:56Z",
		MigrationLatest: 43,
	}
	h := NewRouter(&service.Services{}, RouterConfig{BuildInfo: want})
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q", got)
	}
	var got buildinfo.Info
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("version response=%+v want=%+v", got, want)
	}
}
