# WO Release-time BOM Snapshot

## 目的

製造指図 (WO) は **Release した時点の製造仕様**で最後まで実行される必要があります。
0017以降、Release済みWOの材料予約・部分完成・材料ISSUEは、現在の `bom_components` ではなく、Release時に固定したBOM Snapshotを使用します。

これにより、Release後にBOMを直接編集した場合やECOを適用した場合でも、既存WOの材料構成・使用量・scrap率は変化しません。変更後BOMは将来ReleaseされるWOにだけ反映されます。

## データモデル

### `work_order_bom_snapshots`

WOごとに1件だけ存在するSnapshotヘッダです。

- `id`: Snapshot ID。現在のシステムではこのUUIDをRelease時の製造Revision参照として使用します。
- `work_order_id`: 対象WO。UNIQUE。
- `parent_item_id`: Release時の親品目。
- `captured_at`: Snapshot取得日時。
- `source`: `RELEASE` / legacy backfill種別。
- `notes`: 監査用注記。

### `work_order_bom_snapshot_lines`

Release時点の直下BOMを不変データとして保存します。

- 元 `bom_components.id`（削除後も参照値を保持するためFKにはしない）
- 子品目ID
- 子品目コード・名称・UoM
- `quantity_per`
- `scrap_pct`
- WO全数量に対する `required_qty`
- Release時の `standard_cost_snapshot`

完成実行で使用するのは **Snapshotの子品目ID / quantity_per / scrap_pct** です。

## Releaseの原子処理

`POST /api/work-orders/{id}/release`

```text
BEGIN
  ↓
WO SELECT ... FOR UPDATE
  ↓
PLANNED再確認
  ↓
現在の直下BOMを1回だけ読取
  ↓
BOM Snapshotヘッダ/明細作成
  ↓
Snapshot明細からWO全数量の所要量計算
  ↓
構成品items行をUUID順に FOR UPDATE
  ↓
Available再計算
  ↓
RESERVE
  ↓
PLANNED → RELEASED
  ↓
Routing → WO Operations
  ↓
COMMIT
```

在庫不足・ロック競合・Snapshot保存失敗・Routingコピー失敗のいずれかが起きればTransaction全体をRollbackするため、Snapshotだけ残ることはありません。

## 部分完成

`POST /api/work-orders/{id}/complete`

部分完成は `bom_components` を読みません。

```text
WO 100個
Release時 Snapshot:
  A x 2, scrap 10%

後日 ECO:
  A を削除
  B x 3 に変更

WOを20個部分完成
  ↓
既存WOはSnapshotを使用
  A = 20 × 2 × 1.10 = 44 ISSUE
  B = 0

ECO後に新規ReleaseしたWO
  ↓
新Snapshotを使用
  B = 20 × 3 = 60 ISSUE
```

したがって、工程途中の設計変更によって既存WOの材料が突然切り替わることはありません。

## 完成履歴とのリンク

`work_order_completions.bom_snapshot_id` に、その完成実績で使用したSnapshot IDを記録します。
同じ `completionId` の冪等再送でも、元のWO Snapshotと一致することを確認します。

## Snapshot参照API

```text
GET /api/work-orders/{id}/bom-snapshot
```

Snapshotヘッダと固定明細を返します。WO画面の **BOM固定** ボタンからも確認できます。

## 既存WOのMigration

既存DBでは `0017_wo_bom_snapshot.sql` を適用してください。

既存の `RELEASED / IN_PROGRESS / COMPLETED / CLOSED` WOは次の優先順位でBackfillします。

1. **元のRESERVE履歴が存在する場合**
   - Release時に予約された総数量から `quantity_per` を再構築。
   - 部分完成済みでもUNRESERVE後残高ではなく、元のRESERVE総量を使用します。
   - 元BOMの `quantity` と `scrap_pct` の内訳までは復元できないため、実効 `quantity_per` として保存し `scrap_pct=0` とします。
   - `source=LEGACY_RESERVATION_RECONSTRUCTED`。

2. **RESERVE履歴がない場合**
   - Migration実行時点の現在BOMをSnapshot化します。
   - `source=LEGACY_CURRENT_BOM_FALLBACK` と明示し、過去Release時BOMを完全に復元できたとは扱いません。

0017導入後に新規ReleaseされるWOは `source=RELEASE` となり、Release時点のBOMがTransaction内で正確に固定されます。

## 重要な境界

この変更は **BOM Snapshot** を対象としています。Routingについては既にRelease時に `wo_operations` へコピーされていますが、正式なRouting Revision管理・ECO Effective Date管理は別課題です。
