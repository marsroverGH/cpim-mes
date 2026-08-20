package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
)

// ====================================================================
// MRP Action Messages Service
// ====================================================================

type ActionMessageService struct {
	repos *repository.Repositories
	mrp   *MRPService
}

// SupplyOrder — PO/WO 既存伝票の共通インターフェース
type SupplyOrder struct {
	DocType  string // "PO" or "WO"
	DocNo    string
	ID       uuid.UUID
	ItemID   uuid.UUID
	Quantity float64
	DueDate  time.Time
	Status   string
}

// ActionMessageInput — 純粋関数 GenerateActionMessages の入力
type ActionMessageInput struct {
	Today         time.Time
	LeadTimeDays  map[uuid.UUID]int
	ItemCodes     map[uuid.UUID]string
	PlannedOrders []domain.MRPResult // PlannedOrder > 0 のみ意味あり
	OpenSupplies  []SupplyOrder      // status が OPEN/PLANNED/RELEASED のもの
	ToleranceDays int                // 許容ずれ (デフォルト 1日)
}

// GenerateActionMessages — Planned Order と既存供給伝票を突合し、
// Reschedule / Cancel / Expedite / Release のアクションメッセージを生成する。
//
// マッチング方針 (シンプル版):
//  1. 同一品目の Open Supplies と Planned Orders を日付昇順で並べ、
//     数量を greedy に消し込む
//  2. 残った Open Supply → CANCEL 候補
//  3. 残った Planned Order で needDate <= today+LT → EXPEDITE / RELEASE
//  4. 消し込まれた Supply の納期と Planned 期日が tolerance を超えてズレ → Reschedule
func GenerateActionMessages(in ActionMessageInput) []domain.ActionMessage {
	if in.ToleranceDays <= 0 {
		in.ToleranceDays = 1
	}
	tol := time.Duration(in.ToleranceDays) * 24 * time.Hour
	today := TruncateDay(in.Today)

	// 品目別にグルーピング
	byItemPlanned := make(map[uuid.UUID][]domain.MRPResult)
	for _, p := range in.PlannedOrders {
		if p.PlannedOrder <= 0 {
			continue
		}
		byItemPlanned[p.ItemID] = append(byItemPlanned[p.ItemID], p)
	}
	byItemSupply := make(map[uuid.UUID][]SupplyOrder)
	for _, s := range in.OpenSupplies {
		byItemSupply[s.ItemID] = append(byItemSupply[s.ItemID], s)
	}

	// 全アイテムIDの集合
	allIDs := make(map[uuid.UUID]bool)
	for id := range byItemPlanned {
		allIDs[id] = true
	}
	for id := range byItemSupply {
		allIDs[id] = true
	}

	out := make([]domain.ActionMessage, 0)

	for id := range allIDs {
		planned := byItemPlanned[id]
		supplies := byItemSupply[id]
		code := in.ItemCodes[id]
		lt := in.LeadTimeDays[id]

		// 日付昇順で並べる
		sort.Slice(planned, func(i, j int) bool { return planned[i].Period.Before(planned[j].Period) })
		sort.Slice(supplies, func(i, j int) bool { return supplies[i].DueDate.Before(supplies[j].DueDate) })

		// greedy マッチ: 各 planned を順に消化、最古の supply を割り当てる
		used := make([]bool, len(supplies))
		for _, p := range planned {
			needDate := p.Period
			matched := -1
			for i := range supplies {
				if used[i] {
					continue
				}
				matched = i
				break
			}
			if matched >= 0 {
				used[matched] = true
				s := supplies[matched]
				diff := needDate.Sub(TruncateDay(s.DueDate))
				absDiff := diff
				if absDiff < 0 {
					absDiff = -absDiff
				}
				if absDiff > tol {
					kind := "RESCHEDULE_IN"
					sev := "WARNING"
					msg := fmt.Sprintf("%s %s: 必要日 %s に対し既存納期 %s — 前倒し推奨",
						s.DocType, s.DocNo, fmtDate(needDate), fmtDate(s.DueDate))
					if s.DueDate.Before(needDate) {
						kind = "RESCHEDULE_OUT"
						sev = "INFO"
						msg = fmt.Sprintf("%s %s: 必要日 %s に対し既存納期 %s — 後ろ倒し推奨",
							s.DocType, s.DocNo, fmtDate(needDate), fmtDate(s.DueDate))
					}
					sid := s.ID
					curDate := s.DueDate
					out = append(out, domain.ActionMessage{
						Kind:   kind,
						ItemID: id, ItemCode: code,
						Quantity:    p.PlannedOrder,
						NeedDate:    needDate,
						CurrentDate: &curDate,
						RefDocType:  s.DocType, RefDocNo: s.DocNo, RefDocID: &sid,
						Severity: sev,
						Message:  msg,
					})
				}
			} else {
				// 既存伝票なし → EXPEDITE か RELEASE か FUTURE_RELEASE
				releaseBy := today.AddDate(0, 0, lt)
				kind := "FUTURE_RELEASE"
				sev := "INFO"
				msg := fmt.Sprintf("%s: 計画オーダ %s 必要数 %.0f — 将来発行予定",
					code, fmtDate(needDate), p.PlannedOrder)
				if needDate.Before(today) || needDate.Equal(today) {
					kind = "EXPEDITE"
					sev = "CRITICAL"
					msg = fmt.Sprintf("%s: 必要日 %s が既に到来 — 緊急手配が必要 (%.0f)",
						code, fmtDate(needDate), p.PlannedOrder)
				} else if !needDate.After(releaseBy) {
					kind = "RELEASE"
					sev = "WARNING"
					msg = fmt.Sprintf("%s: 必要日 %s — リードタイム %d日内、即時発行を推奨 (%.0f)",
						code, fmtDate(needDate), lt, p.PlannedOrder)
				}
				out = append(out, domain.ActionMessage{
					Kind:   kind,
					ItemID: id, ItemCode: code,
					Quantity:   p.PlannedOrder,
					NeedDate:   needDate,
					RefDocType: "PLANNED",
					Severity:   sev,
					Message:    msg,
				})
			}
		}

		// マッチしなかった supply → CANCEL 候補
		for i, used := range used {
			if used {
				continue
			}
			s := supplies[i]
			sid := s.ID
			out = append(out, domain.ActionMessage{
				Kind:   "CANCEL",
				ItemID: id, ItemCode: code,
				Quantity:   s.Quantity,
				NeedDate:   s.DueDate,
				RefDocType: s.DocType, RefDocNo: s.DocNo, RefDocID: &sid,
				Severity: "WARNING",
				Message: fmt.Sprintf("%s %s: 対応する需要なし — キャンセル候補",
					s.DocType, s.DocNo),
			})
		}
	}

	// 重要度→需要日 順
	sevRank := func(s string) int {
		switch s {
		case "CRITICAL":
			return 0
		case "WARNING":
			return 1
		}
		return 2
	}
	sort.Slice(out, func(i, j int) bool {
		if sevRank(out[i].Severity) != sevRank(out[j].Severity) {
			return sevRank(out[i].Severity) < sevRank(out[j].Severity)
		}
		return out[i].NeedDate.Before(out[j].NeedDate)
	})
	return out
}

func fmtDate(t time.Time) string { return t.Format("2006-01-02") }

// GenerateNettedMRPActions converts MRP v2's already-netted planned orders into
// planner actions. Existing PO/WO supplies must NOT be matched again here because
// MRP.Run has already included them as Scheduled Receipts during netting.
func GenerateNettedMRPActions(today time.Time, planned []domain.MRPResult) []domain.ActionMessage {
	today = TruncateDay(today)
	out := make([]domain.ActionMessage, 0)

	for _, p := range planned {
		if p.PlannedOrder <= 0 {
			continue
		}
		needDate := TruncateDay(p.Period) // planned-order receipt / requirement date
		releaseDate := needDate
		if p.PlannedReleaseDate != nil {
			releaseDate = TruncateDay(*p.PlannedReleaseDate)
		}

		kind := "FUTURE_RELEASE"
		severity := "INFO"
		message := fmt.Sprintf("%s: 計画入荷 %s 数量 %.0f — 計画発行日 %s",
			p.ItemCode, fmtDate(needDate), p.PlannedOrder, fmtDate(releaseDate))

		if !needDate.After(today) {
			kind = "EXPEDITE"
			severity = "CRITICAL"
			message = fmt.Sprintf("%s: 計画入荷日 %s が到来/超過 — 数量 %.0f を緊急手配 (本来発行 %s)",
				p.ItemCode, fmtDate(needDate), p.PlannedOrder, fmtDate(releaseDate))
		} else if !releaseDate.After(today) {
			kind = "RELEASE"
			severity = "WARNING"
			message = fmt.Sprintf("%s: 計画入荷 %s 数量 %.0f — 発行日 %s のため発行推奨",
				p.ItemCode, fmtDate(needDate), p.PlannedOrder, fmtDate(releaseDate))
		}

		out = append(out, domain.ActionMessage{
			Kind:       kind,
			ItemID:     p.ItemID,
			ItemCode:   p.ItemCode,
			Quantity:   p.PlannedOrder,
			NeedDate:   needDate,
			RefDocType: "PLANNED",
			Severity:   severity,
			Message:    message,
		})
	}

	sevRank := func(s string) int {
		switch s {
		case "CRITICAL":
			return 0
		case "WARNING":
			return 1
		default:
			return 2
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if sevRank(out[i].Severity) != sevRank(out[j].Severity) {
			return sevRank(out[i].Severity) < sevRank(out[j].Severity)
		}
		return out[i].NeedDate.Before(out[j].NeedDate)
	})
	return out
}

// Run executes MRP v2 and produces actions from the remaining net planned orders.
// Open PO/WO are already Scheduled Receipts inside MRP.Run, so re-matching them
// here would double-net supply and suppress real shortages.
func (s *ActionMessageService) Run(ctx context.Context, horizonDays int) ([]domain.ActionMessage, error) {
	if horizonDays <= 0 {
		horizonDays = 28
	}
	planned, err := s.mrp.Run(ctx, MRPRequest{HorizonDays: horizonDays})
	if err != nil {
		return nil, err
	}
	return GenerateNettedMRPActions(time.Now(), planned), nil
}
