package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func dDay(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

func TestActionMessages_Expedite(t *testing.T) {
	id := uuid.New()
	in := ActionMessageInput{
		Today:        dDay("2026-05-01"),
		LeadTimeDays: map[uuid.UUID]int{id: 5},
		ItemCodes:    map[uuid.UUID]string{id: "BOLT-1"},
		PlannedOrders: []domain.MRPResult{
			{ItemID: id, ItemCode: "BOLT-1", Period: dDay("2026-04-30"), PlannedOrder: 100},
		},
		OpenSupplies:  nil,
		ToleranceDays: 1,
	}
	got := GenerateActionMessages(in)
	if len(got) != 1 || got[0].Kind != "EXPEDITE" {
		t.Fatalf("expected EXPEDITE, got %+v", got)
	}
	if got[0].Severity != "CRITICAL" {
		t.Errorf("expected CRITICAL severity, got %s", got[0].Severity)
	}
}

func TestActionMessages_Release(t *testing.T) {
	id := uuid.New()
	in := ActionMessageInput{
		Today:        dDay("2026-05-01"),
		LeadTimeDays: map[uuid.UUID]int{id: 7},
		ItemCodes:    map[uuid.UUID]string{id: "BOLT-1"},
		PlannedOrders: []domain.MRPResult{
			// needDate within LT window
			{ItemID: id, ItemCode: "BOLT-1", Period: dDay("2026-05-05"), PlannedOrder: 50},
		},
		ToleranceDays: 1,
	}
	got := GenerateActionMessages(in)
	if len(got) != 1 || got[0].Kind != "RELEASE" {
		t.Fatalf("expected RELEASE, got %+v", got)
	}
}

func TestActionMessages_FutureRelease(t *testing.T) {
	id := uuid.New()
	in := ActionMessageInput{
		Today:        dDay("2026-05-01"),
		LeadTimeDays: map[uuid.UUID]int{id: 5},
		ItemCodes:    map[uuid.UUID]string{id: "BOLT-1"},
		PlannedOrders: []domain.MRPResult{
			{ItemID: id, ItemCode: "BOLT-1", Period: dDay("2026-06-15"), PlannedOrder: 50},
		},
		ToleranceDays: 1,
	}
	got := GenerateActionMessages(in)
	if len(got) != 1 || got[0].Kind != "FUTURE_RELEASE" {
		t.Fatalf("expected FUTURE_RELEASE, got %+v", got)
	}
}

func TestActionMessages_RescheduleIn(t *testing.T) {
	id := uuid.New()
	supplyID := uuid.New()
	in := ActionMessageInput{
		Today:        dDay("2026-05-01"),
		LeadTimeDays: map[uuid.UUID]int{id: 5},
		ItemCodes:    map[uuid.UUID]string{id: "BOLT-1"},
		PlannedOrders: []domain.MRPResult{
			{ItemID: id, ItemCode: "BOLT-1", Period: dDay("2026-05-10"), PlannedOrder: 100},
		},
		OpenSupplies: []SupplyOrder{
			// 既存PO 納期は 5/20、必要日は 5/10 → 10日前倒し
			{DocType: "PO", DocNo: "PO-1", ID: supplyID,
				ItemID: id, Quantity: 100, DueDate: dDay("2026-05-20"), Status: "OPEN"},
		},
		ToleranceDays: 1,
	}
	got := GenerateActionMessages(in)
	if len(got) != 1 || got[0].Kind != "RESCHEDULE_IN" {
		t.Fatalf("expected RESCHEDULE_IN, got %+v", got)
	}
	if got[0].RefDocNo != "PO-1" {
		t.Errorf("expected ref PO-1, got %s", got[0].RefDocNo)
	}
}

func TestActionMessages_RescheduleOut(t *testing.T) {
	id := uuid.New()
	in := ActionMessageInput{
		Today:        dDay("2026-05-01"),
		LeadTimeDays: map[uuid.UUID]int{id: 5},
		ItemCodes:    map[uuid.UUID]string{id: "BOLT-1"},
		PlannedOrders: []domain.MRPResult{
			{ItemID: id, ItemCode: "BOLT-1", Period: dDay("2026-05-30"), PlannedOrder: 100},
		},
		OpenSupplies: []SupplyOrder{
			// 既存PO 納期は 5/15、必要日は 5/30 → 15日後ろ倒し
			{DocType: "PO", DocNo: "PO-1", ID: uuid.New(),
				ItemID: id, Quantity: 100, DueDate: dDay("2026-05-15"), Status: "OPEN"},
		},
		ToleranceDays: 1,
	}
	got := GenerateActionMessages(in)
	if len(got) != 1 || got[0].Kind != "RESCHEDULE_OUT" {
		t.Fatalf("expected RESCHEDULE_OUT, got %+v", got)
	}
}

func TestActionMessages_Cancel(t *testing.T) {
	id := uuid.New()
	in := ActionMessageInput{
		Today:         dDay("2026-05-01"),
		LeadTimeDays:  map[uuid.UUID]int{id: 5},
		ItemCodes:     map[uuid.UUID]string{id: "BOLT-1"},
		PlannedOrders: nil, // 需要なし
		OpenSupplies: []SupplyOrder{
			{DocType: "PO", DocNo: "PO-OBSOLETE", ID: uuid.New(),
				ItemID: id, Quantity: 100, DueDate: dDay("2026-06-01"), Status: "OPEN"},
		},
		ToleranceDays: 1,
	}
	got := GenerateActionMessages(in)
	if len(got) != 1 || got[0].Kind != "CANCEL" {
		t.Fatalf("expected CANCEL, got %+v", got)
	}
}

func TestActionMessages_PerfectMatchNoMessage(t *testing.T) {
	id := uuid.New()
	in := ActionMessageInput{
		Today:        dDay("2026-05-01"),
		LeadTimeDays: map[uuid.UUID]int{id: 5},
		ItemCodes:    map[uuid.UUID]string{id: "BOLT-1"},
		PlannedOrders: []domain.MRPResult{
			{ItemID: id, ItemCode: "BOLT-1", Period: dDay("2026-05-15"), PlannedOrder: 100},
		},
		OpenSupplies: []SupplyOrder{
			{DocType: "PO", DocNo: "PO-1", ID: uuid.New(),
				ItemID: id, Quantity: 100, DueDate: dDay("2026-05-15"), Status: "OPEN"},
		},
		ToleranceDays: 1,
	}
	got := GenerateActionMessages(in)
	if len(got) != 0 {
		t.Errorf("expected no messages for perfect match, got %+v", got)
	}
}

func TestGenerateNettedMRPActions_DoesNotDoubleNetScheduledSupply(t *testing.T) {
	id := uuid.New()
	today := dDay("2026-05-01")
	release := dDay("2026-05-01")
	out := GenerateNettedMRPActions(today, []domain.MRPResult{
		{
			ItemID: id, ItemCode: "BOLT-1",
			Period: dDay("2026-05-10"), PlannedOrder: 40,
			PlannedReleaseDate: &release,
		},
	})
	if len(out) != 1 {
		t.Fatalf("expected one action for remaining net shortage, got %d", len(out))
	}
	if out[0].Kind != "RELEASE" || out[0].Quantity != 40 {
		t.Fatalf("expected RELEASE qty 40, got kind=%s qty=%v", out[0].Kind, out[0].Quantity)
	}
}

func TestGenerateNettedMRPActions_UsesExplicitReleaseDate(t *testing.T) {
	id := uuid.New()
	today := dDay("2026-05-01")
	futureRelease := dDay("2026-05-05")
	out := GenerateNettedMRPActions(today, []domain.MRPResult{
		{
			ItemID: id, ItemCode: "PART-1",
			Period: dDay("2026-05-12"), PlannedOrder: 25,
			PlannedReleaseDate: &futureRelease,
		},
	})
	if len(out) != 1 || out[0].Kind != "FUTURE_RELEASE" {
		t.Fatalf("expected FUTURE_RELEASE, got %+v", out)
	}
}
