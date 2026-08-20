# 画面遷移時にメイン画面が更新されない問題の修正

## 症状

左側メニューをクリックしたとき、URL と青い AppBar のタイトルは更新されるが、`v-main` 内のメイン画面だけが前の画面のまま残ることがある。
ブラウザの再描画やリロードでは一時的に直るが、何度か画面遷移すると再発する。

## 原因候補

Vue Router の遷移状態は更新されている一方で、Vuetify のレイアウト配下にある `router-view` の実画面コンポーネントが再マウントされず、ブラウザの描画・レイアウト計算も古い状態を保持するケースがある。

## 修正内容

1. `App.vue` の `router-view` を scoped slot 形式に変更。
2. `router-view` 自体ではなく、実際に描画される画面コンポーネントに `route.fullPath` と再描画シーケンスを含む `key` を付与。
3. ルート変更後に `nextTick` + `requestAnimationFrame` で `v-main` のレイアウト計算と `resize` イベントを発火。
4. `router.afterEach` でも `resize` を発火し、Vuetify のレイアウト更新を補助。
5. `v-container` にも key を付け、メイン領域の描画不整合を避ける。

## 修正ファイル

- `frontend/src/App.vue`
- `frontend/src/router/index.ts`

## 起動時の確認手順

```bash
cd frontend
rm -rf node_modules/.vite
npm run dev -- --force
```

Docker Compose の場合は以下を実行する。

```bash
docker compose down
docker compose up --build
```

## 確認ポイント

- `/mps` から `/mrp-actions` へ移動したとき、青いタイトルだけでなくメイン見出しも `MRP アクションメッセージ` に変わる。
- 左メニューを連続クリックしても、前画面の `MPS — 基準生産計画` が残らない。
- ブラウザの手動 Redraw やリロードをしなくても画面が追従する。
