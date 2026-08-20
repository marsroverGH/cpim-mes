# CPIM-MES E2E Tests (Playwright)

エンドツーエンドのブラウザテスト。ログインから品目一覧表示、MRP実行までのシナリオを自動化します。

## 前提

- バックエンドとフロントエンドが起動中であること (`docker compose up`)
- 初期ユーザーがシード済 (`admin / admin123`)

## セットアップ (初回のみ)

```bash
cd e2e
npm install
npx playwright install chromium
```

## 実行

```bash
# ヘッドレス実行
npm test

# ブラウザ表示で実行
npm run test:headed

# UI モード (対話的にテスト選択)
npm run test:ui

# 結果レポート閲覧
npm run report
```

## カバーしているシナリオ

| ファイル | シナリオ |
| --- | --- |
| `tests/login.spec.ts`       | 未認証→ログイン画面へリダイレクト / 正規ログイン / 不正ログイン拒否 |
| `tests/items-mrp.spec.ts`   | 品目一覧表示 / 検索フィルタ / MRP実行 / OpenAPI ドキュメントページ |

## 環境変数

- `E2E_BASE_URL` — フロントエンドの URL (デフォルト `http://localhost:5173`)
- `CI=1` — リトライ回数を増やす (CI 用)
