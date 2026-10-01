#!/usr/bin/env bash
# Standalone public-module generator; reuses the pinned image when available.
set -euo pipefail
module_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mode="${1:-write}"
case "${mode}" in write|check) ;; *) exit 2 ;; esac
image="${CANDACE_PROTO_TOOLCHAIN_IMAGE:-candace/proto-codegen:go1.26.5-protoc35.1}"
if ! docker image inspect "${image}" >/dev/null 2>&1; then
  docker build --platform linux/amd64 --file "${module_root}/pkg/proto/Dockerfile.codegen" --tag "${image}" "${module_root}/pkg/proto"
fi
docker run --rm --platform linux/amd64 --user "$(id -u):$(id -g)" \
  --volume "${module_root}:/workspace/candace" "${image}" \
  ./candace/proto/generate-in-container.sh "${mode}"
