# Konflux CI Demo

This file was added to demonstrate the Konflux CI/CD pipeline integration.

## What Konflux does on every push

When a commit is pushed to `main`, Konflux automatically triggers 3 build pipelines:

- **operator** — builds the controller manager image (`quay.io/bkhizgiy/automotive-dev-operator`)
- **bundle** — builds the OLM bundle image (`quay.io/bkhizgiy/automotive-dev-operator-bundle`)
- **catalog** — builds the OLM catalog index image (`quay.io/bkhizgiy/automotive-dev-operator-catalog`)

Each pipeline produces multi-architecture images (linux/arm64 + linux/amd64) and pushes:

| Tag | Description |
|-----|-------------|
| `:latest` | Multi-arch manifest, always points to the latest main build |
| `:latest-arm64` | ARM64 image |
| `:latest-amd64` | AMD64 image |
| `:<sha>` | Immutable per-commit multi-arch manifest |
| `:<sha>-arm64` | Immutable per-commit ARM64 image |
| `:<sha>-amd64` | Immutable per-commit AMD64 image |

## What Konflux does on every Pull Request

When a PR is opened or updated, the same 3 pipelines run but only push revision-tagged images (no `:latest` overwrite):

| Tag | Description |
|-----|-------------|
| `:on-pr-<sha>` | Multi-arch manifest for this PR commit |
| `:on-pr-<sha>-arm64` | ARM64 image for this PR commit |
| `:on-pr-<sha>-amd64` | AMD64 image for this PR commit |

Pipeline status is reported back to the PR as a GitHub check.
