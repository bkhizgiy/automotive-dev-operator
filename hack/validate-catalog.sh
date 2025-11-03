#!/bin/bash
set -e

echo "=========================================="
echo "Validating Operator Catalog"
echo "=========================================="

# Check if opm is available
if ! command -v bin/opm &> /dev/null; then
    echo "OPM tool not found. Downloading..."
    make opm
fi

OPM_CMD=bin/opm

echo ""
if [ ! -d "catalog" ]; then
    echo "ERROR: catalog directory not found!"
    exit 1
fi

if [ ! -f "catalog/automotive-dev-operator.yaml" ]; then
    echo "ERROR: catalog/automotive-dev-operator.yaml not found!"
    exit 1
fi

echo "✓ Catalog directory structure is valid"

echo ""
echo "Step 2: Validating FBC format..."
${OPM_CMD} validate catalog/

if [ $? -eq 0 ]; then
    echo "validation passed"
else
    echo "validation failed"
    exit 1
fi

echo ""
echo "Step 3: Rendering catalog for inspection..."
${OPM_CMD} render catalog/automotive-dev-operator.yaml

if [ -d "bundle" ]; then
    if command -v operator-sdk &> /dev/null || [ -f "bin/operator-sdk" ]; then
        OPERATOR_SDK_CMD="bin/operator-sdk"
        if command -v operator-sdk &> /dev/null; then
            OPERATOR_SDK_CMD="operator-sdk"
        fi

        echo "Validating bundle with operator-sdk..."
        ${OPERATOR_SDK_CMD} bundle validate ./bundle

        if [ $? -eq 0 ]; then
            echo "Bundle validation passed"
        else
            echo "Bundle validation failed"
            exit 1
        fi
    else
        echo "operator-sdk not found, skipping bundle validation"
    fi
else
    echo "Bundle directory not found, skipping bundle validation"
fi

echo ""
echo "=========================================="
echo "Validation Complete!"
echo "=========================================="
echo ""
echo "Your catalog is ready to be built and deployed."
echo ""
echo "Next steps:"
echo "  1. Build: make catalog-build"
echo "  2. Push: make catalog-push"
echo "  3. Deploy: kubectl apply -f catalogsource.yaml"
echo ""
echo "Or use the automated script:"
echo "  ./deploy-catalog.sh"
echo ""

