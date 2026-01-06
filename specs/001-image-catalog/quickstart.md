# Quick Start Guide: Image Catalog

**Date**: 2026-01-04
**Feature**: Image Catalog Backend System

## Overview

The Image Catalog provides a centralized registry of published automotive OS images, enabling operations teams to discover, access, and deploy images across different environments. This guide demonstrates the core workflows for managing catalog images.

## Prerequisites

- Automotive Dev Operator installed and running
- `caib` CLI tool configured with Build API access
- Kubernetes cluster with appropriate RBAC permissions
- At least one completed ImageBuild (for publishing workflow)

## Core Workflows

### 1. View Available Images

**Scenario**: Operations team needs to find available automotive OS images for deployment.

```bash
# List all available images in the catalog
caib catalog list

# Expected output:
NAME                    REGISTRY                                     ARCHITECTURE  DISTRO  TARGET        PHASE       AGE
autosd-cs9-rpi4        quay.io/centos-automotive/autosd:cs9-rpi4    arm64         cs9     raspberry-pi  Available   2d
autosd-cs9-qemu        quay.io/centos-automotive/autosd:cs9-qemu    amd64         cs9     qemu          Available   1d
```

**Filter by hardware target**:
```bash
# Find images compatible with Raspberry Pi
caib catalog list --target raspberry-pi --architecture arm64

# Find images for specific distribution
caib catalog list --distro cs9 --phase Available
```

### 2. Get Image Details

**Scenario**: Operations team needs detailed specifications before deployment.

```bash
# Get complete image information
caib catalog get autosd-cs9-rpi4

# Expected output:
metadata:
  name: autosd-cs9-rpi4
  namespace: default
spec:
  registryUrl: quay.io/centos-automotive/autosd:cs9-rpi4-v1.0.0
  digest: sha256:abc123def456...
  metadata:
    architecture: arm64
    distro: cs9
    distroVersion: "9.4"
    targets:
      - name: raspberry-pi
        verified: true
        notes: "Tested on Pi 4 Model B (8GB)"
    kernelVersion: 6.1.0-rt
    bootcCapable: true
status:
  phase: Available
  registryMetadata:
    sizeBytes: 2147483648
    layerCount: 15
  lastVerificationTime: "2026-01-04T13:00:00Z"
```

### 3. Publish Build Results to Catalog

**Scenario**: Build team has completed an ImageBuild and wants to make it available in the catalog.

**Step 1**: Verify ImageBuild is complete
```bash
# Check build status
caib images list
# or
kubectl get imagebuild my-custom-build -o yaml
```

**Step 2**: Publish to catalog
```bash
# Publish the completed build
caib catalog publish my-custom-build --tags production,tested

# Expected output:
Publishing ImageBuild "my-custom-build" to catalog...
✓ ImageBuild status: Completed
✓ Registry location: quay.io/my-org/custom-build:v1.0.0
✓ Creating catalog image "my-custom-build"
✓ Verifying registry accessibility...
✓ Published successfully

Catalog Image: my-custom-build
Registry URL:   quay.io/my-org/custom-build:v1.0.0
Architecture:   arm64
Distro:        cs9
Target:        raspberry-pi
Status:        Available
```

**Step 3**: Verify catalog entry
```bash
caib catalog list | grep my-custom-build
caib catalog get my-custom-build
```

### 4. Add External Images to Catalog

**Scenario**: Platform team wants to add a reference image from an external registry.

```bash
# Add external reference image
caib catalog add reference-cs9 quay.io/centos-automotive/autosd:cs9-reference \
  --architecture amd64 \
  --distro cs9 \
  --distro-version "9.4" \
  --target qemu \
  --tags reference,baseline \
  --bootc-capable

# Expected output:
Adding image to catalog...
✓ Validating registry URL
✓ Creating catalog image "reference-cs9"
✓ Verifying registry accessibility...
✓ Added successfully

Catalog Image: reference-cs9
Registry URL:   quay.io/centos-automotive/autosd:cs9-reference
Architecture:   amd64
Distro:        cs9
Target:        qemu
Status:        Available
```

### 5. Working with Private Registries

**Scenario**: Adding images from private registries that require authentication.

**Step 1**: Create registry credentials secret
```bash
# Create secret with registry credentials
kubectl create secret docker-registry my-registry-creds \
  --docker-server=private-registry.example.com \
  --docker-username=myuser \
  --docker-password=mypassword \
  --docker-email=user@example.com
```

**Step 2**: Add image with authentication
```bash
caib catalog add private-image private-registry.example.com/automotive:v1.0.0 \
  --architecture arm64 \
  --distro cs9 \
  --target raspberry-pi \
  --auth-secret my-registry-creds \
  --tags private,custom
```

### 6. Verify Image Accessibility

**Scenario**: Operations team wants to verify an image is still accessible before deployment.

```bash
# Manually trigger verification
caib catalog verify my-custom-build

# Expected output:
Verifying catalog image "my-custom-build"...
✓ Registry accessibility: OK
✓ Digest verification: OK
✓ Metadata extraction: OK

Registry URL:     quay.io/my-org/custom-build:v1.0.0
Resolved Digest:  sha256:abc123...
Size:            1.2 GB
Layers:          12
Last Verified:   2026-01-04T14:30:00Z
Status:          Available
```

## Kubernetes Resource Examples

### Creating CatalogImage Directly

While CLI commands are preferred, you can also create catalog images using kubectl:

```yaml
apiVersion: automotive.sdv.cloud.redhat.com/v1alpha1
kind: CatalogImage
metadata:
  name: my-catalog-image
  namespace: default
  labels:
    automotive.sdv.cloud.redhat.com/architecture: "arm64"
    automotive.sdv.cloud.redhat.com/distro: "cs9"
    automotive.sdv.cloud.redhat.com/target: "raspberry-pi"
spec:
  registryUrl: "quay.io/my-org/automotive-image:v1.0.0"
  digest: "sha256:abc123def456..."
  tags:
    - "production"
    - "automotive"
  verificationInterval: "1h"
  metadata:
    architecture: "arm64"
    distro: "cs9"
    distroVersion: "9.4"
    targets:
      - name: "raspberry-pi"
        verified: true
        notes: "Tested on Pi 4 Model B"
    bootcCapable: true
```

```bash
kubectl apply -f catalog-image.yaml
```

### Checking Status

```bash
# Check catalog image status
kubectl get catalogimage my-catalog-image -o yaml

# Watch for status changes
kubectl get catalogimage my-catalog-image -w

# Check conditions
kubectl get catalogimage my-catalog-image -o jsonpath='{.status.conditions[*].type}'
```

## Integration Patterns

### With CI/CD Pipelines

```bash
# In build pipeline after successful image creation
#!/bin/bash
set -e

# Wait for ImageBuild to complete
kubectl wait --for=condition=Complete imagebuild/${BUILD_NAME} --timeout=30m

# Publish to catalog if build succeeded
if caib catalog publish ${BUILD_NAME} --wait --timeout=5m; then
    echo "✓ Successfully published to catalog"

    # Tag as production if this is a release build
    if [[ "$BRANCH" == "main" ]]; then
        caib catalog get ${BUILD_NAME} --output json | \
            jq '.spec.tags += ["production"]' | \
            kubectl apply -f -
    fi
else
    echo "✗ Failed to publish to catalog"
    exit 1
fi
```

### With Deployment Automation

```bash
# In deployment script
#!/bin/bash

# Find latest available image for target platform
IMAGE=$(caib catalog list --target raspberry-pi --phase Available --output json | \
    jq -r '.items | sort_by(.metadata.creationTimestamp) | reverse | .[0].spec.registryUrl')

if [[ "$IMAGE" == "null" ]]; then
    echo "No available images found for raspberry-pi"
    exit 1
fi

echo "Deploying image: $IMAGE"
# Use $IMAGE in deployment process
```

## Troubleshooting

### Image Shows as "Unavailable"

```bash
# Check detailed status
caib catalog get problematic-image

# Look at conditions for specific error
kubectl get catalogimage problematic-image -o jsonpath='{.status.conditions[?(@.type=="Available")].message}'

# Manually verify registry accessibility
caib catalog verify problematic-image
```

**Common causes**:
- Registry is temporarily unreachable
- Authentication credentials expired
- Image was deleted from registry
- Network connectivity issues

### Authentication Failures

```bash
# Check if secret exists and is valid
kubectl get secret my-registry-creds -o yaml

# Test registry access manually
podman login private-registry.example.com
podman pull private-registry.example.com/image:tag
```

### Performance Issues

```bash
# Check number of catalog images
kubectl get catalogimage --all-namespaces | wc -l

# Look for images stuck in "Verifying" phase
caib catalog list --phase Verifying

# Check controller logs
kubectl logs -n automotive-dev-operator-system deployment/ado-controller-manager
```

## Next Steps

After completing this quickstart:

1. **Operations Teams**: Integrate catalog queries into deployment automation
2. **Build Teams**: Set up automatic publishing for successful builds
3. **Platform Teams**: Establish image promotion workflows (dev → staging → production)
4. **Security Teams**: Implement image verification and signing workflows

## API Reference

For programmatic access, refer to:
- OpenAPI specification: `specs/001-image-catalog/contracts/api-spec.yaml`
- CLI reference: `specs/001-image-catalog/contracts/cli-commands.md`
- Kubernetes CRD documentation: `kubectl explain catalogimage`