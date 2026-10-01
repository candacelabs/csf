#!/usr/bin/env bash
# Runs inside the pinned code-generation image with the repository root
# mounted at /workspace. Use generate.sh from the host.
set -euo pipefail

module_root=/workspace/candace
proto_root=/workspace/candace/services/copilot-adapter/proto
mode="${1:-write}"
case "${mode}" in
  write)
    output_root="${module_root}"
    ;;
  check)
    output_root="$(mktemp -d /tmp/candace-copilot-proto-output.XXXXXX)"
    ;;
  *)
    echo "usage: $0 [write|check]" >&2
    exit 2
    ;;
esac

test "$(protoc --version)" = "libprotoc 35.1"
test "$(protoc-gen-go --version)" = "protoc-gen-go v1.36.11"

export GOCACHE=/tmp/candace-copilot-proto-gocache
export GOMODCACHE=/tmp/candace-copilot-proto-modcache
export GOPATH=/tmp/candace-copilot-proto-gopath

plugin_dir="$(mktemp -d /tmp/candace-copilot-liquid-plugin.XXXXXX)"
(cd /workspace/candace &&
  go build -mod=readonly -o "${plugin_dir}/protoc-gen-liquidproto" ./pkg/liquidproto/cmd/protoc-gen-liquidproto)

protoc \
  -I /workspace/candace/pkg \
  -I "${proto_root}" \
  -I /usr/local/include \
  "--go_out=module=github.com/candacelabs/csf:${output_root}" \
  "--plugin=protoc-gen-liquidproto=${plugin_dir}/protoc-gen-liquidproto" \
  "--liquidproto_out=module=github.com/candacelabs/csf:${output_root}" \
  candace/copilot/v1/adapter.proto

if [[ "${mode}" == check ]]; then
  generated_files=(
    services/copilot-adapter/proto/candace/copilot/v1/adapter.pb.go
    services/copilot-adapter/proto/candace/copilot/v1/adapter_liquid.pb.go
  )
  for generated_file in "${generated_files[@]}"; do
    diff -u "${module_root}/${generated_file}" "${output_root}/${generated_file}"
  done
fi
