# Research Report: Image Catalog Implementation

**Date**: 2026-01-04
**Feature**: Image Catalog Backend System

## Research Summary

This report consolidates research findings for implementing a Kubernetes-native image catalog system for automotive OS images. Research covered three critical areas: container registry integration, automotive metadata standards, and Kubernetes CRD design patterns.

## Container Registry Integration

### Decision: Use `containers/image/v5` Library
**Rationale**: Already integrated in the codebase (`cmd/caib/main.go`), provides comprehensive multi-registry support, and is maintained by Red Hat with excellent OpenShift compatibility.

**Capabilities**:
- Works with Docker Hub, Quay.io, OpenShift internal registry, and OCI-compliant registries
- Built-in authentication using Docker/Podman auth files
- Manifest inspection without downloading entire images
- Format translation between different registry types

**Authentication Strategy**:
1. **Primary**: Explicit ImagePullSecrets referenced in CatalogImage spec
2. **Fallback**: Controller service account tokens for OpenShift internal registry
3. **Default**: Standard auth files (`~/.docker/config.json`, containers auth locations)

**Implementation Pattern**:
```go
// Service account token for OpenShift internal registry
auth := &types.DockerAuthConfig{
    Username: "serviceaccount",  // OpenShift special username
    Password: token,             // Service account token
}
```

### Alternatives Considered
- `google/go-containerregistry`: Available as dependency (v0.20.6) but adds complexity
- Direct registry APIs: Too low-level, lacks multi-registry abstraction

## Automotive Metadata Standards

### Decision: Extend Existing ImageBuild Schema
**Rationale**: Build on proven patterns from automotive-image-builder (AIB) and existing CRD structure.

**Core Metadata Fields**:
- **Architecture**: `amd64`, `arm64` (OCI standard) with normalization from AIB values (`x86_64`, `aarch64`)
- **Distro**: `cs9` (CentOS Stream 9), `autosd10-sig` (established AIB conventions)
- **Target**: `qemu`, `raspberry-pi`, `beaglebone` (extensible for new hardware)
- **Export Format**: `qcow2`, `raw`, `vmdk`, `container` (build artifact types)
- **Build Mode**: `bootc`, `image`, `package` (AIB build strategies)

**Hardware Target Support**:
- Primary: `qemu` (virtual), `raspberry-pi` (Pi 4 has official AutoSD support)
- Extensible: Additional targets via AIB's hardware enablement system
- Verification: Track which targets have been tested vs. theoretically supported

**OCI Annotation Mapping**:
```yaml
# Standard annotations
org.opencontainers.image.created: "2026-01-04T12:00:00Z"
org.opencontainers.image.source: "https://gitlab.com/org/project"

# Automotive-specific (reverse-DNS naming)
com.redhat.automotive.distro: "cs9"
com.redhat.automotive.target: "raspberry-pi"
com.redhat.automotive.bootc-capable: "true"
com.redhat.automotive.aib.version: "1.0.0"
```

### Alternatives Considered
- Generic container image schemas: Lack automotive-specific fields
- Custom metadata format: Breaks OCI compliance and tooling compatibility

## Kubernetes CRD Design Patterns

### Decision: Follow Kubebuilder Best Practices
**Rationale**: Aligns with existing operator architecture, provides stability guarantees, and enables efficient scaling.

**API Versioning Strategy**:
- Start with `v1alpha1` (current project standard)
- Follow progression: `v1alpha1` → `v1beta1` → `v1` with conversion webhooks
- Hub-and-spoke conversion model for backward compatibility

**Status Subresource Design**:
```go
type CatalogImageStatus struct {
    ObservedGeneration int64 `json:"observedGeneration,omitempty"`
    Phase              string `json:"phase,omitempty"` // Pending, Verifying, Available, Unavailable
    Conditions         []metav1.Condition `json:"conditions,omitempty"`
    RegistryMetadata   *RegistryMetadata `json:"registryMetadata,omitempty"`
    LastVerificationTime *metav1.Time `json:"lastVerificationTime,omitempty"`
}
```

**Controller Patterns**:
- **Finalizers**: Clean up external resources (cache entries, registry subscriptions)
- **Circuit Breaker**: Prevent overwhelming failing registries with repeated requests
- **Retry with Backoff**: Handle transient registry failures gracefully
- **Field Indexing**: Enable efficient catalog queries without full cache scans

**Performance Optimizations**:
- Index frequently queried fields: `spec.registryUrl`, `spec.digest`, `status.phase`, `spec.tags`
- Use status subresource to prevent reconcile loops on status-only updates
- Implement pagination for large catalog listings

### Alternatives Considered
- REST API only: Loses Kubernetes-native benefits (RBAC, events, watches)
- ConfigMap storage: Doesn't scale, lacks proper status tracking
- CRD without status subresource: Reduces separation of concerns and RBAC granularity

## Architectural Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| **Registry Client** | `containers/image/v5` | Already integrated, comprehensive support |
| **Authentication** | Kubernetes service account tokens | Constitution compliance, secure |
| **Metadata Schema** | Extend ImageBuild patterns | Consistency with existing AIB workflow |
| **API Version** | `v1alpha1` | Match existing CRD maturity level |
| **Storage** | CRD with status subresource | Kubernetes-native, proper separation |
| **Performance** | Field indexing + circuit breakers | Handle scale and registry failures |

## Implementation Risks and Mitigations

**Registry Availability**: Circuit breaker pattern prevents overwhelming failed registries
**Scale**: Field indexing enables efficient queries on thousands of catalog entries
**Security**: RBAC separation between spec and status, credential management via secrets
**Compatibility**: OCI compliance ensures tooling compatibility, API versioning for evolution

## Next Phase

Phase 1 will design the specific data models and API contracts based on these research decisions, ensuring alignment with the automotive operator's existing patterns and constitutional requirements.