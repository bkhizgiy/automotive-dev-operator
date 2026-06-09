#!/usr/bin/env bash
# Onboards the automotive-dev-operator application onto the Konflux instance.
# Run AFTER the Konflux cluster is up and all components are ready.
#
# What this does:
#   1. Creates the build-pipeline service account
#   2. Binds it to the appstudio-pipelines-runner ClusterRole
#   3. Creates the Quay push secret (with Tekton registry annotation)
#   4. Links the secret to the service account
#   5. Creates the Application CR
#   6. Creates Component CRs: operator, bundle, catalog
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=defaults.env
source "${SCRIPT_DIR}/defaults.env"
if [ -f "${SCRIPT_DIR}/deploy-local.env" ]; then
  # shellcheck source=deploy-local.env
  source "${SCRIPT_DIR}/deploy-local.env"
fi
konflux_apply_image_defaults

export KUBECONFIG="${KUBECONFIG:-${HOME}/.kube/konflux-config}"
NAMESPACE="${NAMESPACE:-default-tenant}"

# ── Validate cluster is reachable ────────────────────────────────────────────
if ! kubectl cluster-info &>/dev/null; then
  echo "ERROR: Cannot reach cluster. Ensure Konflux is deployed and KUBECONFIG is set."
  exit 1
fi

# ── Quay credentials (required) ──────────────────────────────────────────────
QUAY_USER="${QUAY_USER:-}"
QUAY_TOKEN="${QUAY_TOKEN:-}"

if [ -z "$QUAY_USER" ] || [ -z "$QUAY_TOKEN" ]; then
  echo "ERROR: Set QUAY_USER and QUAY_TOKEN before running this script."
  echo ""
  echo "  export QUAY_USER='${QUAY_ORG}+konflux_push'"
  echo "  export QUAY_TOKEN='<robot-account-token>'"
  echo "  bash hack/konflux/onboard-app.sh"
  exit 1
fi

echo "Setting up Konflux application in namespace: ${NAMESPACE}"
echo "  Git repo:    ${GITHUB_REPO_URL} (${GIT_BRANCH})"
echo "  Quay images: ${QUAY_IMAGE_OPERATOR}, ${QUAY_IMAGE_BUNDLE}, ${QUAY_IMAGE_CATALOG}"
echo ""

# ── 1. Service account ───────────────────────────────────────────────────────
echo "Creating build-pipeline service account..."
kubectl create serviceaccount build-pipeline \
  -n "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

# ── 2. RoleBinding ───────────────────────────────────────────────────────────
echo "Binding build-pipeline to appstudio-pipelines-runner..."
kubectl create rolebinding build-pipeline-runner \
  --clusterrole=appstudio-pipelines-runner \
  --serviceaccount="${NAMESPACE}:build-pipeline" \
  -n "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

# ── 3. Quay push secret ──────────────────────────────────────────────────────
echo "Creating Quay push secret..."
kubectl create secret docker-registry quay-push-secret \
  --docker-server=quay.io \
  --docker-username="${QUAY_USER}" \
  --docker-password="${QUAY_TOKEN}" \
  -n "${NAMESPACE}" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "Linking secret to build-pipeline..."
SA="build-pipeline"
PATCH='['
NEEDS_PATCH=false

for FIELD in secrets imagePullSecrets; do
  VAL=$(kubectl get serviceaccount "${SA}" -n "${NAMESPACE}" -o "jsonpath={.${FIELD}[*].name}")
  if echo " ${VAL} " | grep -qw "quay-push-secret"; then
    echo "  ${SA}.${FIELD}: already linked, skipping"
    continue
  fi
  NEEDS_PATCH=true
  if [ -z "${VAL}" ]; then
    PATCH+='{"op":"add","path":"/'"${FIELD}"'","value":[{"name":"quay-push-secret"}]},'
  else
    PATCH+='{"op":"add","path":"/'"${FIELD}"'/-","value":{"name":"quay-push-secret"}},'
  fi
done

if [ "${NEEDS_PATCH}" = "true" ]; then
  PATCH="${PATCH%,}]"
  kubectl patch serviceaccount "${SA}" -n "${NAMESPACE}" --type=json -p="${PATCH}"
  echo "  ${SA}: linked quay-push-secret"
fi

echo "Annotating secret for Tekton registry credential binding..."
kubectl annotate secret quay-push-secret \
  -n "${NAMESPACE}" \
  tekton.dev/docker-0=https://quay.io \
  --overwrite

# ── 4. Application ───────────────────────────────────────────────────────────
echo "Creating Application..."
kubectl apply -f - <<EOF
apiVersion: appstudio.redhat.com/v1alpha1
kind: Application
metadata:
  name: ${APP_NAME}
  namespace: ${NAMESPACE}
spec:
  displayName: ${APP_NAME}
EOF

# ── 5. Components ────────────────────────────────────────────────────────────
echo "Creating operator Component..."
kubectl apply -f - <<EOF
apiVersion: appstudio.redhat.com/v1alpha1
kind: Component
metadata:
  name: operator
  namespace: ${NAMESPACE}
spec:
  application: ${APP_NAME}
  componentName: operator
  source:
    git:
      url: ${GITHUB_REPO_URL}
      revision: ${GIT_BRANCH}
      dockerfileUrl: Dockerfile
  containerImage: ${QUAY_IMAGE_OPERATOR}
EOF

echo "Creating bundle Component..."
kubectl apply -f - <<EOF
apiVersion: appstudio.redhat.com/v1alpha1
kind: Component
metadata:
  name: bundle
  namespace: ${NAMESPACE}
spec:
  application: ${APP_NAME}
  componentName: bundle
  source:
    git:
      url: ${GITHUB_REPO_URL}
      revision: ${GIT_BRANCH}
      dockerfileUrl: bundle.Dockerfile
  containerImage: ${QUAY_IMAGE_BUNDLE}
EOF

echo "Creating catalog Component..."
kubectl apply -f - <<EOF
apiVersion: appstudio.redhat.com/v1alpha1
kind: Component
metadata:
  name: catalog
  namespace: ${NAMESPACE}
spec:
  application: ${APP_NAME}
  componentName: catalog
  source:
    git:
      url: ${GITHUB_REPO_URL}
      revision: ${GIT_BRANCH}
      dockerfileUrl: catalog.Dockerfile
  containerImage: ${QUAY_IMAGE_CATALOG}
EOF

if [ "${CLEANUP_RELEASE_COMPONENT:-false}" = "true" ]; then
  echo "Removing legacy 'release' component..."
  kubectl delete component release -n "${NAMESPACE}" --ignore-not-found
fi

echo ""
echo "Done. Watching for PipelineRuns..."
echo "(Ctrl+C to stop watching, the builds will continue)"
echo ""
sleep 5
kubectl get pipelineruns -n "${NAMESPACE}" -w
