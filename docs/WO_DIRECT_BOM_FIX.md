# WO Direct-BOM Execution Fix

## 目的

多段BOMでWO完成時に再帰展開していたため、親WOとサブアセンブリWOの両方から下位材料が出庫される可能性がありました。

例:

```text
FG
└─ SA x2
   └─ RAW x3
```

旧処理ではFG-WO完成時に `SA` と `RAW` を再帰的にISSUEし、さらにSA-WOでも `RAW` をISSUEするため、RAWの在庫・材料原価が二重消費され得ました。

## 修正内容

- `ReleaseWorkOrder` の予約対象を `BOM.Explode()` から `BOM.ComponentsOf()` に変更。
- `CompleteWorkOrder` の消費対象を `BOM.Explode()` から `BOM.ComponentsOf()` に変更。
- WO実行系専用の `ComponentRequirement` 型を追加し、MRP/参照用の再帰 `ExplodedRow` と型レベルで分離。
- `DirectBOMRequirements()` で直下BOM行だけを `parentQty * componentQty * (1 + scrapPct)` に変換。
- FG-WOはSAだけ、SA-WOはRAWだけを予約・消費する方式に変更。
- WO完成は後続の部分完成対応により、今回完成数量だけを出庫・入庫し `completed_qty` を累計更新。
- 回帰テストを追加:
  - 多段BOMで孫部品を親WOが消費しないこと。
  - scrap率が直下BOM行に一度だけ適用されること。

## 原価について

標準原価のCost Rollupは、多段BOM原価を算出するため再帰計算のままです。これは正しい挙動です。
今回変更したのは**実在庫のRESERVE / UNRESERVE / ISSUE**であり、親WOが孫材料まで実出庫することを止めることで、実行系の材料数量・材料原価の二重消費を防ぎます。

## 検証

- `workflow.go` 内に `BOM.Explode()` 呼び出しが残っていないことを静的確認。
- Release/Completeの双方が `BOM.ComponentsOf(ctx, wo.ItemID)` を使用することを確認。
- backend配下全Goファイルを `go/parser` で構文解析しPASS。
- `workflow_test.go` に多段BOM回帰テストを追加。

### go testについて

元ZIPに `backend/go.sum` が無いため、通常の `go test ./internal/service` は依存モジュールのgo.sum検証で停止します。今回の環境では外部依存を取得できないため、完全なGoテスト実行は未完です。


## 部分完成への拡張

本修正の後続として `docs/WO_PARTIAL_COMPLETION.md` の増分バックフラッシュを実装しています。直下BOM方式は部分完成の各バッチにも維持されます。
