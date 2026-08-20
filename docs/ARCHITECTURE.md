# 設計ドキュメント

## CPIM 知識体系へのマッピング

CPIM (Certified in Planning and Inventory Management) は ASCM (旧 APICS) が
認定する国際資格で、知識体系の中心は **MPC (Manufacturing Planning and Control)**
にあります。本システムは以下のように対応付けています。

| CPIM 領域 | 本システムでの実装 |
| --- | --- |
| Item Master / Inventory | `/items`, `/inventory` |
| Bill of Materials | `/bom` (再帰CTEで多段展開) |
| Work Centers / Routings | `/work-centers`, `/routings` |
| Demand Management & S&OP | `/demand` |
| Master Production Schedule | `/mps` |
| Material Requirements Planning | `/mrp` (BOM展開 × 在庫差引 × ロットサイズ) |
| Capacity Requirements Planning | `/crp` (Plannedオーダ × ルーティング × 作業区能力) |
| Standard Cost Rollup | `/cost-rollup` (BOM+ルーティング再帰積み上げ) |
| Shop Floor Control | `/work-orders` + strict operations (`PENDING→READY→IN_PROGRESS↔PAUSED→COMPLETED`) |
| Procurement | `/purchase-orders` |
| Dashboard / KPI | `/` (Chart.js による可視化) — ERPダッシュボード相当 |

## レイヤアーキテクチャ (Backend)

```
HTTP request
    │
    ▼
[ api ]    chi router + JSON encoding/decoding
    │
    ▼
[ service ] ビジネスロジック (MRP計算, バリデーション)
    │
    ▼
[ repository ] sqlx + PostgreSQL
    │
    ▼
[ database ] PostgreSQL (UUID PK, 外部キー, CHECK制約)
```

## MRP アルゴリズム概要

MRP v2 は CPIM の time-phased MRP の流れに合わせ、次の順序で処理します。

1. `mps_entries.planned` を独立需要 (Gross Requirements) として期間内にバケット化
2. 現在庫を初期 Projected Available Balance とし、OPEN PO と RELEASED/IN_PROGRESS WO 残数を Scheduled Receipts として投入
3. LLC (Low-Level Code) 昇順に、`Gross + Safety Stock - (Projected On Hand + Scheduled Receipts)` で Net Requirements を計算
4. LFL / FOQ / POQ / EOQ を適用して Planned Order Receipt を決定
5. 品目リードタイムをオフセットして Planned Order Release 日を計算
6. Planned Order Release 数量を **直下BOM 1階層だけ** 展開し、そのRelease日に子品目の Gross Requirements を発生
7. 次の LLC レベルで全親からの従属需要を統合してNettingを実施
8. 既存の RELEASED / IN_PROGRESS WO は親品目のScheduled Receiptであると同時に、残数量について開始日に直下部品需要を発生

MRP内部では再帰 `BOM.Explode()` を使用しません。多段BOMは各レベルの Planned Order Release を通じて段階的に展開されるため、下位部品の二重計上を防ぎます。

実装は `backend/internal/service/service.go::MRPService.Run` と `backend/internal/service/mrp_core.go` を参照。

## データベース ER 図 (簡易)

```
items 1───< bom_components >───1 items
items 1───< demand_forecasts
items 1───< mps_entries
items 1───< inventory_txns
items 1───< work_orders
work_orders 1───1 work_order_bom_snapshots 1───< work_order_bom_snapshot_lines
work_orders 1───< work_order_completions >───1 work_order_bom_snapshots
items 1───< purchase_orders
```

## API 一覧

| メソッド | パス | 説明 |
| --- | --- | --- |
| GET    | /api/items                          | 品目一覧 |
| POST   | /api/items                          | 品目登録 |
| PUT    | /api/items/{id}                     | 品目更新 |
| DELETE | /api/items/{id}                     | 品目削除 |
| GET    | /api/items/{id}/bom                 | 直下の構成 |
| POST   | /api/items/{id}/bom                 | 子部品追加 |
| GET    | /api/items/{id}/explode?qty=N       | 多段BOM展開 |
| DELETE | /api/bom/{compId}                   | 構成削除 |
| GET    | /api/demand                         | 需要一覧 |
| POST   | /api/demand                         | 需要登録 |
| GET    | /api/mps                            | MPS一覧 |
| POST   | /api/mps                            | MPS登録(upsert) |
| GET    | /api/inventory/on-hand              | 現在庫サマリ |
| GET    | /api/inventory/{itemId}/transactions| 取引履歴 |
| POST   | /api/inventory/transactions         | 取引登録 |
| GET    | /api/work-orders                    | WO一覧 |
| POST   | /api/work-orders                    | WO登録 |
| PUT    | /api/work-orders/{id}/status        | ステータス変更 |
| GET    | /api/purchase-orders                | PO一覧 |
| POST   | /api/purchase-orders                | PO登録 |
| POST   | /api/mrp/run                        | MRP実行 |
| GET    | /api/work-centers                   | 作業区一覧 |
| POST   | /api/work-centers                   | 作業区登録 |
| PUT    | /api/work-centers/{id}              | 作業区更新 |
| DELETE | /api/work-centers/{id}              | 作業区削除 |
| GET    | /api/routings                       | ルーティング一覧 |
| POST   | /api/routings                       | ルーティング登録 |
| GET    | /api/routings/{id}/operations       | 工程一覧 |
| POST   | /api/routings/{id}/operations       | 工程追加 |
| DELETE | /api/routing-operations/{opId}      | 工程削除 |
| POST   | /api/crp/run                        | CRP実行 |
| GET    | /api/cost-rollup                    | 標準原価積み上げ計算 |
| POST   | /api/auth/login                     | ログイン (JWT発行) |
| GET    | /api/auth/me                        | 現在のユーザー情報 |
| GET    | /api/abc-analysis                   | ABC分析実行 |
| GET    | /api/items/export.csv               | 品目CSVエクスポート |
| POST   | /api/items/import                   | 品目CSVインポート (multipart) |
| GET    | /api/lots                           | ロット一覧 (残数付き) |
| POST   | /api/lots                           | ロット登録 |
| GET    | /api/lots/{id}/movements            | ロット移動履歴 |
| POST   | /api/lots/{id}/movements            | ロット入出庫記録 |
| GET    | /api/lots/{id}/where-used           | Where-used 検索 |
| GET    | /api/items/{itemId}/lots            | 品目別ロット一覧 |
| GET    | /api/audit-log                      | 監査ログ照会 (?username=&resource=) |
| POST   | /api/forecast/run                   | 需要予測 (SMA/EXPO) |
| GET    | /api/cycle-counts                   | サイクルカウント一覧 (?status=) |
| POST   | /api/cycle-counts/generate          | ABC連動でスケジュール自動生成 |
| POST   | /api/cycle-counts/{id}/record       | 実測値を記録 (差異があれば在庫調整も自動起票) |
| GET    | /api/openapi.json                   | OpenAPI 3.0 仕様 (公開) |
| GET    | /api/docs                           | Swagger UI (公開) |
| GET    | /api/inventory/balance              | 在庫サマリ (on_hand/reserved/available) |
| POST   | /api/work-orders/{id}/release       | WO リリース (Release時BOM Snapshot固定→原子的予約) |
| GET    | /api/work-orders/{id}/bom-snapshot  | WO Release時に固定されたBOM Snapshot参照 |
| POST   | /api/work-orders/{id}/complete      | WO 部分完成 (Snapshot基準で今回数量だけ子出庫+親入庫、冪等completionId) |
| POST   | /api/purchase-orders/{id}/receive   | PO部分入荷 (receiptId冪等、履歴+Lot/Inventory原子更新) |
| GET    | /api/purchase-orders/{id}/receipts  | PO入荷履歴 |
| GET    | /api/calendars                      | 作業カレンダー一覧 |
| POST   | /api/calendars                      | カレンダー作成 |
| PUT    | /api/calendars/{id}                 | カレンダー更新 (週次パターン) |
| DELETE | /api/calendars/{id}                 | カレンダー削除 |
| GET    | /api/calendars/{id}/exceptions      | 例外日一覧 |
| POST   | /api/calendars/{id}/exceptions      | 例外日追加 (祝日/振替出勤) |
| DELETE | /api/calendar-exceptions/{exId}     | 例外日削除 |
| POST   | /api/work-orders/{id}/progress      | WO 参考進捗を更新 (在庫は動かさない) |
| GET    | /api/items/{itemId}/atp             | ATP (期間別引当可能数量) |
| GET    | /api/lots/{id}/inspections          | ロット品質検査履歴 |
| POST   | /api/lots/{id}/inspections          | 品質検査記録 (PASS/FAIL/HOLD) |
| GET    | /api/quality/recent                 | 直近の品質検査一覧 |
| POST   | /api/items/recompute-llc            | 全品目の低レベルコードを再計算 |
| GET    | /api/mrp/action-messages            | MRP v2 Netting後のアクション (Expedite/Release/Future Release) |
| GET    | /api/shop-floor/active              | 未完了工程一覧 (PENDING/READY/IN_PROGRESS/PAUSED) |
| GET    | /api/work-orders/{id}/operations    | WO の工程一覧 |
| POST   | /api/wo-operations/{opId}/start     | 工程開始 |
| POST   | /api/wo-operations/{opId}/stop      | 工程一時中断 |
| POST   | /api/wo-operations/{opId}/complete  | 工程完了 |
| GET    | /api/wo-operations/{opId}/logs      | 工程イベントログ |
| GET    | /api/kpi/dashboard                  | KPI 一括取得 (OTIF/在庫回転/スループット/品質/アクション) |
| GET    | /api/item-groups                    | 品目グループ (S&OP ファミリー) 一覧 |
| POST   | /api/item-groups                    | 品目グループ作成 |
| GET    | /api/sop/plans                      | S&OP 月次プラン一覧 |
| POST   | /api/sop/plans                      | S&OP プラン upsert |
| DELETE | /api/sop/plans/{id}                 | S&OP プラン削除 |
| GET    | /api/rccp/run                       | RCCP 実行 (月×作業区負荷) |
| GET    | /api/rccp/profiles                  | RCCP プロファイル一覧 |
| POST   | /api/rccp/profiles                  | RCCP プロファイル upsert |
| GET    | /api/eco                            | ECO 一覧 |
| POST   | /api/eco                            | ECO 作成 (DRAFT) |
| POST   | /api/eco/{id}/approve               | ECO 承認 |
| POST   | /api/eco/{id}/apply                 | ECO 適用 (BOM 反映 + LLC 再計算) |
| POST   | /api/eco/{id}/cancel                | ECO 取消 |
| GET    | /api/eco/{id}/components            | ECO の変更行一覧 |
| POST   | /api/eco/{id}/components            | 変更行追加 (ADD/REMOVE/MODIFY) |
| POST   | /api/agent/ask                      | AI アシスタントへの自然言語クエリ |

## 制限事項 (本リファレンス実装)

- ✅ 認証: JWT + RBAC (admin/planner/operator/viewer) 実装済み
- ✅ MRP: LFL / FOQ / POQ / EOQ の4方式 + Pegging + **低レベルコード(LLC)階層処理**
- ✅ 入力バリデーション: go-playground/validator による struct タグベース (Item に適用)
- ✅ エラーハンドリング: domain.AppError による統一構造化レスポンス
- ✅ ユニットテスト: 主要ロジックに table-driven test (mathutil/abc/mrp/workflow/calendar)
- ✅ CRP: 工程毎の seq_no で 1日ずつ後方展開、作業カレンダーで休日スキップ
- 認可: ロールチェック用ミドルウェア `requireRole` は用意済みだが、現状は「認証必須」のみ。細粒度の認可は未実装
- ABC分析は直近12か月の ISSUE 数量 × Standard Cost（年間使用金額）で分類し、Cycle Count頻度にも連動
- 需要予測: SMA / 指数平滑 / **Holt-Winters加法モデル** (level + trend + seasonal) を実装済み
- マルチテナント、トランザクション分離レベル詳細指定は最小限
