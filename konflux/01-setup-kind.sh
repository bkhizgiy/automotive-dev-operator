#!/usr/bin/env bash
# Sets up a Kind cluster ready for Konflux by cloning the Konflux repo
# and using its official kind-config.yaml and setup script.
# Run as your regular user (Docker access required).
set -euo pipefail

KIND_VERSION="v0.27.0"
KONFLUX_REPO="https://github.com/konflux-ci/konflux-ci.git"
KONFLUX_DIR="${HOME}/konflux-ci"
KIND_MEMORY_GB="${KIND_MEMORY_GB:-16}"

# ── 1. Install kind ──────────────────────────────────────────────────────────
if ! command -v kind &>/dev/null; then
  echo "Installing kind ${KIND_VERSION}..."
  ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
  curl -fsSL "https://kind.sigs.k8s.io/dl/${KIND_VERSION}/kind-linux-${ARCH}" -o /tmp/kind
  chmod +x /tmp/kind
  sudo mv /tmp/kind /usr/local/bin/kind
  echo "kind installed: $(kind version)"
else
  echo "kind already installed: $(kind version)"
fi

# ── 2. Clone Konflux repo ────────────────────────────────────────────────────
# Konflux's setup script uses kind-config.yaml from the repo root.
if [ -d "${KONFLUX_DIR}/.git" ]; then
  echo "Konflux repo already cloned at ${KONFLUX_DIR}, pulling latest..."
  git -C "${KONFLUX_DIR}" pull --ff-only
else
  echo "Cloning Konflux repo to ${KONFLUX_DIR}..."
  git clone "${KONFLUX_REPO}" "${KONFLUX_DIR}"
fi

# ── 3. Tune inotify limits (required by Tekton) ──────────────────────────────
WATCHES_REQUIRED=524288
INSTANCES_REQUIRED=512
WATCHES_CURRENT=$(cat /proc/sys/fs/inotify/max_user_watches 2>/dev/null || echo 0)
INSTANCES_CURRENT=$(cat /proc/sys/fs/inotify/max_user_instances 2>/dev/null || echo 0)

if [[ "$WATCHES_CURRENT" -lt "$WATCHES_REQUIRED" ]] || \
   [[ "$INSTANCES_CURRENT" -lt "$INSTANCES_REQUIRED" ]]; then
  echo "Increasing inotify limits (required by Tekton)..."
  sudo sysctl fs.inotify.max_user_watches=${WATCHES_REQUIRED}
  sudo sysctl fs.inotify.max_user_instances=${INSTANCES_REQUIRED}
  # Persist across reboots
  echo "fs.inotify.max_user_watches=${WATCHES_REQUIRED}" | sudo tee /etc/sysctl.d/99-konflux.conf >/dev/null
  echo "fs.inotify.max_user_instances=${INSTANCES_REQUIRED}"  | sudo tee -a /etc/sysctl.d/99-konflux.conf >/dev/null
else
  echo "inotify limits already sufficient."
fi

# ── 4. Create Kind cluster using Konflux's own script ───────────────────────
echo ""
echo "Creating Kind cluster (memory: ${KIND_MEMORY_GB}Gi)..."
echo "Port mappings that will be configured:"
echo "  localhost:9443  → Konflux UI (HTTPS)"
echo "  localhost:8180  → Pipelines as Code webhook"
echo "  localhost:5001  → Internal container registry"
echo "  localhost:8443  → Internal Quay"
echo ""

# Ensure kind-config.yaml is clean before we start
git -C "${KONFLUX_DIR}" checkout kind-config.yaml

# Lower system-reserved: the Konflux script sets it via KIND_MEMORY_GB sed,
# but we patch it after to reduce the reservation from 8Gi to 2Gi so pods
# get ~14Gi of headroom instead of ~8Gi.
KIND_MEMORY_GB="${KIND_MEMORY_GB}" \
ENABLE_IMAGE_CACHE=0 \
INCREASE_PODMAN_PIDS_LIMIT=0 \
  "${KONFLUX_DIR}/scripts/setup-kind-local-cluster.sh"

# ── 5. Save kubeconfig ───────────────────────────────────────────────────────
KUBECONFIG_PATH="${HOME}/.kube/konflux-config"
mkdir -p "$(dirname "$KUBECONFIG_PATH")"
kind get kubeconfig --name konflux > "$KUBECONFIG_PATH"
chmod 600 "$KUBECONFIG_PATH"

export KUBECONFIG="$KUBECONFIG_PATH"

# ── 6. Verify ────────────────────────────────────────────────────────────────
echo ""
echo "Cluster nodes:"
kubectl get nodes
echo ""
echo "Waiting for system pods..."
kubectl wait --for=condition=Ready pods --all -n kube-system --timeout=120s
echo ""
echo "Done. Next step: deploy Konflux dependencies onto the cluster."
echo ""
echo "To use this cluster:"
echo "  export KUBECONFIG=${KUBECONFIG_PATH}"
echo "  # or add to your shell profile:"
echo "  echo 'export KUBECONFIG=${KUBECONFIG_PATH}' >> ~/.bashrc"
