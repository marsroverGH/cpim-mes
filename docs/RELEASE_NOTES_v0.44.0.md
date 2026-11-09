# v0.44.0 リリースノート

- 種別: マイナーリリース（マイグレーション 0044・0045 を追加）
- ベースライン: v0.43.0（`8e19d3d`）
- マイグレーション: 0001–0045
- Playwright 定義数: 26

v0.43.0 以前の変更は `docs/RELEASE_NOTES_v0.43.0.md` を参照。

## 変更

### 発注の締めと NCR の取り消し（PR #28、マイグレーション 0045）
planner と admin が、`OPEN` / `PARTIALLY_RECEIVED` の発注を締め、`OPEN` / `IN_REWORK` の NCR を取り消せる。どちらも理由が必須で、元に戻せない。
詳細は `docs/PO_CLOSE_NCR_CANCEL.md`。

### 起動時の業務タイムゾーンの確認（PR #27）
`BUSINESS_TIMEZONE` と DB の `app.business_timezone` が違うと、backend は理由を出して起動しない。

### audit_log を追記専用に（PR #26、マイグレーション 0044）
`audit_log` への UPDATE・DELETE・TRUNCATE を DB のトリガで拒否する。記録の失敗をログに出し、パニックした要求も記録する。詳細は `docs/AUDIT_LOG.md`。

### その他
- docker-compose が `BUSINESS_TIMEZONE` を backend に渡す（PR #24）
- マイグレーション 0043 のリハーサル用スクリプトと手順（PR #23）
- OpenAPI に全ルートを記載、`make ci-local` が静的ガードを全部実行（PR #25）

## 運用上の注意（挙動の変化）

- **起動の条件が増えた**: `BUSINESS_TIMEZONE` と DB の設定は同じ名前に揃える。別名（`Japan` と `Asia/Tokyo`）も不一致として扱う。
- **発注を締めると残数は入荷予定から外れる**。作成済みの MRP の結果は変わらないので、再実行する。
- **NCR を取り消してもロットの品質状態は変わらない**。使用可能に戻すには PASS の検査を記録する。
- **短納で締めた発注は**、仕入先の納期遵守率とリードタイムの集計に入らない。
- **`audit_log` の行は消せない**。保持期間は未定。

## アップグレード

起動時の自動マイグレーションで 0044・0045 が適用される（v0.43.0 からは数秒以内の見込み）。v0.42.x から上げる場合は、先に `docs/MIGRATION_0043_REHEARSAL.md` のリハーサルを行う。
down マイグレーションは無いので、戻すときはバックアップから復元する。

## 既知の課題

- アプリの DB ロールが superuser のままで、不変トリガは DB 管理権限があれば外せる（`docs/AUDIT_LOG.md`）。
- 需要予測・統計・スケジューリングの計算は float64 のまま。

## リリース手順

`docs/RELEASE_CHECKLIST.md` に従う。最終判定は `.github/workflows/release-candidate.yml`（`v0.44.0-rc.1`、`scripts/final_clean_room_release_gate.sh`）。
