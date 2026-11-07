package service

import (
	"math"
	"time"

	"github.com/shopspring/decimal"
)

// ====================================================================
// 純粋関数 - 単体テスト容易化のため切り出し
// ====================================================================

// QuantityScale is the number of decimal places kept for quantities computed
// in Go before they are written to the database (the ledger columns are
// unconstrained numeric and would otherwise store binary float artefacts such
// as 0.30000000000000004). Nine places removes the ~1e-16 artefacts, keeps
// any realistic input exact, and keeps accumulated rounding (for example over
// many partial completions) far below the 0.000001 tolerance the database
// guards use.
const QuantityScale = 9

// RoundQty rounds a computed quantity to QuantityScale decimal places (half away
// from zero, like PostgreSQL numeric rounding). The rounding is done in decimal,
// so float noise such as 0.30000000000000004 becomes exactly 0.3.
func RoundQty(x float64) float64 {
	return fq(dq(x))
}

// RoundUpToMultiple — x をロットサイズ m の倍数に切り上げ (decimal で計算する。
// 1.0-0.7 = 0.30000000000000004 は 0.1 の 3 倍として扱い、4 倍にしない)
func RoundUpToMultiple(x, m float64) float64 {
	if m <= 0 {
		return x
	}
	return fq(roundUpToMultipleDec(roundQtyDec(dq(x)), dq(m)))
}

func roundUpToMultipleDec(x, m decimal.Decimal) decimal.Decimal {
	if !m.IsPositive() {
		return x
	}
	if x.Mod(m).IsZero() {
		return x
	}
	return x.Div(m).Floor().Add(decimal.NewFromInt(1)).Mul(m)
}

// TruncateDay — タイムスタンプを日単位に切り捨て (タイムゾーンは引数を踏襲)
func TruncateDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// ====================================================================
// 需要予測コア (純粋関数)
// ====================================================================

// FitSMA — 移動平均で履歴に当てはめた値を返す。fitted[0]=actuals[0]、以降は過去 window 期の平均。
func FitSMA(actuals []float64, window int) []float64 {
	if window < 1 {
		window = 1
	}
	out := make([]float64, len(actuals))
	if len(actuals) == 0 {
		return out
	}
	out[0] = actuals[0]
	for i := 1; i < len(actuals); i++ {
		start := i - window
		if start < 0 {
			start = 0
		}
		sum := 0.0
		for j := start; j < i; j++ {
			sum += actuals[j]
		}
		out[i] = sum / float64(i-start)
	}
	return out
}

// FitEXPO — 単純指数平滑で当てはめ: F[t+1] = α*A[t] + (1-α)*F[t]、F[0]=A[0]
func FitEXPO(actuals []float64, alpha float64) []float64 {
	if alpha <= 0 || alpha > 1 {
		alpha = 0.3
	}
	out := make([]float64, len(actuals))
	if len(actuals) == 0 {
		return out
	}
	out[0] = actuals[0]
	for i := 1; i < len(actuals); i++ {
		out[i] = alpha*actuals[i-1] + (1-alpha)*out[i-1]
	}
	return out
}

// ForecastSMA — SMA で horizon 期間先まで外挿 (rolling)
func ForecastSMA(actuals []float64, window, horizon int) []float64 {
	if window < 1 {
		window = 1
	}
	if window > len(actuals) {
		window = len(actuals)
	}
	out := make([]float64, horizon)
	for h := 0; h < horizon; h++ {
		combined := append([]float64{}, actuals...)
		combined = append(combined, out[:h]...)
		start := len(combined) - window
		if start < 0 {
			start = 0
		}
		sum := 0.0
		for j := start; j < len(combined); j++ {
			sum += combined[j]
		}
		out[h] = sum / float64(len(combined)-start)
	}
	return out
}

// ForecastEXPO — 単純指数平滑で horizon 期間先まで外挿 (将来は一定値)
func ForecastEXPO(actuals, fitted []float64, alpha float64, horizon int) []float64 {
	out := make([]float64, horizon)
	if len(actuals) == 0 || len(fitted) == 0 {
		return out
	}
	last := alpha*actuals[len(actuals)-1] + (1-alpha)*fitted[len(fitted)-1]
	for i := range out {
		out[i] = last
	}
	return out
}

// AccuracyMetrics — MAE/MAPE を返す (i=1 以降の当てはめのみを評価)
func AccuracyMetrics(actuals, fitted []float64) (mae, mape float64) {
	var sumAbs, sumPct, n float64
	for i := 1; i < len(actuals) && i < len(fitted); i++ {
		err := math.Abs(actuals[i] - fitted[i])
		sumAbs += err
		if actuals[i] != 0 {
			sumPct += err / math.Abs(actuals[i])
		}
		n++
	}
	if n == 0 {
		return 0, 0
	}
	return sumAbs / n, sumPct / n * 100
}

// ====================================================================
// MRP ロットサイジング
// ====================================================================

// LotSizeMethod — 計画オーダ確定時のロットサイズ計算方式
type LotSizeMethod string

const (
	LotMethodLFL LotSizeMethod = "LFL" // Lot-for-Lot
	LotMethodFOQ LotSizeMethod = "FOQ" // Fixed Order Quantity (固定ロット)
	LotMethodPOQ LotSizeMethod = "POQ" // Period Order Quantity (P期分まとめて発注)
	LotMethodEOQ LotSizeMethod = "EOQ" // Economic Order Quantity (経済発注量)
)

// EOQ — 経済発注量 (Wilson formula)
//
//	EOQ = sqrt( (2 * D * S) / H )
//	  D: annual demand (年間需要)
//	  S: ordering cost per order (1回あたり発注コスト)
//	  H: holding cost per unit per year (在庫保管費)
func EOQ(annualDemand, orderingCost, holdingCostPerUnit float64) float64 {
	if annualDemand <= 0 || orderingCost <= 0 || holdingCostPerUnit <= 0 {
		return 0
	}
	return math.Sqrt(2 * annualDemand * orderingCost / holdingCostPerUnit)
}

// ApplyLotSize — Net Requirement にロットサイジング方式を適用して PlannedOrder を返す
//
//	netReq:    純所要量 (>= 0)
//	safety:    安全在庫 (上乗せ)
//	lotSize:   品目マスタのロットサイズ (LFL/FOQ で使用)
//	method:    LotSizeMethod
//	eoq:       EOQ 値 (method=EOQ のときのみ参照)
func ApplyLotSize(netReq, safety, lotSize, eoq float64, method LotSizeMethod) float64 {
	if netReq <= 0 && safety <= 0 {
		return 0
	}
	// net requirements are differences of float quantities; keep planned
	// receipts free of binary artefacts (1.0-0.7 = 0.30000000000000004).
	target := roundQtyDec(dq(netReq).Add(dq(safety)))
	if !target.IsPositive() {
		return 0
	}
	switch method {
	case LotMethodFOQ:
		// Fixed Order Quantity: replenish in fixed lot-size multiples.
		if lotSize <= 0 {
			lotSize = 1
		}
		return fq(roundUpToMultipleDec(target, dq(lotSize)))
	case LotMethodEOQ:
		if eoq > 0 {
			// EOQ is a square root and has no exact decimal form; the lot is
			// the EOQ rounded to QuantityScale places.
			return fq(roundUpToMultipleDec(target, roundQtyDec(dq(eoq))))
		}
		// If EOQ cannot be calculated, fall back to Lot-for-Lot rather than
		// silently imposing the item's fixed lot size.
		return fq(target)
	case LotMethodPOQ:
		// POQ demand periods are aggregated by the caller. The receipt quantity
		// itself is therefore Lot-for-Lot against the aggregated net requirement.
		return fq(target)
	case LotMethodLFL:
		fallthrough
	default:
		// Lot-for-Lot means exactly the net requirement; lot_size is not a
		// multiplier in LFL.
		return fq(target)
	}
}

// ====================================================================
// FIFO ロット消費の按分 (純粋関数)
// ====================================================================

// LotBalance — FIFO 消費計算用のロット情報
type LotBalance struct {
	LotID   string // uuid 文字列 (純粋関数のため uuid に依存しない)
	LotNo   string
	Balance float64 // 残数 > 0
}

// LotConsumption — 1ロットからの消費数量
type LotConsumption struct {
	LotID    string
	LotNo    string
	Consumed float64
}

// SplitFIFO — 必要数量 required を FIFO 順 (lots はすでに受入順に並んでいる前提)
// で分割し、各ロットからの消費数量リストを返す。残量不足時は shortage > 0 を返す。
//
// 例:
//
//	lots = [(L1, 5), (L2, 7), (L3, 10)], required = 14
//	→ [(L1, 5), (L2, 7), (L3, 2)], shortage = 0
//
//	required = 30, total balance = 22 → 全消費 + shortage = 8
func SplitFIFO(lots []LotBalance, required float64) (consumptions []LotConsumption, shortage float64) {
	remaining := roundQtyDec(dq(required))
	for _, l := range lots {
		if !remaining.IsPositive() {
			break
		}
		balance := roundQtyDec(dq(l.Balance))
		if !balance.IsPositive() {
			continue
		}
		take := decimal.Min(balance, remaining)
		consumptions = append(consumptions, LotConsumption{
			LotID: l.LotID, LotNo: l.LotNo, Consumed: fq(take),
		})
		remaining = remaining.Sub(take)
	}
	if remaining.IsPositive() {
		shortage = fq(remaining)
	}
	return consumptions, shortage
}

// ====================================================================
// Holt-Winters 加法モデル季節予測 (純粋関数)
// ====================================================================
//
// 加法モデル: y[t] = level + trend*t + seasonal[t mod L] + noise
//
// 平滑化パラメータ:
//   alpha : level の更新速度 (0..1)
//   beta  : trend の更新速度 (0..1)
//   gamma : seasonal の更新速度 (0..1)
//
// シーズン長 L は呼び出し側で指定 (例: 週次データで年次性なら 52、
// 月次データで年次性なら 12)。
// データ系列は L*2 期間以上を強く推奨 (最低でも 2 シーズン分)。

// HoltWintersState — 平滑化結果 (この値があれば任意期間先まで予測可能)
type HoltWintersState struct {
	Level        float64
	Trend        float64
	Seasonal     []float64 // 長さ = SeasonLength
	SeasonLength int
}

// FitHoltWintersAdditive — 与えられた系列に加法 HW を当てはめる。
// 失敗 (データ不足など) 時はゼロ値の State を返す。
func FitHoltWintersAdditive(series []float64, seasonLength int, alpha, beta, gamma float64) HoltWintersState {
	if seasonLength < 2 || len(series) < seasonLength*2 {
		return HoltWintersState{}
	}
	// 初期 level: 最初のシーズン平均
	var sum float64
	for i := 0; i < seasonLength; i++ {
		sum += series[i]
	}
	level := sum / float64(seasonLength)

	// 初期 trend: 1st シーズンと 2nd シーズンの平均差
	var trendSum float64
	for i := 0; i < seasonLength; i++ {
		trendSum += (series[seasonLength+i] - series[i]) / float64(seasonLength)
	}
	trend := trendSum / float64(seasonLength)

	// 初期 seasonal: 各シーズン位置の最初のシーズンの偏差
	seasonal := make([]float64, seasonLength)
	for i := 0; i < seasonLength; i++ {
		seasonal[i] = series[i] - level
	}

	// 平滑化反復 (1 シーズン目以降の各観測値で level/trend/seasonal を更新)
	for t := 0; t < len(series); t++ {
		s := seasonal[t%seasonLength]
		newLevel := alpha*(series[t]-s) + (1-alpha)*(level+trend)
		newTrend := beta*(newLevel-level) + (1-beta)*trend
		newSeason := gamma*(series[t]-newLevel) + (1-gamma)*s
		level = newLevel
		trend = newTrend
		seasonal[t%seasonLength] = newSeason
	}

	return HoltWintersState{
		Level:        level,
		Trend:        trend,
		Seasonal:     seasonal,
		SeasonLength: seasonLength,
	}
}

// ForecastHoltWinters — fit 済み state から h 期間先まで予測。
// 期 t (1..h) の予測値 = level + t*trend + seasonal[(N-1+t) mod L]
// ただし N はフィットに使った系列長。簡略化のため、呼び出し側が
// state を保持する想定で「最後の観測位置からの相対」で計算する。
// ここでは引数 startSeasonIdx に「最後の観測の翌期のシーズン位置」を渡す。
func ForecastHoltWinters(state HoltWintersState, h int, startSeasonIdx int) []float64 {
	if state.SeasonLength == 0 || h <= 0 {
		return nil
	}
	out := make([]float64, h)
	for k := 0; k < h; k++ {
		idx := (startSeasonIdx + k) % state.SeasonLength
		out[k] = state.Level + float64(k+1)*state.Trend + state.Seasonal[idx]
	}
	return out
}
