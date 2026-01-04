# Feature Specification: Image Catalog

**Feature Branch**: `001-image-catalog`
**Created**: 2026-01-04
**Status**: Draft
**Input**: User description: "I want to implement an image catalog feature. These images will represent built OS images (from ImageBuild) that were published. These could be prebuilt images that users can access immediately, or custom images built, that were promoted to the catalog, by some publish mechanism. The storage for all of those will by default be a container registry, usually the internal openshift registry, but could also be some quay registry, so it should be generic in this regard. Since the images are built with automotive-image-builder (agent exists), they have certain architectures, targets, distros etc. This should be represented, as users would want to flash those to specific hardware boards later on. Focus only on the backend, i don't care about the frontend."

## User Scenarios & Testing *(mandatory)*

<!--
  IMPORTANT: User stories should be PRIORITIZED as user journeys ordered by importance.
  Each user story/journey must be INDEPENDENTLY TESTABLE - meaning if you implement just ONE of them,
  you should still have a viable MVP (Minimum Viable Product) that delivers value.
  
  Assign priorities (P1, P2, P3, etc.) to each story, where P1 is the most critical.
  Think of each story as a standalone slice of functionality that can be:
  - Developed independently
  - Tested independently
  - Deployed independently
  - Demonstrated to users independently
-->

### User Story 1 - View Available Images (Priority: P1)

Operations teams need to discover what OS images are available for deployment to automotive hardware. They want to see images that have been successfully built and published, along with their technical specifications, so they can select appropriate images for specific hardware targets.

**Why this priority**: This is the foundational capability that enables all other catalog functions. Without the ability to browse available images, the catalog provides no value.

**Independent Test**: Can be fully tested by querying the catalog and verifying that published images are returned with complete metadata including architecture, distribution, and registry location.

**Acceptance Scenarios**:

1. **Given** the catalog contains published images, **When** a user queries available images, **Then** all published images are returned with metadata
2. **Given** an image has specific hardware targets, **When** a user filters by architecture, **Then** only compatible images are shown
3. **Given** multiple image versions exist, **When** a user queries the catalog, **Then** images are sorted by publication date with newest first

---

### User Story 2 - Publish Built Images (Priority: P2)

Build operators need to promote successfully completed ImageBuild results to the catalog so they become available for deployment. This includes both custom builds and standardized prebuilt images that should be accessible to operations teams.

**Why this priority**: This enables the catalog to be populated with usable images, creating value for users who want to access built images.

**Independent Test**: Can be fully tested by triggering a publish operation on a completed ImageBuild and verifying the image appears in the catalog with correct metadata.

**Acceptance Scenarios**:

1. **Given** an ImageBuild has completed successfully, **When** a publish operation is triggered, **Then** the image appears in the catalog with metadata from the build
2. **Given** a prebuilt image exists in a registry, **When** it is published to the catalog, **Then** it becomes discoverable with proper metadata tags
3. **Given** an image already exists in the catalog, **When** the same image is published again, **Then** the catalog prevents duplicate entries

---

### User Story 3 - Access Image Details (Priority: P3)

Operations teams need to retrieve detailed information about specific catalog images to make deployment decisions. This includes technical specifications like supported architectures, target distributions, and registry locations needed for image flashing to hardware boards.

**Why this priority**: Detailed image information is essential for proper deployment planning but builds on the foundational browse capability.

**Independent Test**: Can be fully tested by selecting a specific catalog image and verifying all technical metadata is available and accurate.

**Acceptance Scenarios**:

1. **Given** an image exists in the catalog, **When** a user requests image details, **Then** complete metadata including architecture, distro, targets, and registry info is returned
2. **Given** an image has multiple architecture variants, **When** a user accesses image details, **Then** all available variants are listed with their specifications
3. **Given** an image has build artifacts, **When** a user views image details, **Then** download URLs and checksums are provided

### Edge Cases

- What happens when a registry becomes unavailable but images are still in the catalog?
- How does the system handle images that exist in the catalog but have been deleted from the registry?
- What occurs when image metadata is corrupted or incomplete during publication?
- How are versioning conflicts resolved when the same image tag is published multiple times?

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST store catalog entries for published OS images with complete metadata
- **FR-002**: System MUST support publishing images from completed ImageBuild operations
- **FR-003**: System MUST support publishing prebuilt images from external sources
- **FR-004**: System MUST track image metadata including architecture, distribution, target hardware, and registry location
- **FR-005**: System MUST provide query capabilities to filter images by architecture, distribution, and target type
- **FR-006**: System MUST support multiple container registry backends including OpenShift internal registry and external registries
- **FR-007**: System MUST prevent duplicate catalog entries for identical images
- **FR-008**: System MUST validate registry accessibility before allowing image publication
- **FR-009**: System MUST maintain audit trail of image publication operations
- **FR-010**: System MUST support image versioning and track publication timestamps
- **FR-011**: System MUST handle registry authentication using Kubernetes service account tokens

### Key Entities *(include if feature involves data)*

- **CatalogImage**: Represents a published OS image with metadata including registry location, architecture specifications, distribution type, target hardware compatibility, publication timestamp, and build source information
- **Registry**: Represents a container registry backend with authentication credentials and accessibility configuration
- **ImageMetadata**: Contains technical specifications extracted from automotive-image-builder including supported architectures, target distributions, hardware compatibility, and build parameters

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Users can discover available images within 5 seconds of querying the catalog
- **SC-002**: Image publication operations complete within 30 seconds for standard-sized OS images
- **SC-003**: Catalog supports at least 1000 concurrent image metadata queries without performance degradation
- **SC-004**: 95% of published images maintain registry accessibility over 24-hour periods
- **SC-005**: Image metadata query results are 100% accurate compared to actual registry contents
- **SC-006**: Zero duplicate catalog entries exist after publishing the same image multiple times
