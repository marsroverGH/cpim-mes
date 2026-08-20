# MRP v2 変更内容

## 目的

MRP エンジンを次の time-phased planning sequence に置換しました。

```text
MPS
  -> Netting
  -> Planned Order Receipt
  -> Lead-time Offset
  -> Planned Order Release
  -> Direct-child BOM Explosion
  -> Next LLC Level
```

## 主な変更

- `demand_forecasts` を MRP の直接入力から外し、`mps_entries.planned` を正式な独立需要入力に変更。
- MRP 実行前に LLC を再計算し、循環BOM/不正LLCでは計画を中断。
- `OPEN` Purchase Order を Scheduled Receipt としてNetting。
- `RELEASED` / `IN_PROGRESS` Work Order の残数量を Scheduled Receipt としてNetting。
- 既存の released/in-progress WO は、その残数量について開始日に直下BOM需要も生成。
- Net Requirement は Safety Stock を期末必要在庫として含めて計算。
- Planned Order Receipt を明示的にAPI結果へ追加。
- Planned Order Release Date を品目Lead Timeから算出。
- MRP内部では再帰 `BOM.Explode()` を使用せず、`ComponentsOf()` による直下1階層展開へ変更。
- 下位部品需要は親の Planned Order Release 日に発生。過去日になる場合はMRP開始日のpast-due bucketへ集約。
- LFLを真のLot-for-Lotへ修正し、固定ロット倍数への切り上げを廃止。
- POQは集約済みNet Requirementをそのまま計画入荷数量に使用。
- EOQ計算不能時はLFLへフォールバック。
- MRP画面に Scheduled Receipts / Planned Order Receipt / Planned Order Release Date を追加。
- Dashboardの計画オーダ推移はReceipt日ではなくRelease日を使用。
- MRP Action MessagesはScheduled Receiptの二重Nettingを防止し、MRP v2で残った純不足分からReleaseアクションを生成。

## テスト

以下のMRP純粋ロジックを依存パッケージなしで分離テストし、すべてPASSしています。

- Scheduled Receiptを含むNetting
- Safety Stock維持
- Lead-time Offset
- Direct-child requirement + scrap
- Past-due dependent requirement bucket
- LFL / FOQ / POQ / EOQ lot sizing

また、backend配下の全Goファイルは `go/parser` による構文解析をPASSしています。

## ビルド確認上の注意

元ZIPには `backend/go.sum` がありません。解析環境では外部DNSへのアクセスが遮断されているため `go mod tidy` が依存取得で停止し、完全な `go test ./...` は実行できませんでした。
Frontendも元ZIPに `node_modules` / lock file が無いため完全ビルドは未実行ですが、変更したTypeScript部分はTypeScript parserで構文確認済みです。
