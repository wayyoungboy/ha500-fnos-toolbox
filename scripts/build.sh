#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PACKAGE="${ROOT}/package/ha500-toolbox"
BINARY="${PACKAGE}/app/bin/ha500-toolbox"
VERSION="$(awk -F= '$1 ~ /^version[[:space:]]*$/ {gsub(/[[:space:]]/, "", $2); print $2}' "${PACKAGE}/manifest")"
STAGE_ONLY=false
[ "${1:-}" = "--stage-only" ] && STAGE_ONLY=true

BUILD_TEMP="$(mktemp -d)"
trap 'rm -rf -- "${BUILD_TEMP}"' EXIT

GOTOOLCHAIN=go1.25.12 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o "${BUILD_TEMP}/ha500-toolbox" "${ROOT}/cmd/ha500-toolbox"
mkdir -p "$(dirname "${BINARY}")"
install -m 0755 "${BUILD_TEMP}/ha500-toolbox" "${BINARY}"
python3 "${ROOT}/scripts/validate_package.py"

if [ "${STAGE_ONLY}" = true ]; then
    echo "Staged ${BINARY}"
    exit 0
fi

FNPACK_BIN="${FNPACK:-fnpack}"
command -v "${FNPACK_BIN}" >/dev/null 2>&1 || {
    echo "fnpack is unavailable; run scripts/build.sh --stage-only or set FNPACK" >&2
    exit 1
}
mkdir -p "${ROOT}/dist"
rm -f -- "${ROOT}/dist/ha500_toolbox.fpk" "${ROOT}/dist/ha500_toolbox_${VERSION}_x86_64.fpk"
(cd "${ROOT}/dist" && "${FNPACK_BIN}" build --directory "${PACKAGE}")
[ -f "${ROOT}/dist/ha500_toolbox.fpk" ] || {
    echo "fnpack did not produce ha500_toolbox.fpk" >&2
    exit 1
}
mv "${ROOT}/dist/ha500_toolbox.fpk" "${ROOT}/dist/ha500_toolbox_${VERSION}_x86_64.fpk"
echo "Built ${ROOT}/dist/ha500_toolbox_${VERSION}_x86_64.fpk"
