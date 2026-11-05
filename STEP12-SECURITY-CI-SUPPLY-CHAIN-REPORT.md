# CPIM-MES v0.41.1 Step 12 — Security CI / Supply Chain 実装・検証レポート

- 対象: CPIM-MES v0.41.1 Production Hardening
- 基準: Step 11 Backup / Restore / Disaster Recovery 完成版
- 実装日: 2026-11-06
- Migration: 0001〜0041 / 41本
- Migration 0042: 作成なし

## 1. 目的

Step 12は、アプリケーションのPlanning/MES semanticsを変更せず、release前に以下をfail-closedで検証する。

```text
Source integrity
→ Go vulnerability analysis
→ npm vulnerability audit
→ Production image build
→ Container vulnerability scan
→ SPDX SBOM generation
→ Security evidence collection
→ Release bundle manifest / SHA-256 verification
```

## 2. Security tool pin

`scripts/security_tool_versions.env`へexact pinを追加した。

```text
GOVULNCHECK_VERSION=v1.9.0
TRIVY_VERSION=0.76.1
SYFT_VERSION=1.55.0
```

2026-11-06時点の公式公開情報を確認して採用した。

Trivy/Syftは単にrelease URLからbinaryを取得しない。

```text
upstream checksum file download
        ↓
repository-pinned checksum-file SHA-256と比較
        ↓
verified checksum fileからasset SHA-256取得
        ↓
release asset SHA-256比較
        ↓
install
```

とする。

Pinned checksum-file SHA-256:

```text
Trivy 0.76.1 checksums:
885debc41b570179e81af560e5f393a66be29b9ff8c52915674f3eb2304c535d

Syft 1.55.0 checksums:
aba5ac8ee870bb93fd3b2058471ffe7e57492ba673b5e0bcbfca92006c2f4dc7
```

`govulncheck`はexact Go module versionをGo checksum database経由でinstallし、applicationの`go.mod/go.sum`へ追加しない。

## 3. GitHub Actions supply-chain boundary

`.github/workflows/security.yml`を新設した。

Security workflowの権限は:

```yaml
permissions:
  contents: read
```

のみ。

GitHub公式Actionもmajor aliasではなくexact release tagへ固定した。

```text
actions/checkout@v7.0.1
actions/setup-go@v7.0.0
actions/setup-node@v7.1.0
actions/upload-artifact@v7.0.2
actions/download-artifact@v8.0.2
```

checkoutでは:

```text
persist-credentials: false
```

setup-go/setup-nodeではSecurity workflow用dependency cacheを無効化した。

## 4. Source integrity gate

`scripts/verify_source_checksums.py`を追加した。

Security CIが外部Security toolをdownloadする前に、clean checkoutを`SHA256SUMS.txt`と照合する。

拒否対象:

```text
checksum mismatch
missing file
stale checksum entry
path traversal
source bundle symlink
malformed SHA-256 entry
```

`SHA256SUMS.txt`自身はself-referenceを避けるため対象外。

## 5. govulncheck

Security CIでは:

```bash
cd backend
govulncheck -json ./...
```

を実行する。

Policy:

> Reachable known Go vulnerabilityが1件でも検出された場合、releaseを失敗させる。

`govulncheck -json`はmachine-readable streamであり、JSON modeでは脆弱性検出の有無をprocess exit statusだけに依存して判定しない。そこで`evaluate_govulncheck.py`を追加し、`finding.trace[0].function`が存在するsymbol-level findingだけをreachableとして明示判定する。module/package-level findingはevidenceとして残すが、このpolicyではrelease blockingにしない。JSON破損、scanner/network failureもfail-closedでreleaseを停止する。

report:

```text
govulncheck.json
govulncheck.json.stderr
govulncheck.json.exitcode
govulncheck-gate.json
govulncheck-gate.json.stdout
govulncheck-gate.json.stderr
govulncheck-gate.json.exitcode
```

を保存する。

## 6. npm audit

### Production frontend dependency

```bash
npm audit --omit=dev --audit-level=high --json
```

Release blocking:

```text
HIGH
CRITICAL
```

### Frontend complete dependency tree

```bash
npm audit --audit-level=critical --json
```

### E2E/build-test dependency tree

```bash
npm audit --audit-level=critical --json
```

build/test-only packageはruntime imageへ入らないが、CIでコード実行されるためCRITICALはrelease-blockingとした。

repositoryによる自動audit suppressionは追加していない。

## 7. Container vulnerability scan

実際にdeployするBackend / Frontend imageをbuild後、Trivyでscanする。

```text
severity = HIGH,CRITICAL
scanner  = vuln
exit-code = 1
ignore-unfixed = true
```

Step 12では「修正版が存在するactionable HIGH/CRITICAL」をrelease blockingとする。

`.trivyignore`は作成しない。

unfixed vulnerabilityまでrelease blockingに変更する場合は、組織としてexception/waiver workflowを先に定義してから`--ignore-unfixed`を除去する。

## 8. SBOM

SyftからSPDX JSONを3種類生成する。

```text
SBOM.spdx.json
SBOM-backend-image.spdx.json
SBOM-frontend-image.spdx.json
```

source SBOMとdeployable image SBOMを分離したため、application manifestだけでなくruntime OS packageも追跡できる。

## 9. Verified Release Evidence Bundle

dependency gateとcontainer gateの両方が成功した場合のみrelease bundleを生成する。

主要収録物:

```text
RELEASE-METADATA.txt
SHA256SUMS.txt
SECURITY-METADATA.json
security_tool_versions.env

govulncheck.json
govulncheck-gate.json
govulncheck-gate.json.exitcode
npm-audit-*.json
trivy-backend.json
trivy-frontend.json

SBOM.spdx.json
SBOM-backend-image.spdx.json
SBOM-frontend-image.spdx.json

RELEASE-BUNDLE-MANIFEST.json
RELEASE-BUNDLE-SHA256SUMS.txt
```

`SECURITY-METADATA.json`は少なくとも:

```text
releaseVersion  = v0.41.1
gitCommit       = exact Git SHA
buildTime       = UTC RFC3339
migrationLatest = 41
migrationCount  = 41
migration0042Created = false
```

を持つ。

## 10. Release bundle verification

`scripts/verify_release_bundle.py`を追加した。

以下をfail-closedで検証する。

```text
bundle checksum exact-set
bundle checksum mismatch
manifest exact-set
manifest file size
manifest SHA-256
unsafe path
symlink / nested directory
release version
Git commit format
Migration 41 / 0041
Migration 0042 absent
SPDX document validity
Trivy blocking finding absent
npm blocking count zero
govulncheck command exit zero
govulncheck reachable-finding gate exit zero
govulncheck JSON stream parse
raw JSON streamにもreachable symbol-level findingがないこと
```

## 11. Tamper detection unit test

`scripts/test_security_supply_chain_unit.py`ではsynthetic security evidenceからbundleを作成し、正常verify後にSBOMを1 byte変更する。

期待結果:

```text
normal bundle  → PASS
1-byte tamper  → checksum mismatch → FAIL
```

実測:

```text
Security supply-chain unit tests: PASS (govulncheck reachability + bundle verify + tamper detection + tool pins)
```

## 12. Static Guard

追加:

```text
backend/scripts/check_security_supply_chain.py
```

検証対象:

```text
Security workflow存在
read-only permission
exact tool pins
checksum-file pin
govulncheck exact install
no @latest
npm audit thresholds
Trivy severity policy
no .trivyignore
SPDX SBOM outputs
release bundle builder/verifier
exact GitHub Action release tags
checkout credentials disabled
Security cache disabled
Migration count/latest
Migration 0042 absent
```

実測:

```text
Step 12 Security CI / Supply Chain Guard: 93/93 PASS
```

## 13. 既存回帰

全`backend/scripts/check_*.py`を再実行しPASSした。

重要Gate:

```text
0039 Real-Time Dispatch / Dynamic Rescheduling   PASS
0040 Production Control Tower                   95/95 PASS
0041 Recovery Planning                          98/98 PASS
Step 8 FAILED Run Persistence                   57/57 PASS
Step 9 Version / Build Metadata                 57/57 PASS
Step 10 Observability                           96/96 PASS
Step 11 Backup / Restore / DR                   72/72 PASS
Step 12 Security CI / Supply Chain              93/93 PASS
```

Runtime / lock / CI policy:

```text
Runtime version policy                          PASS
Dependency lock check                           PASS
CI/dependency policy                            PASS
Security supply-chain unit tests                PASS
Source SHA-256 verification                     417/417 PASS
GitHub Actions YAML parse                       PASS
```

## 14. Baseline不変性

Step 11 baselineとのbyte comparisonで以下は完全不変。

```text
backend/migrations
backend/internal/domain
backend/internal/service
backend/internal/repository
frontend/src
e2e/tests
```

さらにapplication dependency filesも変更しない。

```text
backend/go.mod
backend/go.sum
frontend/package.json
frontend/package-lock.json
e2e/package.json
e2e/package-lock.json
backend/Dockerfile
frontend/Dockerfile
```

## 15. Migration

```text
count  = 41
latest = 0041_scenario_based_recovery_planning_what_if_simulation.sql
0042   = absent
```

0041 SHA-256:

```text
325c558c86bd247439e7e7ea765c02b3e2e9bd718a82831aef89960f09ecdf18
```

Step 11から不変。

## 16. この実行環境で未実行のlive security scan

このChatGPT実行環境では外部package/security registryへのDNS/network accessが利用できなかった。

実際に`npm audit`を試行した結果:

```text
getaddrinfo EAI_AGAIN registry.npmjs.org
```

となったため、以下のnetwork-dependent gateはここでは実行していない。

```text
govulncheck vulnerability DB query
npm audit registry query
Trivy DB download/image scan
Syft release binary download
Docker runtime image scan
```

これらは`.github/workflows/security.yml`上でGitHub-hosted runnerが実行する正式Gateである。

重要: network failureもsecurity commandのnon-zero exitになるため、CIではfail-openせずreleaseを停止する。

## 17. Step 12結論

Step 12によりv0.41.1 release flowは:

```text
Reproducible dependency locks
        ↓
Source SHA-256 integrity
        ↓
govulncheck
        ↓
npm audit
        ↓
Build deployable images
        ↓
Trivy container scan
        ↓
Syft SPDX SBOM
        ↓
Security evidence
        ↓
Release manifest + SHA-256
        ↓
Independent bundle verification
```

となった。

Planning/MES semantics、0041 Recovery Planning、Migration schemaは変更していない。
