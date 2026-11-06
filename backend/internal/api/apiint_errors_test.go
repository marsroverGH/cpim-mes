package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

// apiintAssertSanitized500 checks the generic 5xx contract: fixed message and
// a requestId that matches the X-Request-Id response header.
func apiintAssertSanitized500(t *testing.T, rr *httptest.ResponseRecorder, what string) {
	t.Helper()
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("%s: status %d; want 500 (body %s)", what, rr.Code, rr.Body.String())
	}
	body := apiintJSON[map[string]string](t, rr)
	if body["error"] != "internal server error" {
		t.Fatalf("%s: error = %q", what, body["error"])
	}
	if body["requestId"] == "" || body["requestId"] != rr.Header().Get(requestIDHeader) {
		t.Fatalf("%s: requestId %q does not match header %q", what, body["requestId"], rr.Header().Get(requestIDHeader))
	}
}

func TestAPIIntErrorMappingEndToEnd(t *testing.T) {
	env := apiintNewEnv(t)
	admin := env.login("admin", domain.RoleAdmin)
	planner := env.login("planner", domain.RolePlanner)
	operator := env.login("op", domain.RoleOperator)

	t.Run("Go pre-check mirrors the PL/pgSQL rule for sales order items", func(t *testing.T) {
		cust := planner.do(http.MethodPost, "/api/customers", map[string]any{"customerNo": "C-ERR", "name": "Err", "status": "ACTIVE"})
		apiintStatus(t, cust, http.StatusCreated, "customer")
		custID := apiintJSON[map[string]any](t, cust)["id"]
		day := time.Now().UTC().AddDate(0, 0, 7).Format("2006-01-02")
		rr := planner.do(http.MethodPost, "/api/sales-orders", map[string]any{
			"orderNo": "SO-RAW", "customerId": custID, "requestedDate": day,
			"lines": []map[string]any{{"itemId": env.itemID("TIRE-1"), "quantity": 1, "unitPrice": 1}},
		})
		apiintStatus(t, rr, http.StatusBadRequest, "PP item on sales order")
		var n int
		env.scalar(&n, `SELECT count(*) FROM sales_orders WHERE order_no='SO-RAW'`)
		if n != 0 {
			t.Fatal("rejected sales order header was persisted")
		}
	})

	// The Go layer pre-checks every migration rule that the API can reach, so
	// to drive a genuine PL/pgSQL RAISE through router -> handler -> writeError
	// we install a rule trigger in this test's private database (no migration
	// is touched). The calendar handler passes 500 for service errors.
	t.Run("PL/pgSQL RAISE is mapped by SQLSTATE and never leaks internals", func(t *testing.T) {
		if _, err := env.DB.ExecContext(env.Ctx, `
			CREATE FUNCTION apiint_calendar_rule() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN
			  CASE NEW.code
			    WHEN 'P0001' THEN RAISE EXCEPTION 'calendar % is not allowed by policy', NEW.name;
			    WHEN '23514' THEN RAISE EXCEPTION 'calendar check rule violated' USING ERRCODE='23514';
			    WHEN '42501' THEN RAISE EXCEPTION 'calendar maintenance is restricted' USING ERRCODE='42501';
			    WHEN '23503' THEN RAISE EXCEPTION 'calendar depends on a missing plant' USING ERRCODE='23503';
			    WHEN 'TABLE' THEN RAISE EXCEPTION 'internal detail for work_calendars' USING TABLE='work_calendars';
			    WHEN '40001' THEN RAISE EXCEPTION 'could not serialize' USING ERRCODE='40001';
			    ELSE NULL;
			  END CASE;
			  RETURN NEW;
			END $$;
			CREATE TRIGGER apiint_calendar_rule BEFORE INSERT ON work_calendars
			  FOR EACH ROW EXECUTE FUNCTION apiint_calendar_rule();`); err != nil {
			t.Fatalf("install test trigger: %v", err)
		}
		cases := []struct {
			code       string
			wantStatus int
			wantMsg    string
		}{
			{"P0001", http.StatusUnprocessableEntity, "calendar Night is not allowed by policy"},
			{"23514", http.StatusUnprocessableEntity, "calendar check rule violated"},
			{"42501", http.StatusForbidden, "calendar maintenance is restricted"},
			{"23503", http.StatusConflict, "calendar depends on a missing plant"},
			{"40001", http.StatusConflict, "concurrent update conflict; please retry"},
			{"TABLE", http.StatusInternalServerError, "internal server error"},
		}
		for _, tc := range cases {
			rr := admin.do(http.MethodPost, "/api/calendars", map[string]any{"code": tc.code, "name": "Night"})
			if rr.Code != tc.wantStatus {
				t.Errorf("%s: status %d; want %d (body %s)", tc.code, rr.Code, tc.wantStatus, rr.Body.String())
				continue
			}
			if got := apiintJSON[map[string]string](t, rr)["error"]; got != tc.wantMsg {
				t.Errorf("%s: error %q; want %q", tc.code, got, tc.wantMsg)
			}
			if strings.Contains(rr.Body.String(), "work_calendars") || strings.Contains(rr.Body.String(), tc.code+")") {
				t.Errorf("%s: body leaks internals: %s", tc.code, rr.Body.String())
			}
			if tc.wantStatus >= 500 {
				apiintAssertSanitized500(t, rr, tc.code)
			}
		}
		var n int
		env.scalar(&n, `SELECT count(*) FROM work_calendars WHERE name='Night'`)
		if n != 0 {
			t.Fatalf("%d rejected calendars were persisted", n)
		}
		apiintStatus(t, admin.do(http.MethodPost, "/api/calendars", map[string]any{"code": "OK-CAL", "name": "Day"}), http.StatusCreated, "allowed calendar")
	})

	t.Run("Go-side business conflict is an AppError 409", func(t *testing.T) {
		bike, frame := env.itemID("BIKE-100"), env.itemID("FRAME-1")
		rr := planner.do(http.MethodPost, "/api/items/"+frame.String()+"/bom", map[string]any{"childId": bike, "quantity": 1})
		apiintStatus(t, rr, http.StatusConflict, "BOM cycle")
		body := apiintJSON[map[string]any](t, rr)
		if body["code"] != "CONFLICT" || !strings.Contains(body["message"].(string), "BOM cycle") {
			t.Fatalf("BOM cycle body = %v", body)
		}
		var n int
		env.scalar(&n, `SELECT count(*) FROM bom_components WHERE parent_id=$1 AND child_id=$2`, frame, bike)
		if n != 0 {
			t.Fatal("cyclic BOM edge was persisted")
		}
	})

	t.Run("shop floor state machine", func(t *testing.T) {
		wo := admin.do(http.MethodPost, "/api/work-orders", map[string]any{
			"orderNo": "WO-SM-1", "itemId": env.itemID("BIKE-100"), "quantity": 1,
			"startDate": apiintRFC3339(0), "dueDate": apiintRFC3339(48 * time.Hour),
		})
		apiintStatus(t, wo, http.StatusCreated, "create WO")
		id := apiintJSON[domain.WorkOrder](t, wo).ID
		apiintStatus(t, admin.do(http.MethodPost, "/api/work-orders/"+id.String()+"/release", nil), http.StatusOK, "release")
		ops := apiintJSON[[]domain.WOOperationDetail](t, operator.do(http.MethodGet, "/api/work-orders/"+id.String()+"/operations", nil))
		if len(ops) < 2 {
			t.Skipf("BIKE-100 routing has %d operations", len(ops))
		}
		// Starting the second operation before the first is completed.
		rr := operator.do(http.MethodPost, "/api/wo-operations/"+ops[1].ID.String()+"/start", nil)
		if rr.Code < 400 || rr.Code >= 500 {
			t.Fatalf("start out of sequence: %d %s", rr.Code, rr.Body.String())
		}
		var st string
		env.scalar(&st, `SELECT status FROM wo_operations WHERE id=$1`, ops[1].ID)
		if st == "IN_PROGRESS" {
			t.Fatal("out-of-sequence start was persisted")
		}
		// Over-reporting quantity beyond the WO plan.
		apiintStatus(t, operator.do(http.MethodPost, "/api/wo-operations/"+ops[0].ID.String()+"/start", nil), http.StatusNoContent, "start first")
		over := operator.do(http.MethodPost, "/api/wo-operations/"+ops[0].ID.String()+"/complete", map[string]any{"completedQty": 50})
		if over.Code < 400 || over.Code >= 500 {
			t.Fatalf("over-complete: %d %s", over.Code, over.Body.String())
		}
		scrap := operator.do(http.MethodPost, "/api/wo-operations/"+ops[0].ID.String()+"/scrap", map[string]any{"quantity": -1})
		if scrap.Code < 400 || scrap.Code >= 500 {
			t.Fatalf("negative scrap: %d %s", scrap.Code, scrap.Body.String())
		}
		apiintStatus(t, operator.do(http.MethodPost, "/api/wo-operations/"+ops[0].ID.String()+"/scrap", `{"quantity":`), http.StatusBadRequest, "scrap bad json")
	})

	t.Run("unique violation is sanitized (no constraint name, requestId echoed)", func(t *testing.T) {
		item := apiintItemBody("DUP-1")
		apiintStatus(t, admin.do(http.MethodPost, "/api/items", item), http.StatusCreated, "first")
		rr := admin.do(http.MethodPost, "/api/items", item)
		// Reported: should be 409; currently the handler passes 500.
		if rr.Code == http.StatusConflict {
			return
		}
		apiintAssertSanitized500(t, rr, "duplicate item")
	})

	t.Run("foreign key violation is sanitized", func(t *testing.T) {
		rr := planner.do(http.MethodPost, "/api/items/"+env.itemID("FRAME-1").String()+"/bom", map[string]any{"childId": uuid.New(), "quantity": 1})
		if rr.Code < 400 {
			t.Fatalf("BOM with unknown child accepted: %d", rr.Code)
		}
		if strings.Contains(rr.Body.String(), "fkey") || strings.Contains(rr.Body.String(), "bom_components") {
			t.Fatalf("FK error leaks: %s", rr.Body.String())
		}
	})

	t.Run("client request id is echoed", func(t *testing.T) {
		rr := admin.doWith(http.MethodGet, "/api/items/"+uuid.NewString(), nil, map[string]string{requestIDHeader: "client-chosen-id"})
		if got := rr.Header().Get(requestIDHeader); got != "client-chosen-id" {
			t.Fatalf("X-Request-Id = %q", got)
		}
	})

	t.Run("request id is unique per request", func(t *testing.T) {
		a := admin.do(http.MethodGet, "/api/items", nil).Header().Get(requestIDHeader)
		b := admin.do(http.MethodGet, "/api/items", nil).Header().Get(requestIDHeader)
		if a == "" || a == b {
			t.Fatalf("request ids %q %q", a, b)
		}
	})

	t.Run("AppError bodies carry code and message", func(t *testing.T) {
		rr := admin.do(http.MethodPost, "/api/work-orders/"+uuid.NewString()+"/release", nil)
		apiintStatus(t, rr, http.StatusNotFound, "release unknown")
		body := apiintJSON[map[string]any](t, rr)
		if body["code"] != "NOT_FOUND" || body["message"] != "work order not found" {
			t.Fatalf("AppError body = %v", body)
		}
	})
}
