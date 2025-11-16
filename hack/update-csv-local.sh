#!/bin/bash
set -e

# Local script to update CSV with image digests
# This can be run locally or used as reference for automation

OPERATOR_IMAGE=${1:-""}
WEBUI_IMAGE=${2:-""}
CSV_FILE="bundle/manifests/automotive-dev-operator.clusterserviceversion.yaml"

if [ -z "$OPERATOR_IMAGE" ]; then
    echo "Usage: $0 <operator-image-with-digest> [webui-image-with-digest]"
    echo "Example: $0 quay.io/rh-sdv-cloud/automotive-dev-operator@sha256:abc123..."
    exit 1
fi

if [ ! -f "$CSV_FILE" ]; then
    echo "ERROR: CSV file not found at $CSV_FILE"
    exit 1
fi

echo "Updating CSV with image digests..."
echo "Operator image: $OPERATOR_IMAGE"

# Check if yq is installed
if ! command -v yq &> /dev/null; then
    echo "ERROR: yq is required but not installed"
    echo "Install with: brew install yq (macOS) or see https://github.com/mikefarah/yq"
    exit 1
fi

# Update operator image in deployment
yq eval ".spec.install.spec.deployments[].spec.template.spec.containers[] |= 
  select(.name == \"manager\").image = \"${OPERATOR_IMAGE}\"" \
  -i "$CSV_FILE"

# Update related images for disconnected support
yq eval ".spec.relatedImages = [{\"name\": \"manager\", \"image\": \"${OPERATOR_IMAGE}\"}]" \
  -i "$CSV_FILE"

# Update WebUI image if provided
if [ -n "$WEBUI_IMAGE" ]; then
    echo "WebUI image: $WEBUI_IMAGE"
    
    yq eval ".spec.install.spec.deployments[].spec.template.spec.containers[] |= 
      select(.name == \"webui\").image = \"${WEBUI_IMAGE}\"" \
      -i "$CSV_FILE"
    
    yq eval ".spec.relatedImages += [{\"name\": \"webui\", \"image\": \"${WEBUI_IMAGE}\"}]" \
      -i "$CSV_FILE"
fi

# Remove duplicates
yq eval '.spec.relatedImages |= unique_by(.name)' -i "$CSV_FILE"

echo "CSV updated successfully!"
echo ""
echo "Updated file: $CSV_FILE"
echo ""
echo "Next steps:"
echo "1. Review the changes: git diff $CSV_FILE"
echo "2. Rebuild bundle: make bundle"
echo "3. Commit changes: git add $CSV_FILE && git commit -m 'Update CSV with image digests'"

