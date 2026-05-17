# Konflux CI — Self-Hosted Setup on ARM Server

Runbook for deploying a self-hosted Konflux instance on a remote ARM server (aarch64)
and running CI pipelines for this operator project.

## Architecture

```
GitHub PR/push  →  GitHub App webhook  →  smee.io relay
                                              ↓
                                       ARM server (Kind cluster)
                                              ↓
                                   Pipelines as Code (PaC)
                                              ↓
                                      Tekton PipelineRun
                                              ↓
                                   buildah → quay.io images
```

Three pipelines run on every push to `main`:

| Component | Dockerfile         | Output image                              |
|-----------|--------------------|-------------------------------------------|
| operator  | `Dockerfile`       | `quay.io/bkhizgiy/automotive-dev-operator`        |
| bundle    | `bundle.Dockerfile`| `quay.io/bkhizgiy/automotive-dev-operator-bundle` |
| catalog   | `catalog.Dockerfile`| `quay.io/bkhizgiy/automotive-dev-operator-catalog`|

---

## Prerequisites (Fresh Fedora 43 ARM Server)

Run all of this as `root` on the ARM server.

### System packages

```bash
dnf install -y \
  git \
  curl \
  openssl \
  jq \
  make \
  golang \
  tar \
  which
```

### Docker

Fedora 43 ships Podman by default but Konflux CI requires Docker:

```bash
dnf install -y docker
systemctl enable --now docker

# Verify
docker version
```

### kubectl (ARM64)

```bash
curl -Lo /usr/local/bin/kubectl \
  "https://dl.k8s.io/release/$(curl -Ls https://dl.k8s.io/release/stable.txt)/bin/linux/arm64/kubectl"
chmod +x /usr/local/bin/kubectl
kubectl version --client
```

### kind (ARM64)

```bash
curl -Lo /usr/local/bin/kind \
  https://kind.sigs.k8s.io/dl/v0.27.0/kind-linux-arm64
chmod +x /usr/local/bin/kind
kind version
```

### Helm

```bash
curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
helm version
```

### Verify everything is in place

```bash
for cmd in docker kubectl kind helm git curl openssl jq; do
  echo -n "$cmd: " && command -v $cmd && $cmd --version 2>/dev/null | head -1
done
```

---

## One-Time Setup (GitHub App + Smee + Quay)

These are created once and reused across reinstalls.

### GitHub App

1. Go to <https://github.com/settings/apps/new>
2. Fill in:
   - **App name**: anything (e.g. `my-konflux-local`)
   - **Homepage URL**: `https://localhost:9443`
   - **Webhook URL**: set to your smee channel URL (see below)
   - **Webhook secret**: `openssl rand -hex 20` — save this value
3. Permissions → Repository:
   - Checks: Read & write
   - Contents: Read & write
   - Issues: Read & write
   - Pull requests: Read & write
4. Subscribe to events: **Push**, **Pull request**, **Check run**
5. Create the app → note the **App ID**
6. **Generate a private key** → save the `.pem` file → copy to ARM server

### Smee channel

1. Go to <https://smee.io> → **Start a new channel**
2. Copy the URL (e.g. `https://smee.io/abc123`)
3. Paste it into your GitHub App's **Webhook URL** field

### Quay.io robot account

1. Log in to <https://quay.io>
2. Go to your account → **Robot Accounts** → **Create robot account**
3. Name it (e.g. `konflux_push`)
4. Grant it **Write** access to these repos:
   - `bkhizgiy/automotive-dev-operator`
   - `bkhizgiy/automotive-dev-operator-bundle`
   - `bkhizgiy/automotive-dev-operator-catalog`
5. Note the robot username (`bkhizgiy+konflux_push`) and token

---

## Reinstall Procedure

### Step 1 — Clone the repo on the ARM server

```bash
cd ~
git clone git@github.com:bkhizgiy/automotive-dev-operator.git
cd automotive-dev-operator
```

### Step 2 — Fill in the environment file

```bash
cp konflux/deploy-local.env.template konflux/deploy-local.env
```

Edit `konflux/deploy-local.env`:

```bash
GITHUB_APP_ID="<your app ID>"
GITHUB_PRIVATE_KEY_PATH="/root/github-app.pem"   # path to the .pem on this server
WEBHOOK_SECRET="<your webhook secret>"
SMEE_CHANNEL="https://smee.io/abc123"             # your smee channel
KIND_MEMORY_GB=16
ENABLE_IMAGE_CACHE=0                              # keep 0 on ARM to avoid cache corruption
```

Copy the `.pem` file to the server:
```bash
# from your laptop:
scp ~/Downloads/my-app.pem root@<server>:/root/github-app.pem
```

### Step 3 — Create the Kind cluster

```bash
bash konflux/01-setup-kind.sh
```

### Step 4 — Deploy Konflux

```bash
bash konflux/02-deploy-konflux.sh
```

This takes ~20 minutes. If it times out on `tektonconfigs/config`, it is usually because
`tekton-results-postgres` can't pull its image (Docker Hub rate limit).

**Fix Docker Hub rate limit for postgres:**

`kind load docker-image` fails on ARM64 with multi-arch images. Use this workaround:

```bash
# Pull the image on the host (log in to Docker Hub first if rate-limited)
docker login
docker pull bitnami/postgresql@sha256:ac8dd0d6512c4c5fb146c16b1c5f05862bd5f600d73348506ab4252587e7fcc6

# Tag it so docker save includes a repo name
docker tag "bitnami/postgresql@sha256:ac8dd0d6512c4c5fb146c16b1c5f05862bd5f600d73348506ab4252587e7fcc6" \
  bitnami/postgresql:konflux

# Save and import directly into Kind's containerd via stdin (bypasses --all-platforms bug)
docker save bitnami/postgresql:konflux -o /tmp/pg-tagged.tar
docker exec -i konflux-control-plane \
  ctr --namespace=k8s.io images import - < /tmp/pg-tagged.tar

# Tag with the exact digest the pod expects
docker exec konflux-control-plane \
  ctr --namespace=k8s.io images tag \
  docker.io/bitnami/postgresql:konflux \
  "docker.io/bitnami/postgresql@sha256:ac8dd0d6512c4c5fb146c16b1c5f05862bd5f600d73348506ab4252587e7fcc6"

# Bounce the pod
kubectl delete pod tekton-results-postgres-0 -n tekton-pipelines
kubectl get pod tekton-results-postgres-0 -n tekton-pipelines -w
# Should show Running within ~2s, then re-run 02-deploy-konflux.sh
```

Verify all Tekton pods are running:
```bash
kubectl get pods -n tekton-pipelines
```

If you see this error during Step 5 of the deploy script:
```
strict decoding error: unknown field "spec.integrationService.spec.snapshotGarbageCollector"
```
The Konflux CR was already created successfully on the first pass — check that all components are ready and ignore the error:
```bash
kubectl get konflux konflux -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}'
# Should print: All 13 components are ready
```

### Step 5 — Onboard the application

Set your Quay credentials and run the onboard script:

```bash
export QUAY_USER='bkhizgiy+konflux_push'
export QUAY_TOKEN='<robot-account-token>'
bash konflux/03-onboard-app.sh
```

This creates:
- Service accounts: `build-pipeline-operator`, `build-pipeline-bundle`, `build-pipeline-catalog`
- Quay push secret with the `tekton.dev/docker-0=https://quay.io` annotation
- Application and Component CRs for all three components

### Step 6 — Access the UI

The Konflux UI runs on port 9443 of the Kind cluster, mapped to port 9443 on the host.

**SSH tunnel from your laptop:**
```bash
# Do NOT add the server hostname to /etc/hosts — it will break SSH
ssh -L 9443:localhost:9443 root@ampere-hr330a-04.lab.eng.rdu2.redhat.com -N
```

Open Firefox at:
```
https://localhost:9443
```

If Firefox shows a cert warning, click **Advanced** → **Accept the Risk and Continue**.

> **Do not use the hostname in the browser** — `localhost` works fine via the tunnel.
> Do not add the server hostname to `/etc/hosts` — it will cause SSH to resolve to
> `127.0.0.1` and break your SSH connection.

**Login:**
- Username: `user1@konflux.dev`
- Password: `password`

### Step 7 — Trigger the first build

Push an empty commit to trigger all three pipelines:

```bash
git commit --allow-empty -m "trigger: initial build"
git push origin main
```

Watch on the ARM server:
```bash
kubectl get pipelineruns -n default-tenant -w
```

---

## Known Issues & Fixes

### `tekton.dev/docker-0` annotation must be on the Quay secret

Tekton won't inject Quay credentials into pipeline pods unless the secret has this annotation.
The `03-onboard-app.sh` script sets it automatically. If pipelines fail with
`Token not found for quay.io/...` or `unauthorized`, re-apply it:

```bash
kubectl annotate secret quay-push-secret \
  -n default-tenant \
  tekton.dev/docker-0=https://quay.io \
  --overwrite
```

### Service accounts for each component

The build-service generates pipelines that use `build-pipeline-<component>` service accounts,
not the generic `appstudio-pipeline`. All three must exist and have the Quay secret linked.
The `03-onboard-app.sh` script handles this.

### Smee forwards to port 8180 (not 8080)

The gosmee pod is configured to forward to `localhost:8080` (a sidecar handles the relay
to `pipelines-as-code-controller.pipelines-as-code:8180`). This is correct and works
out of the box.

### Bundle resolver fails with `:latest` not found

The `.tekton/` pipeline files use inline `pipelineSpec` (not `pipelineRef: bundles`).
Never replace them with a `pipelineRef` that points to a bundle — the local Tekton
version doesn't support `taskRef.version` used in the current build-definitions main branch,
and the `:latest` tag doesn't exist on the bundle images.

### Pipeline YAML syntax errors block ALL pipelines

PaC validates every `.tekton/*.yaml` file before creating any PipelineRun. One broken file
blocks all three pipelines. Always validate with:
```bash
python3 -c "import yaml; yaml.safe_load(open('.tekton/bundle-push.yaml'))"
```

### Git conflicts in `.tekton/` files

If a rebase leaves conflict markers (`<<<<<<<`, `=======`, `>>>>>>>`), PaC will fail to
parse the YAML and silently skip all pipeline runs. Restore cleanly:
```bash
git show 42e5cfaa:.tekton/operator-push.yaml > .tekton/operator-push.yaml
# repeat for other files or regenerate bundle/catalog from operator template
```

---

## Port Reference

| Host port | Kind NodePort | Service |
|-----------|--------------|---------|
| 9443      | 30011        | Konflux UI (HTTPS) |
| 8888      | 30010        | — (not used) |
| 8180      | 30012        | Pipelines as Code webhook receiver |
| 5001      | 30001        | Local image registry |

---

## Useful Commands

```bash
# Watch pipeline runs
kubectl get pipelineruns -n default-tenant -w

# Check why a pipeline failed
kubectl describe pipelinerun -n default-tenant <name> | grep -A10 "Message:"

# Check PaC controller logs (webhook processing)
kubectl logs -n pipelines-as-code deployment/pipelines-as-code-controller --tail=50

# Check smee is receiving webhooks
kubectl logs -n smee-client deployment/gosmee-client -c gosmee --tail=20

# Re-trigger a build manually
kubectl annotate component operator -n default-tenant \
  build.appstudio.openshift.io/request=trigger-simple-build --overwrite

# Check Tekton is healthy
kubectl get tektonconfig config -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
```
