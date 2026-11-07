# 在庫台帳の件数と応答時間（計測記録）

- 計測日: 2026-11-07
- 対象コミット: `f3ea5c0`（main）
- 計測環境: macOS (arm64) / Go 1.27.2 / PostgreSQL 16.15（Docker、`postgres:16.15-alpine3.24`）
- 目的: CLAUDE.md の既知の課題「在庫台帳が全履歴を再走査する」について、実際にどの件数から問題になるかを確かめる。

## 結論

- 台帳全体を集計する処理（在庫残高の一覧・照合、ABC 分析、MRP）は、取引件数にほぼ比例して遅くなる。
- ただし **100 万件でもすべて 0.15 秒以内**で、現時点で直す必要はない。
  品目 200 件規模で 1 日 1,000 件の取引がある工場なら、100 万件に達するのは約 3 年後。
- 入庫・出庫の記帳は 300 万件でも 3〜4ms で、件数の影響を受けない（ロット残高のチェックはロット単位のインデックスで済んでいる）。
- 改善に着手する目安は **取引 100 万件、または在庫画面・受注の約束で 0.3 秒を超えたとき**。下の「気をつける点」の処理が先に重くなる。

## 計測条件

- 使い捨てのテスト用 DB（`internal/testdb` のテンプレートを複製した DB）に、SQL で一括投入した。
  - 品目: シードの 7 品目を 31 倍に複製して 217 品目（複製品目に BOM は無い）
  - ロット: 品目ごとに 20 ロット（約 4,300 ロット）
  - 取引: 直近 730 日に均等に分散。入庫（+10）・出庫（−5）を交互に、10 件に 2 件は引当・引当解除（ロット移動なし）
  - 入庫・出庫には同数量の `lot_movements` を 1 行ずつ付けた（取引 100 万件 → ロット移動 80 万件）
  - 一括投入時だけ `session_replication_role = replica` でトリガを止め、投入後に `ANALYZE` した
- 各処理をサービス層（Go）から 3〜5 回呼び、中央値を記録した。HTTP・認証のオーバーヘッドは含まない。
- 計測用のテストはリポジトリに含めていない（再計測の方法は末尾）。

## 結果（中央値）

| 処理 | 0 件 | 10 万件 | 100 万件 | 300 万件 |
|---|---:|---:|---:|---:|
| 在庫残高の一覧（`Inventory.OnHand`、`v_stock_balance`） | 1ms | 9ms | 79ms | 230ms |
| 在庫残高（引当込み、`Inventory.Balance`） | 1ms | 14ms | 118ms | 370ms |
| 台帳とロットの照合（`Inventory.Reconciliation`） | 2ms | 18ms | 124ms | 385ms |
| MRP（`MRP.Simulate`、90 日） | 82ms | 70ms | 145ms | 296ms |
| ABC 分析（`ABC.Run`） | 3ms | 18ms | 108ms | 339ms |
| CTP（`CTPEngine.MaterialReady`、BIKE-100 を 10 台） | 5ms | 5ms | 20ms | 127ms |
| 安全在庫の需要統計（`demandStatistics`、1 品目・90 日） | 1ms | 2ms | 12ms | 38ms |
| ATP（`ATP.Run`、1 品目・90 日） | 1ms | 2ms | 4ms | 11ms |
| 1 品目の取引履歴（`Inventory.Transactions`） | 0.2ms | 0.8ms | 8ms | 18ms |
| 入庫の記帳（`InventoryLedger.Post`、新ロット） | 2ms | 2ms | 3ms | 3ms |
| 出庫の記帳（`InventoryLedger.Post`、FIFO 割当） | 2ms | 2ms | 3ms | 4ms |

一括投入にかかった時間は、100 万件で約 9 秒、300 万件で約 29 秒だった。

## 気をつける点

- **安全在庫の一括更新（`InventoryPolicy.Refresh`）**: 需要統計を品目ごとに繰り返すので、全品目が対象だと 300 万件で約 8 秒（217 品目 × 38ms）になる見込み。
  今回のデータには安全在庫の方針が登録されておらず、Refresh 自体は品目を処理していないため、この値は 1 品目の結果からの**推定**で実測ではない。
- **CTP**: 100 万件から 300 万件で 20ms → 127ms と、件数の比率（3 倍）より大きく伸びた。受注の約束（Order Promising）は明細ごとに CTP を呼ぶので、明細の多い受注で先に重くなる可能性がある。
- **1 品目の取引履歴**: 件数の上限が無く、その品目の取引をすべて返す（300 万件のデータでは 1 品目あたり約 1.4 万件）。

## 直す場合の方向性（未着手）

1. **ロットごとの残高テーブル**をトリガで更新し、在庫残高・照合・CTP・FIFO 割当がこれを読むようにする。取引件数に左右されなくなる（マイグレーションの追加が必要）。
2. **取引履歴の API に件数の上限とページング**を付ける。
3. ABC 分析と需要統計はすでに期間で絞り込んでいるので、今の規模では手を入れない。

## 再計測の方法

`backend/internal/service` に一時的なテストを置き、`newDBFixture` で作った DB に次の要領でデータを投入してから、上の表のサービスを呼んで時間を測る。

```sql
SET session_replication_role = replica;
-- 品目の複製（7 品目 × 30）
INSERT INTO items
SELECT (jsonb_populate_record(NULL::items,
        to_jsonb(i) || jsonb_build_object('id', gen_random_uuid(), 'code', i.code || '-B' || g))).*
  FROM items i, generate_series(1, 30) g;
-- 品目ごとに 20 ロット、取引 N 件と対応するロット移動を generate_series で生成
SET session_replication_role = origin;
ANALYZE;
```

`CPIM_TEST_DATABASE_URL` を設定して実行する（CLAUDE.md の「DB を使うテスト」を参照）。テストは計測後に削除する。
