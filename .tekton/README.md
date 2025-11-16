# Tekton Pipeline Configurations

This directory contains Tekton pipeline configurations for Konflux CI/CD integration.

## Directory Structure

```
.tekton/
├── README.md                                      # This file
├── automotive-dev-operator-pull-request.yaml      # PR pipeline for operator
├── automotive-dev-operator-push.yaml              # Push pipeline for operator (multi-arch)
├── aib-webui-pull-request.yaml                    # PR pipeline for WebUI
├── aib-webui-push.yaml                            # Push pipeline for WebUI (multi-arch)
├── bundle-pull-request.yaml                       # PR pipeline for OLM bundle
├── bundle-push.yaml                               # Push pipeline for OLM bundle
├── catalog-pull-request.yaml                      # PR pipeline for catalog
├── catalog-push.yaml                              # Push pipeline for catalog
├── release-pipeline.yaml                          # Release pipeline for all environments
├── update-csv-workflow.yaml                       # Workflow to update CSV with digests
├── tasks/
│   └── update-csv-digests.yaml                    # Task for updating CSV
└── integration-tests/
    ├── operator-deployment-test-pipeline.yaml     # Operator deployment test
    ├── imagebuild-test-pipeline.yaml              # ImageBuild CR test
    └── webui-test-pipeline.yaml                   # WebUI accessibility test
```

## Pipeline Types

### Pull Request Pipelines

Triggered on pull requests to the main branch. These pipelines:
- Build container images for PR validation
- Run security scans (Clair, Snyk)
- Check for deprecated base images
- Validate bundle/catalog manifests
- Post results back to GitHub PR

Path filters ensure pipelines only run when relevant files change.

### Push Pipelines

Triggered on pushes to the main branch. These pipelines:
- Build multi-architecture images (amd64, arm64)
- Create image manifests combining architectures
- Run comprehensive security scans
- Generate SBOMs
- Sign images with Cosign
- Tag images with commit SHA and 'latest'

### Integration Test Pipelines

Run as part of the build process to validate:
- Operator deployment and health
- Custom resource functionality
- WebUI accessibility
- Multi-component integration

### Release Pipeline

Orchestrates releases to different environments:
- Verifies Enterprise Contract policies
- Pushes images with version tags
- Generates install manifests
- Creates GitHub releases (for production)
- Sends notifications

## Key Features

### Hermetic Builds

All builds use hermetic mode (`HERMETIC=true`) to:
- Isolate from host system changes
- Enable dependency prefetching
- Generate accurate SBOMs
- Ensure reproducibility

### Multi-Architecture Support

Push pipelines build for both:
- linux/amd64
- linux/arm64

Images are combined into multi-arch manifests for flexible deployment.

### Path Filters

Pipelines use CEL expressions to trigger only on relevant changes:

- **Operator**: `api/`, `internal/`, `cmd/`, `Dockerfile`, `go.mod`, `go.sum`
- **WebUI**: `webui/**`
- **Bundle**: `bundle/**`, `config/**`
- **Catalog**: `catalog/**`

### Security Scanning

All images undergo:
- Clair vulnerability scanning
- Snyk code analysis
- Deprecated base image checks
- SBOM validation

### Image Signing

Production images are signed with Cosign using:
- Keyless signing with OIDC
- Transparency log integration (Rekor)
- SLSA provenance attestation

## Task References

Pipelines use tasks from the Konflux task catalog:
- `git-clone`: Clone repository
- `buildah`: Build container images
- `build-image-manifest`: Create multi-arch manifests
- `clair-scan`: Vulnerability scanning
- `snyk-check`: Code security analysis
- `generate-sbom`: SBOM generation
- `cosign-sign`: Image signing
- `operator-sdk-bundle-validate`: OLM bundle validation
- `opm-validate`: Catalog validation

## Customization

To customize pipelines:

1. Edit the relevant YAML file
2. Test locally with `tkn pipeline start`
3. Commit changes to trigger updated pipelines

## Troubleshooting

View pipeline runs:
```sh
tkn pipelinerun list
tkn pipelinerun logs <pipelinerun-name> -f
```

View task logs:
```sh
tkn taskrun list
tkn taskrun logs <taskrun-name>
```

## Resources

- [Konflux Documentation](https://konflux-ci.dev/docs/)
- [Tekton Documentation](https://tekton.dev/docs/)
- [Pipelines as Code](https://pipelinesascode.com/)

