# Konflux Configuration

This directory contains Konflux configuration files for the automotive-dev-operator project.

## Directory Structure

```
konflux/
├── README.md                          # This file
├── application.yaml                   # Application definition
├── enterprise-contract-policy.yaml    # Security and compliance policy
├── components/
│   ├── operator.yaml                  # Operator component definition
│   ├── webui.yaml                     # WebUI component definition
│   ├── bundle.yaml                    # Bundle component definition
│   └── catalog.yaml                   # Catalog component definition
├── integration-tests/
│   ├── operator-deployment-test.yaml  # Operator test scenario
│   ├── imagebuild-test.yaml          # ImageBuild CR test scenario
│   └── webui-test.yaml               # WebUI test scenario
└── release-plans/
    ├── dev-release.yaml              # Development environment release plan
    ├── staging-release.yaml          # Staging environment release plan
    └── prod-release.yaml             # Production environment release plan
```

## Configuration Files

### application.yaml

Defines the Konflux application that groups all components together.

**Key settings:**
- Application name: `automotive-dev-operator`
- GitOps repository URL
- Application display name and description

### Components

Component definitions specify how each container image is built:

#### operator.yaml
- Main operator controller
- Built from root `Dockerfile`
- Multi-arch support (amd64, arm64)
- Target: `quay.io/rh-sdv-cloud/automotive-dev-operator`

#### webui.yaml
- Web user interface
- Built from `webui/Dockerfile`
- Multi-arch support
- Target: `quay.io/rh-sdv-cloud/aib-webui`

#### bundle.yaml
- OLM operator bundle
- Built from `bundle.Dockerfile`
- Depends on operator component
- Target: `quay.io/rh-sdv-cloud/automotive-dev-operator-bundle`

#### catalog.yaml
- OLM catalog
- Built from `catalog.Dockerfile`
- Depends on bundle component
- Target: `quay.io/rh-sdv-cloud/automotive-dev-operator-catalog`

### Enterprise Contract Policy

Defines security and compliance requirements for all builds.

**Policy checks include:**
- SLSA provenance verification
- Build service attestation
- Version control validation
- Image signature verification
- CVE scanning
- Deprecated base image detection
- SBOM validation (CycloneDX, PURL)
- Hermetic build verification
- Test passing requirements

**Collections:**
- `minimal`: Basic security checks
- `slsa3`: SLSA Level 3 compliance

### Integration Tests

Test scenarios that run automatically after builds:

#### operator-deployment-test.yaml
Tests operator installation and basic functionality:
- Deploy operator to test namespace
- Verify pod health
- Create and validate OperatorConfig CR
- Cleanup

#### imagebuild-test.yaml
Tests ImageBuild custom resource workflow:
- Deploy operator with Tekton
- Create ImageBuild CR
- Verify CR processing
- Cleanup

#### webui-test.yaml
Tests WebUI deployment and accessibility:
- Deploy WebUI service
- Verify service health
- Check accessibility
- Cleanup

### Release Plans

Release plans define how and when to release to different environments:

#### dev-release.yaml
- **Target**: Development environment
- **Auto-release**: Enabled on main branch
- **Policy**: Standard verification
- **Grace period**: 7 days

#### staging-release.yaml
- **Target**: Staging environment
- **Auto-release**: Disabled (manual approval)
- **Policy**: Standard verification
- **Grace period**: 14 days

#### prod-release.yaml
- **Target**: Production environment
- **Auto-release**: Disabled (manual approval)
- **Policy**: Strict verification
- **GitHub release**: Created automatically
- **Grace period**: 30 days

## Usage

### Applying Configuration

These configurations can be applied directly to your Konflux namespace:

```sh
# Apply application
kubectl apply -f konflux/application.yaml

# Apply components
kubectl apply -f konflux/components/

# Apply integration tests
kubectl apply -f konflux/integration-tests/

# Apply release plans
kubectl apply -f konflux/release-plans/

# Apply enterprise contract policy
kubectl apply -f konflux/enterprise-contract-policy.yaml
```

### Modifying Configuration

To modify component builds:

1. Edit the relevant component YAML file
2. Update image references, build parameters, or dependencies
3. Apply changes: `kubectl apply -f konflux/components/<component>.yaml`
4. Verify in Konflux UI

To adjust security policies:

1. Edit `enterprise-contract-policy.yaml`
2. Add/remove checks from `include`/`exclude` lists
3. Apply: `kubectl apply -f konflux/enterprise-contract-policy.yaml`

To modify release plans:

1. Edit the release plan YAML file
2. Adjust auto-release, policy, or pipeline parameters
3. Apply: `kubectl apply -f konflux/release-plans/<plan>.yaml`

## Build Dependencies

Component build order is enforced through annotations:

1. **Operator + WebUI** (parallel) - No dependencies
2. **Bundle** - Depends on operator (`build.appstudio.openshift.io/depends-on: automotive-dev-operator`)
3. **Catalog** - Depends on bundle (`build.appstudio.openshift.io/depends-on: automotive-dev-operator-bundle`)

This ensures:
- Bundle includes correct operator image digest
- Catalog references correct bundle image digest

## Security Compliance

All components must pass Enterprise Contract verification before release:

- ✅ SLSA Level 3 provenance
- ✅ Signed with Cosign
- ✅ CVE scan passed
- ✅ No deprecated base images
- ✅ Valid SBOM included
- ✅ Hermetic build verified
- ✅ Tests passed

## Resources

- [Konflux Documentation](https://konflux-ci.dev/docs/)
- [Building OLM Operators in Konflux](https://konflux-ci.dev/docs/end-to-end/building-olm/)
- [Enterprise Contract](https://enterprisecontract.dev/)
- [SLSA Framework](https://slsa.dev/)

