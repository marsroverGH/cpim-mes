# DB ロールの分離

backend は最小権限のロールで接続し、マイグレーションは別の手順で行う。アプリが乗っ取られても、不変トリガを外したり、スキーマや監査ログを変更したりできないようにするため。

## ロール

| ロール | 用途 | 権限 |
|---|---|---|
| `cpim`（`POSTGRES_USER`） | マイグレーション、バックアップ、リストア、pgAdmin | superuser。テーブルの所有者 |
| `cpim_app` | backend の接続 | 全テーブルの SELECT / INSERT / UPDATE / DELETE、シーケンスの利用。`audit_log` は SELECT / INSERT のみ、`schema_migrations` は SELECT のみ |

## 起動の流れ

```
db → migrate（cpim。終わると終了）→ backend（cpim_app）→ frontend
```

`migrate` サービスは backend と同じイメージで `server migrate` を実行する。マイグレーションを適用したあと、`cpim_app` を作成または更新し、権限を付け直す。

- 権限の付与は番号付きのマイグレーションにしていない。リストア（`--no-acl`）で権限が消えても `migrate` で戻り、新しいテーブルにも自動で権限が付く。
- backend は起動時にスキーマのバージョンとチェックサムを確認するだけで、DB を変更しない。古い DB や不一致の DB では起動しない。
- 本番（`APP_ENV=production`）で `DATABASE_URL` が superuser またはテーブルの所有者だと、backend は起動しない。開発環境では警告だけを出す。

## 設定

| 変数 | 渡す先 | 内容 |
|---|---|---|
| `DATABASE_URL` | backend | `cpim_app` の接続先 |
| `MIGRATION_DATABASE_URL` | migrate のみ | `cpim` の接続先 |
| `APP_DB_PASSWORD` | migrate のみ | `cpim_app` に設定するパスワード。`DATABASE_URL` と同じにする |

パスワードを変えるときは、`APP_DB_PASSWORD` と `DATABASE_URL` を両方変えて `migrate` と backend を起動し直す。

## 開発

```bash
cd backend
go run ./cmd/server migrate   # 既定は cpim:cpim@localhost と cpim_app:cpim_app
go run ./cmd/server
```

DB テストは `CPIM_TEST_DATABASE_URL` の管理者でテンプレートを作り、`cpim_app` で接続する。DDL が必要なテストの準備には `testdb.Admin` を使う。

## 残るリスク

- `cpim` のパスワードはサーバーの `.env` にある。サーバーに入れる人は管理権限を得られる。
- `cpim_app` は業務データの更新と、監査ログの追加はできる。既存の監査ログの変更・削除と、トリガの無効化はできない。
