#!/usr/bin/env bash
# Onboards the automotive-dev-operator application onto the Konflux instance.
# Run AFTER 02-deploy-konflux.sh completes successfully.
#
# What this does:
#   1. Creates build-pipeline-{operator,bundle,catalog} service accounts
#   2. Binds them to the appstudio-pipelines-runner ClusterRole
#   3. Creates the Quay push secret (with Tekton registry annotation)
#   4. Links the secret to all service accounts
#   5. Creates the Application CR
#   6. Creates all three Component CRs (operator, bundle, catalog)
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

# ── 1. Service accounts ──────────────────────────────────────────────────────
echo "Creating build-pipeline service accounts..."
for COMPONENT in operator bundle catalog; do
  kubectl create serviceaccount "build-pipeline-${COMPONENT}" \
    -n "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
done
# Legacy SA kept for compatibility
kubectl create serviceaccount appstudio-pipeline -n "${NAMESPACE}" \
  --dry-run=client -o yaml | kubectl apply -f -

# ── 2. RoleBindings ──────────────────────────────────────────────────────────
echo "Binding service accounts to appstudio-pipelines-runner..."
for COMPONENT in operator bundle catalog; do
  kubectl create rolebinding "build-pipeline-${COMPONENT}-runner" \
    --clusterrole=appstudio-pipelines-runner \
    --serviceaccount="${NAMESPACE}:build-pipeline-${COMPONENT}" \
    -n "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
done
kubectl create rolebinding appstudio-pipeline-runner \
  --clusterrole=appstudio-pipelines-runner \
  --serviceaccount="${NAMESPACE}:appstudio-pipeline" \
  -n "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

# ── 3. Quay push secret ──────────────────────────────────────────────────────
echo "Creating Quay push secret..."
QUAY_AUTH=$(echo -n "${QUAY_USER}:${QUAY_TOKEN}" | base64 -w 0)
DOCKER_CONFIG_JSON=$(echo -n "{\"auths\":{\"quay.io\":{\"username\":\"${QUAY_USER}\",\"password\":\"${QUAY_TOKEN}\",\"auth\":\"${QUAY_AUTH}\"}}}" | base64 -w 0)

kubectl apply -f - <<SECRETEOF
apiVersion: v1
kind: Secret
metadata:
  name: quay-push-secret
  namespace: ${NAMESPACE}
  annotations:
    tekton.dev/docker-0: https://quay.io
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: ${DOCKER_CONFIG_JSON}
SECRETEOF

echo "Linking secret to all service accounts..."
for SA in appstudio-pipeline build-pipeline-operator build-pipeline-bundle build-pipeline-catalog; do
  kubectl patch serviceaccount "${SA}" \
    -n "${NAMESPACE}" \
    --type=merge \
    -p='{"secrets":[{"name":"quay-push-secret"}],"imagePullSecrets":[{"name":"quay-push-secret"}]}'
done

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

# ── 5. Components ────────────────────────────────────────────────────────────
echo "Creating operator Component..."
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

echo "Creating bundle Component..."
kubectl apply -f - <<EOF
apiVersion: appstudio.redhat.com/v1alpha1
kind: Component
metadata:
  name: bundle
  namespace: ${NAMESPACE}
spec:
  application: automotive-dev-operator
  componentName: bundle
  source:
    git:
      url: https://github.com/bkhizgiy/automotive-dev-operator
      revision: main
      dockerfileUrl: bundle.Dockerfile
  containerImage: quay.io/bkhizgiy/automotive-dev-operator-bundle
EOF

echo "Creating catalog Component..."
kubectl apply -f - <<EOF
apiVersion: appstudio.redhat.com/v1alpha1
kind: Component
metadata:
  name: catalog
  namespace: ${NAMESPACE}
spec:
  application: automotive-dev-operator
  componentName: catalog
  source:
    git:
      url: https://github.com/bkhizgiy/automotive-dev-operator
      revision: main
      dockerfileUrl: catalog.Dockerfile
  containerImage: quay.io/bkhizgiy/automotive-dev-operator-catalog
EOF

echo ""
echo "Done. Watching for PipelineRuns..."
echo "(Ctrl+C to stop watching, the builds will continue)"
echo ""
sleep 5
kubectl get pipelineruns -n "${NAMESPACE}" -w
