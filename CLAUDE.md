# CLAUDE.md

CPIM-MES: CPIM準拠の生産管理（MES）システム。現行リリース v0.42.1（マイグレーション 0001–0042）。
ChatGPT で開発され、2026-11 に Claude Code へ移行した。移行時の実測結果は `docs/BASELINE_MEASUREMENT.md`。

## 構成

- `backend/` Go 1.26（chi v5, sqlx, pgx v5）。エントリは `backend/cmd/server`、本体は `backend/internal/{api,service,repository,domain,migration,...}`
- `backend/migrations/` PostgreSQL 16 のマイグレーション（埋め込み、`embed.go`）。業務ルールの多くは PL/pgSQL 関数・トリガ側にもある（Go と二重実装）
- `frontend/` Vue 3 + Vuetify + Pinia + Vite。本番は Nginx コンテナ
- `e2e/` Playwright（定義数 26）
- `scripts/` リリース・DR・セキュリティ用スクリプト、`backend/scripts/check_*.py` 静的ガード 38 本
- `docker-compose*.yml` 本体 + `dev` / `prod` / `bootstrap`（clean DB 初回のみ）/ `locktest`

## よく使うコマンド

```bash
# Backend
cd backend && go mod verify && go vet -mod=readonly ./... && go test -mod=readonly -race -count=1 ./...

# Frontend（vue-tsc 型検査込み）
cd frontend && npm ci && npm run build

# Docker 一式・0001→0042 適用・観測性/失敗永続化ゲート・Playwright・リストア訓練（CI の Integration ジョブ相当）
./scripts/measure_local_stack.sh        # ログは .measure/、終了時に down -v

# 静的ガード一式・CI 相当
make ci-local
```

- **DB を使うテスト**: `CPIM_TEST_DATABASE_URL` に使い捨て PostgreSQL 16 の管理者 URL を設定すると有効になる（未設定ならスキップ）。
  各テストはマイグレーション済みテンプレートを複製した専用 DB を使う（`backend/internal/testdb`）。ローカルでは例えば:
  `docker run -d --name cpim-mes-testdb -e POSTGRES_USER=cpim -e POSTGRES_PASSWORD=testpw -p 127.0.0.1:55499:5432 postgres:16.15-alpine3.24` →
  `CPIM_TEST_DATABASE_URL='postgres://cpim:testpw@127.0.0.1:55499/postgres?sslmode=disable' go test -race ./...`。CI の Backend ジョブも同じ方式。
- 既知の未修正バグは `docs/TEST_FINDINGS.md`（2026-11-08 時点で未修正は無い）。新しく見つけた未修正バグはテストで `KNOWN BUG` として skip し、直したら skip を消して回帰テスト（失敗扱い）にする。
- 依存 lock を意図的に更新するときだけ `make lock`。通常は `-mod=readonly` / `npm ci` を使う。
- `scripts/test_release_candidate_unit.py` は Python 3.12+ 必須（ローカルが 3.9 だと実行不可。CI では動く）。

## 必ず守ること

### SHA256SUMS.txt はコミットしない

`SHA256SUMS.txt` はリリース時に `scripts/generate_source_checksums.py` が git 管理下のファイルから自動生成する（`.gitignore` 済み）。
普段の変更で作り直す必要はない。配布物（リリース候補の source tar）の改ざん検知にだけ使う。
手元で確認したいときは `make verify-source-checksums`。

### 公開リポジトリと秘密情報

- GitHub `marsroverGH/cpim-mes` は **Public**。`docs/analysis/`（解析レポート）は `.git/info/exclude` で除外しており、**絶対に push しない**。
- `.env*`（例ファイル以外）、`.secrets/`、`.measure/` はコミットしない。
- 既存のリモートブランチ（`main` 以外の `bootstrap/*` など）は書き換えない。変更は `claude/*` ブランチ → PR → main。
- コミット作者は `hbbluesband <hbbluesband@gmail.com>`（リポジトリローカル設定済み）。

### ローカル環境

- 同じマシンで別プロジェクトの Docker（crm-app が 8080 を使用、ほか stock / webapp）が動いている。**触らない・止めない**。
  検証用スタックは compose プロジェクト名 `cpim-mes-measure`、ポート `18080` を使う（`measure_local_stack.sh` が設定済み）。

## 変更時の注意

- **バージョン番号・件数のハードコード**: リリース番号（`v0.42.1`）、マイグレーション最新番号（42）、Playwright 定義数（26）、権限数（54）、保護ルート数（120）が
  workflows・`buildinfo.go`・`openapi.json`・`check_*.py`・docs・RELEASE-*-METADATA.txt に散在する。
  リリースを上げる・マイグレーションやテストを追加するときは `git grep` で全箇所を洗うこと。
- **静的ガードは文字列の有無しか見ていない**。ガードが全 PASS でも実テストが落ちていることがある（実際にあった）。
  判定は `go test` と Playwright の実行結果で行う。逆に、ガードが参照している文字列を消すとガードが落ちる。
- **マイグレーションは追記のみ**。既存ファイル（特に 0041 以前）はバイト単位で不変が前提（ハッシュを docs で記録している）。down マイグレーションは無い。
- **業務日付**: 日付の比較・集計・「今日」は `service.BusinessDate` / `BusinessToday()` を使う（業務タイムゾーンの暦日を UTC 0 時で表す。DB の `eco_business_timezone()` と一致）。`TruncateDay(time.Now())` はサーバーの TZ に依存するので使わない。日付が絡むテストは `TZ=UTC` と `TZ=Asia/Tokyo` の両方で通すこと。
- **シフト時間帯は UTC 基準**（2026-11 決定）: 詳細スケジューリング・有限 CRP では、作業区の `shiftStartMinute` を UTC 0 時からの分として扱う（480 = 8:00 UTC = 17:00 JST）。業務日付とは基準が違うので、業務タイムゾーンに寄せる変更はしない（`TestShiftWindowIsInterpretedInUTC` が落ちる）。
- API の日付は RFC3339（`2026-11-11T00:00:00Z`）。`YYYY-MM-DD` は 400 になる。
- E2E で UI ログインした直後は `await expect(page).toHaveURL(/\/$/)` でリダイレクト完了を待ってから遷移する。

## 既知の課題（未対応）

移行時の解析とテスト拡充で見つかった主な改善点（テストで見つかった不具合の一覧は `docs/TEST_FINDINGS.md`）。着手するときは個別に相談のうえ進める。

- 数量計算が float（2026-11-08 に第 1 段階として、MRP のロット切り上げと、Go で計算した数量を保存前に小数 9 桁へそろえる処理を修正。残りは内部計算の decimal 化と、主要な数量列の桁固定（マイグレーション）。Go で数量を計算して保存するときは `service.RoundQty` を通す）
- 在庫台帳が全履歴を再走査する（2026-11-07 計測: 取引 100 万件でも 0.15 秒以内で、今は不要。着手の目安と方向性は `docs/LEDGER_SCALING_MEASUREMENT.md`）
