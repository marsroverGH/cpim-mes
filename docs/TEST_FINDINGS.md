# テスト拡充で見つかった不具合（2026-11）

実 DB（`internal/testdb`）を使ったテストを追加した際に見つかった、本番コードの不具合と要確認事項。
未修正のものは、テスト側が正しい挙動を記述しており、不具合に当たると `KNOWN BUG` として `t.Skip` / ログ出力する。
修正すると自動的に検証が有効になる（`go test -v ./... | grep -i "known bug"` で現在の該当箇所を一覧できる）。

## 修正済み

| 内容 | 修正 |
| --- | --- |
| 作業指示の完了が必ず失敗（`completed_at` の CASE が text 型推論、42804） | `workflow.go`: `$2::text` / `$3::timestamptz` を明示。あわせて、完了の再送（冪等リトライ）で結果を読み直す `CompletionLine` に `db` タグが無く失敗していた隠れ不具合も修正 |
| 進捗報告が必ず失敗（`$1` の型不一致、42P08） | `repository.go`: `$1::numeric` を明示 |
| 監査ログ一覧が NULL payload で失敗 | `traceability.go`: NULL を空の payload として読む（API では `payload` を省略） |
| S&OP 製品構成バージョンの有効化・一覧が失敗（DRAFT の `activated_by` が NULL） | `sop.go`: 列を明示し `COALESCE(activated_by,'')` で読む |
| CTP が 2 階層で使われる部品を同じ空き在庫で二重に判定し、不足でも「今日可能」と回答する（#8） | `ctp.go`: BOM 展開結果を部品ごとに合算してから空き在庫と照合 |
| ABC 分析で使用金額最大の品目が B/C になり得る（#9） | `abc.go`: 使用金額 1 位の品目は常に A（それ以外は従来どおり累積比率で判定） |
| 安全在庫計算の品目マスター値へのフォールバックが到達不能（#10） | `inventory_policy.go`: 観測日数を品目の最初の在庫取引からの日数で数える。履歴の無い品目は品目マスター値、履歴の短い品目は窓全体で薄めずに平均を計算 |
| CSV 取込で既存品目を更新すると CSV に無い計画パラメータが初期値に戻る（#12） | `csv.go`: 既存品目は CSV の列だけを上書き |
| CSV 取込がドメイン検証を通さず負の値を受け付ける（#13） | `csv.go`: `domain.Item` と同じ範囲検証（lotSize>0、safetyStock/standardCost>=0、リードタイム 0〜3650、文字数、NaN/Inf） |
| 同一実行内の 2 つのアクションが同じ部品・同じ残業枠を使うと、自分自身の変更を「ドリフト」と判定して PARTIAL_FAILED になる（#16） | `recovery_execution.go`: preflight と実行開始ゲートで、前のアクションの効果を SAVEPOINT 内で実際に適用してから次のアクションを評価し、最後にロールバックする。全体としてのみ実行不能な計画（同じ作業指示の二重リリース、合計で日をまたぐ残業など）は承認前に BLOCKED になる |
| 同じ実行計画への Execute が 8 件以上同時に来るとデッドロック（40P01）が起き、リトライ上限を超えて 409 になる（#14） | `recovery_execution.go`: Execute を実行計画ごとのセッション advisory lock で直列化（トランザクション開始前に取得し、同じ接続で処理）。後続の呼び出しは完了済みの実行を返す。開始ゲート（実行→アクションの順）とアクション処理（アクション→外部キー経由の実行行）のロック順序の逆転が原因だった |
| 同一 PO への RecordEvent が 6 件以上同時だと 40001 がリトライ上限を超える（#15） | `supplier_scheduling.go`: PO ごとのセッション advisory lock をトランザクション開始前に取得する（`advisory_lock.go` の `withSessionAdvisoryLock`）。待機者が古いスナップショットを持たなくなり、24 件同時でも全件成功する |
| 存在しない ID で 404 でなく 500（`GET /api/calendars/{id}`・`/api/crp/schedule-runs/{id}`）、`GET /api/items/{未知}` の 404 本文が `sql: no rows in result set` | `api/errors.go`: `writeError` が `sql.ErrNoRows` を 404・汎用メッセージにする。`getItem` は 500 を渡し、DB 障害の本文が漏れないようにした |
| 一意制約・外部キー違反で 409 でなく 500（品目・顧客・受注の重複登録、使用中の作業区の削除） | `api/errors.go`: ハンドラが 5xx を渡した組み込みの 23505・23503 を 409・汎用メッセージにする（制約名は出さない） |
| 存在しない ID の DELETE が 204（品目・作業区・カレンダー・S&OP 計画など） | `repository/delete.go`: 1 行も削除しなかった DELETE は NOT_FOUND（404）にする |
| 日付形式の不統一（`/api/abc-analysis?asOf` と受注本文の日付は `YYYY-MM-DD` のみ） | `service.ParseDateOnly`: 日付のみの入力は `YYYY-MM-DD` に加えて RFC3339 も受け付ける（業務日付として解釈）。従来の `YYYY-MM-DD` もそのまま使える |
| クライアント指定の `X-Request-Id` をそのまま採用・返却する | `api/errors.go`: `assignRequestID` が常にサーバー側で ID を生成する（chi `middleware.RequestID` を置き換え） |
| 供給者信頼性の `result_hash` が丸め前の float で計算され、保存値から再計算できない | `service/supplier_scheduling.go`: 結果を列の桁（numeric(12,4)・numeric(7,6)）に丸めてから保存・ハッシュ化する |
| `createDemand` ハンドラが到達不能（0031 で需要予測が読み取り専用化） | `api/router.go` から削除。`DemandRepo.Create` は DB 側の拒否を確かめるテストで使うため残している |

## 要判断（未対応）

- 詳細スケジューリング・有限 CRP の稼働時間帯は「UTC の日」を基準に置かれる（例: 8:00 開始のシフトは 8:00 UTC＝17:00 JST）。本番コンテナ（TZ=UTC）の従来動作を維持しているが、工場の稼働時間を業務タイムゾーンで解釈すべきなら、時間帯の基準を `BusinessLocation()` に変える必要がある（既存スケジュールの時刻が 9 時間ずれるため要判断）。
