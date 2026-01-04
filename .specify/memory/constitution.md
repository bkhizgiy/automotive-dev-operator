<!--
Sync Impact Report:
- Version: initial → 1.0.0 (initial constitution creation)
- Principles: 5 core principles defined for Kubernetes operator development
- Added sections: Operator-specific constraints, quality assurance practices
- Templates requiring updates: ✅ reviewed all templates
- Follow-up TODOs: none
-->

# Automotive Dev Operator Constitution

## Core Principles

### I. Controller-First Architecture
Every feature begins as a controller reconciliation loop or extends an existing controller; Controllers must be stateless, idempotent, and handle partial failures gracefully; Clear separation between controller logic, business logic, and Kubernetes API interactions required.

### II. Kubernetes-Native Design
All functionality exposed via Custom Resources and standard Kubernetes patterns; Status subresources track operations; Events communicate progress; RBAC controls access; No bypassing Kubernetes APIs for core functionality.

### III. Container-First Builds (NON-NEGOTIABLE)
All build operations run in containers; Build environments are reproducible and versioned; No host dependencies beyond container runtime; Build artifacts must be container-deliverable.

### IV. API Compatibility
Backward compatibility maintained for Custom Resource APIs; API versioning follows Kubernetes conventions; Breaking changes require new API versions; Field deprecation follows standard lifecycle.

### V. Observable Operations
Structured logging for all operations; Metrics exposed for monitoring; Status conditions reflect current state; Build progress tracked and reportable; Debugging information available without cluster access.

## Operator Constraints

### Multi-Component Architecture
Operator consists of: controller manager, build API, CLI tool, and WebUI; Each component has single responsibility; Components communicate via Kubernetes APIs or REST; No tight coupling between optional components.

### Build Integration Standards
Tekton TaskRuns for all build execution; automotive-image-builder as primary build tool; Registry storage for artifacts; OpenShift Routes for artifact serving where available.

### Authentication & Authorization
OpenShift OAuth integration for web components; Kubernetes RBAC for API access; Service account tokens for inter-component communication; No shared secrets between environments.

## Quality Assurance

### Testing Requirements
Unit tests for controller reconciliation logic; Integration tests for CRD operations; E2E tests for complete build workflows; Mock external dependencies in unit tests.

### Code Generation
Run `make generate manifests` after API changes; Kubebuilder annotations drive RBAC generation; CRD schemas auto-generated from Go types; No manual YAML maintenance for generated resources.

### Release Management
Semantic versioning for operator releases; Multi-arch container images; Pinned installation manifests; CLI binaries for supported architectures.

## Governance

Constitution supersedes all other development practices; Changes require documentation of rationale and impact assessment; Breaking changes need migration plans; All reviews verify compliance with these principles.

Use CLAUDE.md for runtime development guidance and coding standards; Complexity beyond these principles must be justified in design documents; Prefer Kubernetes patterns over custom solutions.

**Version**: 1.0.0 | **Ratified**: 2026-01-04 | **Last Amended**: 2026-01-04