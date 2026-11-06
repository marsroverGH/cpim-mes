# v0.42.0 実測ベースライン（Claude Code 移行時）

- 計測日: 2026-11-06
- 対象コミット: `47c6ba9`（無改変アーカイブ）＋ `f935183`（.gitignore のみ変更）
- 計測環境: macOS (arm64) / Go 1.27.2 / Node 24.21.0 / npm 11.19.0 / Docker 29.8.2 / Python 3.9.6
- 目的: ChatGPT 環境で「実行できなかった」とされたランタイムゲートを実際に実行し、真の出発点を記録する。

## 結果サマリ

| ゲート | 結果 | 備考 |
| --- | --- | --- |
| `go mod verify` | ✅ PASS | all modules verified |
| `go vet ./...` | ✅ PASS | |
| `go test -race -count=1 ./...` | ❌ **FAIL（245 PASS / 1 FAIL）** | `internal/buildinfo` のテスト不具合（下記 1） |
| `npm ci` + `npm run build`（vue-tsc 型検査含む） | ✅ PASS | 600kB 超チャンク警告あり |
| Docker 本番イメージビルド＋起動 | ✅ PASS | 39 秒。backend 37MB / frontend 101MB |
| 空 DB への 0001→0042 適用 | ✅ PASS | `schema_migrations` 42 件 / latest 42 |
| 観測性ゲート（/metrics 非公開） | ✅ PASS | 4 メトリクス存在、公開側 404 |
| 強制リカバリ失敗の永続化ゲート | ✅ PASS | 5/5 |
| Playwright | ❌ **FAIL（25 PASS / 1 FAIL）** | 0042 の E2E（下記 2）。`--retries=0` で実行 |
| バックアップ／リストア訓練 | ✅ PASS | オブジェクト一覧・行数とも一致 |
| 静的ガード `backend/scripts/check_*.py` | ✅ 38/38 PASS | ※実テストが失敗していても PASS（下記 3） |
| `scripts/verify_source_checksums.py` | ❌ FAIL | 想定どおり。ファイル追加・.gitignore 変更で失敗（下記 4） |
| `scripts/test_release_candidate_unit.py` | ⚠️ 実行不可 | Python 3.12+ 必須（`tarfile.extractall(filter=)`）。ローカルは 3.9 |

## 判明した不具合

### 1. `buildinfo` テストのバージョン更新漏れ

`backend/internal/buildinfo/buildinfo_test.go:17` は `Current(41)` を呼んでいながら `MigrationLatest != 42` を期待している。
v0.41 → v0.42 への手作業の数値置換が片側だけ行われたもの。**本番コードは正常**（実起動時の `/version` は `migrationLatest: 42`）。

### 2. 0042 リカバリ実行 E2E は一度も成功していない

`e2e/tests/recovery-execution.spec.ts:49-53` で Work Order 作成が `400` を返す。
原因はテストが日付を `YYYY-MM-DD`（`day()` ヘルパ）で送っているのに対し、`POST /api/work-orders` は
Go の `time.Time`（RFC3339, 例 `2026-11-11T00:00:00Z`）しか受け付けないこと。他の spec は `dateTime()` ヘルパで RFC3339 を送っている。
このため **v0.42.0 の主機能（Prepare → Preflight → Approve → Execute）の E2E 検証は未完了**。

### 3. 静的ガードは実際の失敗を検出しない

上記 1・2 が存在する状態で 38 本すべて PASS。ガードはソース中の文字列の有無を見ているだけで、
`check_controlled_recovery_execution.py` は失敗している E2E の「ソース文字列」を確認して PASS としている。

### 4. SHA256SUMS は git 運用と両立しない

無改変ベースライン以外のあらゆる変更（`.gitignore` 修正、ドキュメント追加）で失敗する。git の履歴ハッシュで代替し、廃止を推奨。

## カバレッジ（`go test -cover`）

| パッケージ | カバレッジ |
| --- | --- |
| internal/config | 85.3% |
| internal/observability | 83.1% |
| internal/migration | 23.3% |
| internal/service | 15.5% |
| internal/api | 12.5% |
| internal/repository | 0% |
| internal/domain | 0% |

## 再現方法

```bash
cd backend && go mod verify && go vet ./... && go test -race -count=1 ./...
cd frontend && npm ci && npm run build
./scripts/measure_local_stack.sh   # Docker 起動・移行・ゲート・Playwright・リストア訓練（ログは .measure/）
```

`measure_local_stack.sh` は CI の Integration / Playwright ジョブを再現する。他プロジェクトと衝突しないよう
compose プロジェクト名 `cpim-mes-measure`・ポート `18080` を使い、終了時に `down -v` で破棄する。
