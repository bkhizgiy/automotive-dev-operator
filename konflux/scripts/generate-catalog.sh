#!/usr/bin/env bash
# Generate file-based catalog index from a bundle image already pushed to Quay.
set -euo pipefail

ROOT="${1:-.}"
BUNDLE_IMAGE="${BUNDLE_IMAGE:?BUNDLE_IMAGE is required}"

HOME="${HOME:-/tekton/home}"
export HOME

# opm render uses containers/image and requires a signature policy file.
mkdir -p "${HOME}/.config/containers"
cat > "${HOME}/.config/containers/policy.json" <<'EOF'
{
  "default": [
    {
      "type": "insecureAcceptAnything"
    }
  ]
}
EOF

# Use Tekton-injected registry credentials (same as skopeo wait step).
export DOCKER_CONFIG="${DOCKER_CONFIG:-${HOME}/.docker}"

cd "${ROOT}"
VERSION="$(tr -d '[:space:]' < VERSION)"
export BUNDLE_IMG="${BUNDLE_IMAGE}"
export VERSION

echo "Generating catalog from bundle ${BUNDLE_IMG} (OLM version ${VERSION})..."
make catalog-update
