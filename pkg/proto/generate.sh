#!/usr/bin/env bash
# Build the pinned protobuf toolchain and regenerate or verify the Liquid Proto
# contracts owned by the public module's pkg/ tree: liquidproto, boundedbuffer,
# cron and widget/refinement. No host Go/protoc installation is used.
#
# This chain owns the `candace/pkg` -I root. The `candace/<domain>/v1` contract
# tree has its own chain under go/proto, which reuses the toolchain image built
# from the Dockerfile beside this script.
set -euo pipefail

proto_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${proto_dir}/../../.." && pwd)"
mode="${1:-write}"

case "${mode}" in
  write | check) ;;
  *)
    echo "usage: $0 [write|check]" >&2
    exit 2
    ;;
esac

toolchain_image="${CANDACE_PROTO_TOOLCHAIN_IMAGE:-candace/proto-codegen:go1.26.5-protoc35.1}"

docker build \
  --platform linux/amd64 \
  --file "${proto_dir}/Dockerfile.codegen" \
  --tag "${toolchain_image}" \
  "${proto_dir}"

docker run --rm \
  --platform linux/amd64 \
  --user "$(id -u):$(id -g)" \
  --volume "${repo_root}:/workspace" \
  "${toolchain_image}" \
  ./candace/pkg/proto/generate-in-container.sh "${mode}"
