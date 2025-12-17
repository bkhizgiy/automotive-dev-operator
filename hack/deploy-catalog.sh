#!/bin/bash
set -e

# Parse command line options
UNINSTALL=false
while [[ $# -gt 0 ]]; do
    case $1 in
        --uninstall)
            UNINSTALL=true
            shift
            ;;
        *)
            echo "Unknown option: $1"
            echo "Usage: $0 [--uninstall]"
            exit 1
            ;;
    esac
done

# Configuration
VERSION=${VERSION:-0.0.1}
NAMESPACE=${NAMESPACE:-automotive-dev-operator-system}

# Detect OpenShift internal registry
echo "Detecting OpenShift internal registry..."
INTERNAL_REGISTRY=$(oc get route default-route -n openshift-image-registry -o jsonpath='{.spec.host}' 2>/dev/null || echo "")

if [ -z "$INTERNAL_REGISTRY" ]; then
    echo "Internal registry route not found. Creating it..."
    oc patch configs.imageregistry.operator.openshift.io/cluster --patch '{"spec":{"defaultRoute":true}}' --type=merge

    echo "Waiting for registry route to be created..."
    for i in {1..30}; do
        INTERNAL_REGISTRY=$(oc get route default-route -n openshift-image-registry -o jsonpath='{.spec.host}' 2>/dev/null || echo "")
        if [ -n "$INTERNAL_REGISTRY" ]; then
            break
        fi
        sleep 2
    done

    if [ -z "$INTERNAL_REGISTRY" ]; then
        echo "ERROR: Failed to get internal registry route"
        exit 1
    fi
fi

echo "Using OpenShift internal registry: ${INTERNAL_REGISTRY}"

REGISTRY=${REGISTRY:-${INTERNAL_REGISTRY}}
CATALOG_NAMESPACE=${CATALOG_NAMESPACE:-openshift-marketplace}
OPERATOR_IMG="${REGISTRY}/${NAMESPACE}/automotive-dev-operator:latest"
BUNDLE_IMG="${REGISTRY}/${CATALOG_NAMESPACE}/automotive-dev-operator-bundle:v${VERSION}"
CATALOG_IMG="${REGISTRY}/${CATALOG_NAMESPACE}/automotive-dev-operator-catalog:v${VERSION}"
CONTAINER_TOOL=${CONTAINER_TOOL:-podman}

uninstall_operator() {
    echo "=========================================="
    echo "Uninstalling existing operator"
    echo "=========================================="

    echo "Removing finalizers from OperatorConfig CRs..."
    for oc_name in $(oc get operatorconfig -n ${NAMESPACE} -o name 2>/dev/null); do
        oc patch ${oc_name} -n ${NAMESPACE} --type=merge -p '{"metadata":{"finalizers":[]}}' 2>/dev/null || true
    done
    echo "Deleting OperatorConfig CRs..."
    oc delete operatorconfig --all -n ${NAMESPACE} --ignore-not-found=true --timeout=10s 2>/dev/null || true

    echo "Deleting subscription (if exists)..."
    oc delete subscriptions.operators.coreos.com automotive-dev-operator -n ${NAMESPACE} --ignore-not-found=true

    echo "Deleting CSVs (if exist)..."
    oc delete csv -n ${NAMESPACE} -l operators.coreos.com/automotive-dev-operator.${NAMESPACE}= --ignore-not-found=true 2>/dev/null || true
    # Also try by name pattern
    CSVS=$(oc get csv -n ${NAMESPACE} -o name 2>/dev/null | grep automotive-dev-operator || true)
    if [ -n "$CSVS" ]; then
        echo "$CSVS" | xargs -r oc delete -n ${NAMESPACE} --ignore-not-found=true
    fi

    echo "Deleting InstallPlans (if exist)..."
    oc delete installplan -n ${NAMESPACE} --all --ignore-not-found=true 2>/dev/null || true

    echo "Deleting operator-managed resources..."
    oc delete deployment ado-webui ado-build-api ado-controller-manager -n ${NAMESPACE} --ignore-not-found=true 2>/dev/null || true
    oc delete service ado-webui ado-build-api -n ${NAMESPACE} --ignore-not-found=true 2>/dev/null || true
    oc delete route ado-webui ado-build-api -n ${NAMESPACE} --ignore-not-found=true 2>/dev/null || true
    oc delete serviceaccount ado-controller-manager ado-webui -n ${NAMESPACE} --ignore-not-found=true 2>/dev/null || true
    oc delete configmap ado-webui-nginx-config -n ${NAMESPACE} --ignore-not-found=true 2>/dev/null || true
    oc delete secret ado-oauth-secrets -n ${NAMESPACE} --ignore-not-found=true 2>/dev/null || true

    echo "Waiting for operator pods to terminate..."
    oc wait --for=delete pod -l control-plane=controller-manager -n ${NAMESPACE} --timeout=60s 2>/dev/null || true

    echo "Operator uninstall complete."
    echo ""
}

if [ "$UNINSTALL" = true ]; then
    uninstall_operator
fi

echo "=========================================="
echo "Building and Deploying Operator Catalog"
echo "=========================================="
echo "Version: ${VERSION}"
echo "Operator Namespace: ${NAMESPACE}"
echo "Catalog Namespace: ${CATALOG_NAMESPACE}"
echo "Registry: ${REGISTRY}"
echo "Operator Image: ${OPERATOR_IMG}"
echo "Bundle Image: ${BUNDLE_IMG}"
echo "Catalog Image: ${CATALOG_IMG}"
echo "=========================================="

echo ""
echo "Ensuring push permissions..."
oc policy add-role-to-user system:image-pusher $(oc whoami) -n ${NAMESPACE} 2>/dev/null || true
oc policy add-role-to-user system:image-pusher $(oc whoami) -n ${CATALOG_NAMESPACE} 2>/dev/null || true

echo ""
echo "Logging in to OpenShift registry..."
${CONTAINER_TOOL} login -u $(oc whoami) -p $(oc whoami -t) ${REGISTRY} --tls-verify=false

echo ""
echo "Building operator image..."
make docker-build IMG=${OPERATOR_IMG}

echo ""
echo "Pushing operator image..."
${CONTAINER_TOOL} push ${OPERATOR_IMG} --tls-verify=false

echo ""
echo "Generating bundle..."
make bundle IMG=${OPERATOR_IMG} VERSION=${VERSION}

echo ""
echo "Fixing OPERATOR_IMAGE env var in bundle..."
# The bundle generator doesn't replace env var values, only container images
# We need to manually update the OPERATOR_IMAGE env var to use the internal registry
OPERATOR_IMG_INTERNAL="image-registry.openshift-image-registry.svc:5000/${NAMESPACE}/automotive-dev-operator:latest"
sed -i.bak "s|value: controller:latest|value: ${OPERATOR_IMG_INTERNAL}|g" bundle/manifests/automotive-dev-operator.clusterserviceversion.yaml
rm -f bundle/manifests/automotive-dev-operator.clusterserviceversion.yaml.bak

echo ""
echo "Building bundle image..."
make bundle-build BUNDLE_IMG=${BUNDLE_IMG}

echo ""
echo "Pushing bundle image to OpenShift registry..."
${CONTAINER_TOOL} push ${BUNDLE_IMG} --tls-verify=false

echo ""
echo "Ensuring opm is available..."
if [ ! -f "./bin/opm" ]; then
    echo "opm not found, downloading..."
    make opm
fi

echo ""
echo "Regenerating catalog..."
BUNDLE_IMG_INTERNAL="image-registry.openshift-image-registry.svc:5000/${CATALOG_NAMESPACE}/automotive-dev-operator-bundle:v${VERSION}"
cat > catalog/automotive-dev-operator.yaml << EOF
---
defaultChannel: alpha
name: automotive-dev-operator
schema: olm.package
---
schema: olm.channel
package: automotive-dev-operator
name: alpha
entries:
  - name: automotive-dev-operator.v${VERSION}
---
EOF
./bin/opm render bundle/ --output yaml >> catalog/automotive-dev-operator.yaml
# Update bundle image reference to internal registry (handles both empty and existing image refs)
sed -i.bak "s|^image:.*|image: ${BUNDLE_IMG_INTERNAL}|g" catalog/automotive-dev-operator.yaml
rm -f catalog/automotive-dev-operator.yaml.bak

echo ""
echo "Building catalog image..."
${CONTAINER_TOOL} build -f catalog.Dockerfile -t ${CATALOG_IMG} .

echo ""
echo "Pushing catalog image to OpenShift registry..."
${CONTAINER_TOOL} push ${CATALOG_IMG} --tls-verify=false

echo ""
echo "Updating CatalogSource manifest..."
CATALOG_IMG_INTERNAL="image-registry.openshift-image-registry.svc:5000/${CATALOG_NAMESPACE}/automotive-dev-operator-catalog:v${VERSION}"
sed -i.bak "s|image:.*|image: ${CATALOG_IMG_INTERNAL}|g" catalogsource.yaml
rm -f catalogsource.yaml.bak

echo ""
echo "Applying CatalogSource to OpenShift cluster..."
oc apply -f catalogsource.yaml -n ${CATALOG_NAMESPACE}

echo ""
echo "=========================================="
echo "Deployment Complete!"
echo "=========================================="
echo ""
echo "Your operator catalog has been deployed to OpenShift."
echo ""
echo "To view the catalog pods:"
echo "  oc get pods -n openshift-marketplace | grep automotive-dev-operator"
echo ""
