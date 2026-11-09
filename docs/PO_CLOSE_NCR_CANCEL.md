# 発注の締めと NCR の取り消し（マイグレーション 0045）

## 発注（PO）の締め

`POST /api/purchase-orders/{id}/close`（本文 `{"reason":"..."}`、権限 `po.plan`＝planner / admin）。

- `OPEN` と `PARTIALLY_RECEIVED` の発注を締められる。`RECEIVED` は締められない。
- 理由は必須。実行者・日時・理由は `purchase_orders` に保存する。
- `CLOSED` は最終状態で、入荷も再オープンもできない（DB のトリガでも拒否する）。残数が必要なら新しい発注を作る。
- 残数は MRP・ATP・ペギングの入荷予定から外れる。作成済みの MRP の結果は変わらないので、再実行が必要。
- 短納で締めた発注は、全量入荷ではないため、仕入先の納期遵守率とリードタイムの集計に入らない。

## NCR の取り消し

`POST /api/supplier-quality/ncrs/{id}/cancel`（本文 `{"reason":"..."}`、権限 `quality.ncr.disposition`＝planner / admin）。

- `OPEN` と `IN_REWORK` の NCR を取り消せる。`CLOSED` と `CANCELLED` は不可。
- 理由は必須で、`supplier_ncrs.cancel_reason` と `supplier_ncr_history.notes` に保存する。
- **ロットの品質状態は変えない。** 取り消しで `OK` に戻すと、admin 専用の特採（`USE_AS_IS`）を planner が迂回できてしまう。使用可能に戻すには PASS の検査を記録する。
- 手直しの処置（disposition）は記録として残る。
- 取り消した NCR は、仕入先スコアカードの NCR 件数と重大件数に数えない。不合格の検査は事実として残る。

「購買担当」のロールは無いため、planner がこれらの操作を行う。
