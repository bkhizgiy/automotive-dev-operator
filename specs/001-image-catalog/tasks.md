---

description: "Task list for Image Catalog feature implementation"
---

# Tasks: Image Catalog

**Input**: Design documents from `/specs/001-image-catalog/`
**Prerequisites**: plan.md (required), spec.md (required for user stories), research.md, data-model.md, contracts/

**Tests**: Tests are OPTIONAL and only included if explicitly requested in the feature specification.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- **Kubernetes operator**: `api/v1alpha1/`, `internal/controller/`, `cmd/caib/` at repository root
- Paths follow existing automotive-dev-operator project structure

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Project initialization and basic CRD structure

- [X] T001 Create CatalogImage CRD types in api/v1alpha1/catalogimage_types.go
- [X] T002 Add CatalogImage to groupversion scheme in api/v1alpha1/groupversion_info.go
- [X] T003 [P] Generate CRD manifests using make generate manifests
- [X] T004 [P] Update main.go to register CatalogImage controller in cmd/main.go

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core infrastructure that MUST be complete before ANY user story can be implemented

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [X] T005 Create CatalogImage controller scaffold in internal/controller/catalogimage/catalogimage_controller.go
- [X] T006 [P] Implement registry client interface in internal/controller/catalogimage/registry.go
- [X] T007 [P] Add RBAC annotations and permissions for CatalogImage controller
- [X] T008 Implement finalizer handling pattern in catalogimage_controller.go
- [X] T009 Add controller field indexing for efficient queries in SetupWithManager
- [X] T010 Create base status update methods with proper condition handling
- [X] T011 [P] Configure controller manager to watch CatalogImage resources
- [X] T012 [P] Add controller metrics and logging infrastructure
- [X] T012a [P] Create audit event helper in internal/controller/catalogimage/audit.go

**Checkpoint**: Foundation ready - user story implementation can now begin in parallel

---

## Phase 3: User Story 1 - View Available Images (Priority: P1) 🎯 MVP

**Goal**: Operations teams can discover and filter available automotive OS images with metadata

**Independent Test**: Query the catalog via Build API and verify published images are returned with complete metadata including architecture, distribution, and registry location

### Implementation for User Story 1

- [X] T013 [P] [US1] Implement CatalogImage reconciliation loop for registry verification in catalogimage_controller.go
- [X] T014 [P] [US1] Create registry accessibility checker in internal/controller/catalogimage/registry.go
- [X] T015 [US1] Add phase state transitions (Pending → Verifying → Available) in controller
- [X] T016 [US1] Implement metadata extraction from container registry manifests including publication timestamp and created date
- [X] T017 [US1] Add Build API catalog list endpoint in internal/buildapi/catalog/handlers.go
- [X] T018 [P] [US1] Create catalog API models for responses in internal/buildapi/catalog/models.go
- [X] T019 [P] [US1] Add catalog route registration in internal/buildapi/catalog/routes.go
- [X] T020 [US1] Implement filtering by architecture, distro, and phase in list handler
- [X] T021 [P] [US1] Add caib catalog list command in cmd/caib/catalog/list.go
- [X] T022 [US1] Integrate list command with Build API client in caib CLI

**Checkpoint**: At this point, User Story 1 should be fully functional and testable independently

---

## Phase 4: User Story 2 - Publish Built Images (Priority: P2)

**Goal**: Build operators can promote completed ImageBuild results to the catalog

**Independent Test**: Trigger publish operation on completed ImageBuild and verify image appears in catalog with correct metadata

### Implementation for User Story 2

- [X] T023 [P] [US2] Implement image publishing logic with audit events and publication timestamp recording in internal/controller/catalogimage/publisher.go
- [X] T024 [P] [US2] Add ImageBuild integration to extract metadata during publishing
- [X] T025 [US2] Create Build API publish endpoint in internal/buildapi/catalog/handlers.go
- [X] T026 [US2] Add duplicate detection logic to prevent catalog conflicts
- [X] T027 [US2] Implement registry authentication using service account tokens
- [X] T028 [P] [US2] Add caib catalog publish command in cmd/caib/catalog/publish.go
- [X] T029 [US2] Add validation for ImageBuild completion status before publishing
- [X] T030 [US2] Implement external image publishing for prebuilt images
- [X] T031 [P] [US2] Add caib catalog add command for external images in cmd/caib/catalog/add.go
- [X] T032 [US2] Add automatic label generation for catalog indexing

**Checkpoint**: At this point, User Stories 1 AND 2 should both work independently

---

## Phase 5: User Story 3 - Access Image Details (Priority: P3)

**Goal**: Operations teams can retrieve detailed technical specifications for deployment planning

**Independent Test**: Select specific catalog image and verify all technical metadata is available and accurate

### Implementation for User Story 3

- [X] T033 [P] [US3] Add Build API get image endpoint in internal/buildapi/catalog/handlers.go
- [X] T034 [P] [US3] Implement detailed metadata extraction including layer info and platform data
- [X] T035 [US3] Add registry digest verification and checksum validation
- [X] T036 [P] [US3] Add caib catalog get command in cmd/caib/catalog/get.go
- [X] T037 [US3] Implement manual verification trigger endpoint for image accessibility
- [X] T038 [P] [US3] Add caib catalog verify command in cmd/caib/catalog/verify.go
- [X] T039 [US3] Add download URL and artifact reference resolution
- [X] T040 [US3] Implement multi-architecture variant support in metadata response

**Checkpoint**: All user stories should now be independently functional

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Improvements that affect multiple user stories

- [X] T041 [P] Add periodic registry health checking with configurable intervals
- [X] T042 [P] Implement circuit breaker pattern for failing registries
- [X] T043 [P] Add catalog image removal functionality in caib catalog remove command
- [X] T044 Add comprehensive error handling and user-friendly error messages
- [X] T045 [P] Add controller observability metrics for Prometheus
- [X] T046 [P] Extend audit logging infrastructure for comprehensive catalog operation coverage
- [X] T047 Add image access count tracking in status
- [ ] T048 [P] Add backup and disaster recovery support for catalog metadata
- [X] T048a Handle registry-deleted images: update CatalogImage status to Unavailable with deletion reason
- [ ] T048b [P] Add metadata validation and recovery for corrupted/incomplete catalog entries
- [X] T048c [P] Implement graceful degradation when registry becomes temporarily unavailable
- [X] T049 Run quickstart.md validation against implemented functionality

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3+)**: All depend on Foundational phase completion
  - User stories can then proceed in parallel (if staffed)
  - Or sequentially in priority order (P1 → P2 → P3)
- **Polish (Final Phase)**: Depends on all desired user stories being complete

### User Story Dependencies

- **User Story 1 (P1)**: Can start after Foundational (Phase 2) - No dependencies on other stories
- **User Story 2 (P2)**: Can start after Foundational (Phase 2) - Independent of US1 but may reference shared registry client
- **User Story 3 (P3)**: Can start after Foundational (Phase 2) - Uses catalog data structure from US1 and US2

### Within Each User Story

- CRD types and controller foundation before API endpoints
- Controller reconciliation logic before CLI commands
- Registry client implementation before publishing logic
- API models before handlers
- Story implementation before cross-cutting improvements

### Parallel Opportunities

- All Setup tasks marked [P] can run in parallel
- All Foundational tasks marked [P] can run in parallel (within Phase 2)
- Once Foundational phase completes, all user stories can start in parallel (if team capacity allows)
- Models, CLI commands, and API endpoints within a story marked [P] can run in parallel
- Different user stories can be worked on in parallel by different team members

---

## Parallel Example: User Story 1

```bash
# Launch parallel tasks for User Story 1 (View Available Images):
Task T013: "Implement CatalogImage reconciliation loop" (controller core)
Task T014: "Create registry accessibility checker" (registry client)
Task T018: "Create catalog API models" (API layer)
Task T019: "Add catalog route registration" (API routing)
Task T021: "Add caib catalog list command" (CLI)

# Sequential dependency:
T015: "Add phase state transitions" (depends on T013)
T020: "Implement filtering in list handler" (depends on T018)
T022: "Integrate list command with API client" (depends on T021 + API endpoints)
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational (CRITICAL - blocks all stories)
3. Complete Phase 3: User Story 1 (View Available Images)
4. **STOP and VALIDATE**: Test User Story 1 independently using catalog list operations
5. Deploy/demo basic catalog browsing functionality

### Incremental Delivery

1. Complete Setup + Foundational → Foundation ready
2. Add User Story 1 → Test independently → Deploy/Demo (MVP!)
3. Add User Story 2 → Test independently → Deploy/Demo (Publishing capability)
4. Add User Story 3 → Test independently → Deploy/Demo (Full catalog)
5. Each story adds value without breaking previous stories

### Parallel Team Strategy

With multiple developers:

1. Team completes Setup + Foundational together
2. Once Foundational is done:
   - Developer A: User Story 1 (View Available Images)
   - Developer B: User Story 2 (Publish Built Images)
   - Developer C: User Story 3 (Access Image Details)
3. Stories complete and integrate independently

---

## Implementation Notes

- [P] tasks = different files, no dependencies on incomplete tasks
- [Story] label maps task to specific user story for traceability
- Each user story should be independently completable and testable
- Follow Kubernetes operator patterns from existing automotive-dev-operator codebase
- Use containers/image/v5 library for registry integration (already available)
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- Avoid: vague tasks, same file conflicts, cross-story dependencies that break independence

## Task Count Summary

- **Total Tasks**: 53
- **Setup Phase**: 4 tasks
- **Foundational Phase**: 9 tasks (added T012a for audit infrastructure)
- **User Story 1 (P1)**: 10 tasks
- **User Story 2 (P2)**: 10 tasks
- **User Story 3 (P3)**: 8 tasks
- **Polish Phase**: 12 tasks (added T048a-c for edge case handling)

## Parallel Opportunities

- 25 tasks marked [P] for parallel execution
- User stories can be developed independently after foundational phase
- Maximum parallelization: 3 user stories + polish tasks can run concurrently

## MVP Scope

**Minimum Viable Product**: User Story 1 only (View Available Images)
- Provides immediate value for operations teams
- 23 tasks total (Setup + Foundational + US1)
- Enables catalog browsing and filtering functionality
- Foundation for subsequent publishing and detailed access features