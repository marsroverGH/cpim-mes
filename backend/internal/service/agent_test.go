package service

import (
	"testing"
)

func TestAgentIntent_Stock(t *testing.T) {
	intent, p := classifyIntent("BIKE-100 の在庫")
	if intent != "ITEM_STOCK" {
		t.Errorf("expected ITEM_STOCK, got %s", intent)
	}
	if p.ItemCode != "BIKE-100" {
		t.Errorf("expected item code BIKE-100, got %q", p.ItemCode)
	}
}

func TestAgentIntent_PlannedOrders(t *testing.T) {
	intent, p := classifyIntent("FRAME-1 の計画オーダ")
	if intent != "ITEM_PLANNED_ORDERS" {
		t.Errorf("expected ITEM_PLANNED_ORDERS, got %s", intent)
	}
	if p.ItemCode != "FRAME-1" {
		t.Errorf("expected FRAME-1, got %q", p.ItemCode)
	}
}

func TestAgentIntent_WODueSoon(t *testing.T) {
	intent, p := classifyIntent("今週完成するWO")
	if intent != "WO_DUE_SOON" {
		t.Errorf("expected WO_DUE_SOON, got %s", intent)
	}
	intent2, p2 := classifyIntent("30日以内に完成するWO")
	if intent2 != "WO_DUE_SOON" {
		t.Errorf("expected WO_DUE_SOON for 30日, got %s", intent2)
	}
	if p2.DaysWindow != 30 {
		t.Errorf("expected days=30, got %d", p2.DaysWindow)
	}
	_ = p
}

func TestAgentIntent_POOverdue(t *testing.T) {
	intent, _ := classifyIntent("遅延中のPO")
	if intent != "PO_OVERDUE" {
		t.Errorf("expected PO_OVERDUE, got %s", intent)
	}
}

func TestAgentIntent_KPI(t *testing.T) {
	intent, _ := classifyIntent("KPIを表示")
	if intent != "KPI" {
		t.Errorf("expected KPI, got %s", intent)
	}
	intent2, _ := classifyIntent("ダッシュボード")
	if intent2 != "KPI" {
		t.Errorf("expected KPI for ダッシュボード, got %s", intent2)
	}
}

func TestAgentIntent_Help(t *testing.T) {
	intent, _ := classifyIntent("/help")
	if intent != "HELP" {
		t.Errorf("expected HELP, got %s", intent)
	}
	intent2, _ := classifyIntent("使い方を教えて")
	if intent2 != "HELP" {
		t.Errorf("expected HELP for 使い方, got %s", intent2)
	}
}

func TestAgentIntent_Unknown(t *testing.T) {
	intent, _ := classifyIntent("天気を教えて")
	if intent != "UNKNOWN" {
		t.Errorf("expected UNKNOWN, got %s", intent)
	}
}

func TestAgentIntent_ABC(t *testing.T) {
	intent, _ := classifyIntent("ABC分析を実行")
	if intent != "ABC" {
		t.Errorf("expected ABC, got %s", intent)
	}
}
