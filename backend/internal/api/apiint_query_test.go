package api

import (
	"net/http"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

// TestAPIIntQueryParameterValidation covers the query-string parsers of the
// read endpoints: malformed ids/dates are a 400, well-formed filters are 200.
func TestAPIIntQueryParameterValidation(t *testing.T) {
	env := apiintNewEnv(t)
	c := env.login("planner", domain.RolePlanner)
	bike := env.itemID("BIKE-100").String()
	id := uuid.NewString()

	cases := []struct {
		path string
		want int
	}{
		// ABC analysis takes a business date (YYYY-MM-DD or RFC3339), unlike the
		// scheduling endpoints below which take RFC3339 instants.
		{"/api/abc-analysis?asOf=2026-01-31", http.StatusOK},
		{"/api/abc-analysis?asOf=2026-01-31T00:00:00Z", http.StatusOK}, // RFC3339 accepted like the rest of the API
		{"/api/abc-analysis?asOf=31/01/2026", http.StatusBadRequest},
		{"/api/dispatch?asOf=2026-11-11", http.StatusBadRequest},
		{"/api/dispatch?workCenterId=nope", http.StatusBadRequest},
		{"/api/schedule-adherence/current?asOf=2026-11-11", http.StatusBadRequest},
		{"/api/inventory-policy-versions?itemId=nope", http.StatusBadRequest},
		{"/api/inventory-policy-versions?itemId=" + bike, http.StatusOK},
		{"/api/forecast/runs?itemId=nope", http.StatusBadRequest},
		{"/api/forecast/runs?itemId=" + bike, http.StatusOK},
		{"/api/sop/product-mix/versions?groupId=nope", http.StatusBadRequest},
		{"/api/sop/product-mix/versions?groupId=" + id, http.StatusOK},
		{"/api/sop/disaggregation-runs?planId=nope", http.StatusBadRequest},
		{"/api/sop/disaggregation-runs?planId=" + id, http.StatusOK},
		{"/api/sop/plans/" + id + "/disaggregation/preview?mixVersionId=nope", http.StatusBadRequest},
		{"/api/maintenance-events?workCenterId=nope", http.StatusBadRequest},
		{"/api/maintenance-events?workCenterId=" + id + "&includeTerminal=true", http.StatusOK},
		{"/api/items/" + bike + "/atp?horizonDays=14&bucketDays=7", http.StatusOK},
		{"/api/items/" + bike + "/explode?qty=notanumber", http.StatusOK}, // falls back to qty=1
		{"/api/mrp/action-messages?horizonDays=28", http.StatusOK},
		{"/api/rccp/run?workingDays=20", http.StatusOK},
		{"/api/planning-exceptions?status=OPEN&severity=HIGH&type=SHORTAGE", http.StatusOK},
		{"/api/control-tower?status=OPEN&priorityBand=P1", http.StatusOK},
		{"/api/supplier-quality/ncrs?status=OPEN&supplier=x", http.StatusOK},
		{"/api/recovery-scenarios?status=DRAFT", http.StatusOK},
		{"/api/recovery-scenario-comparison?baselineHash=abc", http.StatusBadRequest},
		{"/api/recovery-scenario-comparison?baselineHash=" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", http.StatusOK},
		{"/api/quality/recent?limit=3", http.StatusOK},
	}
	for _, tc := range cases {
		rr := c.do(http.MethodGet, tc.path, nil)
		if rr.Code != tc.want {
			t.Errorf("GET %s: status %d; want %d (body %s)", tc.path, rr.Code, tc.want, rr.Body.String())
		}
	}
}
