#!/usr/bin/env bash
# Onboards the automotive-dev-operator application onto the Konflux instance.
# Run AFTER 02-deploy-konflux.sh completes successfully.
#
# What this does:
#   1. Creates the appstudio-pipeline service account
#   2. Binds it to the appstudio-pipelines-runner ClusterRole
#   3. Creates the Quay push secret
#   4. Creates the Application CR
#   5. Creates the operator Component CR (triggers first build)
#
# Bundle and catalog components are NOT created here — add them manually
# after the operator build succeeds to avoid OOM from parallel builds.
set -euo pipefail

export KUBECONFIG="${HOME}/.kube/konflux-config"
NAMESPACE="default-tenant"

# ── Validate cluster is reachable ────────────────────────────────────────────
if ! kubectl cluster-info &>/dev/null; then
  echo "ERROR: Cannot reach cluster. Run 02-deploy-konflux.sh first."
  exit 1
fi

# ── Quay credentials (required) ──────────────────────────────────────────────
QUAY_USER="${QUAY_USER:-}"
QUAY_TOKEN="${QUAY_TOKEN:-}"

if [ -z "$QUAY_USER" ] || [ -z "$QUAY_TOKEN" ]; then
  echo "ERROR: Set QUAY_USER and QUAY_TOKEN before running this script."
  echo ""
  echo "  export QUAY_USER='bkhizgiy+konflux_push'"
  echo "  export QUAY_TOKEN='<robot-account-token>'"
  echo "  bash konflux/03-onboard-app.sh"
  exit 1
fi

echo "Setting up Konflux application in namespace: ${NAMESPACE}"
echo ""

# ── 1. Service account ───────────────────────────────────────────────────────
echo "Creating appstudio-pipeline service account..."
kubectl create serviceaccount appstudio-pipeline -n "${NAMESPACE}" \
  --dry-run=client -o yaml | kubectl apply -f -

# ── 2. RoleBinding ───────────────────────────────────────────────────────────
echo "Binding appstudio-pipeline to appstudio-pipelines-runner..."
kubectl create rolebinding appstudio-pipeline-runner \
  --clusterrole=appstudio-pipelines-runner \
  --serviceaccount="${NAMESPACE}:appstudio-pipeline" \
  -n "${NAMESPACE}" \
  --dry-run=client -o yaml | kubectl apply -f -

# ── 3. Quay push secret ──────────────────────────────────────────────────────
echo "Creating Quay push secret..."
kubectl create secret docker-registry quay-push-secret \
  --docker-server=quay.io \
  --docker-username="${QUAY_USER}" \
  --docker-password="${QUAY_TOKEN}" \
  -n "${NAMESPACE}" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "Linking secret to service account..."
kubectl patch serviceaccount appstudio-pipeline \
  -n "${NAMESPACE}" \
  --type=merge \
  -p='{"secrets":[{"name":"quay-push-secret"}],"imagePullSecrets":[{"name":"quay-push-secret"}]}'

# ── 4. Application ───────────────────────────────────────────────────────────
echo "Creating Application..."
kubectl apply -f - <<EOF
apiVersion: appstudio.redhat.com/v1alpha1
kind: Application
metadata:
  name: automotive-dev-operator
  namespace: ${NAMESPACE}
spec:
  displayName: automotive-dev-operator
EOF

# ── 5. Operator component (triggers build) ───────────────────────────────────
echo "Creating operator Component (this will trigger a PipelineRun)..."
kubectl apply -f - <<EOF
apiVersion: appstudio.redhat.com/v1alpha1
kind: Component
metadata:
  name: operator
  namespace: ${NAMESPACE}
spec:
  application: automotive-dev-operator
  componentName: operator
  source:
    git:
      url: https://github.com/bkhizgiy/automotive-dev-operator
      revision: main
      dockerfileUrl: Dockerfile
  containerImage: quay.io/bkhizgiy/automotive-dev-operator
EOF

echo ""
echo "Done. Watching for PipelineRun..."
echo "(Ctrl+C to stop watching, the build will continue)"
echo ""
sleep 5
kubectl get pipelineruns -n "${NAMESPACE}" -w
