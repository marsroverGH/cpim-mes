package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
)

// ====================================================================
// AI Agent Service (intent → action)
// ====================================================================
//
// 自然言語で発行された質問・指示を解釈し、既存サービスを呼び出して回答する。
// 本実装は「ルールベースの意図解析」をデフォルトとし、後で LLM (Anthropic API)
// に差し替え可能なインターフェイスにしておく。
//
// 想定クエリ例:
//   - 「BIKE-100 の在庫」→ Inventory.OnHand で品目をフィルタ
//   - 「BIKE-100 の計画オーダ」→ MRP.Run → ItemCode==BIKE-100 を抽出
//   - 「今週完成するWO」→ WorkOrders.List → due_date 7日以内
//   - 「遅延中のPO」→ Purchases.List → due_date < today AND status=OPEN
//   - 「ABC分析」→ ABC.Run
//   - 「KPI」「ダッシュボード」→ KPI.Compute

type AgentService struct {
	repos *repository.Repositories
	mrp   *MRPService
	abc   *ABCService
	kpi   *KPIService
}

func NewAgentService(repos *repository.Repositories, mrp *MRPService, abc *ABCService, kpi *KPIService) *AgentService {
	return &AgentService{repos: repos, mrp: mrp, abc: abc, kpi: kpi}
}

// AgentRequest — クライアントからの入力
type AgentRequest struct {
	Query string `json:"query"`
}

// AgentResponse — エージェントの応答
type AgentResponse struct {
	Intent      string      `json:"intent"`
	Summary     string      `json:"summary"`
	Suggestions []string    `json:"suggestions,omitempty"` // 関連する次の操作候補
	Data        interface{} `json:"data,omitempty"`        // 構造化データ (テーブル表示用)
}

// Ask — クエリを解釈し回答を生成
func (a *AgentService) Ask(ctx context.Context, req AgentRequest) (*AgentResponse, error) {
	q := strings.TrimSpace(req.Query)
	if q == "" {
		return nil, errors.New("query is empty")
	}
	intent, params := classifyIntent(q)

	switch intent {
	case "ITEM_STOCK":
		return a.handleItemStock(ctx, params)
	case "ITEM_PLANNED_ORDERS":
		return a.handleItemPlannedOrders(ctx, params)
	case "WO_DUE_SOON":
		return a.handleWODueSoon(ctx, params)
	case "PO_OVERDUE":
		return a.handlePOOverdue(ctx)
	case "ABC":
		return a.handleABC(ctx)
	case "KPI":
		return a.handleKPI(ctx)
	case "HELP":
		return a.handleHelp(), nil
	default:
		return &AgentResponse{
			Intent:  "UNKNOWN",
			Summary: fmt.Sprintf("「%s」の意図を解釈できませんでした。/help と入力すると使い方が表示されます。", q),
			Suggestions: []string{
				"BIKE-100 の在庫",
				"BIKE-100 の計画オーダ",
				"今週完成するWO",
				"遅延中のPO",
				"KPI",
			},
		}, nil
	}
}

// ====================================================================
// Intent Classification (ルールベース)
// ====================================================================

type intentParams struct {
	ItemCode   string
	DaysWindow int
}

var (
	codeRe = regexp.MustCompile(`[A-Z][A-Z0-9_-]*-?[A-Z0-9_-]+`)
	numRe  = regexp.MustCompile(`\d+`)
)

// classifyIntent — クエリ文字列からインテントとパラメータを抽出
func classifyIntent(q string) (string, intentParams) {
	low := strings.ToLower(q)
	p := intentParams{}

	// 品目コード抽出 (英大文字+数字-_の連続)
	if m := codeRe.FindString(q); m != "" {
		p.ItemCode = m
	}
	// 日数の抽出
	if m := numRe.FindString(q); m != "" {
		if n, err := strconv.Atoi(m); err == nil {
			p.DaysWindow = n
		}
	}

	switch {
	case strings.Contains(low, "help") || strings.Contains(q, "ヘルプ") || strings.Contains(q, "使い方") || q == "/help":
		return "HELP", p
	case strings.Contains(q, "在庫") || strings.Contains(low, "stock") || strings.Contains(low, "on-hand") || strings.Contains(low, "onhand"):
		return "ITEM_STOCK", p
	case strings.Contains(q, "計画オーダ") || strings.Contains(q, "計画指示") ||
		strings.Contains(low, "planned order") || strings.Contains(q, "MRP"):
		return "ITEM_PLANNED_ORDERS", p
	case (strings.Contains(q, "WO") || strings.Contains(q, "製造指示")) &&
		(strings.Contains(q, "完成") || strings.Contains(q, "今週") || strings.Contains(q, "近") || strings.Contains(q, "予定")):
		if p.DaysWindow == 0 {
			p.DaysWindow = 7
		}
		return "WO_DUE_SOON", p
	case strings.Contains(q, "PO") && (strings.Contains(q, "遅延") || strings.Contains(q, "遅れ")):
		return "PO_OVERDUE", p
	case strings.Contains(q, "ABC") || strings.Contains(q, "abc分析"):
		return "ABC", p
	case strings.Contains(q, "KPI") || strings.Contains(q, "ダッシュボード") || strings.Contains(q, "kpi"):
		return "KPI", p
	}
	return "UNKNOWN", p
}

// ====================================================================
// Intent Handlers
// ====================================================================

func (a *AgentService) findItemByCode(ctx context.Context, code string) (*domain.Item, error) {
	if code == "" {
		return nil, errors.New("品目コードが指定されていません")
	}
	items, err := a.repos.Items.List(ctx)
	if err != nil {
		return nil, err
	}
	codeUp := strings.ToUpper(code)
	for _, it := range items {
		if strings.EqualFold(it.Code, codeUp) {
			c := it
			return &c, nil
		}
	}
	return nil, fmt.Errorf("品目 %s が見つかりません", code)
}

func (a *AgentService) handleItemStock(ctx context.Context, p intentParams) (*AgentResponse, error) {
	if p.ItemCode == "" {
		// 全件サマリ
		on, err := a.repos.Inventory.OnHand(ctx)
		if err != nil {
			return nil, err
		}
		var total float64
		for _, r := range on {
			total += r.OnHand
		}
		return &AgentResponse{
			Intent:      "ITEM_STOCK",
			Summary:     fmt.Sprintf("全品目の在庫合計は %.0f です。品目を指定すると個別表示します。", total),
			Data:        on,
			Suggestions: []string{"BIKE-100 の在庫", "ABC分析"},
		}, nil
	}
	item, err := a.findItemByCode(ctx, p.ItemCode)
	if err != nil {
		return nil, err
	}
	bal, err := a.repos.Inventory.BalanceFor(ctx, item.ID)
	if err != nil || bal == nil {
		return &AgentResponse{
			Intent:  "ITEM_STOCK",
			Summary: fmt.Sprintf("%s の在庫情報が取得できませんでした", item.Code),
		}, nil
	}
	return &AgentResponse{
		Intent: "ITEM_STOCK",
		Summary: fmt.Sprintf("%s — 物理在庫 %.0f / 予約 %.0f / 利用可能 %.0f",
			item.Code, bal.OnHand, bal.Reserved, bal.Available()),
		Data: bal,
		Suggestions: []string{
			fmt.Sprintf("%s の計画オーダ", item.Code),
			fmt.Sprintf("%s のATP", item.Code),
		},
	}, nil
}

func (a *AgentService) handleItemPlannedOrders(ctx context.Context, p intentParams) (*AgentResponse, error) {
	if p.ItemCode == "" {
		return &AgentResponse{
			Intent:  "ITEM_PLANNED_ORDERS",
			Summary: "品目コードを指定してください (例: BIKE-100 の計画オーダ)",
		}, nil
	}
	all, err := a.mrp.Run(ctx, MRPRequest{HorizonDays: 56})
	if err != nil {
		return nil, err
	}
	codeUp := strings.ToUpper(p.ItemCode)
	var matched []domain.MRPResult
	for _, r := range all {
		if strings.EqualFold(r.ItemCode, codeUp) && r.PlannedOrder > 0 {
			matched = append(matched, r)
		}
	}
	if len(matched) == 0 {
		return &AgentResponse{
			Intent:  "ITEM_PLANNED_ORDERS",
			Summary: fmt.Sprintf("%s について、今後56日内の計画オーダはありません", p.ItemCode),
		}, nil
	}
	return &AgentResponse{
		Intent:      "ITEM_PLANNED_ORDERS",
		Summary:     fmt.Sprintf("%s の計画オーダ %d 件を検出しました", p.ItemCode, len(matched)),
		Data:        matched,
		Suggestions: []string{"MRP アクションメッセージ"},
	}, nil
}

func (a *AgentService) handleWODueSoon(ctx context.Context, p intentParams) (*AgentResponse, error) {
	wos, err := a.repos.WorkOrders.List(ctx)
	if err != nil {
		return nil, err
	}
	days := p.DaysWindow
	if days == 0 {
		days = 7
	}
	now := TruncateDay(time.Now())
	cutoff := now.AddDate(0, 0, days)
	matched := make([]domain.WorkOrder, 0)
	for _, w := range wos {
		if w.Status != "RELEASED" && w.Status != "IN_PROGRESS" {
			continue
		}
		if w.DueDate.Before(cutoff) {
			matched = append(matched, w)
		}
	}
	return &AgentResponse{
		Intent:      "WO_DUE_SOON",
		Summary:     fmt.Sprintf("今後%d日以内に納期を迎える進行中WOは %d 件です", days, len(matched)),
		Data:        matched,
		Suggestions: []string{"Shop Floor を開く", "MRP アクション"},
	}, nil
}

func (a *AgentService) handlePOOverdue(ctx context.Context) (*AgentResponse, error) {
	pos, err := a.repos.Purchases.List(ctx)
	if err != nil {
		return nil, err
	}
	now := TruncateDay(time.Now())
	matched := make([]domain.PurchaseOrder, 0)
	for _, p := range pos {
		if (p.Status == "OPEN" || p.Status == "PARTIALLY_RECEIVED") && p.DueDate.Before(now) {
			matched = append(matched, p)
		}
	}
	return &AgentResponse{
		Intent:  "PO_OVERDUE",
		Summary: fmt.Sprintf("納期超過の未完納 PO は %d 件です", len(matched)),
		Data:    matched,
	}, nil
}

func (a *AgentService) handleABC(ctx context.Context) (*AgentResponse, error) {
	rows, err := a.abc.Run(ctx)
	if err != nil {
		return nil, err
	}
	var aCnt, bCnt, cCnt int
	for _, r := range rows {
		switch r.ABCClass {
		case "A":
			aCnt++
		case "B":
			bCnt++
		case "C":
			cCnt++
		}
	}
	return &AgentResponse{
		Intent:  "ABC",
		Summary: fmt.Sprintf("ABC分析（年間使用金額）結果: A=%d 品目 / B=%d / C=%d", aCnt, bCnt, cCnt),
		Data:    rows,
	}, nil
}

func (a *AgentService) handleKPI(ctx context.Context) (*AgentResponse, error) {
	d, err := a.kpi.Compute(ctx)
	if err != nil {
		return nil, err
	}
	return &AgentResponse{
		Intent: "KPI",
		Summary: fmt.Sprintf(
			"OTIF %.1f%% / 在庫回転 %.2f / 30日完成 %.0f / 仕掛 %.0f / アクション(C) %d",
			d.OTIFRate, d.InventoryTurnover, d.ThroughputUnits, d.WIPUnits, d.CriticalActions),
		Data: d,
	}, nil
}

func (a *AgentService) handleHelp() *AgentResponse {
	return &AgentResponse{
		Intent:  "HELP",
		Summary: "サポート対象クエリの例",
		Suggestions: []string{
			"BIKE-100 の在庫",
			"BIKE-100 の計画オーダ",
			"今週完成するWO",
			"30日以内に完成するWO",
			"遅延中のPO",
			"ABC分析",
			"KPI",
		},
	}
}

// 未使用 import 抑止
var _ = uuid.Nil
