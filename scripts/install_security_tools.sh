#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
source "$ROOT/scripts/security_tool_versions.env"

BIN_DIR="${SECURITY_TOOL_BIN_DIR:-$ROOT/.tools/bin}"
SCOPE="${1:-all}"
case "$SCOPE" in all|dependency|container) ;; *) echo "ERROR: scope must be all, dependency, or container" >&2; exit 1 ;; esac
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
mkdir -p "$BIN_DIR"

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "ERROR: required command not found: $1" >&2
    exit 1
  }
}

if [[ "$SCOPE" == "all" || "$SCOPE" == "dependency" ]]; then
  need_cmd go
fi
if [[ "$SCOPE" == "all" || "$SCOPE" == "container" ]]; then
  for cmd in curl sha256sum tar; do need_cmd "$cmd"; done
fi

case "$(uname -s)" in
  Linux) os=linux ;;
  *)
    echo "ERROR: scripts/install_security_tools.sh currently supports Linux CI runners only." >&2
    echo "Install the exact pinned tools manually on other platforms and run the gate scripts." >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  x86_64|amd64)
    trivy_arch="64bit"
    syft_arch="amd64"
    ;;
  aarch64|arm64)
    trivy_arch="ARM64"
    syft_arch="arm64"
    ;;
  *)
    echo "ERROR: unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

curl_https() {
  curl --fail --location --silent --show-error --retry 3 --retry-all-errors \
    --proto '=https' --tlsv1.2 "$@"
}

verify_pinned_checksum_file() {
  local file="$1" expected="$2"
  local actual
  actual="$(sha256sum "$file" | awk '{print $1}')"
  if [[ "$actual" != "$expected" ]]; then
    echo "ERROR: checksum-file digest mismatch for $(basename "$file")" >&2
    echo "  expected: $expected" >&2
    echo "  actual:   $actual" >&2
    exit 1
  fi
}

verify_release_asset() {
  local checksum_file="$1" asset="$2"
  local name expected actual
  name="$(basename "$asset")"
  expected="$(awk -v n="$name" '$2 == n || $2 == "*" n {print $1; exit}' "$checksum_file")"
  if [[ -z "$expected" ]]; then
    echo "ERROR: $name not present in verified checksum file" >&2
    exit 1
  fi
  actual="$(sha256sum "$asset" | awk '{print $1}')"
  if [[ "$actual" != "$expected" ]]; then
    echo "ERROR: release asset digest mismatch for $name" >&2
    echo "  expected: $expected" >&2
    echo "  actual:   $actual" >&2
    exit 1
  fi
}

# govulncheck is fetched through the Go module proxy/checksum database at an exact version.
# It is installed outside the application module and never modifies go.mod/go.sum.
if [[ "$SCOPE" == "all" || "$SCOPE" == "dependency" ]]; then
  echo "Installing govulncheck ${GOVULNCHECK_VERSION}"
  (
    export GOBIN="$BIN_DIR"
    export GOSUMDB="${GOSUMDB:-sum.golang.org}"
    go install "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}"
  )
fi

if [[ "$SCOPE" == "all" || "$SCOPE" == "container" ]]; then
trivy_checksums="$TMP_DIR/trivy_checksums.txt"
trivy_asset="$TMP_DIR/trivy_${TRIVY_VERSION}_Linux-${trivy_arch}.tar.gz"
curl_https -o "$trivy_checksums" \
  "https://github.com/aquasecurity/trivy/releases/download/v${TRIVY_VERSION}/trivy_${TRIVY_VERSION}_checksums.txt"
verify_pinned_checksum_file "$trivy_checksums" "$TRIVY_CHECKSUM_FILE_SHA256"
curl_https -o "$trivy_asset" \
  "https://github.com/aquasecurity/trivy/releases/download/v${TRIVY_VERSION}/$(basename "$trivy_asset")"
verify_release_asset "$trivy_checksums" "$trivy_asset"
tar -xzf "$trivy_asset" -C "$TMP_DIR" trivy
install -m 0755 "$TMP_DIR/trivy" "$BIN_DIR/trivy"

syft_checksums="$TMP_DIR/syft_checksums.txt"
syft_asset="$TMP_DIR/syft_${SYFT_VERSION}_${os}_${syft_arch}.tar.gz"
curl_https -o "$syft_checksums" \
  "https://github.com/anchore/syft/releases/download/v${SYFT_VERSION}/syft_${SYFT_VERSION}_checksums.txt"
verify_pinned_checksum_file "$syft_checksums" "$SYFT_CHECKSUM_FILE_SHA256"
curl_https -o "$syft_asset" \
  "https://github.com/anchore/syft/releases/download/v${SYFT_VERSION}/$(basename "$syft_asset")"
verify_release_asset "$syft_checksums" "$syft_asset"
tar -xzf "$syft_asset" -C "$TMP_DIR" syft
install -m 0755 "$TMP_DIR/syft" "$BIN_DIR/syft"
fi

echo "Installed security tools ($SCOPE):"
[[ -x "$BIN_DIR/govulncheck" ]] && "$BIN_DIR/govulncheck" -version || true
[[ -x "$BIN_DIR/trivy" ]] && "$BIN_DIR/trivy" --version || true
[[ -x "$BIN_DIR/syft" ]] && "$BIN_DIR/syft" version || true
