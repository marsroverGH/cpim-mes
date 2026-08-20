package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestCanonicalPromiseHashIsOrderIndependent(t *testing.T) {
	lineA, lineB := uuid.New(), uuid.New()
	d1 := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	lines := []domain.OrderPromiseLineResult{
		{SalesOrderLineID: lineA, RequestedQty: 10, RequestedDate: d1, ATPQty: 4, CTPQty: 6, EarliestFullDate: &d2, MaterialReadyDate: &d1, CapacityReadyDate: &d2, PromiseMethod: "ATP_CTP", ConstraintType: "CAPACITY"},
		{SalesOrderLineID: lineB, RequestedQty: 5, RequestedDate: d1, ATPQty: 5, EarliestFullDate: &d1, PromiseMethod: "ATP", ConstraintType: "NONE"},
	}
	confs := []domain.OrderPromiseConfirmation{
		{SalesOrderLineID: lineA, SequenceNo: 2, Quantity: 6, ConfirmedDate: d2, Source: "CTP_PRODUCTION"},
		{SalesOrderLineID: lineA, SequenceNo: 1, Quantity: 4, ConfirmedDate: d1, Source: "ATP"},
		{SalesOrderLineID: lineB, SequenceNo: 1, Quantity: 5, ConfirmedDate: d1, Source: "ATP"},
	}
	h1 := canonicalPromiseHash(lines, confs)
	lines[0], lines[1] = lines[1], lines[0]
	confs[0], confs[2] = confs[2], confs[0]
	h2 := canonicalPromiseHash(lines, confs)
	if h1 != h2 {
		t.Fatalf("canonical promise hash depends on input ordering: %s != %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("expected sha256 hex hash, got %q", h1)
	}
}

func TestNormalizePromiseHorizon(t *testing.T) {
	if got, err := normalizePromiseHorizon(0); err != nil || got != 180 {
		t.Fatalf("default horizon got=%d err=%v", got, err)
	}
	if got, err := normalizePromiseHorizon(366); err != nil || got != 366 {
		t.Fatalf("max horizon got=%d err=%v", got, err)
	}
	if _, err := normalizePromiseHorizon(367); err == nil {
		t.Fatal("expected horizon validation error")
	}
}
