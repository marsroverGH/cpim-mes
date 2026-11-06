# テスト拡充で見つかった不具合（2026-11）

実 DB（`internal/testdb`）を使ったテストを追加した際に見つかった、本番コードの不具合と要確認事項。
**どれも未修正**。テスト側は正しい挙動を記述しており、不具合に当たると `KNOWN BUG` として `t.Skip` / ログ出力する。
修正すると自動的に検証が有効になる（`go test -v ./... | grep -i "known bug"` で現在の該当箇所を一覧できる）。

## 機能が動かない（優先度：高）

| # | 内容 | 場所 | 影響 |
| --- | --- | --- | --- |
| 1 | 作業指示の完了が必ず失敗する。`completed_at=CASE WHEN $2='COMPLETED' THEN $3 ELSE NULL END` が text 型と推論され SQLSTATE 42804 | `backend/internal/service/workflow.go:957-966` | `POST /api/work-orders/{id}/complete` が常に 400（ロールバックは正常）。E2E はこの経路を通らないため未検出だった |
| 2 | 進捗報告が必ず失敗する。`$1` を numeric と `$1 > 0` で併用し SQLSTATE 42P08 | `backend/internal/repository/repository.go:437-446` | `POST /api/work-orders/{id}/progress` が常に 500 |
| 3 | 監査ログ一覧が取れなくなる。NULL の `payload`（本文の無い DELETE 等で記録）を `json.RawMessage` に Scan できない | `backend/internal/repository/traceability.go:99-114` | 該当行が 1 件でもあると `GET /api/audit-log` が 500 |
| 4 | S&OP 製品構成バージョンを有効化・一覧できない。DRAFT 行の `activated_by` が NULL で `string` に Scan できない | `backend/internal/service/sop.go:68, 157` | S&OP の配分機能が使えない |

## 計算結果が誤る（優先度：高〜中）

| # | 内容 | 場所 |
| --- | --- | --- |
| 5 | MRP の期間バケットがタイムゾーンで分裂する（DATE 列は UTC、開始日はローカル）。計画期間最終日の MPS が落ちる（再現: 18 → 15） | `backend/internal/service/service.go`（`MRPService.run`） |
| 6 | MRP アクションメッセージの「今日」がローカル、期日が UTC。JST では当日期日が CRITICAL でなく WARNING になる | `backend/internal/service/action_messages.go:213` |
| 7 | 開始日を省略した CRP で休日が無視される（開始日・期間の既定値が無く、例外カレンダーの範囲が西暦 1 年になる） | `backend/internal/service/capacity.go:163-196`、`backend/internal/api/capacity.go:188` |
| 8 | CTP が 2 階層で使われる部品を同じ空き在庫で二重に判定し、不足でも「今日可能」と回答する | `backend/internal/service/ctp.go:44-104` |
| 9 | ABC 分析で使用金額最大の品目が B/C になり得る（累積比率に自分自身を含めて判定） | `backend/internal/service/abc.go:131` |
| 10 | 安全在庫計算で品目マスター値へのフォールバックが到達不能。履歴の無い品目が 0 になる | `backend/internal/service/inventory_policy.go`（`Refresh` 付近 408 行） |
| 11 | 詳細スケジューリング・ペギングの日付計算がサーバーのタイムゾーンに依存する | `backend/internal/service/detailed_scheduling.go:756-888`、`backend/internal/service/pegging.go:303` |

## データを壊す・検証漏れ（優先度：中）

| # | 内容 | 場所 |
| --- | --- | --- |
| 12 | CSV 取込で既存品目を更新すると、CSV に無い計画パラメータ（ロットサイズ方式・POQ・発注費・保管費率・グループ）が初期値に戻る | `backend/internal/service/csv.go`（`ImportItems` / `parseItemRow`） |
| 13 | CSV 取込がドメイン検証を通さず、負の lotSize / safetyStock / standardCost を受け付ける | 同上 |

## 同時実行（優先度：中）

| # | 内容 | 場所 |
| --- | --- | --- |
| 14 | 同じ実行計画への Execute が 8 件以上同時に来るとデッドロック（40P01）が起き、5 回のリトライで解消しきれず 409 になる。データ整合性は保たれる | `backend/internal/service/recovery_execution.go:781, 1091`（ロック順序） |
| 15 | 同一 PO への RecordEvent が 6 件以上同時だと 40001 がリトライ上限を超える（advisory lock の時点でスナップショットが固定され、待機者が必ず 1 回失敗する） | `backend/internal/service/supplier_scheduling.go:179, 199` |
| 16 | 同一実行内の 2 つのアクションが同じ部品・同じ残業枠を使うと、自分自身の変更を「ドリフト」と判定して PARTIAL_FAILED になる | `backend/internal/service/recovery_execution.go:476, 586` |

## API のステータス・その他（優先度：低）

- 存在しない ID で 404 でなく 500: `GET /api/calendars/{id}`（`api/calendar.go:25`）、`GET /api/crp/schedule-runs/{id}`（`api/capacity.go:242`）
- 一意制約違反で 409 でなく 500: `POST /api/items`・`/api/customers`・`/api/sales-orders`、使用中の作業区の削除
- `GET /api/items/{未知}` の 404 本文が `sql: no rows in result set`
- 存在しない ID の DELETE が 204（品目・作業区・カレンダー・S&OP 計画）
- 日付形式の不統一（`/api/abc-analysis?asOf` は `YYYY-MM-DD`、受注本文も `YYYY-MM-DD` を受理）
- クライアント指定の `X-Request-Id` をそのまま採用・返却する（chi `middleware.RequestID`）
- 供給者信頼性の `result_hash` が丸め前の float で計算され、保存値から再計算できない（`service/supplier_scheduling.go:354`）
- `DemandRepo.Create`・`createDemand` は到達不能（0031 で需要予測が読み取り専用化）
