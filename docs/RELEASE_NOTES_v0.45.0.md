# v0.45.0 リリースノート

- 種別: マイナーリリース（マイグレーション 0046 を追加、DB ロールの分離）
- ベースライン: v0.44.0（`d5f0d10`）
- マイグレーション: 0001–0046
- Playwright 定義数: 26

v0.44.0 以前の変更は `docs/RELEASE_NOTES_v0.44.0.md` を参照。

## 変更

### DB ロールの分離（PR #30）
backend は最小権限のロール `cpim_app` で DB に接続する。マイグレーションは 1 回だけ動く `migrate` サービス（`server migrate`）が所有者ロールで行い、実行のたびに `cpim_app` の権限を付け直す。
`cpim_app` はスキーマの変更、トリガの無効化、`audit_log` の既存行の変更・削除ができない。
backend は起動時にスキーマを確認するだけで DB を変更しない。本番では superuser やテーブルの所有者で接続すると起動しない。詳細は `docs/DB_ROLES.md`。

### 発注を締めるときの原因区分と納期遵守率（PR #31、マイグレーション 0046）
発注を締めるときに、原因（仕入先都合 `SUPPLIER` / 自社都合 `INTERNAL`）の指定が必須になった。
`SUPPLIER` で締めた発注は、仕入先の納期遵守率で未遵守として数える。`INTERNAL` と、0046 より前に締めた発注（`UNKNOWN`）は集計に入れない。リードタイムの統計は全量入荷の発注だけで計算する。
詳細は `docs/PO_CLOSE_NCR_CANCEL.md`。

## 運用上の注意（挙動の変化）

- **接続設定の変更が必要**: `DATABASE_URL` を `cpim_app` に向け、`MIGRATION_DATABASE_URL` と `APP_DB_PASSWORD` を追加する（下の「アップグレード」）。設定しないと本番の compose は起動しない。
- **マイグレーションは `migrate` サービスで行う**。backend 単体を起動しても、DB が古ければマイグレーションせずに停止する。
- **リストア後は `migrate` を実行する**。リストアは `--no-acl` で行うため、`cpim_app` の権限は `migrate` で付け直す（`docs/BACKUP_RESTORE.md`）。
- **発注の締めの API に `cause` が必須**: `POST /api/purchase-orders/{id}/close` の本文は `{"cause":"SUPPLIER|INTERNAL","reason":"..."}`。`cause` が無いと 400。
- **仕入先の納期遵守率が下がることがある**: 仕入先都合で締めた発注が未遵守として加わるため。期限を過ぎても締めていない発注は、引き続き集計に入らない。

## アップグレード

v0.44.0 から上げる手順（マイグレーション 0046 は列の追加だけで、数秒以内の見込み）:

1. バックアップを取る（`docs/BACKUP_RESTORE.md`）。
2. `.env.production` を変更する（`.env.production.example` を参照）。
   - `MIGRATION_DATABASE_URL`: これまでの `DATABASE_URL`（所有者 `cpim` の接続先）
   - `APP_DB_PASSWORD`: `cpim_app` に設定する新しいパスワード
   - `DATABASE_URL`: ユーザーを `cpim_app`、パスワードを `APP_DB_PASSWORD` と同じにした接続先
   - `MIGRATION_INSTALLED_BY` を設定していた場合は、`production-migrate` などに変える（任意）
3. いつもの compose コマンドで起動する。`migrate` が 0046 を適用して `cpim_app` を作り、終わってから backend が起動する。
4. `/version` が `v0.45.0` / `migrationLatest: 46` を返すこと、ログインして画面が使えることを確認する。

v0.43.0 以前から上げる場合は、`docs/RELEASE_NOTES_v0.44.0.md` の手順も参照する。
down マイグレーションは無いので、戻すときはバックアップから復元する。

## 既知の課題

- 所有者 `cpim` のパスワードはサーバーの `.env` にあり、サーバーに入れる人は DB の管理権限を得られる（`docs/DB_ROLES.md`）。
- `audit_log` の保持期間は未定。
- 需要予測・統計・スケジューリングの計算は float64 のまま。

## リリース手順

`docs/RELEASE_CHECKLIST.md` に従う。最終判定は `.github/workflows/release-candidate.yml`（`v0.45.0-rc.1`、`scripts/final_clean_room_release_gate.sh`）。
