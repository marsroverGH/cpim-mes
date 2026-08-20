package service

import (
	"math"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestConsumeForecastQty(t *testing.T) {
	tests := []struct {
		name                      string
		forecast, orders          float64
		consumed, remaining, over float64
		total                     float64
	}{
		{"orders consume forecast", 100, 60, 60, 40, 0, 100},
		{"orders exceed forecast", 100, 140, 100, 0, 40, 140},
		{"no orders", 100, 0, 0, 100, 0, 100},
		{"no forecast", 0, 30, 0, 0, 30, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, r, o, total := consumeForecastQty(tt.forecast, tt.orders)
			for label, pair := range map[string][2]float64{
				"consumed": {c, tt.consumed}, "remaining": {r, tt.remaining},
				"over": {o, tt.over}, "total": {total, tt.total},
			} {
				if math.Abs(pair[0]-pair[1]) > 1e-9 {
					t.Fatalf("%s got %v want %v", label, pair[0], pair[1])
				}
			}
		})
	}
}

func TestBuildConsumptionBucketsUsesOrderDueDateBucket(t *testing.T) {
	runID := uuid.New()
	itemID := uuid.New()
	start := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	values := []domain.ForecastValue{
		{ForecastRunID: runID, Period: start, Quantity: 100},
		{ForecastRunID: runID, Period: start.AddDate(0, 0, 7), Quantity: 80},
	}
	orders := []domain.DemandForecast{
		{ItemID: itemID, DueDate: start.AddDate(0, 0, 2), Quantity: 60, Source: "ORDER"},
		{ItemID: itemID, DueDate: start.AddDate(0, 0, 8), Quantity: 100, Source: "ORDER"},
		{ItemID: itemID, DueDate: start.AddDate(0, 0, 3), Quantity: 999, Source: "FORECAST"},
	}
	got := buildConsumptionBuckets(values, orders, 7)
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].OrderQty != 60 || got[0].TotalDemand != 100 || got[0].RemainingForecast != 40 {
		t.Fatalf("bucket0=%+v", got[0])
	}
	if got[1].OrderQty != 100 || got[1].TotalDemand != 100 || got[1].OrderAboveForecast != 20 {
		t.Fatalf("bucket1=%+v", got[1])
	}
}
