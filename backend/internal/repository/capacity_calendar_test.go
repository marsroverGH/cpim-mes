package repository_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestWorkCenterCRUDAndResourceGuard(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	wcs := f.repos.WorkCenters
	wc := f.workCenter(t, 2, 2)
	if wc.ID == uuid.Nil || wc.CreatedAt.IsZero() {
		t.Fatalf("defaults: %+v", wc)
	}
	if err := wcs.Create(f.ctx, &domain.WorkCenter{Code: wc.Code, Name: "dup", Efficiency: 1, Utilization: 1, MachineCount: 1, WorkerCount: 1}); err == nil {
		t.Fatal("duplicate work center code must be rejected")
	}

	got, err := wcs.Get(f.ctx, wc.ID)
	if err != nil || got.Code != wc.Code || got.MachineCount != 2 {
		t.Fatalf("Get: %+v %v", got, err)
	}
	if !approxEqual(got.EffectiveCapacityMin(), 960) {
		t.Fatalf("effective capacity = %v", got.EffectiveCapacityMin())
	}

	// A routing operation needing 2 machines pins the work center's capacity.
	it := f.item(t, "FG")
	rt := &domain.Routing{ItemID: it.ID, Description: "main", IsActive: true}
	if err := f.repos.Routings.Create(f.ctx, rt); err != nil {
		t.Fatal(err)
	}
	op := &domain.RoutingOperation{RoutingID: rt.ID, SeqNo: 10, WorkCenterID: wc.ID, RunMinutesPerUnit: 1, MachinesRequired: 2, WorkersRequired: 1}
	if err := f.repos.Routings.AddOperation(f.ctx, op); err != nil {
		t.Fatal(err)
	}
	got.MachineCount = 1
	wantErrContains(t, wcs.Update(f.ctx, got), "work center resource reduction would invalidate a primary routing operation")

	got.MachineCount = 3
	got.Name = "renamed"
	if err := wcs.Update(f.ctx, got); err != nil {
		t.Fatal(err)
	}
	if g, _ := wcs.Get(f.ctx, wc.ID); g.Name != "renamed" || g.MachineCount != 3 {
		t.Fatalf("update not applied: %+v", g)
	}

	list, err := wcs.List(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].Code > list[i].Code {
			t.Fatal("List must be ordered by code")
		}
	}

	// Deleting a work center that is still referenced by routings fails; an
	// unreferenced one can be deleted.
	if err := wcs.Delete(f.ctx, wc.ID); err == nil {
		t.Fatal("referenced work center must not be deletable")
	}
	spare := f.workCenter(t, 1, 1)
	if err := wcs.Delete(f.ctx, spare.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := wcs.Get(f.ctx, spare.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted work center: %v", err)
	}
}

func TestRoutingsOperationsAndAlternatives(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rts := f.repos.Routings
	it := f.item(t, "FG")
	primary := f.workCenter(t, 2, 2)
	small := f.workCenter(t, 1, 1)
	big := f.workCenter(t, 4, 4)

	inactive := &domain.Routing{ItemID: it.ID, Description: "old", IsActive: false}
	active := &domain.Routing{ItemID: it.ID, Description: "current", IsActive: true}
	for _, r := range []*domain.Routing{inactive, active} {
		if err := rts.Create(f.ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := rts.ActiveForItem(f.ctx, it.ID)
	if err != nil || got.ID != active.ID {
		t.Fatalf("ActiveForItem: %+v %v", got, err)
	}
	if _, err := rts.ActiveForItem(f.ctx, uuid.New()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("ActiveForItem unknown: %v", err)
	}

	// Operations are inserted out of order but returned by seq_no.
	op20 := &domain.RoutingOperation{RoutingID: active.ID, SeqNo: 20, WorkCenterID: primary.ID, RunMinutesPerUnit: 2, MachinesRequired: 1, WorkersRequired: 1}
	op10 := &domain.RoutingOperation{RoutingID: active.ID, SeqNo: 10, WorkCenterID: primary.ID, SetupMinutes: 15, RunMinutesPerUnit: 1, MachinesRequired: 2, WorkersRequired: 1}
	for _, op := range []*domain.RoutingOperation{op20, op10} {
		if err := rts.AddOperation(f.ctx, op); err != nil {
			t.Fatal(err)
		}
	}
	// Inactive routing ops must not leak into OperationsForItem.
	if err := rts.AddOperation(f.ctx, &domain.RoutingOperation{RoutingID: inactive.ID, SeqNo: 5, WorkCenterID: primary.ID, MachinesRequired: 1, WorkersRequired: 1}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		op   domain.RoutingOperation
		want string
	}{
		{"machines exceed primary", domain.RoutingOperation{RoutingID: active.ID, SeqNo: 30, WorkCenterID: small.ID, MachinesRequired: 2, WorkersRequired: 1}, "exceeds primary work center capacity"},
		{"overlap needs transfer batch", domain.RoutingOperation{RoutingID: active.ID, SeqNo: 40, WorkCenterID: primary.ID, OverlapEnabled: true, MachinesRequired: 1, WorkersRequired: 1}, "routing_overlap_transfer_qty_chk"},
	}
	for _, tc := range tests {
		op := tc.op
		wantErrContains(t, rts.AddOperation(f.ctx, &op), tc.want)
	}

	ops, err := rts.Operations(f.ctx, active.ID)
	if err != nil || len(ops) != 2 || ops[0].ID != op10.ID || ops[1].ID != op20.ID {
		t.Fatalf("Operations: %+v %v", ops, err)
	}
	byItem, err := rts.OperationsForItem(f.ctx, it.ID)
	if err != nil || len(byItem) != 2 || byItem[0].SeqNo != 10 {
		t.Fatalf("OperationsForItem: %+v %v", byItem, err)
	}

	op20.Description = "finish"
	op20.SetupFamily = "PAINT"
	op20.SeqNo = 30
	if err := rts.UpdateOperation(f.ctx, op20); err != nil {
		t.Fatal(err)
	}
	ops, _ = rts.Operations(f.ctx, active.ID)
	if ops[1].SeqNo != 30 || ops[1].Description != "finish" || ops[1].SetupFamily != "PAINT" {
		t.Fatalf("UpdateOperation: %+v", ops[1])
	}

	// Alternatives: defaults, primary/alternative and capacity guards.
	alt := &domain.RoutingOperationAlternative{RoutingOperationID: op10.ID, WorkCenterID: big.ID, Priority: -1, IsActive: true}
	if err := rts.AddAlternative(f.ctx, alt); err != nil {
		t.Fatal(err)
	}
	if alt.Priority != 100 || alt.RunTimeMultiplier != 1 || alt.SetupTimeMultiplier != 1 || alt.CreatedAt.IsZero() {
		t.Fatalf("alternative defaults: %+v", alt)
	}
	altTests := []struct {
		name string
		alt  domain.RoutingOperationAlternative
		want string
	}{
		{"same as primary", domain.RoutingOperationAlternative{RoutingOperationID: op10.ID, WorkCenterID: primary.ID, IsActive: true}, "must differ from primary"},
		{"too small", domain.RoutingOperationAlternative{RoutingOperationID: op10.ID, WorkCenterID: small.ID, IsActive: true}, "exceeds alternative work center capacity"},
		{"duplicate", domain.RoutingOperationAlternative{RoutingOperationID: op10.ID, WorkCenterID: big.ID, IsActive: true}, "duplicate key"},
		{"unknown operation", domain.RoutingOperationAlternative{RoutingOperationID: uuid.New(), WorkCenterID: big.ID, IsActive: true}, "not found"},
	}
	for _, tc := range altTests {
		a := tc.alt
		err := rts.AddAlternative(f.ctx, &a)
		if err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
		wantErrContains(t, err, tc.want)
	}
	second := &domain.RoutingOperationAlternative{RoutingOperationID: op20.ID, WorkCenterID: small.ID, Priority: 5, RunTimeMultiplier: 1.5, SetupTimeMultiplier: 2, IsActive: true}
	if err := rts.AddAlternative(f.ctx, second); err != nil {
		t.Fatal(err)
	}
	alts, err := rts.Alternatives(f.ctx, op10.ID)
	if err != nil || len(alts) != 1 || alts[0].WorkCenterID != big.ID {
		t.Fatalf("Alternatives: %+v %v", alts, err)
	}

	// Raising requirements beyond an active alternative's capacity is rejected.
	op20.MachinesRequired = 2
	wantErrContains(t, rts.UpdateOperation(f.ctx, op20), "active alternative work center capacity")

	if err := rts.DeleteAlternative(f.ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if a, _ := rts.Alternatives(f.ctx, op20.ID); len(a) != 0 {
		t.Fatalf("alternative not deleted: %+v", a)
	}
	if err := rts.DeleteOperation(f.ctx, op10.ID); err != nil {
		t.Fatal(err)
	}
	// Alternatives cascade with their operation.
	if a, _ := rts.Alternatives(f.ctx, op10.ID); len(a) != 0 {
		t.Fatalf("alternatives must cascade: %+v", a)
	}
	if ops, _ = rts.Operations(f.ctx, active.ID); len(ops) != 1 {
		t.Fatalf("operation not deleted: %d", len(ops))
	}

	all, err := rts.List(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].CreatedAt.Before(all[i].CreatedAt) {
			t.Fatal("routings List must be newest first")
		}
	}
}

func TestSetupMatrixUpsert(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	wcs := f.repos.WorkCenters
	wc := f.workCenter(t, 1, 1)

	wildcard := &domain.WorkCenterSetupMatrixRow{WorkCenterID: wc.ID, ToSetupFamily: "B", SetupMinutes: 30}
	if err := wcs.UpsertSetupMatrix(f.ctx, wildcard); err != nil {
		t.Fatal(err)
	}
	if wildcard.FromSetupFamily != "*" {
		t.Fatalf("empty from-family must default to wildcard, got %q", wildcard.FromSetupFamily)
	}
	specific := &domain.WorkCenterSetupMatrixRow{WorkCenterID: wc.ID, FromSetupFamily: "A", ToSetupFamily: "B", SetupMinutes: 10}
	if err := wcs.UpsertSetupMatrix(f.ctx, specific); err != nil {
		t.Fatal(err)
	}
	// Upserting the same transition updates minutes in place.
	if err := wcs.UpsertSetupMatrix(f.ctx, &domain.WorkCenterSetupMatrixRow{WorkCenterID: wc.ID, FromSetupFamily: "A", ToSetupFamily: "B", SetupMinutes: 12}); err != nil {
		t.Fatal(err)
	}
	if err := wcs.UpsertSetupMatrix(f.ctx, &domain.WorkCenterSetupMatrixRow{WorkCenterID: wc.ID, FromSetupFamily: "A", ToSetupFamily: "C", SetupMinutes: -1}); err == nil {
		t.Fatal("negative setup minutes must be rejected")
	}

	rows, err := wcs.SetupMatrix(f.ctx, wc.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("SetupMatrix: %+v %v", rows, err)
	}
	// ORDER BY to_setup_family, from_setup_family: '*' sorts before 'A'.
	if rows[0].FromSetupFamily != "*" || rows[1].ID != specific.ID || !approxEqual(rows[1].SetupMinutes, 12) {
		t.Fatalf("matrix rows: %+v", rows)
	}
	if err := wcs.DeleteSetupMatrix(f.ctx, wildcard.ID); err != nil {
		t.Fatal(err)
	}
	if rows, _ = wcs.SetupMatrix(f.ctx, wc.ID); len(rows) != 1 {
		t.Fatalf("after delete: %+v", rows)
	}
}

func TestCalendarsAndExceptions(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	cals := f.repos.Calendars

	// Migrations seed STD-5DAY as the single default calendar.
	def, err := cals.Default(f.ctx)
	if err != nil || def == nil || def.Code != "STD-5DAY" {
		t.Fatalf("Default: %+v %v", def, err)
	}
	if def.MinutesForWeekday(time.Monday) != 480 || def.MinutesForWeekday(time.Sunday) != 0 {
		t.Fatalf("seed weekly pattern: %+v", def)
	}

	c := &domain.WorkCalendar{Code: uniq("CAL"), Name: "two shifts", MondayMin: 960, TuesdayMin: 960, WednesdayMin: 960, ThursdayMin: 960, FridayMin: 960}
	if err := cals.Create(f.ctx, c); err != nil {
		t.Fatal(err)
	}
	if c.ID == uuid.Nil || c.CreatedAt.IsZero() {
		t.Fatalf("defaults: %+v", c)
	}
	invalid := []domain.WorkCalendar{
		{Code: uniq("CAL"), Name: "too long day", MondayMin: 1441},
		{Code: uniq("CAL"), Name: "negative", SundayMin: -1},
		{Code: c.Code, Name: "dup code"},
		{Code: uniq("CAL"), Name: "second default", IsDefault: true},
	}
	for _, bad := range invalid {
		b := bad
		if err := cals.Create(f.ctx, &b); err == nil {
			t.Fatalf("calendar %q must be rejected", b.Name)
		}
	}

	// Switching the default requires clearing the old one first (partial unique index).
	c.IsDefault = true
	if err := cals.Update(f.ctx, c); err == nil {
		t.Fatal("two default calendars must be rejected")
	}
	def.IsDefault = false
	if err := cals.Update(f.ctx, def); err != nil {
		t.Fatal(err)
	}
	if err := cals.Update(f.ctx, c); err != nil {
		t.Fatal(err)
	}
	if d, _ := cals.Default(f.ctx); d == nil || d.ID != c.ID {
		t.Fatalf("new default: %+v", d)
	}
	list, err := cals.List(f.ctx)
	if err != nil || len(list) < 2 || list[0].ID != c.ID {
		t.Fatalf("List must put the default first: %+v %v", list, err)
	}

	// Exceptions: upsert by (calendar, date), range filter, delete.
	holiday := &domain.CalendarException{CalendarID: c.ID, ExceptionDate: date(2026, 12, 31), Kind: "HOLIDAY", Description: "year end"}
	if err := cals.AddException(f.ctx, holiday); err != nil {
		t.Fatal(err)
	}
	if err := cals.AddException(f.ctx, &domain.CalendarException{CalendarID: c.ID, ExceptionDate: date(2026, 12, 31), Kind: "WORKDAY", Minutes: 240, Description: "half day"}); err != nil {
		t.Fatal(err)
	}
	if err := cals.AddException(f.ctx, &domain.CalendarException{CalendarID: c.ID, ExceptionDate: date(2027, 1, 2), Kind: "WORKDAY", Minutes: 480}); err != nil {
		t.Fatal(err)
	}
	if err := cals.AddException(f.ctx, &domain.CalendarException{CalendarID: c.ID, ExceptionDate: date(2027, 1, 3), Kind: "VACATION"}); err == nil {
		t.Fatal("unknown exception kind must be rejected")
	}

	exs, err := cals.Exceptions(f.ctx, c.ID)
	if err != nil || len(exs) != 2 {
		t.Fatalf("Exceptions: %+v %v", exs, err)
	}
	if exs[0].ID != holiday.ID || exs[0].Kind != "WORKDAY" || exs[0].Minutes != 240 || exs[0].Description != "half day" {
		t.Fatalf("upsert must update the existing row: %+v", exs[0])
	}
	inRange, err := cals.ExceptionsInRange(f.ctx, c.ID, date(2027, 1, 1), date(2027, 1, 31))
	if err != nil || len(inRange) != 1 || !inRange[0].ExceptionDate.Equal(date(2027, 1, 2)) {
		t.Fatalf("ExceptionsInRange: %+v %v", inRange, err)
	}
	if err := cals.DeleteException(f.ctx, holiday.ID); err != nil {
		t.Fatal(err)
	}
	if exs, _ = cals.Exceptions(f.ctx, c.ID); len(exs) != 1 {
		t.Fatalf("after delete: %+v", exs)
	}

	// Deleting a calendar cascades its exceptions and nulls work-center links.
	other := &domain.WorkCalendar{Code: uniq("CAL"), Name: "temp"}
	if err := cals.Create(f.ctx, other); err != nil {
		t.Fatal(err)
	}
	wc := &domain.WorkCenter{Code: uniq("WC"), Name: "wc", Efficiency: 1, Utilization: 1, MachineCount: 1, WorkerCount: 1, CalendarID: &other.ID}
	if err := f.repos.WorkCenters.Create(f.ctx, wc); err != nil {
		t.Fatal(err)
	}
	if err := cals.AddException(f.ctx, &domain.CalendarException{CalendarID: other.ID, ExceptionDate: date(2027, 2, 1), Kind: "HOLIDAY"}); err != nil {
		t.Fatal(err)
	}
	if err := cals.Delete(f.ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := cals.Get(f.ctx, other.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted calendar Get: %v", err)
	}
	if exs, _ := cals.Exceptions(f.ctx, other.ID); len(exs) != 0 {
		t.Fatal("exceptions must cascade")
	}
	if g, _ := f.repos.WorkCenters.Get(f.ctx, wc.ID); g.CalendarID != nil {
		t.Fatal("work center calendar link must be set NULL")
	}

	// With no default at all, Default returns (nil, nil).
	c.IsDefault = false
	if err := cals.Update(f.ctx, c); err != nil {
		t.Fatal(err)
	}
	if d, err := cals.Default(f.ctx); d != nil || err != nil {
		t.Fatalf("no default: %+v %v", d, err)
	}
}

func TestCycleCounts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ccs := f.repos.CycleCounts
	it := f.item(t, "RM")

	if last, err := ccs.LastScheduledFor(f.ctx, it.ID); err != nil || last != nil {
		t.Fatalf("no counts yet: %v %v", last, err)
	}

	expected := 100.0
	early := &domain.CycleCount{ItemID: it.ID, ABCClass: "A", ScheduledDate: date(2026, 11, 2), ExpectedQty: &expected}
	late := &domain.CycleCount{ItemID: it.ID, ABCClass: "A", ScheduledDate: date(2026, 12, 2), ExpectedQty: &expected}
	for _, c := range []*domain.CycleCount{late, early} {
		if err := ccs.Create(f.ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if early.Status != "PENDING" || early.CreatedAt.IsZero() {
		t.Fatalf("defaults: %+v", early)
	}
	if err := ccs.Create(f.ctx, &domain.CycleCount{ItemID: it.ID, ABCClass: "D", ScheduledDate: date(2026, 11, 2)}); err == nil {
		t.Fatal("invalid ABC class must be rejected")
	}

	last, err := ccs.LastScheduledFor(f.ctx, it.ID)
	if err != nil || last == nil || !last.Equal(date(2026, 12, 2)) {
		t.Fatalf("LastScheduledFor: %v %v", last, err)
	}

	// Recording a count derives variance in the DB (generated column).
	counted := 97.0
	cd := date(2026, 11, 3)
	early.CountedQty, early.CountedDate, early.Status, early.Notes = &counted, &cd, "COUNTED", "short 3"
	if err := ccs.Update(f.ctx, early); err != nil {
		t.Fatal(err)
	}
	got, err := ccs.Get(f.ctx, early.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Variance == nil || !approxEqual(*got.Variance, -3) || got.Status != "COUNTED" || got.Notes != "short 3" {
		t.Fatalf("counted: %+v", got)
	}
	early.Status = "LOST"
	if err := ccs.Update(f.ctx, early); err == nil {
		t.Fatal("invalid status must be rejected")
	}

	pending, err := ccs.List(f.ctx, "PENDING")
	if err != nil {
		t.Fatal(err)
	}
	countedList, _ := ccs.List(f.ctx, "COUNTED")
	all, _ := ccs.List(f.ctx, "")
	has := func(list []domain.CycleCountWithItem, id uuid.UUID) bool {
		for _, c := range list {
			if c.ID == id {
				if c.ItemCode != it.Code {
					t.Fatalf("joined item code = %q", c.ItemCode)
				}
				return true
			}
		}
		return false
	}
	if !has(pending, late.ID) || has(pending, early.ID) || !has(countedList, early.ID) || !has(all, early.ID) || !has(all, late.ID) {
		t.Fatal("status filter mismatch")
	}
	if _, err := ccs.Get(f.ctx, uuid.New()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get unknown: %v", err)
	}
}
