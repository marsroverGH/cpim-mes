# 数量の精度（float 誤差の対策）

数量は Go 側で float64 として計算している。v0.42.0 までは、計算した値を丸めずに保存していたため、
桁数の決まっていない `numeric` 列に `0.30000000000000004` のような値（float の誤差）が残ることがあった。

## 対策の段階

| 段階 | 内容 | 状態 |
|---|---|---|
| 第 1 段階 | MRP のロット切り上げの誤差を修正。Go で計算した数量を、保存前に小数 9 桁（`service.QuantityScale`）へそろえる（`service.RoundQty`） | v0.42.1 |
| 第 2 段階 | 数量の主要経路の計算を decimal（`github.com/shopspring/decimal`）で行う。domain・API・recovery のハッシュは float64 のまま | v0.43.0 |
| 第 3 段階 | マイグレーション 0043 で主な数量列（25 列）を `numeric(20,9)` に固定。既存の誤差はアップグレード時に丸める | v0.43.0 |

## 第 2 段階: decimal で計算する範囲

次の計算は、入力の float64 を decimal に変換して計算し、結果を小数 9 桁に丸めてから float64 に戻す（`backend/internal/service/quantity.go` の `dq` / `fq`）。
許容誤差（`1e-9`）による比較は、丸めた decimal どうしの比較に置き換えた。

| 経路 | 主な関数 |
|---|---|
| MRP の正味計算・ロットまとめ | `netMRPBucket`、`netMRPBucketWithInventoryPolicy`（Min-Max）、`ApplyLotSize`、`RoundUpToMultiple`、`directComponentRequirement`、MRP 実行中の総所要量・予定受入の積み上げ |
| 在庫台帳・FIFO | `SplitFIFO`、台帳の FIFO 割当、ロット残高の確認、割当合計の確認 |
| 作業指示 | BOM の必要量（`bomRequiredQty`）、引当の解除量、分割完了（`CalcCompletionBatch`） |
| PO の受入 | `CalcPurchaseReceiptState`、`PurchaseScheduledRemaining` |
| 受注 | 引当・引当解除・出荷の数量確認（入力の数量は小数 9 桁に丸める） |

**対象外**（float64 のまま）: domain の構造体と API の JSON、需要予測・統計（EOQ・安全在庫の計算そのもの、OEE など）、スケジューリングの時間（分）、
recovery 計画のハッシュ（公開済みの証跡のハッシュを変えないため）。EOQ は平方根なので、ロットとしては小数 9 桁に丸めた値を使う。

## 第 3 段階: マイグレーション 0043

`backend/migrations/0043_quantity_column_scale.sql`。対象の列と、対象外にした列（棚卸、比率・時間・金額、計画の出力）はファイル先頭のコメントと
`docs/RELEASE_NOTES_v0.43.0.md` を参照。

- 型の変更で、小数 9 桁を超える値は 0 から遠い方向に丸められる。1 行あたりの変化は最大 0.0000000005 で、DB のガードの許容誤差 0.000001 より十分小さい。
- 型の変更をさえぎるビューと列トリガは、定義を取得して作り直す。ビューの上に別のビューが載っている場合は、取りこぼさないよう中止する。
- 最後に、在庫台帳とロット台帳の一致と、ロット残高が負でないことを確認する。
- テスト: `backend/internal/migration/migration_0043_db_test.go`（0042 の時点の DB に誤差つきのデータを入れてから 0043 を適用する）。

## 既存データの確認

v0.42.1 以降のコードは小数 9 桁を超える数量を保存しない。小数 9 桁を超える値があれば、それは v0.42.0 以前に保存された誤差である。
[`scripts/sql/check_quantity_float_artefacts.sql`](../scripts/sql/check_quantity_float_artefacts.sql) で数える。

```bash
PGOPTIONS='-c default_transaction_read_only=on -c statement_timeout=30min' \
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/sql/check_quantity_float_artefacts.sql
```

- 読み取りだけを行う。上のように読み取り専用のセッションで実行する。
- `public` スキーマで桁数の決まっていない `numeric` 列（v0.42.x で 128 列、0043 の適用後は 103 列）をすべて全件走査する。
  本番で実行するときは、レプリカかリストアしたバックアップ（`docs/BACKUP_RESTORE.md`）で行うのが望ましい。
  `numeric(18,6)` のように桁が決まっている列は誤差を持てないので対象外。
- 結果は、誤差つきの値が 1 件以上ある列ごとに 1 行。

| 列 | 意味 |
|---|---|
| `table_name` / `column_name` | 対象の列 |
| `rows_over_9_decimals` | 小数 9 桁を超える値の件数 |
| `max_scale` | その列で最も長い小数の桁数（float の誤差なら 15〜17 桁程度） |
| `example` | 該当する値のうち最小のもの |

**0 行なら誤差は残っていない。**

### 誤差が見つかったとき

- 誤差は 1e-15 程度で、表示や集計の結果にはほとんど影響しない。急いで直す必要はない。
- **手で `UPDATE` して丸めない。** 在庫台帳はロットの数量とロット移動の合計が一致することをトリガで保証しているため、
  1 行だけ丸めると整合が崩れる。v0.43.0 へのアップグレード（マイグレーション 0043）でまとめて丸める。
- 件数と列を記録しておく。0043 の適用後は、0043 で桁を固定した列からは報告されなくなる（固定していない列は引き続き対象）。

## テスト

- `backend/internal/service/quantity_artefact_check_db_test.go`: 確認用 SQL が、誤差つきの値（`0.30000000000000004`）を報告し、
  小数 9 桁ちょうどの値や、誤差の無いデータを報告しないこと。
- `backend/internal/service/ops_quantity_rounding_db_test.go`: 作業指示の引当・FIFO 払出・分割完了で、台帳に誤差が残らないこと。
