#!/usr/bin/env bash
set -ex

SCRIPT_DIR="$(
    cd "$(dirname "$BASH_SOURCE[0]")"
    pwd
)"

export REGISTRY=${REGISTRY:-docker.io}
export REPOSITORY=${REPOSITORY:-kubevirt}
export IMAGE_NAME=${IMAGE_NAME:-fedora-extended}
export TAG=${TAG:-tests}
export CLOUD_CONFIG_PATH=${CLOUD_CONFIG_PATH:-user-cloud-config}

readonly VM_IMAGE_URL="https://download.fedoraproject.org/pub/fedora/linux/releases/32/Cloud/x86_64/images/Fedora-Cloud-Base-32-1.6.x86_64.qcow2"
readonly VM_IMAGE="source-image.qcow2"

full_image_tag="${REGISTRY}/${REPOSITORY}/${IMAGE_NAME}:${TAG}"
build_directory="${IMAGE_NAME}_build"
new_vm_image_name="provisioned-image.qcow2"

trap 'cleanup' EXIT

function cleanup() {
    echo "cleanup"
    rm -rf "${build_directory}" || true
    rm -f "temp-${VM_IMAGE}" || true
    rm -f Dockerfile || true
    rm -f "${IMAGE_NAME}-${TAG}.tar"
}

function customize_image() {
  local source_image=$1
  local customized_image=$2
  local cloud_config=$3

  # Backup the VM image and pass copy of the original image
  # in case customizing script fail.
  vm_image_temp="temp-${source_image}"
  cp "$source_image" "$vm_image_temp"

  # TODO: convert this script to container
  ${SCRIPT_DIR}/customize-image.sh "$vm_image_temp" "$customized_image" "$cloud_config"

  # Backup no longer needed.
  rm -f "$vm_image_temp"
}

pushd "${SCRIPT_DIR}"
  cleanup

  if ! [ -e "$VM_IMAGE" ]; then
    # Download base VM image
    curl -L $VM_IMAGE_URL -o $VM_IMAGE
  fi

  mkdir "${build_directory}"

  customize_image "$VM_IMAGE" "${build_directory}/${new_vm_image_name}" "${CLOUD_CONFIG_PATH}"

  ${SCRIPT_DIR}/build-container-disk.sh "${IMAGE_NAME}" "${TAG}" "${build_directory}/${new_vm_image_name}"

  ${SCRIPT_DIR}/publish-container-disk.sh "${IMAGE_NAME}:${TAG}" "$full_image_tag"
popd
