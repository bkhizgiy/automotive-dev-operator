#!/bin/sh
set -e


# Make the internal registry trusted
# TODO think about whether this is really the right approach
mkdir -p /etc/containers
cat > /etc/containers/registries.conf << EOF
[registries.insecure]
registries = ['image-registry.openshift-image-registry.svc:5000']
EOF

if [ -e /dev/fuse ]; then
  if ! command -v fuse-overlayfs >/dev/null 2>&1; then
    echo "Installing fuse-overlayfs..."
    dnf install -y fuse-overlayfs 2>/dev/null || yum install -y fuse-overlayfs 2>/dev/null || true
  fi

  if command -v fuse-overlayfs >/dev/null 2>&1; then
    echo "Configuring fuse-overlayfs for container storage..."
    cat > /etc/containers/storage.conf << EOF
[storage]
driver = "overlay"
runroot = "/run/containers/storage"
graphroot = "/var/lib/containers/storage"

[storage.options.overlay]
mount_program = "/usr/bin/fuse-overlayfs"
EOF
  else
    echo "Warning: fuse-overlayfs install failed, using vfs driver"
    export STORAGE_DRIVER=vfs
  fi
else
  echo "Warning: /dev/fuse not available, using vfs driver"
  export STORAGE_DRIVER=vfs
fi

TOKEN=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
REGISTRY="image-registry.openshift-image-registry.svc:5000"
NAMESPACE=$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace)

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
export CONTAINERS_REGISTRIES_CONF="/etc/containers/registries.conf"

# Read registry credentials from workspace if available
REGISTRY_AUTH_DIR="/workspace/registry-auth"
if [ -f "$REGISTRY_AUTH_DIR/REGISTRY_URL" ]; then
    REGISTRY_URL=$(cat "$REGISTRY_AUTH_DIR/REGISTRY_URL")
fi
if [ -f "$REGISTRY_AUTH_DIR/REGISTRY_USERNAME" ]; then
    REGISTRY_USERNAME=$(cat "$REGISTRY_AUTH_DIR/REGISTRY_USERNAME")
fi
if [ -f "$REGISTRY_AUTH_DIR/REGISTRY_PASSWORD" ]; then
    REGISTRY_PASSWORD=$(cat "$REGISTRY_AUTH_DIR/REGISTRY_PASSWORD")
fi
if [ -f "$REGISTRY_AUTH_DIR/REGISTRY_TOKEN" ]; then
    REGISTRY_TOKEN=$(cat "$REGISTRY_AUTH_DIR/REGISTRY_TOKEN")
fi
if [ -f "$REGISTRY_AUTH_DIR/REGISTRY_AUTH_FILE_CONTENT" ]; then
    REGISTRY_AUTH_FILE_CONTENT=$(cat "$REGISTRY_AUTH_DIR/REGISTRY_AUTH_FILE_CONTENT")
fi

if [ -n "$REGISTRY_AUTH_FILE_CONTENT" ]; then
    echo "Using provided registry auth file content"
    echo "$REGISTRY_AUTH_FILE_CONTENT" > $HOME/.custom_authjson
    export REGISTRY_AUTH_FILE=$HOME/.custom_authjson
elif [ -n "$REGISTRY_USERNAME" ] && [ -n "$REGISTRY_PASSWORD" ] && [ -n "$REGISTRY_URL" ]; then
    echo "Creating registry auth from username/password for $REGISTRY_URL"
    mkdir -p $HOME/.config
    AUTH_STRING=$(echo -n "$REGISTRY_USERNAME:$REGISTRY_PASSWORD" | base64 -w0)
    cat > $HOME/.custom_authjson <<EOF
{
  "auths": {
    "$REGISTRY_URL": {
      "auth": "$AUTH_STRING"
    }
  }
}
EOF
    export REGISTRY_AUTH_FILE=$HOME/.custom_authjson
elif [ -n "$REGISTRY_TOKEN" ] && [ -n "$REGISTRY_URL" ]; then
    echo "Creating registry auth from token for $REGISTRY_URL"
    mkdir -p $HOME/.config
    cat > $HOME/.custom_authjson <<EOF
{
  "auths": {
    "$REGISTRY_URL": {
      "auth": "$(echo -n "token:$REGISTRY_TOKEN" | base64 -w0)"
    },
    "$REGISTRY": {
      "auth": "$(echo -n "serviceaccount:$TOKEN" | base64 -w0)"
    }
  }
}
EOF
    export REGISTRY_AUTH_FILE=$HOME/.custom_authjson
fi

if [ -n "$BUILDAH_REGISTRY_AUTH_FILE" ]; then
    export BUILDAH_REGISTRY_AUTH_FILE="$REGISTRY_AUTH_FILE"
fi

osbuildPath="/usr/bin/osbuild"
storePath="/_build"
runTmp="/run/osbuild/"

mkdir -p "$storePath"
mkdir -p "$runTmp"

MANIFEST_FILE=$(cat /tekton/results/manifest-file-path)
if [ -z "$MANIFEST_FILE" ]; then
    echo "Error: No manifest file path provided"
    exit 1
fi

echo "using manifest file: $MANIFEST_FILE"

if [ ! -f "$MANIFEST_FILE" ]; then
    echo "error: Manifest file not found at $MANIFEST_FILE"
    exit 1
fi

if mountpoint -q "$osbuildPath"; then
    exit 0
fi

rootType="system_u:object_r:root_t:s0"
chcon "$rootType" "$storePath"

installType="system_u:object_r:install_exec_t:s0"
if ! mountpoint -q "$runTmp"; then
  mount -t tmpfs tmpfs "$runTmp"
fi

destPath="$runTmp/osbuild"
cp -p "$osbuildPath" "$destPath"
chcon "$installType" "$destPath"

mount --bind "$destPath" "$osbuildPath"

cd $(workspaces.shared-workspace.path)

if [ "$(params.export-format)" = "image" ]; then
  file_extension=".raw"
elif [ "$(params.export-format)" = "qcow2" ]; then
  file_extension=".qcow2"
else
  file_extension=".$(params.export-format)"
fi

cleanName=$(params.distro)-$(params.target)
exportFile=${cleanName}${file_extension}

BUILD_MODE="$(params.mode)"
if [ -z "$BUILD_MODE" ]; then
  BUILD_MODE="bootc"
fi

CUSTOM_DEFS=""
CUSTOM_DEFS_FILE="$(workspaces.manifest-config-workspace.path)/custom-definitions.env"
if [ -f "$CUSTOM_DEFS_FILE" ]; then
  echo "Processing custom definitions from $CUSTOM_DEFS_FILE"
  while read -r line || [[ -n "$line" ]]; do
    for def in $line; do
      CUSTOM_DEFS+=" --define $def"
    done
  done < "$CUSTOM_DEFS_FILE"
else
  echo "No custom-definitions.env file found"
fi

AIB_OVERRIDE_ARGS_FILE="$(workspaces.manifest-config-workspace.path)/aib-override-args.txt"
AIB_EXTRA_ARGS_FILE="$(workspaces.manifest-config-workspace.path)/aib-extra-args.txt"
AIB_ARGS=""
if [ -f "$AIB_OVERRIDE_ARGS_FILE" ]; then
  echo "Using override automotive-image-builder args from $AIB_OVERRIDE_ARGS_FILE"
  AIB_ARGS="$(cat "$AIB_OVERRIDE_ARGS_FILE")"
elif [ -f "$AIB_EXTRA_ARGS_FILE" ]; then
  echo "Adding extra automotive-image-builder args from $AIB_EXTRA_ARGS_FILE"
  AIB_ARGS="$(cat "$AIB_EXTRA_ARGS_FILE")"
else
  echo "No extra/override AIB args file found"
fi

arch="$(params.target-architecture)"
case "$arch" in
  "arm64")
    arch="aarch64"
    ;;
  "amd64")
    arch="x86_64"
    ;;
esac

get_flag_value() {
  flag_name="$1"; shift
  args_str="$*"
  val=$(echo "$args_str" | sed -nE "s/.*${flag_name}=([^ ]+).*/\1/p" | head -n1)
  if [ -n "$val" ]; then
    echo "$val"; return 0
  fi
  val=$(echo "$args_str" | awk -v f="$flag_name" '{for (i=1;i<=NF;i++) if ($i==f && (i+1)<=NF) {print $(i+1); exit}}')
  [ -n "$val" ] && echo "$val"
}

USE_OVERRIDE=false
if [ -f "$AIB_OVERRIDE_ARGS_FILE" ]; then
  USE_OVERRIDE=true
  override_format=$(get_flag_value "--format" $AIB_ARGS)
  if [ -z "$override_format" ]; then
    override_format=$(get_flag_value "--export" $AIB_ARGS)
  fi
  override_distro=$(get_flag_value "--distro" $AIB_ARGS)
  override_target=$(get_flag_value "--target" $AIB_ARGS)
  [ -n "$override_distro" ] && cleanName="$override_distro-${cleanName#*-}"
  [ -n "$override_target" ] && cleanName="${cleanName%-*}-$override_target"
  if [ -n "$override_format" ]; then
    case "$override_format" in
      image|raw)
        file_extension=".raw" ;;
      qcow2)
        file_extension=".qcow2" ;;
      *)
        file_extension=".$override_format" ;;
    esac
  fi
  exportFile=${cleanName}${file_extension}
fi

CONTAINER_PUSH="$(params.container-push)"
BUILD_DISK_IMAGE="$(params.build-disk-image)"
EXPORT_OCI="$(params.export-oci)"
BUILDER_IMAGE="$(params.builder-image)"
CLUSTER_REGISTRY_ROUTE="$(params.cluster-registry-route)"

BOOTC_CONTAINER_NAME="localhost/aib-build:$(params.distro)-$(params.target)"

BUILD_CONTAINER_ARG=""
LOCAL_BUILDER_IMAGE="localhost/aib-build:$(params.distro)"

# For bootc builds, if no builder-image is provided but cluster-registry-route is set,
# use the image that prepare-builder cached in the cluster registry
if [ -z "$BUILDER_IMAGE" ] && [ "$BUILD_MODE" = "bootc" ] && [ -n "$CLUSTER_REGISTRY_ROUTE" ]; then
  NAMESPACE=$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace)
  BUILDER_IMAGE="${CLUSTER_REGISTRY_ROUTE}/${NAMESPACE}/aib-build:$(params.distro)"
  echo "Using builder image from cluster registry: $BUILDER_IMAGE"
fi

if [ -n "$BUILDER_IMAGE" ] && [ "$BUILD_MODE" = "bootc" ]; then
  echo "Pulling builder image to local storage: $BUILDER_IMAGE"

  TOKEN=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token 2>/dev/null || echo "")
  if [ -n "$TOKEN" ]; then
    REGISTRY_HOST=$(echo "$BUILDER_IMAGE" | cut -d'/' -f1)
    cat > /tmp/builder-auth.json <<EOF
{
  "auths": {
    "$REGISTRY_HOST": {
      "auth": "$(echo -n "serviceaccount:$TOKEN" | base64 -w0)"
    }
  }
}
EOF
    skopeo copy --authfile=/tmp/builder-auth.json \
      "docker://$BUILDER_IMAGE" \
      "containers-storage:$LOCAL_BUILDER_IMAGE"
  else
    skopeo copy \
      "docker://$BUILDER_IMAGE" \
      "containers-storage:$LOCAL_BUILDER_IMAGE"
  fi

  echo "Builder image ready in local storage: $LOCAL_BUILDER_IMAGE"
  BUILD_CONTAINER_ARG="--build-container $LOCAL_BUILDER_IMAGE"
fi

if [ "$USE_OVERRIDE" = true ]; then
  build_command="aib --verbose \
  build \
  $CUSTOM_DEFS \
  --build-dir=/output/_build \
  --osbuild-manifest=/output/image.json \
  $AIB_ARGS \
  $MANIFEST_FILE \
  /output/${exportFile}"
  echo "Running the build command (override): $build_command"
  eval "$build_command"
else
  case "$BUILD_MODE" in
    bootc)
      build_command="aib --verbose build \
      --distro $(params.distro) \
      --target $(params.target) \
      --arch=${arch} \
      --build-dir=/output/_build \
      --osbuild-manifest=/output/image.json \
      $BUILD_CONTAINER_ARG \
      $CUSTOM_DEFS \
      $AIB_ARGS \
      $MANIFEST_FILE \
      $BOOTC_CONTAINER_NAME \
      /output/${exportFile}"
      echo "Running bootc build: $build_command"
      eval "$build_command"

      if [ -n "$CONTAINER_PUSH" ]; then
        echo "Pushing container to registry: $CONTAINER_PUSH"
        skopeo copy \
          --authfile="$REGISTRY_AUTH_FILE" \
          "containers-storage:$BOOTC_CONTAINER_NAME" \
          "docker://$CONTAINER_PUSH"
        echo "Container pushed successfully to $CONTAINER_PUSH"
      fi
      ;;
    image)
      build_command="aib-dev --verbose \
      build \
      $CUSTOM_DEFS \
      --distro $(params.distro) \
      --target $(params.target) \
      --arch=${arch} \
      --format $(params.export-format) \
      --build-dir=/output/_build \
      --osbuild-manifest=/output/image.json \
      $AIB_ARGS \
      $MANIFEST_FILE \
      /output/${exportFile}"
      echo "Running the build command: $build_command"
      eval "$build_command"
      ;;
    package)
      build_command="aib-dev --verbose \
      build \
      $CUSTOM_DEFS \
      --distro $(params.distro) \
      --target $(params.target) \
      --arch=${arch} \
      --format $(params.export-format) \
      --build-dir=/output/_build \
      --osbuild-manifest=/output/image.json \
      $AIB_ARGS \
      $MANIFEST_FILE \
      /output/${exportFile}"
      echo "Running the build command: $build_command"
      eval "$build_command"
      ;;
    *)
      echo "Error: Unknown build mode '$BUILD_MODE'. Supported modes: bootc, image, package"
      exit 1
      ;;
  esac
fi

echo "Build completed. Contents of output directory:"
ls -la /output/ || true

pushd /output
ln -sf ./${exportFile} ./disk.img

echo "copying build artifacts to shared workspace..."

mkdir -p $(workspaces.shared-workspace.path)

if [ -d "/output/${exportFile}" ]; then
    echo "${exportFile} is a directory, copying recursively..."
    cp -rv "/output/${exportFile}" $(workspaces.shared-workspace.path)/ || echo "Failed to copy ${exportFile}"
else
    echo "${exportFile} is a regular file, copying..."
    cp -v "/output/${exportFile}" $(workspaces.shared-workspace.path)/ || echo "Failed to copy ${exportFile}"
fi

pushd $(workspaces.shared-workspace.path)
if [ -d "${exportFile}" ]; then
    echo "Creating symlink to directory ${exportFile}"
    ln -sf ${exportFile} disk.img
elif [ -f "${exportFile}" ]; then
    echo "Creating symlink to file ${exportFile}"
    ln -sf ${exportFile} disk.img
else
    echo "Warning: ${exportFile} not found in workspace, cannot create symlink"
fi
popd

cp -v /output/image.json $(workspaces.shared-workspace.path)/image.json || echo "Failed to copy image.json"

echo "Contents of shared workspace:"
ls -la $(workspaces.shared-workspace.path)/

COMPRESSION="$(params.compression)"
echo "Requested compression: $COMPRESSION"

ensure_lz4() {
  if ! command -v lz4 >/dev/null 2>&1; then
    echo "lz4 not found. Attempting to install..."
    if command -v dnf >/dev/null 2>&1; then
      dnf -y install lz4 || true
    fi
    if command -v microdnf >/dev/null 2>&1; then
      microdnf install -y lz4 || true
    fi
    if command -v yum >/dev/null 2>&1; then
      yum -y install lz4 || true
    fi
    if ! command -v lz4 >/dev/null 2>&1; then
      echo "lz4 still not available; falling back to gzip"
      COMPRESSION="gzip"
    fi
  fi
}

if [ "$COMPRESSION" = "lz4" ]; then
  ensure_lz4
fi

compress_file_gzip() {
  src="$1"; dest="$2"
  gzip -c "$src" > "$dest"
}

compress_file_lz4() {
  src="$1"; dest="$2"
  lz4 -z -f -q "$src" "$dest"
}

tar_dir_gzip() {
  dir="$1"; out="$2"
  tar -C $(workspaces.shared-workspace.path) -czf "$out" "$dir"
}

tar_dir_lz4() {
  dir="$1"; out="$2"
  tar -C $(workspaces.shared-workspace.path) -cf - "$dir" | lz4 -z -f -q > "$out"
}

compress_file() {
  src="$1"; dest="$2"
  case "$COMPRESSION" in
    lz4) compress_file_lz4 "$src" "$dest" ;;
    gzip|*) compress_file_gzip "$src" "$dest" ;;
  esac
}

tar_dir() {
  dir="$1"; out="$2"
  case "$COMPRESSION" in
    lz4) tar_dir_lz4 "$dir" "$out" ;;
    gzip|*) tar_dir_gzip "$dir" "$out" ;;
  esac
}

case "$COMPRESSION" in
  lz4)
    EXT_FILE=".lz4"
    EXT_DIR=".tar.lz4"
    ;;
  gzip|*)
    EXT_FILE=".gz"
    EXT_DIR=".tar.gz"
    ;;
esac

final_name=""
if [ -d "$(workspaces.shared-workspace.path)/${exportFile}" ]; then
  echo "Preparing compressed parts for directory ${exportFile}..."
  final_compressed_name="${exportFile}${EXT_DIR}"
  parts_dir="$(workspaces.shared-workspace.path)/${final_compressed_name}-parts"
  mkdir -p "$parts_dir"
  (
    cd "$(workspaces.shared-workspace.path)"
    for item in "${exportFile}"/*; do
      [ -e "$item" ] || continue
      base=$(basename "$item")
      if [ -f "$item" ]; then
        echo "Creating $parts_dir/${base}${EXT_FILE}"
        compress_file "$item" "$parts_dir/${base}${EXT_FILE}" || echo "Failed to create $parts_dir/${base}${EXT_FILE}"
      elif [ -d "$item" ]; then
        echo "Creating $parts_dir/${base}${EXT_DIR}"
        tar_dir "${exportFile}/$base" "$parts_dir/${base}${EXT_DIR}" || echo "Failed to create $parts_dir/${base}${EXT_DIR}"
      fi
    done
  )
  echo "Creating compressed archive ${final_compressed_name} in shared workspace..."
  tar_dir "${exportFile}" "$(workspaces.shared-workspace.path)/${final_compressed_name}" || echo "Failed to create ${final_compressed_name}"
  echo "Compressed archive size:" && ls -lah $(workspaces.shared-workspace.path)/${final_compressed_name} || true
  if [ -f "$(workspaces.shared-workspace.path)/${final_compressed_name}" ]; then
    echo "Removing uncompressed directory ${exportFile} (keeping parts directory)"
    rm -rf "$(workspaces.shared-workspace.path)/${exportFile}"
    pushd $(workspaces.shared-workspace.path)
    ln -sf ${final_compressed_name} disk.img
    final_name="${final_compressed_name}"
    popd
    echo "Available artifacts:"
    ls -la $(workspaces.shared-workspace.path)/ || true
    if [ -d "$(workspaces.shared-workspace.path)/${final_compressed_name}-parts" ]; then
      echo "Individual compressed parts in ${final_compressed_name}-parts/:"
      ls -la "$(workspaces.shared-workspace.path)/${final_compressed_name}-parts/" || true
    fi
  fi
elif [ -f "$(workspaces.shared-workspace.path)/${exportFile}" ]; then
  echo "Creating compressed file ${exportFile}${EXT_FILE} in shared workspace..."
  compress_file "$(workspaces.shared-workspace.path)/${exportFile}" "$(workspaces.shared-workspace.path)/${exportFile}${EXT_FILE}" || echo "Failed to create ${exportFile}${EXT_FILE}"
  echo "Compressed file size:" && ls -lah $(workspaces.shared-workspace.path)/${exportFile}${EXT_FILE} || true
  if [ -f "$(workspaces.shared-workspace.path)/${exportFile}${EXT_FILE}" ]; then
    pushd $(workspaces.shared-workspace.path)
    ln -sf ${exportFile}${EXT_FILE} disk.img
    final_name="${exportFile}${EXT_FILE}"
    popd
  fi
fi

if [ -z "$final_name" ]; then
  guess=$(ls -1 $(workspaces.shared-workspace.path)/${cleanName}* 2>/dev/null | head -n1)
  if [ -n "$guess" ]; then
    final_name=$(basename "$guess")
  fi
fi
if [ -n "$final_name" ]; then
  echo "Writing artifact filename to Tekton result: $final_name"
  echo "$final_name" > /tekton/results/artifact-filename || echo "Failed to write Tekton result"
  echo "Verifying Tekton result file:"
  cat /tekton/results/artifact-filename || echo "Failed to read Tekton result"
else
  echo "Warning: final_name is empty, no artifact filename will be recorded"
fi


echo "Syncing filesystem to ensure all artifacts are written..."
sync
echo "Filesystem sync completed"
