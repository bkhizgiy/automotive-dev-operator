#!/bin/bash
set -e

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
echo "Building bundle image..."
make bundle-build BUNDLE_IMG=${BUNDLE_IMG}

echo ""
echo "Pushing bundle image to OpenShift registry..."
${CONTAINER_TOOL} push ${BUNDLE_IMG} --tls-verify=false

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
# Update bundle image reference to internal registry
sed -i.bak "s|image:.*automotive-dev-operator-bundle.*|image: ${BUNDLE_IMG_INTERNAL}|g" catalog/automotive-dev-operator.yaml
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

