# マイグレーション 0043 のリハーサル（v0.42.x → v0.43.0）

0043（Quantity Column Scale）は、主な数量列（16 テーブル 25 列）を `numeric(20,9)` に変える。
対象のテーブルは書き換えられ、その間は排他ロック（ACCESS EXCLUSIVE）が掛かる。
本番でアップグレードする前に、**本番のバックアップをリストアした別の環境**で、次の 2 点を確かめる。

1. 0043 が止まらずに適用できること（止まる条件は下の表）。
2. 適用にかかる時間。メンテナンス時間と `MIGRATION_TIMEOUT` に収まるかを判断する材料にする。

経緯と対象の列は `docs/QUANTITY_PRECISION.md` と `docs/RELEASE_NOTES_v0.43.0.md`。

## 使うもの

`scripts/rehearse_migration_0043.sh`。本番の DB には向けない。

| モード | していること | DB への影響 |
|---|---|---|
| 既定（preflight） | 対象テーブルの件数とサイズ、保存済みの float 誤差、0043 が止まる条件を調べる | 一時テーブルだけを作り、最後に ROLLBACK する（業務のテーブルは変えない。ただし対象テーブルを全件走査する） |
| `--dry-run` | preflight に加えて、0043 をトランザクションの中で実行し、時間を測り、結果を確かめてから **ROLLBACK** する | 何も残らない。実行中は 16 テーブルに排他ロックが掛かるので、ほかのセッションが接続していると実行を拒否する |

- 0043 の対象の列は、マイグレーションのファイルから読み取る（スクリプトに列の一覧を二重に持たない）。
- DB がマイグレーション 42 でなければ止まる（43 なら「適用済み」と表示して終わる）。
- 終了コード: 0 = 問題なし、1 = 0043 が止まる条件がある（またはドライランが失敗した）、2 = 使い方・接続の誤り。

## 手順

### 1. リハーサル用の環境にリストアする

本番と**別のホスト、または別の Compose プロジェクト**に、v0.42.x のまま DB を用意してバックアップをリストアする（`docs/BACKUP_RESTORE.md`）。
バックアップには業務データが入っているので、リハーサル環境も本番と同じ扱いにする。

```bash
# 例: リハーサル用ホストで、v0.42.1 のソースから DB だけを起動してリストアする
export COMPOSE_PROJECT_NAME=cpim-rehearsal CPIM_ENV_FILE=.env.rehearsal
docker compose --env-file "$CPIM_ENV_FILE" -f docker-compose.yml -f docker-compose.prod.yml up -d db
./scripts/restore_postgres.sh backups/cpim-mes-YYYYMMDDTHHMMSSZ.dump --target-db cpim_rehearsal
```

`restore_postgres.sh` は、Compose の `db` サービスの中で、`POSTGRES_DB` とは別の DB（ここでは `cpim_rehearsal`）にリストアする。

- **backend はまだ起動しない。** v0.43.0 の backend を起動すると、その場で 0043 が本当に適用される。
- スクリプトは v0.43.0 のソース（このファイルがあるチェックアウト）から実行する。

### 2. preflight（DB は変えない）

```bash
PSQL="docker compose --env-file $CPIM_ENV_FILE -f docker-compose.yml -f docker-compose.prod.yml exec -T db psql -U cpim -d cpim_rehearsal" \
  ./scripts/rehearse_migration_0043.sh | tee rehearsal-0043-preflight.txt
```

`-U` は `.env.rehearsal` の `POSTGRES_USER` に置き換える（`$PSQL` は空白で区切るので、パスに空白を含めない）。`psql` が手元にあれば `--database-url 'postgres://...'` でもよい。

出力の見方:

| 項目 | 見るところ |
|---|---|
| tables 0043 rewrites | 件数とサイズ。書き換えにかかる時間は、ほぼ `total_size` に比例する |
| columns | `type` が `numeric` で `scale` が空であること。`over_9` は丸められる値の件数、`too_large` は 0 であること |
| views / column triggers | v0.42.x では、ビュー 7 個とトリガ 1 個（`mps_forecast_provenance_guard_trg`）。数が違えば、本番で独自に作ったものがある |
| ledger before 0043 | `unreconciled_items` と `negative_lots` が 0 であること。`near_tolerance_items` は差が許容誤差の半分を超えている品目の数（参考） |
| stored float artefacts | 0043 で丸められる前の誤差の一覧。**記録として保存しておく** |
| other sessions | ドライランの前に 0 にする（backend などが接続していないこと） |
| blockers | `none` 以外なら、0043 は止まる |

### 3. ドライラン（時間を測る）

```bash
PSQL="docker compose --env-file $CPIM_ENV_FILE -f docker-compose.yml -f docker-compose.prod.yml exec -T db psql -U cpim -d cpim_rehearsal" \
  ./scripts/rehearse_migration_0043.sh --dry-run | tee rehearsal-0043-dry-run.txt
```

- `Time:` の行が各文の実行時間、`dry run wall time` が全体の時間。
- 実行後の確認として、`numeric(20,9)` の列が 25、台帳の不一致が 0、残っている float 誤差の一覧（0043 が変えない列のもの）を表示する。
- 最後に ROLLBACK するので、DB は元のまま。何度でも繰り返せる。
- リハーサル環境のディスク・メモリ・キャッシュの状態は本番と違うので、時間は目安として、余裕を持たせる。
- 参考（2026-11-08、ローカルの PostgreSQL 16 / Docker）: `inventory_txns` と `lot_movements` が各 100 万行（対象テーブル計 716 MB）で、0043 の本体が約 4.5 秒。

### 4. 本番の段取りを決める

- **メンテナンス時間**: ドライランの時間に、バックアップの取得と backend の起動・確認の時間を足す。
- **`MIGRATION_TIMEOUT`**（既定 `10m`）: backend の起動時のマイグレーションはこの時間で打ち切られ、0043 は ROLLBACK される（DB は変わらない）。
  ドライランが既定の半分（5 分）を超えたらスクリプトが警告する。そのときは本番の `.env` で十分に長くする（例: `MIGRATION_TIMEOUT=60m`）。
- **frontend の起動待ち**: `docker-compose.prod.yml` では frontend が backend の healthy を待つ。backend はマイグレーションが終わるまで healthy にならず、
  ヘルスチェックの待ちは最大で約 2 分（`start_period` 10 秒 + 10 秒 × 12 回）。0043 がそれより長くかかりそうなら、`docker compose up -d backend` で backend だけを先に起動し、
  ログで 0043 の適用を確かめてから frontend を起動する。
- **戻し方**: down マイグレーションは無い。戻すときは、アップグレードの直前に取ったバックアップから復元し、v0.42.1 で起動する。

### 5. 本当に適用する（任意、リハーサル環境で）

リハーサル環境で v0.43.0 の backend を起動すると、0043 が実際に適用される。本番と同じ手順を通しで確かめたいときに行う。
適用後に `scripts/sql/check_quantity_float_artefacts.sql` を実行し、0043 で固定した 25 列が一覧に出ないことを確かめる。

## 0043 が止まる条件と対処

どれも、0043 は 1 つのトランザクションで中止され、DB は何も変わらない。

| preflight の表示 | 原因 | 対処 |
|---|---|---|
| `... is not an unconstrained numeric` | 本番で列の型を独自に変えている | 0043 を適用する前に、変更の経緯を確認する。0043 は v0.42.x の標準のスキーマを前提にしている |
| `... too large for numeric(20,9)` | 絶対値が 10^11 以上の数量がある | 入力の誤りであることが多い。業務担当と確認し、正しい値に直してから再度リハーサルする |
| `view ... depends on a view 0043 recreates` / `materialized view` | 本番で独自のビューを追加している | そのビューを一時的に削除し、0043 の後で作り直す手順を用意する |
| `trigger ... has enabled state` | トリガを `REPLICA` / `ALWAYS` にしている | 状態を確認し、標準の状態に戻してから適用する |
| `... already differ between inventory and lot ledger` / `negative balance` | **0043 の前から**台帳が一致していない | 0043 の問題ではない。不一致の原因を調べてから進める（`v_inventory_lot_reconciliation`） |

ドライランが失敗したとき（`RESULT: dry run FAILED`）は、直前に出ている `ERROR:` の行を見る。`lock_timeout` によるものなら、ほかのセッションがロックを持っている。

## 記録しておくもの

- preflight とドライランの出力（`rehearsal-0043-*.txt`）。中身は件数・サイズ・列名・数値の例で、パスワードなどは含まないが、業務データの規模がわかるので社外に出さない。
- 使ったバックアップの日時と `.sha256`、リハーサル環境の構成（CPU・メモリ・ディスク）。
- 決めたメンテナンス時間と `MIGRATION_TIMEOUT` の値。
