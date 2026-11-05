# CPIM-MES — CPIM準拠 生産管理システム

APICS (現ASCM) **CPIM (Certified in Planning and Inventory Management)** の知識体系に準拠した、Webベースの生産管理システム (Manufacturing Execution System) のリファレンス実装です。

## 技術スタック

| レイヤ | 技術 |
| --- | --- |
| フロントエンド | Vue 3 + Vuetify 3 + Vite + TypeScript + Pinia |
| バックエンド | Go 1.26.10 + chi router + sqlx |
| データベース | PostgreSQL 16.15 |
| 開発支援 | Docker Compose |

Production Hardeningのruntime baselineは **Go 1.26.10 / Node.js 24.20.0 LTS / npm 11.19.0 / PostgreSQL 16.15 / Alpine 3.24** です。詳細は [`docs/RUNTIME_SUPPORT.md`](./docs/RUNTIME_SUPPORT.md) を参照してください。

Step 12ではSecurity CI / Supply Chain gateを追加しています。`govulncheck`、`npm audit`、Trivy container scan、Syft SPDX SBOM、release bundle checksum/manifest検証をRelease Gateとして実行します。詳細は [`docs/SECURITY_CI_SUPPLY_CHAIN.md`](./docs/SECURITY_CI_SUPPLY_CHAIN.md) を参照してください。

## CPIM 機能モジュール対応

| # | モジュール | CPIM領域 | 実装場所 |
| --- | --- | --- | --- |
| 1 | 品目マスタ + CSV import/export | Inventory | `/items` |
| 2 | BOM (Bill of Materials) | MPC | `/bom` |
| 3 | 作業区マスタ (Work Centers + Overhead) | Capacity | `/work-centers` |
| 4 | ルーティング (Routings) | Capacity | `/routings` |
| 5 | 需要管理 (Demand Management) | SOP | `/demand` |
| 6 | 需要予測 (SMA / EXPO / Holt-Winters) + Version / Consumption | SOP | `/forecast` |
| 7 | MPS (Master Production Schedule) | MPC | `/mps` |
| 8 | MRP (Material Requirements Planning) | MPC | `/mrp` |
| 9 | CRP (Capacity Requirements Planning) | Capacity | `/crp` |
| 10| 原価積み上げ (Material+Labor+Overhead) | Costing | `/cost-rollup` |
| 11| ABC分析 (年間使用金額 Pareto) | Inventory | `/abc-analysis` |
| 12| サイクルカウント (棚卸計画) | Inventory | `/cycle-count` |
| 13| 在庫管理 (Inventory Control) | Inventory | `/inventory` |
| 14| ロット追跡 (Lot/Traceability) | Quality | `/lots` |
| 15| 製造指示 (Work Orders / Shop Floor) | Execution | `/work-orders` |
| 16| ガントチャート (WO Timeline) | Reporting | `/gantt` |
| 17| 購買管理 (Purchasing) | Procurement | `/purchase-orders` |
| 18| ダッシュボード (KPI + Charts) | Reporting | `/` |
| 19| 認証 + RBAC | Security | `/login` |
| 20| 監査ログ (Audit Trail) | Security/Compliance | `/audit-log` |
| 21| OpenAPI/Swagger UI | Developer Experience | `/api/docs` |
| 22| バーコード/QR スキャン | Shop Floor UX | (Inventory・Lots画面の📷ボタン) |
| 23| E2E テスト (Playwright) | Quality | `e2e/` ディレクトリ |
| 24| 業務一貫フロー (WO リリース/PO 入荷/WO 完成) | Execution | WO/PO 画面の "リリース"/"入荷"/"完成入力" ボタン |
| 25| 作業カレンダー (祝日/振替) + CRP連動 | Capacity | `/calendars` |
| 26| WIP / 部分完成 | Execution | WO画面の "完成入力"（物理完成）+ "進捗入力"（参考進捗） |
| 27| ATP (Available-to-Promise) | SOP | `/atp` |
| 28| 品質検査 (PASS/FAIL/HOLD) + ロット保留 | Quality | ロット詳細パネル |
| 29| MRP 低レベルコード (LLC) 処理 | Planning | 品目マスタの「LLC再計算」ボタン |
| 30| Holt-Winters 季節予測 | Forecasting | 需要予測ページの "HW" 選択 |
| 31| MRP アクションメッセージ | Planning | `/mrp-actions` |
| 32| Shop Floor Control (工程実績) | Execution | `/shop-floor` |
| 33| KPI ダッシュボード | Insights | `/` (Dashboard 上部) |
| 34| S&OP (月次需給計画) | Strategy | `/sop` |
| 35| RCCP (粗能力計画) | Capacity | `/rccp` |
| 36| Engineering Change (ECO/ECN) | Change Mgmt | `/eco` |
| 37| AI アシスタント (自然言語) | UX | `/agent` |
| 38| GitHub Actions CI/CD | DevOps | `.github/workflows/ci.yml` |
| 39| WO Release-time BOM Snapshot | Execution/Change Mgmt | WO画面の「BOM固定」/ `GET /api/work-orders/{id}/bom-snapshot` |
| 40| Unified Inventory / Lot Ledger | Inventory/Traceability | `/inventory`・`/lots` / migration `0018` |
| 41| Backend RBAC hardening | Security/Compliance | 全更新APIのpermission middleware / `docs/BACKEND_RBAC.md` |
| 42| BOM Cycle / Transactional LLC Guard | Planning/Data Integrity | migration `0019` / `docs/BOM_CYCLE_LLC_GUARD.md` |
| 43| Strict Shop Floor State Machine | Execution/Data Integrity | migration `0021` / `docs/SHOP_FLOOR_STATE_MACHINE.md` |
| 44| PO Partial Receipts + Remaining Supply | Procurement/Planning | migration `0022` / `docs/PO_PARTIAL_RECEIPTS.md` |
| 45| ECO Effective Date + Immutable Approval/Application Audit | Change Mgmt/Compliance | migration `0023` / `docs/ECO_EFFECTIVE_DATE_AUDIT.md` |
| 46| Automatic DB Migration Manager | Reliability/Operations | startup gate / `schema_migrations` / `docs/AUTOMATIC_DATABASE_MIGRATIONS.md` |
| 47| Transactional Quality Inspection + Immutable Lot Quality Audit | Quality/Data Integrity | migration `0024` / `docs/QUALITY_TRANSACTION_MANAGEMENT.md` |
| 48| Forecast Version + Forecast Consumption | Demand Management/Planning | migration `0025` / `docs/FORECAST_VERSION_CONSUMPTION.md` |
| 49| S&OP → MPS Product-Mix Disaggregation | S&OP/MPS | migration `0026` / `docs/SOP_MPS_DISAGGREGATION.md` |
| 50| Supplier Quality + NCR / Disposition / Scorecard | Quality/Procurement | `/supplier-quality` / migration `0027` / `docs/SUPPLIER_QUALITY_NCR.md` |
| 51| ABC Annual Dollar Usage | Inventory | `/abc-analysis` / migration `0028` / `docs/ABC_ANNUAL_DOLLAR_USAGE.md` |
| 52| Finite-capacity CRP | Capacity | `/crp` / migration `0029` / `docs/CRP_FINITE_CAPACITY_SCHEDULING.md` |
| 53| Detailed Scheduling (Alt WC / Transfer Batch / Seq Setup / Machine & Labor) | Detailed Scheduling | `/detailed-scheduling` / migration `0030` / `docs/DETAILED_SCHEDULING.md` |

## Supplier Quality / NCR

Supplier Qualification (`APPROVED / CONDITIONAL / BLOCKED`)、Incoming Inspection Required、Supplier由来FAIL検査からのNCR自動起票、返品・廃棄・REWORK・USE_AS_ISのDisposition、Supplier Quality Scorecardを実装しています。BLOCKED SupplierはPO作成/受入を拒否し、検査必須Supplierの受入Lotは自動HOLDになります。返品/廃棄はUnified Inventory/Lot Ledgerへ物理在庫移動として記録されます。詳細は [`docs/SUPPLIER_QUALITY_NCR.md`](./docs/SUPPLIER_QUALITY_NCR.md) を参照してください。

## BOM循環防止とLLC原子更新

BOMの追加・削除とECO Applyは、`BOM topology lock → BOM変更 → 全グラフ循環検査 → LLC再計算 → COMMIT` を同一DB Transactionで実行します。A→BとB→Aのような競合登録もPostgreSQL advisory lockで直列化され、循環またはLLC再計算失敗時はBOM変更自体がRollbackされます。DB側にもmigration `0019_bom_cycle_guard.sql` の遅延循環制約を追加しています。詳細は [`docs/BOM_CYCLE_LLC_GUARD.md`](./docs/BOM_CYCLE_LLC_GUARD.md) を参照してください。

Shop Floor工程は `PENDING → READY → IN_PROGRESS ↔ PAUSED → COMPLETED` の状態機械で制御します。前工程が完了するまで後工程は開始できず、START/STOP/COMPLETEと工程ログは同一Transactionです。担当者はJWTの現在ユーザーから取得し、実績時間もサーバー時刻で計測します。DB側でもmigration `0021_shop_floor_state_machine.sql` が工程順序と遷移を検証します。詳細は [`docs/SHOP_FLOOR_STATE_MACHINE.md`](./docs/SHOP_FLOOR_STATE_MACHINE.md) を参照してください。


## ECO有効日・承認/適用監査

ECOは必ず`DRAFT`で作成され、申請者・承認者・適用者はClient入力ではなくJWTの現在ユーザーから記録します。承認後はECOヘッダと構成行が固定され、`effective_date`より前のApplyはBackendとPostgreSQLの双方で拒否されます。`eco_status_history`はappend-onlyで、承認/適用/取消の実行者User ID・ユーザー名・時刻・有効日のスナップショットを保存します。詳細は [`docs/ECO_EFFECTIVE_DATE_AUDIT.md`](./docs/ECO_EFFECTIVE_DATE_AUDIT.md) を参照してください。

## Backend RBAC

更新権限はFrontendのボタン表示ではなく、Go Backendの `requirePermission(...)` で強制します。`viewer` は参照専用、`operator` は現場実績、`planner` は計画・WO/PO作成・BOM/ECO起票、`admin` はマスター・手動在庫調整・ECO承認/適用を含む全権限です。JWTのroleは各リクエストでDB上の現在roleへ再同期されるため、無効化・降格が次リクエストから反映されます。詳細は [`docs/BACKEND_RBAC.md`](./docs/BACKEND_RBAC.md) を参照してください。

## 業務一貫フロー (受注→購買→製造→完成)

> 📘 **新しい方は [`docs/tutorial.md`](./docs/tutorial.md) を参照してください。**
> 「BIKE-100 を 10台製造する」エンドツーエンドのシナリオを 20 分で体験できます。

CPIM の MPS→MRP→計画オーダ→実行までの流れを以下の3操作で完結します。

### MRP v2 の計算順序

MRP は `demand_forecasts` を直接所要量として使わず、**MPS (`mps_entries.planned`) を正式な独立需要入力**として処理します。

```text
MPS
  ↓
Netting (在庫 + Scheduled Receipts + Safety Stock)
  ↓
Planned Order Receipt
  ↓ Lead Time Offset
Planned Order Release
  ↓
Direct-child BOM Explosion (直下1階層のみ)
  ↓
次の LLC レベルで Netting
```

Scheduled Receipts には `OPEN` / `PARTIALLY_RECEIVED` の購買発注**未入荷残数量 (`quantity - received_qty`)**と、`RELEASED` / `IN_PROGRESS` の製造指図残数量を使用します。
多段BOMは再帰的に一括展開せず、親品目の **Planned Order Release 日** に直下部品の Gross Requirement を発生させるため、下位部品の二重計上を防ぎます。


1. **WO リリース** (`POST /api/work-orders/{id}/release`)
   **そのWO品目の直下BOMだけ**を同一Transaction内で読み取り、`work_order_bom_snapshots` / `work_order_bom_snapshot_lines` にRelease時BOMを固定します。
   そのSnapshotから直下構成品の必要数を計算し、**`RESERVE` トランザクションで予約**します。
   サブアセンブリの下位部品はサブアセンブリ自身のWOで予約するため、多段BOMの二重予約を防止します。
   Release後にBOM/ECOが変更されても、そのWOは固定Snapshotを使い続けます。
   在庫不足が1点でもあれば全処理をRollback。成功時に WO は `PLANNED` → `RELEASED` に遷移し、`released_at` とBOM Snapshot IDを確定します。

2. **PO 部分入荷** (`POST /api/purchase-orders/{id}/receive`)
   `receiptId` UUID と今回入荷数量を指定し、1つのDB Transactionで `purchase_receipts` 履歴 +
   `inventory_txns (RECEIPT)` + `lot_movements (RECEIPT)` を起票します。
   100発注に20入荷なら `received_qty=20 / remaining=80 / PARTIALLY_RECEIVED`、完納時だけ `RECEIVED` になります。
   同じ`receiptId`の再送は二重入庫せず、過入荷もPO行ロックとDB制約で拒否します。
   MRP/ATPのScheduled Receiptは未入荷残数量だけです。詳細は `docs/PO_PARTIAL_RECEIPTS.md` を参照してください。

3. **WO 部分完成** (`POST /api/work-orders/{id}/complete`)
   `quantity` は累計ではなく**今回完成数量**です。材料構成は現在の `bom_components` を再読込せず、**Release時のBOM Snapshot**だけを対象に、
   今回完成数量の分だけ `UNRESERVE` → `ISSUE` → FIFOロット `CONSUMED` を実行します。

   親完成品も今回数量だけ `RECEIPT` + `PRODUCED` し、`completed_qty` を累計更新します。
   残数量があれば `IN_PROGRESS`、全数量に到達した時だけ `COMPLETED` になります。
   例: WO=100、今回完成=20、BOM使用量=2なら、材料40だけをISSUEし、完成品20だけをRECEIPTします。

   `completionId` UUIDによる冪等制御、WO行/材料ロットの行ロック、`work_order_completions` 履歴を追加しています。
   同じcompletionIdを再送しても在庫は二重計上されません。詳細は `docs/WO_PARTIAL_COMPLETION.md` を参照してください。
   BOM Snapshotの詳細は `docs/WO_BOM_SNAPSHOT.md` を参照してください。

   例: `FG → SA → RAW` の場合、FG-WOではSAだけを消費し、RAWはSA-WOでのみ消費します。
   これにより多段BOMでの材料出庫・材料原価の二重消費も防ぎます。
   全てを **単一の DB トランザクション** で実行。ロールバック時は何も書き込まれません。

これにより `inventory_txns` が物理在庫の真実の源 (single source of truth) となり、
`v_stock_balance` ビューで `on_hand` / `reserved` / `available` を一元管理できます。

## 初回Admin bootstrap

固定demo user / 固定passwordは **v0.41.1 Step 3で廃止**しました。Development/Test/Productionのいずれにも既知credentialsは自動作成されません。

`users` テーブルが空の場合、BackendはHTTP listen前に起動を停止し、次の2項目を要求します。

```text
BOOTSTRAP_ADMIN_USERNAME=<initial administrator username>
BOOTSTRAP_ADMIN_PASSWORD_FILE=<path to a one-line secret file>
```

passwordを環境変数へ直接渡す `BOOTSTRAP_ADMIN_PASSWORD` は禁止です。secret fileは15〜72 bytesで、bootstrap後は削除してください。既存usersが1件以上あるDBではbootstrapは何も上書きしません。詳細は [`docs/BOOTSTRAP_ADMIN.md`](./docs/BOOTSTRAP_ADMIN.md) を参照してください。

既存v0.41.0/Step 2の開発DBに旧demo accountが残っている場合、Step 3は既存userを自動変更・削除しません。**そのDBをProductionへ昇格させず、Productionはclean DB + bootstrapまたは別途安全に移行した認証データを使用してください。** Migration 0003の歴史的コメントはchecksum互換性のため変更していませんが、実行コードから固定credentialsは除去済みです。

## クイックスタート

### 前提

- Docker Desktop (Compose v2)
- もしくは Node.js 24.20.0 / npm 11.19.0 / Go 1.26.10 / PostgreSQL 16.15 のローカル環境


### 依存関係ロックとCI

依存関係は `backend/go.sum`、`frontend/package-lock.json`、`e2e/package-lock.json` をコミットして固定します。 Dockerfileもこれらを必須とし、Goは `-mod=readonly`、Nodeは `npm ci` でビルドします。初回のみ `./scripts/generate-lockfiles.sh` を実行してください。更新時はリポジトリ直下で `./scripts/generate-lockfiles.sh` を実行し、`python3 scripts/check_dependency_locks.py` で検証してください。CI定義は `.github/workflows/ci.yml`、詳細は [`docs/DEPENDENCY_LOCKS_AND_CI.md`](./docs/DEPENDENCY_LOCKS_AND_CI.md) を参照してください。

### Production configuration fail-closed

本番Backendは `APP_ENV=production` を指定します。`DATABASE_URL` と `JWT_SECRET` を明示し、wildcard CORSを無効化し、secure cookie policyを満たさない限り起動しません。Step 3以降 `SEED_DEMO_USERS` は削除済み設定で、指定すると起動エラーになります。

```text
APP_ENV=production
DATABASE_URL=<production DSN>
JWT_SECRET=<high-entropy secret>
ALLOWED_ORIGINS=
COOKIE_SECURE=true
COOKIE_SAMESITE=Strict
```

clean DBの初回だけ、Admin usernameとpassword fileを追加指定します。`.env.production.example`、[`docs/PRODUCTION_CONFIGURATION.md`](./docs/PRODUCTION_CONFIGURATION.md)、[`docs/BOOTSTRAP_ADMIN.md`](./docs/BOOTSTRAP_ADMIN.md) を参照してください。Step 4以降、base `docker-compose.yml` はDB/Backendの共通内部定義で、Developmentは `docker-compose.dev.yml`、Productionは `docker-compose.prod.yml` を必ず重ねます。

### Production Frontend Container / Compose topology

Step 4ではProductionのVite dev serverを廃止し、`frontend/Dockerfile`を **Node build stage → Nginx static runtime** のmulti-stage imageへ変更しました。ProductionではNginxのみをhostへ公開し、`/api`を内部Backendへreverse proxyします。DBとBackendはhostへ公開しません。

Development:

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build
```

Production:

```bash
docker compose --env-file .env.production -f docker-compose.yml -f docker-compose.prod.yml up -d --build
```

clean DBの初回だけ上記へ `-f docker-compose.bootstrap.yml` を追加します。Productionのbrowser endpointは既定で `http://localhost:8080`（実運用ではTLS終端の背後）です。詳細は [`docs/PRODUCTION_FRONTEND_CONTAINER.md`](./docs/PRODUCTION_FRONTEND_CONTAINER.md) を参照してください。

### Dockerでclean DBを初回起動

固定credentialsはないため、初回だけsecret fileを作成してbootstrap overrideを使用します。

```bash
mkdir -p .secrets
openssl rand -base64 36 | tr -d '\n' > .secrets/bootstrap-admin-password
chmod 600 .secrets/bootstrap-admin-password
export BOOTSTRAP_ADMIN_USERNAME=initial-admin
docker compose -f docker-compose.yml -f docker-compose.dev.yml -f docker-compose.bootstrap.yml up --build
```

作成したpasswordを安全なpassword managerへ保存し、初回Adminでログインできることを確認してください。その後stackを停止（volumeは削除しない）し、`.secrets/bootstrap-admin-password` を削除して `docker-compose.bootstrap.yml` なしの通常構成へ戻します。既存usersがあるDBはbootstrap override不要です。

### 依存Lock / CI

再現可能Buildのため、`backend/go.sum`、`frontend/package-lock.json`、`e2e/package-lock.json`を正式なlockfileとして管理します。Frontend/E2Eの直接依存はexact version、Nodeは24.20.0（LTS）、npmは11.19.0に固定しています。Docker buildはGoを`-mod=readonly`、Frontendを`npm ci`で実行し、lockfile無しのunlocked fallbackはありません。

GitHub Actionsは `.github/workflows/ci.yml` でGo test/vet、Vue build/typecheck、既存業務整合性チェック、Docker build、Playwright E2Eを実行します。lockfile生成専用workflowは `.github/workflows/generate-lockfiles.yml` です。詳細は `docs/DEPENDENCY_LOCKS_AND_CI.md` を参照してください。

BackendはAPI起動前に、埋め込みSQL Migrationを`schema_migrations`で自動管理します。
新規DBは0001から最新版まで順次適用し、既存DBは旧スキーマを安全にbaselineして未適用分だけ実行します。
Migration失敗・checksum不一致・履歴欠落・DBがアプリより新しい場合はBackendを起動しません。
`/docker-entrypoint-initdb.d`へのMigration mountは不要です。詳細は [`docs/AUTOMATIC_DATABASE_MIGRATIONS.md`](./docs/AUTOMATIC_DATABASE_MIGRATIONS.md) を参照してください。

- フロントエンド: http://localhost:5173
- バックエンドAPI: http://localhost:8080
- PostgreSQL:     localhost:55433 (Development overrideのみ)

### バックエンドテスト

依存lock生成済み環境では、通常のテストで `go.mod` / `go.sum` を変更しません。

```bash
cd backend
go mod download
go mod verify
make test   # -mod=readonly でユニットテスト実行
make cover  # -mod=readonly でカバレッジ計測
```

依存を意図的に更新する場合のみ、リポジトリrootで `make lock` を実行してください。

主要ロジック (MRPロットサイジング、需要予測SMA/EXPO、ABC分類、POQ集約) には
table-driven test を整備済み。`go test ./internal/service/` で実行可能。

### E2E テスト (Playwright)

```bash
cd e2e
npm ci
npx playwright install chromium  # 初回のみ
npm test                         # ヘッドレス実行
npm run test:headed              # ブラウザ表示
```

ログイン→品目一覧→検索フィルタ→MRP実行→OpenAPI ドキュメントの主要シナリオを
カバー。詳細は `e2e/README.md` を参照。

### API ドキュメント

サーバ起動後 http://localhost:8080/api/docs で Swagger UI が開きます。
Authorize ボタンに `Bearer <token>` を貼れば、保護エンドポイントを試行できます。

### ローカル起動

lockfileがまだ無い生成アーカイブを初めて展開した場合は、インターネット接続環境で最初に一度だけ次を実行します。

```bash
npm install --global npm@11.19.0
make lock
make check-locks
```

以後の通常のDocker開発起動では依存定義を変更しません。

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build
```

FrontendをVite HMRで実行する開発用構成は `frontend/Dockerfile.dev` に分離されています。Productionでは `frontend/Dockerfile` のstatic build + Nginxのみを使用します。

## ディレクトリ構成

```
cpim-mes/
├── backend/                  Go API server
│   ├── cmd/server/           エントリポイント
│   ├── internal/
│   │   ├── api/              HTTPハンドラ
│   │   ├── domain/           ドメインモデル
│   │   ├── repository/       永続化層 (PostgreSQL)
│   │   ├── service/          ビジネスロジック (MRP計算等)
│   │   ├── config/           設定
│   │   └── migration/        起動時DB Migration管理
│   ├── migrations/           SQLマイグレーション
│   └── go.mod
├── frontend/                 Vue 3 + Vuetify SPA
│   ├── src/
│   │   ├── api/              APIクライアント
│   │   ├── views/            画面コンポーネント
│   │   ├── components/       共通コンポーネント
│   │   ├── stores/           Pinia ストア
│   │   ├── router/           Vue Router
│   │   └── types/            TypeScript 型定義
│   ├── package.json
│   └── vite.config.ts
├── docs/                     設計ドキュメント
├── docker-compose.yml
└── README.md
```

## ライセンス

MIT

### Atomic WO release (0016)
WO release now performs status validation, component locking, availability re-check, reservations, and the RELEASED transition in one transaction. Shared component rows are locked with `SELECT ... FOR UPDATE` in deterministic UUID order, preventing concurrent WO releases from over-reserving the same stock. See `docs/WO_ATOMIC_RELEASE.md`.

### Unified Inventory / Lot Ledger (0018)
Physical RECEIPT/ISSUE/ADJUST now uses one atomic ledger path: every `inventory_txns` header must be fully allocated to lot movements in the same database transaction. PostgreSQL deferred constraints reject commits whose item-level quantity and lot allocations differ. `v_stock_balance.on_hand` is lot-backed and `GET /api/inventory/reconciliation` exposes a zero-difference health check. See `docs/INVENTORY_LOT_UNIFIED_LEDGER.md`.

## 品質検査の原子処理と監査

品質検査はmigration `0024_quality_transaction_audit.sql` により、Lot行ロック → 検査記録 → Lot品質状態更新 → 不可変な品質状態履歴追加を1つのDB Transactionで実行します。検査者はClient入力ではなくJWTの現在ユーザーから取得し、検査証跡と品質状態履歴はappend-onlyです。`lots.quality_status`の直接変更も禁止されます。詳細は [`docs/QUALITY_TRANSACTION_MANAGEMENT.md`](./docs/QUALITY_TRANSACTION_MANAGEMENT.md) を参照してください。


## Forecast Version / Consumption

Forecast結果はVersionとして保存し、ACTIVE VersionにCustomer Orderをbucket単位で消し込んでMPSへ反映できます。詳細は `docs/FORECAST_VERSION_CONSUMPTION.md` を参照してください。

### Finite-capacity CRP

CRP now supports finite forward scheduling with firm WO load first, MRP planned orders second, work-center calendars, efficiency/utilization, multi-day operation splitting, late/unscheduled visibility, and immutable schedule snapshots. See `docs/CRP_FINITE_CAPACITY_SCHEDULING.md`.

### Detailed Scheduling

Detailed Scheduling extends finite CRP with alternative Work Centers, transfer-batch lot streaming, sequence-dependent setup, parallel machine lanes and Work Center labor head-count constraints. Released/in-progress WOs remain firm load; planned orders are assigned to the earliest feasible candidate while calendars and routing dependencies are preserved. Detailed schedule snapshots are immutable and Shop Floor execution supports transfer-batch overlap without allowing downstream good quantity to overtake its predecessor. See `docs/DETAILED_SCHEDULING.md`.


## Sales Order / Customer Order Management (0031)

Formal customer orders now own committed demand, inventory allocation, partial/full shipment and cancellation. Existing `demand_forecasts(source=ORDER)` rows are migrated to legacy Sales Orders and the legacy demand table becomes read-only. See `docs/SALES_ORDER_MANAGEMENT.md`.

## Advanced Order Promising / CTP (0032)

Sales Orders can now run side-effect-free ATP→CTP promise checks. ATP excludes the order's own demand, material CTP respects quality/supplier constraints, and capacity CTP reuses the Detailed Scheduling allocator. Promise acceptance revalidates a canonical result hash before writing promised dates, with immutable audit evidence. See `docs/ADVANCED_ORDER_PROMISING.md`.

## Backorder Processing / Product Allocation (0033)

Backorder Processing re-evaluates committed Sales Orders after supply/capacity changes using order priority, customer service class, Product Allocation limits, ATP and CTP. Preview is operationally side-effect free; Publish revalidates a canonical result hash and writes immutable publication evidence before changing promised dates. See `docs/BACKORDER_PROCESSING_PRODUCT_ALLOCATION.md`.

## Full Pegging / Exception Management (0034)

Sales Orders can now be traced through Promise/BOP decisions, usable inventory, Work Orders, frozen/live BOM requirements, Purchase Orders, supplier and quality constraints, and Detailed Scheduling / Work Center capacity evidence. Pegging snapshots and detected root causes are immutable; Planner/Admin exception actions are append-only. See `docs/FULL_PEGGING_EXCEPTION_MANAGEMENT.md`.

## v0.35.0 — Supplier Scheduling + Lead-Time Reliability (migration 0035)

Supplier commitments and ASN are now append-only scheduling evidence. Historical completed PO receipts generate supplier/item lead-time reliability (P50/P90, variability, on-time rate and conservative recommended lead time). MRP, CTP and Full Pegging share one canonical Purchase Order planning date with provenance: ASN → supplier confirmation → reliability adjustment → original PO due date. See `docs/SUPPLIER_SCHEDULING_LEAD_TIME_RELIABILITY.md`.

### 0036 Statistical Safety Stock / Inventory Policy
Versioned Service-Level policies calculate statistical Safety Stock, Reorder Point and Min/Max targets from ISSUE demand variability and 0035 supplier lead-time reliability. MRP, ATP/CTP and Full Pegging consume the same effective policy; policy breaches appear as planning exceptions. See `docs/STATISTICAL_INVENTORY_POLICY.md`.

### 0037 Maintenance + Capacity Downtime
Preventive Maintenance, Breakdown, Planned Downtime and Unplanned Downtime are modeled as append-only Work Center capacity events. Detailed Scheduling subtracts machine/worker capacity at exact time boundaries, CTP reuses the same allocator, and every persisted schedule freezes the maintenance revisions it used. Full Pegging traces those snapshots to maintenance-specific root-cause exceptions. See `docs/MAINTENANCE_CAPACITY_DOWNTIME.md`.

### 0038 OEE + Production Performance + Actual Capacity Feedback
Shop Floor START/STOP/COMPLETE/SCRAP evidence, operation standards and 0037 Maintenance history are converted into immutable Work Center performance snapshots: Availability, Performance, Quality, OEE, setup/speed losses, MTBF and MTTR. Planner-approved capacity feedback versions calibrate CRP, Detailed Scheduling and CTP without overwriting Work Center master data; Detailed Scheduling freezes the feedback version it used and Full Pegging can surface `OEE_CAPACITY_RISK` root causes. See `docs/OEE_PRODUCTION_PERFORMANCE_CAPACITY_FEEDBACK.md`.

### 0039 Real-Time Dispatching + Dynamic Rescheduling + Schedule Adherence
Detailed Scheduling now has an explicit Active Execution Schedule. Work Center dispatch lists combine current Shop Floor state, due-date/priority urgency and setup-family continuity; Schedule Adherence compares planned vs actual start/completion without counting untouched future operations as on-time observations. Shop Floor, Maintenance/Breakdown, OEE Capacity Feedback and Quality HOLD evidence queue auditable reschedule signals. Dynamic rescheduling creates a new immutable candidate, blocks Frozen-horizon commitment changes, audits Firm/Flexible changes, and atomically activates safe candidates. Full Pegging follows the active execution run and traces `RESCHEDULE_RUN` / `RESCHEDULED_BY` root-cause evidence. See `docs/REALTIME_DISPATCH_DYNAMIC_RESCHEDULING_SCHEDULE_ADHERENCE.md`.

## 0040 Production Control Tower

Production Control Tower / Constraint & Exception Prioritization adds an
operational intervention layer above Full Pegging and Planning Exceptions.

It ranks current Sales Order risks using severity, lateness, revenue at risk,
customer importance, constraint impact and aging, and preserves each scoring
result as immutable evidence.

Key capabilities:

- stable Control Tower cases derived from Planning Exceptions
- Revenue at Risk and Open Order Value
- P1/P2/P3/P4 priority scoring
- material / capacity / supplier / execution constraint scoring
- immutable canonical snapshots with refresh idempotency
- ranked recommended interventions
- ACKNOWLEDGE / ASSIGN / START / RESOLVE / REOPEN / CLOSE workflow
- planner/admin RBAC
- Production Control Tower dashboard and case history UI

Operations and implementation details:

- `docs/0040-production-control-tower.md`

## 0041 Scenario-Based Recovery Planning / What-if Simulation

Production Control Towerの制約・例外に対して、実運用データを直接変更せず
複数のRecovery ScenarioをWhat-if評価できるRecovery Planningを追加。

主な機能:

- EXPEDITE_PO / ALTERNATE_WORK_CENTER
- ADD_OVERTIME_CAPACITY / RESCHEDULE_WO / RELEASE_WO
- canonical baseline/request/result hash
- retry-safe successful-run uniqueness
- same-baseline Scenario Comparison
- P1 Reduction / Revenue Recovered / Impact Days Recovered
- Estimated Action Cost / Net Value / Recovery Score
- immutable Recovery Plan publication
- Vue/Vuetify UI
- RBAC / OpenAPI / Static Guard / E2E

SimulationおよびPublishはPO、WO、Work Center、Sales Order、
Inventory等の実運用状態を直接変更しない。

詳細:
docs/0041-scenario-based-recovery-planning.md


## v0.41.1 Step 5 security

Browser authentication now uses HttpOnly session cookies with signed CSRF protection; CLI/API Bearer tokens use `/api/auth/token`. See `docs/COOKIE_AUTH_CORS_CSRF.md`.


## v0.41.1 Step 6 security

Login authentication now has independent per-IP and per-username failed-attempt limits, `429 Retry-After`, and dedicated `LOGIN_SUCCESS / LOGIN_FAILURE / LOGIN_THROTTLED / LOGOUT` audit events using the existing `audit_log`. See `docs/LOGIN_RATE_LIMIT_AUTH_AUDIT.md`.

## v0.41.1 Step 7 User Administration

User lifecycle management is now backend-enforced through `admin.users.manage`. Admins can create users, change roles, enable/disable accounts, and reset passwords; every authenticated user can change their own password using the current password. Role/deactivation changes are effective on the next request, and transaction/advisory-lock protection prevents the last active administrator from being disabled or demoted. Password-bearing request bodies are excluded from audit payloads. See `docs/USER_ADMINISTRATION.md`.

## v0.41.1 Step 8 — 0041 FAILED Run Persistence

Recovery Planning simulation now commits the `RUNNING` run header before the pure What-if engine executes, persists successful evidence in a separate transaction, and terminalizes any post-`RUNNING` failure in an independent `FAILED + failure_reason + completed_at` transaction. Tx B re-locks the scenario and recomputes the action request hash so action/lifecycle drift cannot become stale success evidence. FAILED requests remain retryable because the existing partial unique index still covers only `SUCCEEDED` runs. See `docs/0041-scenario-based-recovery-planning.md`.

## v0.41.1 Step 9 — Version / Build Metadata

Step 9 adds a public `GET /version` endpoint with release version, Git commit, UTC build time, and the Migration Manager's validated latest migration. Production Docker builds inject version/commit/build-time into both backend and frontend images, Nginx proxies `/version`, and CI verifies the running production topology reports `v0.41.1`, the exact GitHub SHA, a real build timestamp, and `migrationLatest: 41`. See `docs/VERSION_BUILD_METADATA.md`.


## v0.41.1 Step 10 — Observability

Step 10 adds a private backend `GET /metrics` endpoint with Prometheus-compatible HTTP, DB pool, MRP, Detailed Scheduling, Pegging, Dynamic Rescheduling, Control Tower and Recovery Simulation metrics. Production Nginx explicitly returns 404 for public `/metrics`; Prometheus is expected to scrape `backend:8080/metrics` from the private application network. Optional OTLP/HTTP JSON server tracing is enabled only when `OTEL_EXPORTER_OTLP_ENDPOINT` is configured, uses W3C `traceparent`, a bounded asynchronous queue and non-blocking exporter failure handling. No new Go dependency or database migration is introduced. See `docs/OBSERVABILITY.md`.

## Backup / Restore / Disaster Recovery (v0.41.1 Step 11)

Production DB port remains private. Logical backups and restore drills operate through the Compose `db` service:

```bash
CPIM_ENV_FILE=.env.production ./scripts/backup_postgres.sh
CPIM_ENV_FILE=.env.production ./scripts/restore_drill.sh
```

Restore is intentionally fail-closed against in-place overwrite of `POSTGRES_DB`; restore into a separate candidate database/instance, verify, then perform a controlled cutover. Production PITR requires a separately designed and monitored physical base-backup + WAL archive destination. See [`docs/BACKUP_RESTORE.md`](./docs/BACKUP_RESTORE.md) and [`docs/DISASTER_RECOVERY.md`](./docs/DISASTER_RECOVERY.md).


## v0.41.1 Step 12 — Security CI / Supply Chain

Release-blocking security gates run in `.github/workflows/security.yml`: reachable Go vulnerability analysis with pinned `govulncheck`, npm audit policy, Trivy scans of deployable Backend/Frontend images, and Syft SPDX SBOM generation. The verified security evidence bundle contains manifests and SHA-256 checksums and is produced only after dependency and container gates pass. See [`docs/SECURITY_CI_SUPPLY_CHAIN.md`](./docs/SECURITY_CI_SUPPLY_CHAIN.md).

## v0.41.1 Step 13 — Final Clean-room Release Candidate Gate

The authoritative RC promotion workflow is `.github/workflows/release-candidate.yml`. A single ephemeral runner verifies source checksums, locked runtimes, Go race/vet/tests, all static guards, frontend production build, vulnerability/SBOM gates, an empty-volume `0001 -> 0041` migration, production network boundaries, `/version`, private observability, forced 0041 failure persistence, all **25 Playwright tests**, and the live backup/restore drill. It then builds and independently verifies a tamper-evident `v0.41.1-rc.N` bundle. See [`docs/FINAL_CLEAN_ROOM_RELEASE_GATE.md`](./docs/FINAL_CLEAN_ROOM_RELEASE_GATE.md) and [`docs/RELEASE_CHECKLIST.md`](./docs/RELEASE_CHECKLIST.md).
