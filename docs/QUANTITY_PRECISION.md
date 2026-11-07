# 数量の精度（float 誤差の対策）

数量は Go 側で float64 として計算している。v0.42.0 までは、計算した値を丸めずに保存していたため、
桁数の決まっていない `numeric` 列に `0.30000000000000004` のような値（float の誤差）が残ることがあった。

## 対策の段階

| 段階 | 内容 | 状態 |
|---|---|---|
| 第 1 段階 | MRP のロット切り上げの誤差を修正。Go で計算した数量を、保存前に小数 9 桁（`service.QuantityScale`）へそろえる（`service.RoundQty`） | v0.42.1 |
| 第 2 段階 | 内部計算の decimal 化 | 未着手 |
| 第 3 段階 | 主要な数量列の桁固定（マイグレーション） | 未着手 |

## 既存データの確認

v0.42.1 以降のコードは小数 9 桁を超える数量を保存しない。小数 9 桁を超える値があれば、それは v0.42.0 以前に保存された誤差である。
[`scripts/sql/check_quantity_float_artefacts.sql`](../scripts/sql/check_quantity_float_artefacts.sql) で数える。

```bash
PGOPTIONS='-c default_transaction_read_only=on -c statement_timeout=30min' \
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/sql/check_quantity_float_artefacts.sql
```

- 読み取りだけを行う。上のように読み取り専用のセッションで実行する。
- `public` スキーマで桁数の決まっていない `numeric` 列（2026-11 時点で 128 列）をすべて全件走査する。
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
  1 行だけ丸めると整合が崩れる。直すときは、第 3 段階のマイグレーションでまとめて扱う。
- 件数と列を記録しておく（第 3 段階の移行計画の入力になる）。

## テスト

- `backend/internal/service/quantity_artefact_check_db_test.go`: 確認用 SQL が、誤差つきの値（`0.30000000000000004`）を報告し、
  小数 9 桁ちょうどの値や、誤差の無いデータを報告しないこと。
- `backend/internal/service/ops_quantity_rounding_db_test.go`: 作業指示の引当・FIFO 払出・分割完了で、台帳に誤差が残らないこと。
