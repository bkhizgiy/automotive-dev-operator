# Konflux Quick Start Guide

Quick reference for working with Konflux CI/CD for automotive-dev-operator.

## What Was Set Up

This repository now includes complete Konflux integration with:

- ✅ Automated build pipelines for all components
- ✅ Multi-architecture support (amd64, arm64)
- ✅ Security scanning and SLSA Level 3 compliance
- ✅ Integration testing
- ✅ Release management for multiple environments
- ✅ Hermetic builds with SBOM generation
- ✅ Image signing with Cosign

## File Structure

```
.
├── KONFLUX_SETUP.md              # Detailed setup instructions
├── KONFLUX_QUICK_START.md        # This file
├── .tekton/                      # Tekton pipeline definitions
│   ├── *-pull-request.yaml       # PR validation pipelines
│   ├── *-push.yaml               # Main branch build pipelines
│   ├── release-pipeline.yaml     # Release orchestration
│   ├── tasks/                    # Custom Tekton tasks
│   └── integration-tests/        # Integration test pipelines
├── konflux/                      # Konflux configuration
│   ├── application.yaml          # Application definition
│   ├── components/               # Component definitions
│   ├── integration-tests/        # Test scenarios
│   ├── release-plans/            # Release strategies
│   └── enterprise-contract-policy.yaml  # Security policy
└── hack/
    └── update-csv-local.sh       # Local CSV update helper
```

## Quick Commands

### Check Pipeline Status

```sh
# View recent pipeline runs
tkn pipelinerun list

# Watch a specific run
tkn pipelinerun logs <name> -f

# Check build status in Konflux UI
# https://console.redhat.com/preview/application-pipeline
```

### Trigger Builds

Builds are triggered automatically on:
- **Pull Request**: Creates PR validation build
- **Push to main**: Creates multi-arch production builds
- **Git tag (v*)**: Triggers production release

### Verify Images

```sh
# Pull latest image
podman pull quay.io/rh-sdv-cloud/automotive-dev-operator:latest

# Verify signature
cosign verify \
  --certificate-identity-regexp=https://github.com/centos-automotive-suite/automotive-dev-operator \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  quay.io/rh-sdv-cloud/automotive-dev-operator:latest

# View SBOM
cosign download sbom quay.io/rh-sdv-cloud/automotive-dev-operator:latest | jq
```

### Update CSV After Build

```sh
# Get image digest from Konflux or registry
DIGEST=$(skopeo inspect docker://quay.io/rh-sdv-cloud/automotive-dev-operator:latest | jq -r '.Digest')

# Update CSV
./hack/update-csv-local.sh \
  "quay.io/rh-sdv-cloud/automotive-dev-operator@${DIGEST}"

# Rebuild bundle
make bundle
git add bundle/
git commit -m "Update CSV with image digest"
git push
```

## Component Build Flow

```
┌─────────────────────────────────────────────────────┐
│  Pull Request or Push to main                       │
└─────────────────────────────────────────────────────┘
                         │
        ┌────────────────┴────────────────┐
        │                                 │
        ▼                                 ▼
┌──────────────┐                  ┌──────────────┐
│   Operator   │                  │    WebUI     │
│   (multi-    │                  │   (multi-    │
│    arch)     │                  │    arch)     │
└──────┬───────┘                  └──────────────┘
       │
       ▼
┌──────────────┐
│    Bundle    │ (depends on operator digest)
│              │
└──────┬───────┘
       │
       ▼
┌──────────────┐
│   Catalog    │ (depends on bundle)
│              │
└──────────────┘
       │
       ▼
┌──────────────────────────────────────────┐
│  Integration Tests                       │
│  - Operator deployment                   │
│  - ImageBuild CR                         │
│  - WebUI accessibility                   │
└──────────────────────────────────────────┘
       │
       ▼
┌──────────────────────────────────────────┐
│  Enterprise Contract Verification        │
│  - SLSA provenance                       │
│  - CVE scan                              │
│  - Image signing                         │
└──────────────────────────────────────────┘
       │
       ▼
┌──────────────────────────────────────────┐
│  Release (based on environment)          │
│  - Dev: Auto                             │
│  - Staging: Manual approval              │
│  - Production: Manual approval + GitHub  │
└──────────────────────────────────────────┘
```

## Common Tasks

### Add a New Component

1. Create component YAML in `konflux/components/`
2. Add corresponding pipeline in `.tekton/`
3. Apply to cluster: `kubectl apply -f konflux/components/your-component.yaml`

### Modify Security Policy

1. Edit `konflux/enterprise-contract-policy.yaml`
2. Add/remove checks in `include`/`exclude` sections
3. Apply: `kubectl apply -f konflux/enterprise-contract-policy.yaml`

### Add Integration Test

1. Create test scenario in `konflux/integration-tests/`
2. Create pipeline in `.tekton/integration-tests/`
3. Apply: `kubectl apply -f konflux/integration-tests/your-test.yaml`

### Promote to Staging

1. Go to Konflux UI
2. Navigate to Releases
3. Select successful build from dev
4. Click "Promote to Staging"
5. Approve release

### Create Production Release

1. Ensure staging validation is complete
2. Create and push git tag:
   ```sh
   git tag v1.0.0
   git push origin v1.0.0
   ```
3. Konflux automatically triggers production release
4. Review and approve in Konflux UI
5. GitHub release is created automatically

## Image Locations

All images are published to Quay.io:

| Component | Repository | Tags |
|-----------|------------|------|
| Operator | `quay.io/rh-sdv-cloud/automotive-dev-operator` | `latest`, `<sha>`, `v*` |
| WebUI | `quay.io/rh-sdv-cloud/aib-webui` | `latest`, `<sha>`, `v*` |
| Bundle | `quay.io/rh-sdv-cloud/automotive-dev-operator-bundle` | `latest`, `<sha>`, `v*` |
| Catalog | `quay.io/rh-sdv-cloud/automotive-dev-operator-catalog` | `latest`, `<sha>`, `v*` |

Each image includes:
- Multi-arch manifest (amd64, arm64)
- Cosign signature
- SBOM attachment
- SLSA provenance

## Troubleshooting

### Build Failed

```sh
# Check pipeline logs
tkn pipelinerun logs <pipelinerun-name> -f

# Check specific task
tkn taskrun logs <taskrun-name>

# Common issues:
# - Image registry auth: Check secrets
# - CVE scan failed: Review scan results, update base images
# - Test failed: Check test logs in task
```

### Integration Test Failed

```sh
# View test results in Konflux UI
# Or check TaskRun logs:
tkn taskrun logs -l tekton.dev/pipelineTask=<test-task-name>

# Rerun test:
# - Fix issue in code
# - Push changes
# - Test automatically reruns
```

### Enterprise Contract Failed

```sh
# View policy results in Konflux UI
# Common failures:
# - Missing SBOM: Check SBOM generation task
# - CVE detected: Update dependencies or request exception
# - Unsigned image: Check signing task
# - SLSA provenance missing: Check build attestation
```

## Next Steps

1. **Complete Setup**: Follow [KONFLUX_SETUP.md](./KONFLUX_SETUP.md) for initial configuration
2. **Review Pipelines**: Check `.tekton/` directory and customize as needed
3. **Test Integration**: Create a PR to trigger validation builds
4. **Configure Notifications**: Set up Slack/email notifications for build status
5. **Customize Policies**: Adjust security policies based on your requirements

## Resources

- [Konflux Documentation](https://konflux-ci.dev/docs/)
- [Building OLM Operators](https://konflux-ci.dev/docs/end-to-end/building-olm/)
- [Enterprise Contract](https://enterprisecontract.dev/)
- [Tekton Documentation](https://tekton.dev/docs/)
- [SLSA Framework](https://slsa.dev/)
- [Cosign Documentation](https://docs.sigstore.dev/cosign/overview/)

## Support

- Konflux Community: https://konflux-ci.dev/community/
- GitHub Issues: https://github.com/centos-automotive-suite/automotive-dev-operator/issues
- Konflux Slack: Join the community channel

