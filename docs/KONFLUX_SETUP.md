# Konflux Setup Guide

This guide walks you through setting up Konflux CI/CD for the automotive-dev-operator.

## Prerequisites

- Admin access to the GitHub repository: `centos-automotive-suite/automotive-dev-operator`
- Access to Konflux (production: https://console.redhat.com/preview/application-pipeline or community tenant)
- Container registry credentials for `quay.io/rh-sdv-cloud`

## Step 1: Install Konflux GitHub App

1. Navigate to https://konflux-ci.dev/docs/installing/github-app/
2. Follow the instructions to install the Konflux GitHub App on your GitHub organization
3. Grant the app access to the `automotive-dev-operator` repository
4. This enables Pipelines as Code (PaC) to trigger builds on push/PR events

## Step 2: Create Konflux Application

1. Log in to Konflux UI
2. Click "Create Application"
3. Enter application name: `automotive-dev-operator`
4. Select your workspace
5. Click "Create"

## Step 3: Add Components

### Component 1: Operator Manager

1. Click "Add Component" in your application
2. Select "Import from Git"
3. Enter the following details:
   - **Component name**: `automotive-dev-operator`
   - **Git URL**: `https://github.com/centos-automotive-suite/automotive-dev-operator`
   - **Git reference**: `main`
   - **Context directory**: `/`
   - **Dockerfile**: `Dockerfile`
   - **Target port**: `8443`
4. Advanced settings:
   - **Container image registry**: `quay.io/rh-sdv-cloud/automotive-dev-operator`
   - **Enable multi-arch**: Yes (amd64, arm64)
5. Click "Create Component"

### Component 2: WebUI

1. Click "Add Component"
2. Select "Import from Git"
3. Enter the following details:
   - **Component name**: `aib-webui`
   - **Git URL**: `https://github.com/centos-automotive-suite/automotive-dev-operator`
   - **Git reference**: `main`
   - **Context directory**: `/webui`
   - **Dockerfile**: `webui/Dockerfile`
   - **Target port**: `3000`
4. Advanced settings:
   - **Container image registry**: `quay.io/rh-sdv-cloud/aib-webui`
   - **Enable multi-arch**: Yes (amd64, arm64)
5. Click "Create Component"

### Component 3: OLM Bundle

1. Click "Add Component"
2. Select "Import from Git"
3. Enter the following details:
   - **Component name**: `automotive-dev-operator-bundle`
   - **Git URL**: `https://github.com/centos-automotive-suite/automotive-dev-operator`
   - **Git reference**: `main`
   - **Context directory**: `/`
   - **Dockerfile**: `bundle.Dockerfile`
4. Advanced settings:
   - **Container image registry**: `quay.io/rh-sdv-cloud/automotive-dev-operator-bundle`
   - **Dependencies**: Add dependency on `automotive-dev-operator` component
5. Click "Create Component"

### Component 4: Catalog (File-Based Catalog)

1. Click "Add Component"
2. Select "Import from Git"
3. Enter the following details:
   - **Component name**: `automotive-dev-operator-catalog`
   - **Git URL**: `https://github.com/centos-automotive-suite/automotive-dev-operator`
   - **Git reference**: `main`
   - **Context directory**: `/`
   - **Dockerfile**: `catalog.Dockerfile`
4. Advanced settings:
   - **Container image registry**: `quay.io/rh-sdv-cloud/automotive-dev-operator-catalog`
   - **Dependencies**: Add dependency on `automotive-dev-operator-bundle` component
5. Click "Create Component"

## Step 4: Configure Build Pipelines

After components are created, Konflux will automatically generate build pipelines. These are stored as `.tekton/*.yaml` files in your repository.

The configuration files in this repository (`konflux/` and `.tekton/` directories) provide additional customization:
- Path filters to prevent redundant rebuilds
- Hermetic build configuration
- Integration test scenarios
- Release plans

## Step 5: Configure Container Registry Secrets

1. In Konflux UI, navigate to "Secrets"
2. Add a new secret for Quay.io:
   - **Name**: `quay-io-push-secret`
   - **Type**: `kubernetes.io/dockerconfigjson`
   - **Registry**: `quay.io`
   - **Username**: Your Quay.io username
   - **Password**: Your Quay.io token
3. Link this secret to all components

## Step 6: Verify Setup

1. Create a test branch and push a small change
2. Open a Pull Request
3. Verify that Konflux pipelines are triggered
4. Check that build status is reported back to GitHub PR

## Next Steps

- Review and customize `.tekton/` pipeline configurations
- Set up integration tests
- Configure release plans for different environments
- Enable Enterprise Contract policies for security compliance

## Resources

- [Konflux Documentation](https://konflux-ci.dev/docs/)
- [Building OLM Operators in Konflux](https://konflux-ci.dev/docs/end-to-end/building-olm/)
- [Konflux GitHub App Setup](https://konflux-ci.dev/docs/installing/github-app/)

