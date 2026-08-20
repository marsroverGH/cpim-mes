# WO Partial Completion / Incremental Backflush

## 目的

WOの完成を一括処理ではなく、**今回完成した数量だけ**在庫計上する方式へ変更しました。

例: WO数量100、BOMが `FG -> COMPONENT x2` の場合:

| 完成処理 | 今回完成 | 累計完成 | 材料ISSUE | 完成品RECEIPT | WO状態 |
|---|---:|---:|---:|---:|---|
| 1回目 | 20 | 20 | 40 | 20 | IN_PROGRESS |
| 2回目 | 20 | 40 | 40 | 20 | IN_PROGRESS |
| 最終回 | 60 | 100 | 120 | 60 | COMPLETED |

材料所要量には **Release時に固定されたBOM Snapshot** の各直下行のscrap率を各回に適用します。Release後のBOM/ECO変更は既存WOへ影響しません。

## API

`POST /api/work-orders/{id}/complete`

```json
{
  "quantity": 20,
  "lotNo": "FG-LOT-001",
  "completionId": "c33a9f9c-6e33-4df8-9e1f-825e2cbbd26d"
}
```

- `quantity`: **今回完成数量**。累計値ではありません。
- `lotNo`: 任意。空欄なら部分完成ごとに一意な番号を自動採番します。
- `completionId`: クライアント生成UUID。同一IDの再送は冪等で、ISSUE/RECEIPTを二重起票しません。
- 旧クライアント互換のため `quantity` を省略すると残数量を全て完成します。

応答には `bomSnapshotId`, `bomSnapshotAt`, `completedNow`, `completedQty`, `remainingQty`, `status`, `idempotentHit` を返します。

## 在庫処理

各部分完成を単一DBトランザクションで処理します。

1. WO行を `SELECT ... FOR UPDATE` でロック。
2. `completionId` の既処理チェック。
3. 今回数量がWO残数量以下であることを検証。
4. Release時の **BOM Snapshot直下行のみ**を今回数量で所要量計算（現在の `bom_components` は参照しない）。
5. WOリリース時予約残高を確認。
6. 子部品ロットをFIFO順にロックして在庫確認。
7. 今回分だけ `UNRESERVE`。
8. 今回分だけ `ISSUE` + `lot_movements(CONSUMED)`。
9. 親品目を今回数量だけ `RECEIPT` + `lot_movements(PRODUCED)`。
10. `work_order_completions` に履歴登録。
11. `work_orders.completed_qty` を累計更新。
12. 残数量>0なら `IN_PROGRESS`、0なら `COMPLETED`。

## 完成ロット

空欄時は次の形式で各部分完成に一意なロットを作ります。

```text
WO-YYYYMMDD-WONo-<completionId先頭8文字>
```

同一WOで同じ `lotNo` を明示指定した場合は、同じ製造ロットへ追加入庫できます。
別WO由来の既存ロット番号は拒否します。

`work_orders.produced_lot_id` は互換性のため**最新の完成ロット**を指します。
全履歴は `work_order_completions` が正本です。

## 進捗入力との分離

旧版は `/progress` が `completed_qty` を直接変更していましたが、在庫移動が伴わないため不整合でした。
0015以降:

- `completed_qty`: 材料ISSUE・完成品RECEIPTまで完了した物理完成数量。
- `reported_progress_qty`: 在庫を動かさない参考進捗。

既存DBの `RELEASED` / `IN_PROGRESS` WOに旧進捗値がある場合、migration 0015で
`reported_progress_qty` へ退避し、`completed_qty` は0へ戻します。

## ステータス保護

在庫処理を迂回しないよう、汎用ステータス更新APIからの
`PLANNED -> RELEASED` / `IN_PROGRESS -> COMPLETED` 等を禁止しました。

- Release: `/work-orders/{id}/release`
- 物理完成: `/work-orders/{id}/complete`
- 手動状態変更: `COMPLETED -> CLOSED` のみ

## Migration

新規DBでは `0015_partial_wo_completion.sql` が初期化時に適用されます。
最新版ではBackend起動時のAutomatic Migration Managerが0015を含む未適用Migrationを自動適用します。
