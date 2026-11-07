package repository_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
)

func TestLotsBalancesAndWhereUsed(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	lots := f.repos.Lots
	it := f.item(t, "RM")
	otherItem := f.item(t, "RM")

	lotA, _ := f.receiveLot(t, it.ID, 50)
	lotB, _ := f.receiveLot(t, it.ID, 20)
	f.receiveLot(t, otherItem.ID, 5)
	f.issueLot(t, it.ID, lotA, 15, "WO-ABC", time.Now())
	f.issueLot(t, it.ID, lotA, 5, "WO-XYZ", time.Now())

	// Direct writes are forbidden: lots must come from the ledger service.
	wantErrContains(t, lots.Create(f.ctx, &domain.Lot{ItemID: it.ID}), "forbidden")
	wantErrContains(t, lots.AddMovement(f.ctx, &domain.LotMovement{LotID: lotA}), "forbidden")

	got, err := lots.Get(f.ctx, lotA)
	if err != nil || got.ItemID != it.ID || !approxEqual(got.Quantity, 50) || got.QualityStatus != "OK" {
		t.Fatalf("Get: %+v %v", got, err)
	}
	if _, err := lots.Get(f.ctx, uuid.New()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get unknown: %v", err)
	}

	byItem, err := lots.ByItem(f.ctx, it.ID)
	if err != nil || len(byItem) != 2 {
		t.Fatalf("ByItem: %+v %v", byItem, err)
	}
	balances := map[uuid.UUID]float64{}
	for _, l := range byItem {
		if l.ItemCode != it.Code {
			t.Fatalf("joined item code: %q", l.ItemCode)
		}
		balances[l.ID] = l.Balance
	}
	if !approxEqual(balances[lotA], 30) || !approxEqual(balances[lotB], 20) {
		t.Fatalf("lot balances: %v", balances)
	}

	all, err := lots.List(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, l := range all {
		if l.ID == lotA || l.ID == lotB {
			seen++
		}
	}
	if seen != 2 {
		t.Fatalf("List missing lots (%d/2)", seen)
	}

	mv, err := lots.Movements(f.ctx, lotA)
	if err != nil || len(mv) != 3 || mv[0].MovementType != "RECEIPT" {
		t.Fatalf("Movements: %+v %v", mv, err)
	}
	used, err := lots.WhereUsed(f.ctx, lotA)
	if err != nil || len(used) != 2 {
		t.Fatalf("WhereUsed: %+v %v", used, err)
	}
	for _, u := range used {
		if u.MovementType != "ISSUE" || (u.RefDoc != "WO-ABC" && u.RefDoc != "WO-XYZ") {
			t.Fatalf("WhereUsed must only return consumption with a ref doc: %+v", u)
		}
	}
	if used, _ := lots.WhereUsed(f.ctx, lotB); len(used) != 0 {
		t.Fatalf("receipt-only lot has no where-used rows: %+v", used)
	}

	// Lot item identity is immutable.
	_, err = f.db.Exec(`UPDATE lots SET item_id=$1 WHERE id=$2`, otherItem.ID, lotA)
	wantErrContains(t, err, "immutable")
}

func TestQualityInspectionsDriveLotStatus(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	q := f.repos.Quality
	it := f.item(t, "RM")
	lot, _ := f.receiveLot(t, it.ID, 10)
	op := f.user(t, domain.RoleOperator)
	viewer := f.user(t, domain.RoleViewer)

	inspect := func(user uuid.UUID, result string, defects float64) error {
		_, err := f.db.Exec(`INSERT INTO quality_inspections (lot_id, inspector_user_id, inspector, result, defect_qty, notes, inspected_at)
		                     VALUES ($1,$2,'forged-name',$3,$4,'n','2000-01-01')`, lot, user, result, defects)
		return err
	}

	// Authorization / validation in the BEFORE INSERT trigger.
	rejects := []struct {
		name   string
		user   uuid.UUID
		result string
		defect float64
		want   string
	}{
		{"viewer may not inspect", viewer.ID, "PASS", 0, "not allowed"},
		{"unknown inspector", uuid.New(), "PASS", 0, "inactive or does not exist"},
		{"defects exceed lot", op.ID, "FAIL", 11, "defect_qty"},
	}
	for _, tc := range rejects {
		wantErrContains(t, inspect(tc.user, tc.result, tc.defect), tc.want)
	}

	if err := inspect(op.ID, "HOLD", 0); err != nil {
		t.Fatal(err)
	}
	if err := inspect(op.ID, "PASS", 1); err != nil {
		t.Fatal(err)
	}
	l, _ := f.repos.Lots.Get(f.ctx, lot)
	if l.QualityStatus != "OK" {
		t.Fatalf("lot status after PASS = %s", l.QualityStatus)
	}

	ins, err := q.ListByLot(f.ctx, lot)
	if err != nil || len(ins) != 2 {
		t.Fatalf("ListByLot: %+v %v", ins, err)
	}
	newest, oldest := ins[0], ins[1]
	if newest.Result != "PASS" || oldest.Result != "HOLD" {
		t.Fatalf("ListByLot must be newest first: %s, %s", newest.Result, oldest.Result)
	}
	// Server-authoritative identity, timestamp and status transition.
	if newest.Inspector != op.Username || newest.InspectedAt.Year() == 2000 {
		t.Fatalf("inspector/time must be DB-derived: %+v", newest)
	}
	if oldest.PreviousStatus == nil || *oldest.PreviousStatus != "OK" || oldest.ResultingStatus != "HOLD" ||
		newest.PreviousStatus == nil || *newest.PreviousStatus != "HOLD" || newest.ResultingStatus != "OK" {
		t.Fatalf("status chain: %+v / %+v", oldest, newest)
	}

	hist, err := q.StatusHistory(f.ctx, lot)
	if err != nil || len(hist) != 2 || hist[0].ToStatus != "OK" || hist[1].ToStatus != "HOLD" || hist[0].Source != "INSPECTION" {
		t.Fatalf("StatusHistory: %+v %v", hist, err)
	}
	if hist[0].InspectionID == nil || *hist[0].InspectionID != newest.ID {
		t.Fatalf("history must link the inspection: %+v", hist[0])
	}

	recent, err := q.Recent(f.ctx, 1)
	if err != nil || len(recent) != 1 || recent[0].ID != newest.ID {
		t.Fatalf("Recent(1): %+v %v", recent, err)
	}
	for _, lim := range []int{0, -5, 501} {
		if r, err := q.Recent(f.ctx, lim); err != nil || len(r) != 2 {
			t.Fatalf("Recent(%d) must clamp to default: %d %v", lim, len(r), err)
		}
	}

	// Evidence is append-only and lot status cannot be edited directly.
	_, err = f.db.Exec(`UPDATE quality_inspections SET result='FAIL' WHERE id=$1`, newest.ID)
	wantErrContains(t, err, "append-only")
	_, err = f.db.Exec(`DELETE FROM quality_status_history WHERE lot_id=$1`, lot)
	wantErrContains(t, err, "append-only")
	_, err = f.db.Exec(`UPDATE lots SET quality_status='REJECTED' WHERE id=$1`, lot)
	wantErrContains(t, err, "inspection-controlled")
}

func TestAuditLogInsertAndFilter(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	audit := f.repos.Audit

	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	entries := []domain.AuditLogEntry{
		{Username: "alice", UserRole: "admin", Action: "POST /api/items", Resource: "items", HTTPStatus: 201, OccurredAt: fixed,
			Payload: repository.MarshalPayload(map[string]any{"code": "X"})},
		{Username: "alice", UserRole: "admin", Action: "DELETE /api/items/1", Resource: "items", ResourceID: "1", HTTPStatus: 204,
			Payload: json.RawMessage(`{}`)},
		{Username: "bob", UserRole: "viewer", Action: "PUT /api/lots/2", Resource: "lots", HTTPStatus: 200,
			Payload: json.RawMessage(`[1,2]`)},
	}
	for i := range entries {
		if err := audit.Insert(f.ctx, &entries[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := audit.Insert(f.ctx, &domain.AuditLogEntry{Username: "x", Action: "a", Resource: "r", Payload: json.RawMessage(`{bad`)}); err == nil {
		t.Fatal("invalid JSON payload must be rejected by jsonb cast")
	}

	tests := []struct {
		name   string
		filter repository.AuditFilter
		want   int
	}{
		{"all", repository.AuditFilter{}, 3},
		{"by user", repository.AuditFilter{Username: "alice"}, 2},
		{"by resource", repository.AuditFilter{Resource: "lots"}, 1},
		{"by both", repository.AuditFilter{Username: "bob", Resource: "items"}, 0},
		{"limit", repository.AuditFilter{Limit: 1}, 1},
		{"oversized limit clamps", repository.AuditFilter{Limit: 100000}, 3},
	}
	for _, tc := range tests {
		rows, err := audit.List(f.ctx, tc.filter)
		if err != nil || len(rows) != tc.want {
			t.Errorf("%s: got %d rows (err %v), want %d", tc.name, len(rows), err, tc.want)
		}
	}

	rows, _ := audit.List(f.ctx, repository.AuditFilter{Username: "alice"})
	// Newest first: the entry with DB-default (now) time precedes the fixed 2026-01-02 one.
	if rows[0].ResourceID != "1" || !rows[1].OccurredAt.Equal(fixed) {
		t.Fatalf("ordering / explicit time: %+v", rows)
	}
	var payload map[string]string
	if err := json.Unmarshal(rows[1].Payload, &payload); err != nil || payload["code"] != "X" {
		t.Fatalf("payload round trip: %s %v", rows[1].Payload, err)
	}
}

// TestAuditListWithNullPayload covers entries recorded without a body (e.g.
// DELETE requests): Insert stores them with a NULL payload, and List must still
// work (regression: scanning NULL jsonb used to fail the whole listing).
func TestAuditListWithNullPayload(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if err := f.repos.Audit.Insert(f.ctx, &domain.AuditLogEntry{Username: "carol", Action: "DELETE /api/lots/9", Resource: "lots", ResourceID: "9", HTTPStatus: 204}); err != nil {
		t.Fatal(err)
	}
	var isNull bool
	if err := f.db.Get(&isNull, `SELECT payload IS NULL FROM audit_log WHERE username='carol'`); err != nil || !isNull {
		t.Fatalf("empty payload must be stored as SQL NULL (null=%v, err=%v)", isNull, err)
	}
	rows, err := f.repos.Audit.List(f.ctx, repository.AuditFilter{Username: "carol"})
	if err != nil || len(rows) != 1 || len(rows[0].Payload) != 0 {
		t.Fatalf("List with NULL payload: %+v %v", rows, err)
	}
}

func TestMarshalPayload(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, ""},
		{"map", map[string]int{"a": 1}, `{"a":1}`},
		{"unmarshalable", math.Inf(1), ""},
		{"channel", make(chan int), ""},
	}
	for _, tc := range tests {
		if got := string(repository.MarshalPayload(tc.in)); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}
