# Security CI / Supply Chain — v0.44.0 Step 12

## Purpose

Step 12 adds release-blocking vulnerability and software-supply-chain gates without changing CPIM-MES planning semantics, database schema, or Migration 0041.

The security workflow is `.github/workflows/security.yml` and is intentionally separate from the normal build/test workflow so security evidence is easy to review and retain.

## Pinned security tools

Exact tool versions are stored in `scripts/security_tool_versions.env`:

```text
GOVULNCHECK_VERSION=v1.9.0
TRIVY_VERSION=0.76.1
SYFT_VERSION=1.55.0
```

`latest` is forbidden.

Security workflowのGitHub公式Actionsもexact release tagへ固定します。checkoutはcredential persistenceを無効化し、setup-node/setup-goのdependency cacheもSecurity workflowでは使用しません。

```text
actions/checkout@v7.0.1
actions/setup-go@v7.0.0
actions/setup-node@v7.1.0
actions/upload-artifact@v7.0.2
actions/download-artifact@v8.0.2
```

Trivy and Syft are downloaded from their upstream GitHub release assets. Before the archive is trusted, CPIM-MES verifies the SHA-256 of the upstream checksum file against a repository-pinned SHA-256. Only then is the archive digest read from that verified checksum file and compared with the downloaded archive.

`govulncheck` is installed at an exact Go module version outside the application module and uses the Go checksum database. It does not edit `backend/go.mod` or `backend/go.sum`.

## Repository source checksum gate

`SHA256SUMS.txt` is not committed; git commit hashes protect the repository. It is generated at release time by `scripts/generate_source_checksums.py` from the git-tracked files and shipped inside the release / release-candidate bundles, so recipients of a source archive can verify it offline with `scripts/verify_source_checksums.py`.

Before downloading security tools, Security CI generates it with `--require-clean` (fails if any tracked file differs from HEAD) and then verifies it (fails on unexpected untracked files, checksum mismatch, path traversal, or symlinks). Files ignored by git are skipped in a checkout. `SHA256SUMS.txt` itself is excluded to avoid self-reference.

## Security gates

### 1. Go vulnerability analysis

```bash
cd backend
govulncheck -json ./...
```

Policy: any reachable known vulnerability reported by `govulncheck` blocks release.

Important: `govulncheck -json` is machine-readable streaming JSON and its JSON mode does not provide the vulnerability/no-vulnerability decision through process exit status alone. CPIM-MES therefore runs `scripts/evaluate_govulncheck.py` after the scan and classifies only symbol-level findings (the first trace frame has `function`) as reachable/release-blocking. Module-only and package-only findings remain evidence but do not block under this reachability policy. Malformed JSON or scanner/network failure also blocks release.

### 2. npm audit

Production frontend runtime dependencies:

```bash
npm audit --omit=dev --audit-level=high --json
```

HIGH or CRITICAL blocks release.

The complete frontend dependency tree and E2E/build-test tree are also checked. These dependencies do not ship in the runtime image, but CRITICAL findings still block release because they execute in CI/build contexts.

No repository-level automatic audit suppression is configured.

### 3. Container vulnerability scan

The exact deployable Backend and Frontend images are built with release version, Git commit, and build time metadata, then scanned with pinned Trivy.

Policy:

```text
Severity: HIGH, CRITICAL
Actionable/fixed vulnerabilities: release-blocking
Unfixed vulnerabilities: reported by policy context but do not block this Step 12 gate
```

The blocking command uses `--ignore-unfixed` so the release gate remains actionable. A repository `.trivyignore` file is intentionally not used.

If the organization requires "all HIGH/CRITICAL including unfixed" to block production, remove `--ignore-unfixed` after defining an emergency exception/waiver process. Do not silently add CVE IDs to an ignore file.

## SBOM

Pinned Syft creates SPDX JSON for:

```text
SBOM.spdx.json
SBOM-backend-image.spdx.json
SBOM-frontend-image.spdx.json
```

The source SBOM records repository/package manifests, while the image SBOMs also represent runtime OS packages and packaged application dependencies visible in the deployable images.

## Verified release evidence bundle

A release bundle is created only when dependency and container security jobs both pass.

The bundle contains:

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

`SECURITY-METADATA.json` binds the evidence to:

```text
releaseVersion = v0.44.0
gitCommit      = exact 40-hex commit
buildTime      = UTC RFC3339
migrationLatest = 46
migration0046Created = true
```

`verify_release_bundle.py` rejects:

- missing or extra checksum entries
- checksum mismatch
- symlink/nested-directory payloads
- unsafe manifest paths
- manifest size/hash mismatch
- invalid or empty SPDX documents
- non-zero vulnerability-gate exit codes
- blocking Trivy findings
- blocking npm audit counts
- invalid/empty govulncheck JSON stream
- reachable symbol-level govulncheck finding
- disagreement between raw govulncheck stream and gate summary
- version/migration metadata drift

## CI artifact policy

Security reports are retained for diagnosis even if a gate fails. The verified release bundle is not created unless all preceding security gates succeed.

Security reports and SBOMs contain dependency/image metadata and should be treated as internal release evidence. Database backup data is not part of this bundle.

## Local commands

Linux with the supported Go/Node/npm runtime:

```bash
./scripts/install_security_tools.sh all
./scripts/run_dependency_security_gate.sh
```

After building images:

```bash
BACKEND_IMAGE=cpim-mes-backend:security-scan \
FRONTEND_IMAGE=cpim-mes-frontend:security-scan \
./scripts/run_container_security_gate.sh
```

Static/unit checks that do not require Docker or vulnerability-database network access:

```bash
python3 backend/scripts/check_security_supply_chain.py
python3 scripts/test_security_supply_chain_unit.py
```

## Non-goals

Step 12 does not:

- add Migration 0042
- alter MRP, Detailed Scheduling, Pegging, Control Tower, or Recovery Planning semantics
- add application runtime dependencies
- sign production images or publish them to a registry
- define a company CVE exception/waiver process
- replace registry-native provenance/attestation when a production registry is selected

Image signing/provenance attestation can be added in a later controlled release after the production registry and keyless/signing policy are selected.
