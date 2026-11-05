# CPIM-MES v0.41.1 Step 9 — Version / Build Metadata 実装・検証レポート

- 基準: v0.41.1 Step 8 完成版
- 対象: Production Hardening Step 9
- Schema Migration: 0001〜0041 / 41本
- Migration 0042: 作成なし
- 目的: 実行中releaseのVersion / Git Commit / Build Time / Migration Latestを、build artifactとruntime endpointから一貫して確認可能にする。

## 1. 実装概要

### Backend buildinfo

`backend/internal/buildinfo`を追加し、以下をbuild-time injectable variableとして管理する。

```text
Version
Commit
BuildTime
```

Source実行時のdefaultは次のとおり。

```text
Version   = v0.41.1-dev
Commit    = unknown
BuildTime = unknown
```

Production backend imageではGo linker `-ldflags -X`を利用して次を注入する。

```text
RELEASE_VERSION -> buildinfo.Version
VCS_REF          -> buildinfo.Commit
BUILD_TIME       -> buildinfo.BuildTime
```

### Migration latest

`migrationLatest`は手書きのHTTP定数ではなく、startup Migration Managerが検証した`result.LatestVersion`を`buildinfo.Current()`へ渡す。

したがってHTTP serverが起動した時点で、`/version`のmigration latestとstartup migration gateが同じ値を共有する。

### Public endpoint

新規endpoint:

```http
GET /version
```

例:

```json
{
  "version": "v0.41.1",
  "commit": "0123456789abcdef0123456789abcdef01234567",
  "buildTime": "2026-11-05T12:34:56Z",
  "migrationLatest": 41
}
```

特性:

```text
Authentication: 不要
Cache-Control: no-store
Secrets: 含まない
```

`/version`は`/api`認証groupより外側に配置し、production Nginxも同一pathをBackendへreverse proxyする。

## 2. Container build metadata

Backend / FrontendのProduction imageへ共通build argsを追加した。

```text
RELEASE_VERSION
VCS_REF
BUILD_TIME
```

両imageへOCI labelを追加。

```text
org.opencontainers.image.version
org.opencontainers.image.revision
org.opencontainers.image.created
```

Frontend production buildでは同じ値を以下へcompileする。

```text
VITE_APP_VERSION
VITE_APP_COMMIT
VITE_APP_BUILD_TIME
```

Navigation drawerにもrelease versionを表示する。

## 3. Package version整合

以下を`0.41.1`へ更新した。

```text
frontend/package.json
frontend/package-lock.json
e2e/package.json
e2e/package-lock.json
```

依存package version自体は変更していない。

## 4. Production Compose

`docker-compose.prod.yml`でBackend / Frontendのbuild argsを同じ値から供給する。

```text
RELEASE_VERSION=${RELEASE_VERSION:-v0.41.1}
VCS_REF=${VCS_REF:-unknown}
BUILD_TIME=${BUILD_TIME:-unknown}
```

DB / Backendのhost-port非公開というStep 4のtopologyは維持している。

## 5. GitHub Actions

CI release buildでは各Docker build jobで以下を設定する。

```text
RELEASE_VERSION=v0.41.1
VCS_REF=$GITHUB_SHA
BUILD_TIME=$(date -u +'%Y-%m-%dT%H:%M:%SZ')
```

E2E startup wait gateにも`/version`確認を追加した。

Playwrightへ以下を渡す。

```text
E2E_EXPECTED_VERSION=v0.41.1
E2E_EXPECTED_COMMIT=<GitHub SHA>
```

Step 9 E2EはProduction Nginx経由で次を検証する。

```text
/version HTTP 200
Cache-Control no-store
version == v0.41.1
commit == GitHub SHA
buildTime != unknown
buildTime RFC3339 UTC
migrationLatest == 41
```

## 6. Static Guard

追加:

```text
backend/scripts/check_version_build_metadata.py
```

結果:

```text
Step 9 Version / Build Metadata Static Guard: 57/57 PASS
```

主なguard:

- buildinfo package存在
- version/commit/buildTime/migrationLatest JSON field
- Migration Manager resultからmigrationLatest取得
- public `/version`
- `Cache-Control: no-store`
- Backend linker injection
- Backend/Frontend OCI label
- Frontend Vite metadata injection
- Production Nginx `/version` proxy
- Compose build args
- package version 0.41.1
- CI Git SHA/UTC build time injection
- E2E version check
- Migration count 41
- latest 0041
- 0042 absent

## 7. 検証結果

全Backend Static Guard:

```text
33/33 scripts PASS
```

重要既存Gate:

```text
0039 Real-Time Dispatch / Rescheduling  106 PASS
0040 Production Control Tower           95/95 PASS
0041 Recovery Planning                  98/98 PASS
Step 8 FAILED Run Persistence           57/57 PASS
Step 9 Version / Build Metadata         57/57 PASS
```

追加検証:

```text
Runtime version policy        PASS
Dependency lock check         PASS
CI/dependency policy          PASS
JSON parse                    PASS
YAML parse                    PASS
Go parser                     PASS
Go fmt                        PASS
buildinfo isolated unit test  PASS
real -ldflags injection       PASS
Nginx syntax                  PASS
TypeScript syntax             PASS
changed-file whitespace       PASS
```

E2E test definition count:

```text
24
```

## 8. Migration invariants

```text
Migration count   41
Latest            0041_scenario_based_recovery_planning_what_if_simulation.sql
Migration 0042    absent
```

0041 SHA-256:

```text
325c558c86bd247439e7e7ea765c02b3e2e9bd718a82831aef89960f09ecdf18
```

Step 8と完全一致。

## 9. Step 8から不変の主要業務領域

```text
backend/migrations            UNCHANGED
backend/internal/domain       UNCHANGED
backend/internal/service      UNCHANGED
backend/internal/repository   UNCHANGED
scripts/test_recovery_failed_persistence.py UNCHANGED
```

したがってStep 9はbuild/runtime identity境界の変更であり、Planning / Execution / Recovery semanticsを変更していない。

## 10. この実行環境で未実行の最終Runtime Gate

現在の作業環境には、プロジェクト指定のGo 1.26.7 toolchain用module download環境とDocker daemonがないため、以下はGitHub Actionsまたは開発Macでの最終実行対象。

```text
go mod verify
go vet -mod=readonly ./...
go test -mod=readonly -race -count=1 ./...

frontend npm ci
frontend npm run typecheck
frontend npm run build

Docker production image build
Playwright 24/24
```

ただし、buildinfo package単体は利用可能なGo toolchain上でunit testし、実際の`-ldflags -X` injectionまで実行確認済み。

## 11. 結論

Step 9により、Productionで「何が動いているか」を次の4項目で機械的に識別できる。

```text
Release Version
Git Commit
Build Time
Migration Latest
```

このidentityはBackend binary、Frontend build、OCI image label、Production Nginx endpoint、CI E2Eで同じrelease metadataへ収束する。

次フェーズはStep 10 — Observability。
