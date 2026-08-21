# 0040 Production Control Tower
## Constraint / Exception Prioritization

## 1. Purpose

0034 Full Pegging + Exception Management 以降で蓄積される Planning Exception を、
単なる例外一覧ではなく「今、どの受注へ管理介入すべきか」という
経営・計画優先度へ変換する Production Control Tower を提供する。

Control Tower は Planning Exception 自体を書き換えない。

Planning Exception
→ Control Tower Case
→ immutable scored Snapshot
→ ranked Recommendation
→ append-only Case Action

という独立した介入管理レイヤを構成する。

## 2. Case Identity

Control Tower Case は安定した case_key を持ち、以下を基礎に同一原因を追跡する。

- Sales Order
- Sales Order Line
- Exception Type
- Root Cause

Planning Exception が再計算されても、同じ業務原因は同じCaseとして追跡する。

## 3. Business Impact

Snapshotには以下を保存する。

- Order Value
- Open Order Value
- Revenue at Risk
- Sales Order Priority
- Customer Service Class
- Impact Days
- Exception Severity
- Aging
- Root Cause Type / Ref

Open Order Value / Revenue at Risk のOpen数量は物理列ではなく、

    quantity - shipped_qty - cancelled_qty

で算出する。

Allocated quantity はまだ未出荷受注なのでOpen Quantityから除外しない。

## 4. Priority Scoring

Priority Scoreは0〜100。

Weight:

- Severity: 20%
- Delivery / Lateness: 15%
- Revenue: 20%
- Customer: 10%
- Constraint: 25%
- Aging: 10%

Constraint Scoreは次の最大値を使用する。

- Material
- Capacity
- Supplier
- Execution

Priority Band:

- P1: >= 75
- P2: >= 55
- P3: >= 30
- P4: < 30

以下の重大ConstraintはScoreに関係なくP1とする。

- MATERIAL_SHORTAGE
- SUPPLIER_BLOCKED
- CAPACITY_UNSCHEDULED
- DISPATCH_BLOCKED
- FROZEN_HORIZON_CONFLICT
- EXECUTION_COMMITMENT_CONFLICT

## 5. Immutable Snapshot

Control Tower Snapshotはappend-only evidence。

canonical result hashを使用し、

    UNIQUE(case_id, result_hash)

によって、同じ状態を繰り返しRefreshしてもSnapshotを増殖させない。

Recommendationも新Snapshot生成時だけ作成する。

## 6. Recommendations

代表的な介入候補:

- EXPEDITE_PO
- RESCHEDULE_WO
- ALTERNATE_WORK_CENTER
- RELEASE_WO
- REVIEW_CAPACITY
- REVIEW_QUALITY_HOLD
- RECALCULATE_PROMISE
- CONTACT_CUSTOMER
- REVIEW_FROZEN_CONFLICT
- MANUAL_REVIEW

Recommendationは優先順位 rank_no を持つ。

## 7. Case Lifecycle

Case workflow:

    OPEN
      ↓ ACKNOWLEDGE
    ACKNOWLEDGED
      ↓ ASSIGN
    ASSIGNED
      ↓ START
    IN_PROGRESS
      ↓ RESOLVE
    RESOLVED
      ├─ REOPEN
      └─ CLOSE
    CLOSED

Action historyはappend-only。

Mutationはplanner/adminのみ許可する。

## 8. API

- POST /api/control-tower/refresh
- GET /api/control-tower
- GET /api/control-tower/cases/{id}
- GET /api/control-tower/cases/{id}/recommendations
- GET /api/control-tower/cases/{id}/actions
- POST /api/control-tower/cases/{id}/actions

Permissions:

- planning.control_tower.refresh
- planning.control_tower.manage

## 9. Production Control Tower UI

画面:

    /production-control-tower

表示内容:

- P1 / P2
- Open Cases
- Unassigned Cases
- Revenue at Risk
- Priority Score
- Sales Order / Customer
- Item
- Exception
- Root Cause
- Owner
- Status
- Recommended Interventions
- Case History

## 10. Verification

0040 release acceptance:

- Clean migration 0001 → 0040
- 40 migration ledger entries
- Go test PASS
- Go vet PASS
- Frontend vue-tsc PASS
- Frontend Vite build PASS
- 0039 Static Guard: 106 checks PASS
- 0040 Static Guard: 93 checks PASS before documentation checks
- Dedicated Production Control Tower E2E PASS
- Clean-room complete E2E: 17/17 PASS

The clean-room acceptance test removes the cpim-locktest DB volume before
starting the complete migration and E2E sequence.
