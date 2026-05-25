#!/usr/bin/env bash
# Generate OLM bundle manifests for the commit being built.
set -euo pipefail

ROOT="${1:-.}"
OPERATOR_IMAGE="${OPERATOR_IMAGE:?OPERATOR_IMAGE is required}"

cd "${ROOT}"
VERSION="$(tr -d '[:space:]' < VERSION)"
export IMG="${OPERATOR_IMAGE}"
export VERSION

echo "Generating bundle for operator ${IMG} (OLM version ${VERSION})..."
make bundle
