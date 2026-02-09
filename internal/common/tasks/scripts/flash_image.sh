#!/bin/bash
set -euo pipefail

echo "=== Jumpstarter Flash Operation ==="
echo "Image: ${IMAGE_REF}"
echo "Exporter Selector: ${EXPORTER_SELECTOR}"

export JMP_CLIENT_CONFIG="${JMP_CLIENT_CONFIG:-/workspace/jumpstarter-client/client.yaml}"

if [[ ! -f "${JMP_CLIENT_CONFIG}" ]]; then
    echo "ERROR: Jumpstarter client config not found at ${JMP_CLIENT_CONFIG}"
    exit 1
fi

echo "Using client config: ${JMP_CLIENT_CONFIG}"

FLASH_CMD="${FLASH_CMD:-j storage flash \{image_uri\}}"
FLASH_CMD=$(echo "${FLASH_CMD}" | sed "s|{image_uri}|${IMAGE_REF}|g")


LEASE_DURATION="${LEASE_DURATION:-03:00:00}"

echo "Flash command: ${FLASH_CMD}"
echo "Lease duration: ${LEASE_DURATION}"
echo ""

echo "Creating lease on exporter matching: ${EXPORTER_SELECTOR}"

LEASE_NAME=$(jmp create lease --client-config "${JMP_CLIENT_CONFIG}" -l "${EXPORTER_SELECTOR}" --duration "${LEASE_DURATION}" -o name)

if [[ -z "${LEASE_NAME}" ]]; then
    echo "ERROR: Failed to create lease"
    exit 1
fi

echo ""
echo "Lease acquired: ${LEASE_NAME}"
echo "Duration: ${LEASE_DURATION}"
echo ""

# Write lease ID to Tekton result
if [[ -n "${RESULTS_LEASE_ID_PATH:-}" ]]; then
    echo -n "${LEASE_NAME}" > "${RESULTS_LEASE_ID_PATH}"
fi

FLASH_SUCCESS=false

cleanup() {
    if [[ "${FLASH_SUCCESS}" != "true" ]]; then
        echo ""
        echo "Releasing lease ${LEASE_NAME} due to failure..."
        jmp delete leases --client-config "${JMP_CLIENT_CONFIG}" "${LEASE_NAME}" || true
    fi
}
trap cleanup EXIT

echo "Starting flash operation..."
echo "Executing: ${FLASH_CMD}"

# Read OCI credentials from mounted secret workspace if available
OCI_USERNAME=""
OCI_PASSWORD=""
FLASH_OCI_AUTH_PATH="${FLASH_OCI_AUTH_PATH:-/workspace/flash-oci-auth}"
if [ -f "${FLASH_OCI_AUTH_PATH}/username" ] && [ -f "${FLASH_OCI_AUTH_PATH}/password" ]; then
    OCI_USERNAME=$(cat "${FLASH_OCI_AUTH_PATH}/username")
    OCI_PASSWORD=$(cat "${FLASH_OCI_AUTH_PATH}/password")
fi

# Build jmp shell command with optional OCI credentials written to file on exporter
JMP_SHELL_ARGS="--client-config ${JMP_CLIENT_CONFIG} --lease ${LEASE_NAME}"

if [ -n "${OCI_USERNAME}" ] && [ -n "${OCI_PASSWORD}" ]; then
    echo "OCI credentials provided, forwarding to exporter via auth file"
    REGISTRY_HOST=$(echo "${IMAGE_REF}" | cut -d'/' -f1)
    AUTH_B64=$(printf '%s:%s' "${OCI_USERNAME}" "${OCI_PASSWORD}" | base64 | tr -d '\n')

    # Function to escape JSON string values
    escape_json() {
        printf '%s\n' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
    }

    # Safely construct JSON without jq dependency
    REGISTRY_HOST_ESCAPED=$(escape_json "${REGISTRY_HOST}")
    AUTH_B64_ESCAPED=$(escape_json "${AUTH_B64}")
    AUTH_JSON="{\"auths\":{\"${REGISTRY_HOST_ESCAPED}\":{\"auth\":\"${AUTH_B64_ESCAPED}\"}}}"

    # Pipe auth config through stdin to avoid credentials in command args
    # shellcheck disable=SC2086
    set +e  # Temporarily disable errexit to capture exit code
    echo "${AUTH_JSON}" | jmp shell ${JMP_SHELL_ARGS} -- sh -c "
        mkdir -p /tmp/.oci-auth && \
        cat > /tmp/.oci-auth/config.json && \
        REGISTRY_AUTH_FILE=/tmp/.oci-auth/config.json ${FLASH_CMD}; \
        EXIT_CODE=\$?; \
        rm -rf /tmp/.oci-auth; \
        exit \$EXIT_CODE
    "
    FLASH_EXIT=$?
    set -e  # Restore errexit
else
    # No credentials, run flash command directly
    # shellcheck disable=SC2086
    set +e  # Temporarily disable errexit to capture exit code
    jmp shell ${JMP_SHELL_ARGS} -- ${FLASH_CMD}
    FLASH_EXIT=$?
    set -e  # Restore errexit
fi

if [ ${FLASH_EXIT} -ne 0 ]; then
    echo ""
    echo "ERROR: Flash command failed"
    exit 1
fi

FLASH_SUCCESS=true