# CLAUDE.md

CPIM-MES: CPIM準拠の生産管理（MES）システム。現行リリース v0.42.0（マイグレーション 0001–0042）。
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

- 依存 lock を意図的に更新するときだけ `make lock`。通常は `-mod=readonly` / `npm ci` を使う。
- `scripts/test_release_candidate_unit.py` は Python 3.12+ 必須（ローカルが 3.9 だと実行不可。CI では動く）。

## 必ず守ること

### SHA256SUMS.txt の再生成（ファイルを追加・変更・削除したら毎回）

`SHA256SUMS.txt` は git 管理下の全ファイルのハッシュ一覧。更新しないと CI の
「Backend / Go（Static business-integrity guards）」と「Dependency vulnerability gates」が checksum mismatch で落ちる。
変更をコミットに含めた（`git add` 済みの）状態で、リポジトリ root で実行する:

```bash
git ls-files -z | grep -zv '^SHA256SUMS.txt$' | xargs -0 shasum -a 256 \
  | awk '{h=$1; $1=""; sub(/^ +/,""); print h"  ./"$0}' | LC_ALL=C sort -k2 > SHA256SUMS.txt
python3 scripts/verify_source_checksums.py
```

新規ファイルは `git add` してから実行すること（`git ls-files` に載らないと一覧に入らない）。

### 公開リポジトリと秘密情報

- GitHub `marsroverGH/cpim-mes` は **Public**。`docs/analysis/`（解析レポート）は `.git/info/exclude` で除外しており、**絶対に push しない**。
- `.env*`（例ファイル以外）、`.secrets/`、`.measure/` はコミットしない。
- 既存のリモートブランチ（`main` 以外の `bootstrap/*` など）は書き換えない。変更は `claude/*` ブランチ → PR → main。
- コミット作者は `hbbluesband <hbbluesband@gmail.com>`（リポジトリローカル設定済み）。

### ローカル環境

- 同じマシンで別プロジェクトの Docker（crm-app が 8080 を使用、ほか stock / webapp）が動いている。**触らない・止めない**。
  検証用スタックは compose プロジェクト名 `cpim-mes-measure`、ポート `18080` を使う（`measure_local_stack.sh` が設定済み）。

## 変更時の注意

- **バージョン番号・件数のハードコード**: リリース番号（`v0.42.0`）、マイグレーション最新番号（42）、Playwright 定義数（26）、権限数（54）、保護ルート数（120）が
  workflows・`buildinfo.go`・`openapi.json`・`check_*.py`・docs・RELEASE-*-METADATA.txt に散在する。
  リリースを上げる・マイグレーションやテストを追加するときは `git grep` で全箇所を洗うこと。
- **静的ガードは文字列の有無しか見ていない**。ガードが全 PASS でも実テストが落ちていることがある（実際にあった）。
  判定は `go test` と Playwright の実行結果で行う。逆に、ガードが参照している文字列を消すとガードが落ちる。
- **マイグレーションは追記のみ**。既存ファイル（特に 0041 以前）はバイト単位で不変が前提（ハッシュを docs で記録している）。down マイグレーションは無い。
- API の日付は RFC3339（`2026-11-11T00:00:00Z`）。`YYYY-MM-DD` は 400 になる。
- E2E で UI ログインした直後は `await expect(page).toHaveURL(/\/$/)` でリダイレクト完了を待ってから遷移する。

## 既知の課題（未対応）

移行時の解析で見つかった主な改善点。着手するときは個別に相談のうえ進める。

- SHA256SUMS 方式の廃止（git のコミットハッシュ／タグでの検証へ）
- シリアライザブル競合（SQLSTATE 40001）のリトライ無し
- `writeError` が内部エラー文言 `err.Error()` をそのまま返す
- 数量計算が float
- 在庫台帳が全履歴を再走査する
- カバレッジが低い（service 15.5%、api 12.5%、repository/domain 0%）
