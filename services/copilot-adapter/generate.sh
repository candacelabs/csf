#!/usr/bin/env bash
# Regenerate or verify the Go HTTP boundary derived from openapi.yaml.
set -euo pipefail

service_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mode="${1:-write}"
codegen_image="${COPILOT_ADAPTER_CODEGEN_IMAGE:-candace/copilot-adapter-codegen:oapi-v2.8.0-go1.26.5}"

case "${mode}" in
  write | check) ;;
  *)
    echo "usage: $0 [write|check]" >&2
    exit 2
    ;;
esac

if ! docker image inspect "${codegen_image}" >/dev/null 2>&1; then
  docker build \
  --platform linux/amd64 \
  --file "${service_dir}/../../tools/oapi-codegen/Dockerfile" \
  --tag "${codegen_image}" \
  "${service_dir}/../../tools/oapi-codegen"
fi

run_codegen() {
  local source_dir="$1"
  docker run --rm \
    --platform linux/amd64 \
    --user "$(id -u):$(id -g)" \
    --env HOME=/tmp \
    --volume "${source_dir}:/src" \
    --workdir /src \
    "${codegen_image}" \
    --config oapi-codegen.yaml openapi.yaml
}

if [[ "${mode}" == write ]]; then
  case "${service_dir}" in
    */services/copilot-adapter) ;;
    *)
      echo "refusing to generate in unexpected path: ${service_dir}" >&2
      exit 1
      ;;
  esac
  mkdir -p "${service_dir}/gen/api"
  run_codegen "${service_dir}"
  exit 0
fi

check_dir="$(mktemp -d "${TMPDIR:-/tmp}/candace-copilot-adapter-openapi.XXXXXX")"
cleanup() {
  rm -rf -- "${check_dir}"
}
trap cleanup EXIT

cp "${service_dir}/openapi.yaml" "${service_dir}/oapi-codegen.yaml" "${check_dir}/"
mkdir -p "${check_dir}/gen/api"
run_codegen "${check_dir}"
diff -u "${service_dir}/gen/api/api.gen.go" "${check_dir}/gen/api/api.gen.go"
