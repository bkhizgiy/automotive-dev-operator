#!/usr/bin/env bash
# Wait until a container image is pullable from the registry.
set -euo pipefail

IMAGE_REF="${1:?image ref required (e.g. quay.io/org/image:tag)}"
AUTHFILE="${AUTHFILE:-${HOME}/.docker/config.json}"
MAX_ATTEMPTS="${MAX_ATTEMPTS:-120}"
SLEEP_SECS="${SLEEP_SECS:-30}"

for attempt in $(seq 1 "${MAX_ATTEMPTS}"); do
  if skopeo inspect "docker://${IMAGE_REF}" --authfile "${AUTHFILE}" >/dev/null 2>&1; then
    echo "Image available: ${IMAGE_REF}"
    exit 0
  fi
  echo "Waiting for ${IMAGE_REF} (${attempt}/${MAX_ATTEMPTS})..."
  sleep "${SLEEP_SECS}"
done

echo "Timeout waiting for ${IMAGE_REF}" >&2
exit 1
