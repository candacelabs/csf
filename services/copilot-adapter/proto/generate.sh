#!/usr/bin/env bash
# Build the pinned protobuf toolchain and regenerate or verify the adapter's
# Liquid Proto configuration contract. No host Go/protoc installation is used.
set -euo pipefail

proto_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
module_root="$(cd "${proto_dir}/../../.." && pwd)"
mode="${1:-write}"

case "${mode}" in
  write | check) ;;
  *)
    echo "usage: $0 [write|check]" >&2
    exit 2
    ;;
esac

toolchain_image="${CANDACE_PROTO_TOOLCHAIN_IMAGE:-candace/proto-codegen:go1.26.5-protoc35.1}"

if ! docker image inspect "${toolchain_image}" >/dev/null 2>&1; then
  docker build \
    --platform linux/amd64 \
    --file "${module_root}/pkg/proto/Dockerfile.codegen" \
    --tag "${toolchain_image}" \
    "${module_root}/pkg/proto"
fi

docker run --rm \
  --platform linux/amd64 \
  --user "$(id -u):$(id -g)" \
  --volume "${module_root}:/workspace/candace" \
  "${toolchain_image}" \
  ./candace/services/copilot-adapter/proto/generate-in-container.sh "${mode}"
