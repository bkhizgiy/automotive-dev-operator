# CLI Command Contracts: caib catalog

**Date**: 2026-01-04
**Feature**: Image Catalog CLI Commands

## Command Structure

All catalog commands are under the `caib catalog` subcommand, following the existing CLI patterns.

```bash
caib catalog <command> [flags] [args]
```

## Commands

### `caib catalog list`

List images in the catalog with filtering options.

**Usage**:
```bash
caib catalog list [flags]
```

**Flags**:
```bash
  -n, --namespace string      Kubernetes namespace (default: current context namespace)
      --architecture string   Filter by architecture (amd64, arm64)
      --distro string         Filter by distribution (cs9, autosd10-sig)
      --target string         Filter by hardware target (qemu, raspberry-pi, beaglebone)
      --phase string          Filter by phase (Available, Unavailable, etc.)
      --tags string           Filter by tags (comma-separated)
      --output string         Output format (table, json, yaml) (default "table")
      --limit int             Maximum results to show (default 20)
      --all-namespaces        List images across all namespaces
```

**Output Formats**:

*Table (default)*:
```
NAME                    REGISTRY                                     ARCHITECTURE  DISTRO  TARGET        PHASE       AGE
autosd-cs9-rpi4        quay.io/centos-automotive/autosd:cs9-rpi4    arm64         cs9     raspberry-pi  Available   2d
autosd-cs9-qemu        quay.io/centos-automotive/autosd:cs9-qemu    amd64         cs9     qemu          Available   1d
custom-build-001       registry.local/custom:v1.0.0                arm64         cs9     raspberry-pi  Verifying   5h
```

*JSON*:
```json
{
  "items": [
    {
      "metadata": {"name": "autosd-cs9-rpi4", "namespace": "default"},
      "spec": {"registryUrl": "quay.io/centos-automotive/autosd:cs9-rpi4"},
      "status": {"phase": "Available"}
    }
  ]
}
```

**Examples**:
```bash
# List all available images
caib catalog list

# List images for Raspberry Pi
caib catalog list --target raspberry-pi

# List images in JSON format
caib catalog list --output json

# List ARM64 images across all namespaces
caib catalog list --architecture arm64 --all-namespaces
```

### `caib catalog get`

Get detailed information about a specific catalog image.

**Usage**:
```bash
caib catalog get <name> [flags]
```

**Flags**:
```bash
  -n, --namespace string   Kubernetes namespace (default: current context namespace)
      --output string      Output format (yaml, json) (default "yaml")
```

**Output**:
```yaml
metadata:
  name: autosd-cs9-rpi4
  namespace: default
  creationTimestamp: "2026-01-02T10:00:00Z"
spec:
  registryUrl: quay.io/centos-automotive/autosd:cs9-rpi4-v1.0.0
  digest: sha256:abc123def456...
  tags: [production, automotive, embedded]
  verificationInterval: 1h
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
  lastVerificationTime: "2026-01-04T13:00:00Z"
  registryMetadata:
    resolvedDigest: sha256:def456abc123...
    sizeBytes: 2147483648
    layerCount: 15
  conditions:
    - type: Available
      status: "True"
      reason: RegistryAccessible
      message: Image is accessible in registry
```

**Examples**:
```bash
# Get image details
caib catalog get autosd-cs9-rpi4

# Get image details in JSON
caib catalog get autosd-cs9-rpi4 --output json
```

### `caib catalog publish`

Publish a completed ImageBuild to the catalog.

**Usage**:
```bash
caib catalog publish <imagebuild-name> [flags]
```

**Flags**:
```bash
  -n, --namespace string           Kubernetes namespace of ImageBuild (default: current context namespace)
      --catalog-name string        Name for catalog image (default: ImageBuild name)
      --catalog-namespace string   Namespace for catalog image (default: ImageBuild namespace)
      --tags strings               Additional tags to apply (can be used multiple times)
      --verify                     Verify registry accessibility after publishing (default true)
      --wait                       Wait for publishing to complete (default true)
      --timeout duration           Timeout for waiting (default 5m)
```

**Output**:
```
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

**Examples**:
```bash
# Publish ImageBuild to catalog
caib catalog publish my-build

# Publish with custom catalog name and tags
caib catalog publish my-build --catalog-name production-image --tags production,stable

# Publish without waiting
caib catalog publish my-build --wait=false
```

### `caib catalog add`

Manually add an external image to the catalog.

**Usage**:
```bash
caib catalog add <name> <registry-url> [flags]
```

**Flags**:
```bash
  -n, --namespace string        Kubernetes namespace (default: current context namespace)
      --architecture string     Image architecture (amd64, arm64) (required)
      --distro string          Distribution identifier (required)
      --distro-version string  Distribution version
      --target strings         Hardware targets (can be used multiple times)
      --tags strings           Tags to apply (can be used multiple times)
      --digest string          Specific digest to reference
      --auth-secret string     Secret containing registry credentials
      --verify                 Verify registry accessibility (default true)
      --bootc-capable          Mark as bootc-capable
      --kernel-version string  Kernel version in the image
```

**Output**:
```
Adding image to catalog...
✓ Validating registry URL
✓ Creating catalog image "external-image"
✓ Verifying registry accessibility...
✓ Added successfully

Catalog Image: external-image
Registry URL:   external-registry.example.com/automotive:latest
Architecture:   arm64
Distro:        cs9
Target:        raspberry-pi
Status:        Available
```

**Examples**:
```bash
# Add external image
caib catalog add external-image external-registry.example.com/automotive:latest \
  --architecture arm64 --distro cs9 --target raspberry-pi

# Add with authentication
caib catalog add secure-image private-registry.com/image:v1.0.0 \
  --architecture amd64 --distro autosd10-sig --auth-secret registry-creds
```

### `caib catalog remove`

Remove an image from the catalog.

**Usage**:
```bash
caib catalog remove <name> [flags]
```

**Flags**:
```bash
  -n, --namespace string   Kubernetes namespace (default: current context namespace)
      --force              Skip confirmation prompt
```

**Output**:
```
Removing catalog image "old-image"...
Are you sure you want to remove this image from the catalog? (y/N): y
✓ Removed successfully
```

**Examples**:
```bash
# Remove image (with confirmation)
caib catalog remove old-image

# Force remove without confirmation
caib catalog remove old-image --force
```

### `caib catalog verify`

Manually trigger verification of a catalog image.

**Usage**:
```bash
caib catalog verify <name> [flags]
```

**Flags**:
```bash
  -n, --namespace string   Kubernetes namespace (default: current context namespace)
      --wait               Wait for verification to complete (default true)
      --timeout duration   Timeout for waiting (default 2m)
```

**Output**:
```
Verifying catalog image "test-image"...
✓ Registry accessibility: OK
✓ Digest verification: OK
✓ Metadata extraction: OK

Registry URL:     quay.io/test/image:latest
Resolved Digest:  sha256:abc123...
Size:            1.2 GB
Layers:          12
Last Verified:   2026-01-04T14:30:00Z
Status:          Available
```

**Examples**:
```bash
# Verify image
caib catalog verify my-image

# Verify without waiting
caib catalog verify my-image --wait=false
```

## Global Flags

All catalog commands inherit global caib flags:

```bash
      --server string       Build API server URL (env: CAIB_SERVER)
      --kubeconfig string   Path to kubeconfig file
      --context string      Kubernetes context to use
      --token string        Authentication token (env: CAIB_TOKEN)
      --timeout duration    Request timeout (default 30s)
  -v, --verbose            Verbose output
      --dry-run            Show what would be done without executing
```

## Configuration

Commands use the same configuration sources as other caib commands:

1. Command-line flags
2. Environment variables (`CAIB_SERVER`, `CAIB_TOKEN`)
3. kubeconfig file for Kubernetes authentication
4. caib config file (`~/.config/caib/config.yaml`)

## Error Handling

**Exit Codes**:
- `0`: Success
- `1`: General error
- `2`: Invalid arguments or flags
- `3`: Authentication/authorization error
- `4`: Resource not found
- `5`: Resource conflict (already exists)
- `6`: Timeout

**Error Messages**:
```bash
# Resource not found
Error: catalog image "missing-image" not found in namespace "default"

# Authentication error
Error: failed to authenticate with build API: invalid token

# Registry unreachable
Error: failed to verify registry accessibility: connection timeout
```

## Integration with Existing Commands

### Relationship with `caib build`

The catalog integrates with existing build workflow:

```bash
# Traditional workflow
caib build my-manifest.aib.yml
caib images list  # Shows completed builds

# Enhanced workflow with catalog
caib build my-manifest.aib.yml
caib catalog publish my-build  # Promote to catalog
caib catalog list  # Shows published images
```

### Relationship with `caib images`

- `caib images`: Shows all Image CRs (build results)
- `caib catalog`: Shows published/curated catalog images
- Catalog images may reference Image CRs but are separate entities

## Command Examples by User Journey

### Operations Team Discovery
```bash
# Find available ARM64 images for Raspberry Pi
caib catalog list --architecture arm64 --target raspberry-pi

# Get detailed specs for deployment planning
caib catalog get autosd-cs9-rpi4
```

### Build Team Publication
```bash
# Publish completed build to catalog
caib catalog publish my-custom-build --tags production,tested

# Verify published image is accessible
caib catalog verify my-custom-build
```

### Platform Team Management
```bash
# Add reference images to catalog
caib catalog add reference-cs9 quay.io/centos-automotive/autosd:cs9-reference \
  --architecture amd64 --distro cs9 --target qemu --tags reference,baseline

# Remove deprecated images
caib catalog remove deprecated-image --force
```