# Implementation Plan: Image Catalog

**Branch**: `001-image-catalog` | **Date**: 2026-01-04 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/001-image-catalog/spec.md`

**Note**: This template is filled in by the `/speckit.plan` command. See `.specify/templates/commands/plan.md` for the execution workflow.

## Summary

Backend system for managing published automotive OS images in a discoverable catalog. Operations teams can browse, filter, and access detailed metadata about images built via ImageBuild or published from external sources. Images are stored in container registries (OpenShift internal, Quay) with automotive-specific metadata (architecture, distro, target hardware) to support hardware-specific deployment decisions.

## Technical Context

**Language/Version**: Go 1.22+ (consistent with existing operator codebase)
**Primary Dependencies**: Kubebuilder, controller-runtime, Kubernetes client-go, container registry client libraries
**Storage**: Kubernetes etcd (via Custom Resources), container registries for image artifacts
**Testing**: Go testing package, Ginkgo/Gomega for controller integration tests
**Target Platform**: Kubernetes clusters (OpenShift preferred, generic K8s compatible)
**Project Type**: Kubernetes operator extension (controller + API resources)
**Performance Goals**: 1000+ concurrent catalog queries, <30s image publication operations
**Constraints**: <5s catalog query response, Kubernetes RBAC compliance, registry accessibility validation
**Scale/Scope**: 1000+ catalog images, multi-registry support, automotive metadata complexity

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

**POST-DESIGN RE-CHECK: ✅ PASS** - All requirements remain satisfied after detailed design.

### I. Controller-First Architecture ✅
- **PASS**: Feature extends existing operator with new CatalogImage Custom Resource
- **PASS**: Controller reconciliation loop manages catalog image lifecycle
- **PASS**: Stateless controller design with idempotent operations

### II. Kubernetes-Native Design ✅
- **PASS**: Functionality exposed via Custom Resources (CatalogImage)
- **PASS**: Status subresources track publication state and registry health
- **PASS**: Events communicate publication progress and errors
- **PASS**: RBAC controls catalog access permissions

### III. Container-First Builds ✅
- **PASS**: All image artifacts stored in container registries
- **PASS**: No host dependencies beyond container runtime
- **PASS**: Build integration via existing ImageBuild controller

### IV. API Compatibility ✅
- **PASS**: New CRD follows Kubernetes API versioning (v1alpha1)
- **PASS**: Backward compatibility maintained through API versioning
- **PASS**: Field deprecation follows standard lifecycle

### V. Observable Operations ✅
- **PASS**: Structured logging for all catalog operations
- **PASS**: Metrics exposed for catalog query performance
- **PASS**: Status conditions reflect registry accessibility and publication state
- **PASS**: Build progress tracked via existing ImageBuild integration

### Operator Constraints ✅
- **PASS**: Single responsibility controller for catalog management
- **PASS**: Communication via Kubernetes APIs and existing Build API
- **PASS**: No tight coupling with optional components
- **PASS**: Service account token authentication for registries

**GATE RESULT: PASS** - All constitutional requirements satisfied

## Project Structure

### Documentation (this feature)

```text
specs/[###-feature]/
├── plan.md              # This file (/speckit.plan command output)
├── research.md          # Phase 0 output (/speckit.plan command)
├── data-model.md        # Phase 1 output (/speckit.plan command)
├── quickstart.md        # Phase 1 output (/speckit.plan command)
├── contracts/           # Phase 1 output (/speckit.plan command)
└── tasks.md             # Phase 2 output (/speckit.tasks command - NOT created by /speckit.plan)
```

### Source Code (repository root)

```text
api/v1alpha1/
├── catalogimage_types.go    # CatalogImage CRD definition
├── registry_types.go        # Registry configuration types
└── groupversion_info.go     # API group versioning

internal/controller/
├── catalogimage/            # CatalogImage controller
│   ├── catalogimage_controller.go
│   ├── publisher.go         # Image publication logic
│   └── registry.go          # Registry interaction
└── suite_test.go            # Controller test suite

internal/buildapi/
├── catalog/                 # Catalog API endpoints
│   ├── handlers.go          # REST API handlers
│   ├── models.go           # API response models
│   └── routes.go           # Route registration
└── middleware.go           # Existing API middleware

cmd/caib/
└── catalog/                # CLI catalog commands
    ├── list.go             # List catalog images
    ├── publish.go          # Publish image to catalog
    └── get.go              # Get image details

tests/
├── e2e/
│   ├── catalog_test.go     # End-to-end catalog tests
│   └── registry_test.go    # Registry integration tests
└── integration/
    └── controller_test.go  # Controller integration tests
```

**Structure Decision**: Kubernetes operator extension following existing project conventions. New CatalogImage controller extends operator capabilities while Build API provides REST endpoints for CLI and external access. CLI commands integrate with existing caib tool structure.

## Complexity Tracking

*No constitutional violations - this section intentionally left empty*
