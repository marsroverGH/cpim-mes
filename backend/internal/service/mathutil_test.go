package service

import (
	"math"
	"testing"
	"time"
)

// ---------- RoundUpToMultiple ----------

func TestRoundUpToMultiple(t *testing.T) {
	cases := []struct {
		name string
		x, m float64
		want float64
	}{
		{"exact multiple", 100, 25, 100},
		{"round up", 7, 5, 10},
		{"smaller than lot", 3, 10, 10},
		{"zero x", 0, 5, 0},
		{"zero m falls back to x", 7, 0, 7},
		{"negative m falls back to x", 7, -5, 7},
		{"fractional", 12.5, 5, 15},
		{"large lot multiple", 250, 100, 300},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RoundUpToMultiple(c.x, c.m)
			if got != c.want {
				t.Errorf("RoundUpToMultiple(%v, %v) = %v, want %v", c.x, c.m, got, c.want)
			}
		})
	}
}

// ---------- TruncateDay ----------

func TestTruncateDay(t *testing.T) {
	loc := time.UTC
	in := time.Date(2026, 4, 26, 14, 53, 20, 1234, loc)
	got := TruncateDay(in)
	want := time.Date(2026, 4, 26, 0, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("TruncateDay = %v, want %v", got, want)
	}
}

func TestTruncateDay_PreservesLocation(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Tokyo")
	if loc == nil {
		t.Skip("Asia/Tokyo not available in this image")
	}
	in := time.Date(2026, 4, 26, 23, 30, 0, 0, loc)
	got := TruncateDay(in)
	if got.Location() != loc {
		t.Errorf("location not preserved: got %v want %v", got.Location(), loc)
	}
}

// ---------- FitSMA ----------

func TestFitSMA(t *testing.T) {
	actuals := []float64{10, 20, 30, 40, 50}
	got := FitSMA(actuals, 2)

	// Expected:
	// fitted[0] = actuals[0] = 10
	// fitted[1] = avg(actuals[0:1]) = 10
	// fitted[2] = avg(actuals[0:2]) = 15  (only 2 elements so window=2 OK)
	// fitted[3] = avg(actuals[1:3]) = 25
	// fitted[4] = avg(actuals[2:4]) = 35
	want := []float64{10, 10, 15, 25, 35}
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %d want %d", len(got), len(want))
	}
	for i := range got {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Errorf("FitSMA[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestFitSMA_EmptyAndSingle(t *testing.T) {
	if got := FitSMA(nil, 3); len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
	got := FitSMA([]float64{42}, 3)
	if len(got) != 1 || got[0] != 42 {
		t.Errorf("single-element fitted should equal actual: got %v", got)
	}
}

// ---------- FitEXPO ----------

func TestFitEXPO(t *testing.T) {
	actuals := []float64{100, 110, 95, 105}
	alpha := 0.5
	got := FitEXPO(actuals, alpha)

	// fitted[0] = 100
	// fitted[1] = 0.5*100 + 0.5*100 = 100
	// fitted[2] = 0.5*110 + 0.5*100 = 105
	// fitted[3] = 0.5*95  + 0.5*105 = 100
	want := []float64{100, 100, 105, 100}
	for i := range got {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Errorf("FitEXPO[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestFitEXPO_AlphaClampedToDefault(t *testing.T) {
	// alpha out of (0,1] should fall back to 0.3
	a := []float64{10, 20}
	g1 := FitEXPO(a, 0)
	g2 := FitEXPO(a, 0.3)
	if g1[1] != g2[1] {
		t.Errorf("expected alpha=0 to fall back to 0.3, got different values: %v vs %v", g1, g2)
	}
}

// ---------- ForecastSMA ----------

func TestForecastSMA_RollingExtrapolation(t *testing.T) {
	// stable history → flat forecast
	got := ForecastSMA([]float64{50, 50, 50, 50}, 3, 4)
	if len(got) != 4 {
		t.Fatalf("expected 4 forecast points, got %d", len(got))
	}
	for i, v := range got {
		if math.Abs(v-50) > 1e-9 {
			t.Errorf("forecast[%d] = %v, want 50 (stable extrapolation)", i, v)
		}
	}
}

// ---------- AccuracyMetrics ----------

func TestAccuracyMetrics_PerfectFitIsZero(t *testing.T) {
	a := []float64{10, 20, 30}
	mae, mape := AccuracyMetrics(a, a)
	if mae != 0 || mape != 0 {
		t.Errorf("perfect fit should yield 0 errors: mae=%v mape=%v", mae, mape)
	}
}

func TestAccuracyMetrics_KnownValues(t *testing.T) {
	a := []float64{0, 10, 20}
	f := []float64{0, 12, 18}
	mae, mape := AccuracyMetrics(a, f)
	// i=1: |10-12|=2, pct=2/10=0.2
	// i=2: |20-18|=2, pct=2/20=0.1
	// mae = 4/2 = 2.0
	// mape = (0.2 + 0.1)/2 * 100 = 15
	if math.Abs(mae-2.0) > 1e-9 {
		t.Errorf("mae = %v, want 2.0", mae)
	}
	if math.Abs(mape-15.0) > 1e-9 {
		t.Errorf("mape = %v, want 15.0", mape)
	}
}

// ---------- EOQ ----------

func TestEOQ(t *testing.T) {
	// EOQ = sqrt(2 * 1000 * 50 / 2) = sqrt(50000) ≈ 223.6068
	got := EOQ(1000, 50, 2)
	want := math.Sqrt(50000)
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("EOQ = %v, want %v", got, want)
	}
}

func TestEOQ_ZeroInputs(t *testing.T) {
	cases := []struct{ d, s, h float64 }{
		{0, 50, 2}, {1000, 0, 2}, {1000, 50, 0}, {-1, 50, 2},
	}
	for _, c := range cases {
		if got := EOQ(c.d, c.s, c.h); got != 0 {
			t.Errorf("EOQ(%v,%v,%v) should be 0 for invalid input, got %v", c.d, c.s, c.h, got)
		}
	}
}

// ---------- ApplyLotSize ----------

func TestApplyLotSize_LFL(t *testing.T) {
	// Lot-for-Lot = exactly the net requirement; fixed lot size is ignored.
	got := ApplyLotSize(7, 0, 5, 0, LotMethodLFL)
	if got != 7 {
		t.Errorf("LFL(7,0,5) = %v, want 7", got)
	}
}

func TestApplyLotSize_FOQ_WithSafetyStock(t *testing.T) {
	// net=23, safety=5, lot=10 → ceil((23+5)/10)*10 = 30
	got := ApplyLotSize(23, 5, 10, 0, LotMethodFOQ)
	if got != 30 {
		t.Errorf("FOQ(23,5,10) = %v, want 30", got)
	}
}

func TestApplyLotSize_EOQ(t *testing.T) {
	// net=100, eoq=75 → ceil(100/75)*75 = 150
	got := ApplyLotSize(100, 0, 1, 75, LotMethodEOQ)
	if got != 150 {
		t.Errorf("EOQ(100, eoq=75) = %v, want 150", got)
	}
}

func TestApplyLotSize_EOQ_FallbackWhenZero(t *testing.T) {
	// EOQ=0 falls back to true Lot-for-Lot.
	got := ApplyLotSize(7, 0, 5, 0, LotMethodEOQ)
	if got != 7 {
		t.Errorf("EOQ fallback should behave like LFL: got %v", got)
	}
}

func TestApplyLotSize_POQ_UsesAggregatedNetExactly(t *testing.T) {
	got := ApplyLotSize(37, 0, 10, 0, LotMethodPOQ)
	if got != 37 {
		t.Errorf("POQ aggregated net should be ordered exactly: got %v want 37", got)
	}
}

func TestApplyLotSize_ZeroNet(t *testing.T) {
	cases := []LotSizeMethod{LotMethodLFL, LotMethodFOQ, LotMethodPOQ, LotMethodEOQ}
	for _, m := range cases {
		if got := ApplyLotSize(0, 0, 10, 100, m); got != 0 {
			t.Errorf("method=%s: zero net should yield 0 planned, got %v", m, got)
		}
	}
}

// ---------- SplitFIFO ----------

func TestSplitFIFO_SingleLotSufficient(t *testing.T) {
	lots := []LotBalance{
		{LotID: "L1", LotNo: "L-001", Balance: 100},
	}
	got, short := SplitFIFO(lots, 30)
	if short != 0 {
		t.Errorf("expected no shortage, got %v", short)
	}
	if len(got) != 1 || got[0].Consumed != 30 {
		t.Errorf("expected single consumption of 30, got %+v", got)
	}
}

func TestSplitFIFO_SpansMultipleLots(t *testing.T) {
	lots := []LotBalance{
		{LotID: "L1", LotNo: "L-001", Balance: 5},
		{LotID: "L2", LotNo: "L-002", Balance: 7},
		{LotID: "L3", LotNo: "L-003", Balance: 10},
	}
	got, short := SplitFIFO(lots, 14)
	if short != 0 {
		t.Errorf("expected no shortage, got %v", short)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 consumptions, got %d: %+v", len(got), got)
	}
	if got[0].Consumed != 5 || got[1].Consumed != 7 || got[2].Consumed != 2 {
		t.Errorf("FIFO split wrong: %+v", got)
	}
}

func TestSplitFIFO_StopsAtExactMatch(t *testing.T) {
	lots := []LotBalance{
		{LotID: "L1", Balance: 10},
		{LotID: "L2", Balance: 5},
	}
	got, short := SplitFIFO(lots, 10)
	if short != 0 {
		t.Errorf("no shortage expected, got %v", short)
	}
	if len(got) != 1 {
		t.Errorf("should not touch L2, got %d consumptions", len(got))
	}
}

func TestSplitFIFO_DetectsShortage(t *testing.T) {
	lots := []LotBalance{
		{LotID: "L1", Balance: 5},
		{LotID: "L2", Balance: 7},
	}
	got, short := SplitFIFO(lots, 30)
	if short != 18 {
		t.Errorf("expected shortage 18 (=30-5-7), got %v", short)
	}
	// Should still consume everything available
	total := 0.0
	for _, c := range got {
		total += c.Consumed
	}
	if total != 12 {
		t.Errorf("expected total consumed 12, got %v", total)
	}
}

func TestSplitFIFO_SkipsZeroBalance(t *testing.T) {
	lots := []LotBalance{
		{LotID: "L1", Balance: 0},
		{LotID: "L2", Balance: 10},
	}
	got, short := SplitFIFO(lots, 5)
	if short != 0 {
		t.Errorf("no shortage expected, got %v", short)
	}
	if len(got) != 1 || got[0].LotID != "L2" {
		t.Errorf("should skip empty L1, got %+v", got)
	}
}

func TestSplitFIFO_ZeroRequired(t *testing.T) {
	lots := []LotBalance{{LotID: "L1", Balance: 100}}
	got, short := SplitFIFO(lots, 0)
	if len(got) != 0 || short != 0 {
		t.Errorf("zero required should yield empty consumption, got %+v / shortage=%v", got, short)
	}
}

// ---------- Holt-Winters ----------

func TestHoltWinters_RecoversConstantSeason(t *testing.T) {
	// シーズン長4で、高低高低高低高低 のパターン。
	// レベル一定・トレンドゼロで季節成分のみ
	series := []float64{10, 5, 12, 7, 10, 5, 12, 7, 10, 5, 12, 7}
	state := FitHoltWintersAdditive(series, 4, 0.3, 0.1, 0.3)
	if state.SeasonLength != 4 {
		t.Fatalf("expected season length 4, got %d", state.SeasonLength)
	}
	// 1期先予測: 系列終端の翌期は idx 0 → 高めの値が期待される
	future := ForecastHoltWinters(state, 4, 0)
	if len(future) != 4 {
		t.Fatalf("expected 4 future points, got %d", len(future))
	}
	// 季節成分が正しく分離できているなら、future[0] と future[2] は future[1] と future[3] より大きい
	if !(future[0] > future[1] && future[2] > future[3]) {
		t.Errorf("seasonality not recovered: future=%v", future)
	}
}

func TestHoltWinters_InsufficientDataReturnsZero(t *testing.T) {
	// シーズン長 4 だが、データは 5 点のみ → 不足 (8 点必要)
	series := []float64{1, 2, 3, 4, 5}
	state := FitHoltWintersAdditive(series, 4, 0.3, 0.1, 0.3)
	if state.SeasonLength != 0 {
		t.Errorf("expected zero state on insufficient data, got %+v", state)
	}
}

func TestHoltWinters_InvalidSeasonLength(t *testing.T) {
	state := FitHoltWintersAdditive([]float64{1, 2, 3, 4}, 1, 0.3, 0.1, 0.3)
	if state.SeasonLength != 0 {
		t.Error("season length < 2 should return zero state")
	}
}

func TestHoltWinters_LinearTrendSeasonality(t *testing.T) {
	// トレンド +1/期 + 季節成分 [0, +5, 0, -5]
	series := []float64{}
	for c := 0; c < 4; c++ { // 4 シーズン分
		base := float64(c * 4)
		series = append(series, base+0, base+1+5, base+2, base+3-5)
	}
	state := FitHoltWintersAdditive(series, 4, 0.3, 0.1, 0.3)
	if state.SeasonLength != 4 {
		t.Fatalf("expected fit, got empty state")
	}
	// 翌期 (シーズンインデックス 0) の予測値はおよそ 16 + 0 = 16 前後 (誤差許容)
	future := ForecastHoltWinters(state, 1, 0)
	if len(future) != 1 {
		t.Fatalf("expected 1 forecast, got %d", len(future))
	}
	// 大ざっぱに 14〜18 の範囲に入れば良しとする
	if future[0] < 12 || future[0] > 20 {
		t.Errorf("forecast %v out of expected range [12, 20]", future[0])
	}
}
