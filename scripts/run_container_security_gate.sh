#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${SECURITY_ARTIFACT_DIR:-$ROOT/security-artifacts}"
BIN_DIR="${SECURITY_TOOL_BIN_DIR:-$ROOT/.tools/bin}"
BACKEND_IMAGE="${BACKEND_IMAGE:-cpim-mes-backend:security-scan}"
FRONTEND_IMAGE="${FRONTEND_IMAGE:-cpim-mes-frontend:security-scan}"
mkdir -p "$OUT"

for tool in trivy syft; do
  if [[ ! -x "$BIN_DIR/$tool" ]]; then
    echo "ERROR: $tool not installed at $BIN_DIR/$tool" >&2
    exit 1
  fi
done

# SBOMs include source dependency manifests plus the two deployable runtime images.
"$BIN_DIR/syft" "dir:$ROOT" -o "spdx-json=$OUT/SBOM.spdx.json"
"$BIN_DIR/syft" "$BACKEND_IMAGE" -o "spdx-json=$OUT/SBOM-backend-image.spdx.json"
"$BIN_DIR/syft" "$FRONTEND_IMAGE" -o "spdx-json=$OUT/SBOM-frontend-image.spdx.json"

status=0
scan_image() {
  local image="$1" output="$2" label="$3"
  echo "==> Trivy: $label"
  set +e
  "$BIN_DIR/trivy" image \
    --quiet \
    --scanners vuln \
    --severity HIGH,CRITICAL \
    --ignore-unfixed \
    --exit-code 1 \
    --format json \
    --output "$output" \
    "$image"
  local rc=$?
  set -e
  printf '%s\n' "$rc" >"${output}.exitcode"
  if [[ $rc -ne 0 ]]; then
    echo "FAIL: fixable HIGH/CRITICAL vulnerabilities found in $label" >&2
    status=1
  else
    echo "PASS: $label"
  fi
}

scan_image "$BACKEND_IMAGE" "$OUT/trivy-backend.json" "backend runtime image"
scan_image "$FRONTEND_IMAGE" "$OUT/trivy-frontend.json" "frontend runtime image"

cat > "$OUT/container-security-policy.txt" <<'POLICY'
CPIM-MES Step 12 container vulnerability policy
- Scanner: pinned Trivy release installed through verified upstream checksums
- Runtime images: backend and frontend
- Blocking severities: HIGH, CRITICAL
- --ignore-unfixed is enabled so the release gate blocks actionable fixed vulnerabilities
- No repository .trivyignore or vulnerability-ID suppression file is used
- Full SBOMs are generated independently with pinned Syft
POLICY

exit "$status"
