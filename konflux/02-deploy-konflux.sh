#!/usr/bin/env bash
# Deploys Konflux onto the Kind cluster created by 01-setup-kind.sh.
# Run AFTER 01-setup-kind.sh completes.
set -euo pipefail

KONFLUX_DIR="${HOME}/konflux-ci"
KUBECONFIG_PATH="${HOME}/.kube/konflux-config"

export KUBECONFIG="${KUBECONFIG_PATH}"

# ── Sanity checks ────────────────────────────────────────────────────────────
if ! kind get clusters 2>/dev/null | grep -q "^konflux$"; then
  echo "ERROR: Kind cluster 'konflux' not found. Run 01-setup-kind.sh first."
  exit 1
fi

if [ ! -d "${KONFLUX_DIR}" ]; then
  echo "ERROR: Konflux repo not found at ${KONFLUX_DIR}. Run 01-setup-kind.sh first."
  exit 1
fi

echo "Using cluster: $(kubectl config current-context)"
echo "Konflux repo:  ${KONFLUX_DIR}"
echo ""

# ── Copy env file if it exists ───────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOCAL_ENV="${SCRIPT_DIR}/deploy-local.env"
KONFLUX_ENV="${KONFLUX_DIR}/scripts/deploy-local.env"

if [ -f "${LOCAL_ENV}" ]; then
  echo "Copying deploy-local.env to Konflux scripts directory..."
  cp "${LOCAL_ENV}" "${KONFLUX_ENV}"
else
  echo "No deploy-local.env found — deploying without GitHub integration."
  echo "(You can add it later by filling in konflux/deploy-local.env.template"
  echo " and re-running this script.)"
  echo ""
fi

# ── Deploy ───────────────────────────────────────────────────────────────────
# DEPLOY_LOCAL_SKIP_KIND=1  → skip cluster creation (already done)
# SKIP_SECRETS=true         → skip GitHub App secrets if env file is missing
cd "${KONFLUX_DIR}"

if [ -f "${LOCAL_ENV}" ]; then
  DEPLOY_LOCAL_SKIP_KIND=1 \
    ./scripts/deploy-local.sh
else
  DEPLOY_LOCAL_SKIP_KIND=1 \
  SKIP_SECRETS=true \
    ./scripts/deploy-local.sh
fi
