package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestCalculateOEERatios(t *testing.T) {
	a, p, q, oee, speed := calculateOEERatios(480, 60, 60, 30, 300, 90, 10)
	if a < 0.79 || a > 0.81 {
		t.Fatalf("availability=%v want about .80", a)
	}
	if p < 0.66 || p > 0.67 {
		t.Fatalf("performance=%v want about .667", p)
	}
	if q < 0.89 || q > 0.91 {
		t.Fatalf("quality=%v want .90", q)
	}
	if oee < 0.47 || oee > 0.49 {
		t.Fatalf("oee=%v want about .48", oee)
	}
	if speed != 150 {
		t.Fatalf("speed loss=%v want 150", speed)
	}
}

func TestPerformanceHashOrderIndependent(t *testing.T) {
	a := domain.ProductionPerformanceResult{WorkCenterID: uuid.New(), WorkCenterCode: "A", OEE: .7, RecommendedEfficiency: .8, RecommendedUtilization: .9, Confidence: "HIGH"}
	b := domain.ProductionPerformanceResult{WorkCenterID: uuid.New(), WorkCenterCode: "B", OEE: .8, RecommendedEfficiency: .9, RecommendedUtilization: .95, Confidence: "MEDIUM"}
	if canonicalProductionPerformanceHash([]domain.ProductionPerformanceResult{a, b}) != canonicalProductionPerformanceHash([]domain.ProductionPerformanceResult{b, a}) {
		t.Fatal("canonical performance hash depends on result order")
	}
}

func TestOverlapMinutes(t *testing.T) {
	base := time.Date(2026, 8, 21, 8, 0, 0, 0, time.UTC)
	if got := overlapMinutes(base, base.Add(2*time.Hour), base.Add(time.Hour), base.Add(3*time.Hour)); got != 60 {
		t.Fatalf("overlap=%v want 60", got)
	}
}
