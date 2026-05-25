#!/usr/bin/env bash
# Generate file-based catalog index from a bundle image already pushed to Quay.
set -euo pipefail

ROOT="${1:-.}"
BUNDLE_IMAGE="${BUNDLE_IMAGE:?BUNDLE_IMAGE is required}"

cd "${ROOT}"
VERSION="$(tr -d '[:space:]' < VERSION)"
export BUNDLE_IMG="${BUNDLE_IMAGE}"
export VERSION

echo "Generating catalog from bundle ${BUNDLE_IMG} (OLM version ${VERSION})..."
make catalog-update
