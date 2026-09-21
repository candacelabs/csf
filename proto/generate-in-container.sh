#!/usr/bin/env bash
# Generate the public shared contracts in the pinned protobuf container.
set -euo pipefail
module_root=/workspace/candace
mode="${1:-write}"
case "${mode}" in
  write) output="${module_root}" ;;
  check) output="$(mktemp -d /tmp/candace-public-proto.XXXXXX)" ;;
  *) exit 2 ;;
esac
test "$(protoc --version)" = "libprotoc 35.1"
test "$(protoc-gen-go --version)" = "protoc-gen-go v1.36.11"
export GOCACHE="${GOCACHE:-/tmp/candace-proto-gocache}"
export GOPATH="${GOPATH:-/tmp/candace-proto-gopath}"
plugin_dir="$(mktemp -d /tmp/candace-public-liquid-plugin.XXXXXX)"
(cd "${module_root}" && go build -mod=readonly -o "${plugin_dir}/protoc-gen-liquidproto" ./pkg/liquidproto/cmd/protoc-gen-liquidproto)
public_schemas=(
  candace/telemetry/v1/telemetry.proto
  candace/candaceos/v1/app_source.proto
  candace/candaceos/v1/node_control.proto
  candace/candaceos/v1/control_runtime.proto
  candace/candaceos/v1/harness.proto
  candace/candaceos/v1/webui.proto
  candace/work/v1/work.proto
  candace/brainspine/v1/brainspine.proto
)

protoc -I "${module_root}/pkg" -I "${module_root}/proto" -I /usr/local/include \
  "--go_out=module=github.com/candacelabs/csf:${output}" \
  "--plugin=protoc-gen-liquidproto=${plugin_dir}/protoc-gen-liquidproto" \
  "--liquidproto_out=module=github.com/candacelabs/csf:${output}" \
  "${public_schemas[@]}"
if [[ "${mode}" == check ]]; then
  for schema in "${public_schemas[@]}"; do
    for suffix in .pb.go _liquid.pb.go; do
      generated="proto/${schema%.proto}${suffix}"
      diff -u "${module_root}/${generated}" "${output}/${generated}"
    done
  done
fi
