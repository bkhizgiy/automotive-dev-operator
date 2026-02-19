#!/bin/bash
set -e

validate_arg() {
  local arg="$1"
  local name="$2"
  # Block shell metacharacters that could be used for injection
  if [[ "$arg" =~ [\;\|\&\$\`\(\)\{\}\<\>\!\\] ]]; then
    echo "ERROR: Invalid characters in $name: $arg"
    exit 1
  fi
}

validate_custom_def() {
  local def="$1"
  # Custom defs should be KEY=VALUE format only
  if [[ ! "$def" =~ ^[a-zA-Z_][a-zA-Z0-9_]*=.*$ ]]; then
    echo "ERROR: Invalid custom definition format: $def (expected KEY=VALUE)"
    exit 1
  fi
  validate_arg "$def" "custom definition"
}

echo "Prepare builder for distro: $DISTRO, arch: $TARGET_ARCH"

# If BUILDER_IMAGE is provided, use it directly
if [ -n "$BUILDER_IMAGE" ]; then
  echo "Using provided builder image: $BUILDER_IMAGE"
  echo -n "$BUILDER_IMAGE" > "$RESULT_PATH"
  exit 0
fi

# Set up cluster registry details
TOKEN=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
NAMESPACE=$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace)

if [ -n "$CLUSTER_REGISTRY_ROUTE" ]; then
  echo "Using external registry route: $CLUSTER_REGISTRY_ROUTE"
  REGISTRY="$CLUSTER_REGISTRY_ROUTE"
else
  REGISTRY="image-registry.openshift-image-registry.svc:5000"
fi

# Include a short hash of the AIB image in the registry tag so that different
# AIB versions cache their builder images separately and don't overwrite each other.
AIB_HASH=$(echo -n "$AIB_IMAGE" | sha256sum | cut -c1-8)
TARGET_IMAGE="${REGISTRY}/${NAMESPACE}/aib-build:${DISTRO}-${TARGET_ARCH}-${AIB_HASH}"
echo "AIB image: $AIB_IMAGE (hash: $AIB_HASH)"

mkdir -p $HOME/.config
cat > $HOME/.authjson <<EOF
{
  "auths": {
    "$REGISTRY": {
      "auth": "$(echo -n "serviceaccount:$TOKEN" | base64 -w0)"
    }
  }
}
EOF
export REGISTRY_AUTH_FILE=$HOME/.authjson

# Make internal registry trusted (fallback for internal service)
mkdir -p /etc/containers
cat > /etc/containers/registries.conf << EOF
[registries.insecure]
registries = ['image-registry.openshift-image-registry.svc:5000']
EOF

echo "Configuring kernel overlay storage driver"
cat > /etc/containers/storage.conf << EOF
[storage]
driver = "overlay"
runroot = "/run/containers/storage"
graphroot = "/var/lib/containers/storage"
EOF

if ! mountpoint -q /var/tmp; then
  VAR_TMP_SIZE="${VAR_TMP_SIZE:-20G}"
  echo "Creating loopback ext4 filesystem for /var/tmp (${VAR_TMP_SIZE} sparse)"
  truncate -s "$VAR_TMP_SIZE" /tmp/var-tmp.img
  mkfs.ext4 -q /tmp/var-tmp.img
  mount -o loop /tmp/var-tmp.img /var/tmp
fi

# Local image name (what we'll actually use - nested containers can access this)
LOCAL_IMAGE="localhost/aib-build:${DISTRO}-${TARGET_ARCH}"

# Check if image already exists in cluster registry (skip if rebuild requested)
if [ "$REBUILD_BUILDER" = "true" ]; then
  echo "Rebuild requested, skipping cache check"
else
  echo "Checking if $TARGET_IMAGE exists in cluster registry..."
  if skopeo inspect --authfile="$REGISTRY_AUTH_FILE" "docker://$TARGET_IMAGE" >/dev/null 2>&1; then
    echo "Builder image found in cluster registry: $TARGET_IMAGE"
    echo -n "$TARGET_IMAGE" > "$RESULT_PATH"
    exit 0
  fi
fi

echo "Builder image not found, building..."

# Install custom CA certificates if available
if [ -d /etc/pki/ca-trust/custom ] && ls /etc/pki/ca-trust/custom/*.pem >/dev/null 2>&1; then
  echo "Installing custom CA certificates..."
  cp /etc/pki/ca-trust/custom/*.pem /etc/pki/ca-trust/source/anchors/ 2>/dev/null || true
  update-ca-trust extract 2>/dev/null || true
fi

# Set up SELinux contexts for osbuild
osbuildPath="/usr/bin/osbuild"
storePath="/_build"
runTmp="/run/osbuild/"

mkdir -p "$storePath"
mkdir -p "$runTmp"

rootType="system_u:object_r:root_t:s0"
chcon "$rootType" "$storePath" || true

installType="system_u:object_r:install_exec_t:s0"
if ! mountpoint -q "$runTmp"; then
  mount -t tmpfs tmpfs "$runTmp"
fi

destPath="$runTmp/osbuild"
cp -p "$osbuildPath" "$destPath"
chcon "$installType" "$destPath" || true

mount --bind "$destPath" "$osbuildPath"

# Load custom definitions (e.g., distro_baseurl) from manifest config workspace
declare -a CUSTOM_DEFS_ARGS=()
CUSTOM_DEFS_FILE="/workspace/manifest-config/custom-definitions.env"
if [ -f "$CUSTOM_DEFS_FILE" ]; then
  while IFS= read -r line || [[ -n "$line" ]]; do
    # Skip empty lines and comments
    [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
    validate_custom_def "$line"
    CUSTOM_DEFS_ARGS+=("--define" "$line")
    echo "  Custom definition: $line"
  done < "$CUSTOM_DEFS_FILE"
fi

echo "Running: aib build-builder --distro $DISTRO ${CUSTOM_DEFS_ARGS[*]}"
aib --verbose build-builder --distro "$DISTRO" "${CUSTOM_DEFS_ARGS[@]}"

# Find what image was actually created by aib (it might use distro-target naming)
echo "Checking for created builder images..."
ACTUAL_IMAGE=""
for img in $(podman images --format "{{.Repository}}:{{.Tag}}" | grep "localhost/aib-build:"); do
    echo "Found image: $img"
    if [[ "$img" == *"$DISTRO"* ]]; then
        ACTUAL_IMAGE="$img"
        echo "Using image: $ACTUAL_IMAGE"
        break
    fi
done

if [ -z "$ACTUAL_IMAGE" ]; then
    echo "ERROR: Could not find builder image containing '$DISTRO'"
    podman images | grep aib-build || echo "No aib-build images found"
    exit 1
fi

echo "Pushing to cluster registry: $TARGET_IMAGE"
skopeo copy --authfile="$REGISTRY_AUTH_FILE" \
  "containers-storage:$ACTUAL_IMAGE" \
  "docker://$TARGET_IMAGE"

echo "Builder image ready: $TARGET_IMAGE"
echo -n "$TARGET_IMAGE" > "$RESULT_PATH"
