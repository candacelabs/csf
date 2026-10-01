#!/usr/bin/env bash
# Shared proto owns Go/Python/wire types; upstream protoc plugins own projections.
set -euo pipefail
research_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
module_root="$(cd "${research_dir}/../../.." && pwd)"
mode="${1:-write}"
case "${mode}" in write|check) ;; *) exit 2 ;; esac
image=candace/brain-spine-codegen:go1.26.5-protoc35.1
buf_image=bufbuild/buf:1.72.0@sha256:65bd496a89c762ad7151ca9e7d885a45dacb3671a8e8ec39738b9f844d3405ea
if ! docker image inspect "${image}" >/dev/null 2>&1; then
  docker build -f "${research_dir}/Dockerfile" -t "${image}" "${research_dir}"
fi
output="${research_dir}/generated"
temporary="$(mktemp -d /tmp/candace-brain-spine-gen.XXXXXX)"
trap 'rm -rf -- "${temporary}"' EXIT
mkdir -p "${temporary}/python" "${temporary}/openapi"
# API projection is an OCaml compiler target, with the same pinned toolchain
# used by csfc. The host needs neither Python nor a protobuf Python install.
(cd "${module_root}" && CANDACE_BAZEL_WORKSPACE="${module_root}" bash tools/bazel.sh build //csf/compiler/api_codegen:generate)
docker run --rm --user "$(id -u):$(id -g)" \
  -v "${module_root}:/workspace/candace:ro" -v "${temporary}:/out" "${image}" \
  protoc -I /workspace/candace/proto -I /workspace/candace/pkg -I /usr/local/include \
  --python_out=/out/python \
  candace/brainspine/v1/brainspine.proto candace/email/v1/email.proto \
  candace/provenance/v1/receipt.proto liquidproto/v1/refinement.proto
docker run --rm --user "$(id -u):$(id -g)" \
  -v "${module_root}:/workspace/candace:ro" -v "${temporary}:/out" "${image}" \
  protoc -I /workspace/candace/csf/tools/codegen/api -I /workspace/candace/proto \
  -I /workspace/candace/pkg -I /opt/googleapis -I /usr/local/include \
  --openapiv3_out=/out/openapi \
  --descriptor_set_out=/out/brainspine.descriptor.pb --include_imports \
  adapter.proto
# Buf uses protobuf's own descriptor schema; no handwritten wire decoder.
docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp \
  --entrypoint buf -v "${temporary}:/out" "${buf_image}" \
  convert /out/brainspine.descriptor.pb#format=binpb \
  --type google.protobuf.FileDescriptorSet \
  --from /out/brainspine.descriptor.pb#format=binpb \
  --to /out/descriptor.json#format=json
"${module_root}/bazel-bin/csf/compiler/api_codegen/generate.exe" \
  "${temporary}/descriptor.json" "${temporary}" "${temporary}/api_cgen.go"
rm "${temporary}/descriptor.json"
docker run --rm --user "$(id -u):$(id -g)" -v "${temporary}:/out" "${image}" gofmt -w /out/api_cgen.go
if [[ "${mode}" == check ]]; then
  diff -u "${module_root}/csf/api_cgen.go" "${temporary}/api_cgen.go"
else
  cp "${temporary}/api_cgen.go" "${module_root}/csf/api_cgen.go"
fi
rm "${temporary}/api_cgen.go"
if [[ "${mode}" == check ]]; then
  diff -ru --exclude=__pycache__ "${output}" "${temporary}"
else
  mkdir -p "${output}"
  cp -R "${temporary}/." "${output}/"
fi
