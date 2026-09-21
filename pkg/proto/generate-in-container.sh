#!/usr/bin/env bash
# Runs inside the pinned code-generation image with the repository root mounted
# at /workspace. Use generate.sh from the host.
#
# Every contract this script owns now belongs to the public module: liquidproto,
# boundedbuffer, cron and widget/refinement are all packages of
# github.com/candacelabs/csf/pkg. `candace/pkg` is the single -I root, which
# is what keeps the descriptor keys liquidproto/v1/..., boundedbuffer/v1/...,
# cron/v1/... and widget/refinement/v1/... unchanged across the moves that
# brought these packages together.
set -euo pipefail

repo_root=/workspace
pkg_root="${repo_root}/candace/pkg"
mode="${1:-write}"
case "${mode}" in
  write)
    output="${pkg_root}"
    ;;
  check)
    output="$(mktemp -d /tmp/candace-pkg-proto.XXXXXX)"
    ;;
  *)
    echo "usage: $0 [write|check]" >&2
    exit 2
    ;;
esac

test "$(protoc --version)" = "libprotoc 35.1"
test "$(protoc-gen-go --version)" = "protoc-gen-go v1.36.11"

export GOCACHE=/tmp/candace-pkg-proto-gocache
export GOMODCACHE=/tmp/candace-pkg-proto-modcache
export GOPATH=/tmp/candace-pkg-proto-gopath

plugin_dir="$(mktemp -d /tmp/candace-pkg-liquid-plugin.XXXXXX)"
(cd "${repo_root}/candace" && \
  go build -mod=readonly -o "${plugin_dir}/protoc-gen-liquidproto" ./pkg/liquidproto/cmd/protoc-gen-liquidproto)

module=github.com/candacelabs/csf/pkg

protoc \
  -I "${pkg_root}" \
  -I /usr/local/include \
  "--go_out=module=${module}:${output}" \
  liquidproto/v1/refinement.proto

protoc \
  -I "${pkg_root}" \
  -I /usr/local/include \
  "--go_out=module=${module}:${output}" \
  "--plugin=protoc-gen-liquidproto=${plugin_dir}/protoc-gen-liquidproto" \
  "--liquidproto_out=module=${module}:${output}" \
  boundedbuffer/v1/buffer.proto

protoc \
  -I "${pkg_root}" \
  -I /usr/local/include \
  "--go_out=module=${module}:${output}" \
  "--plugin=protoc-gen-liquidproto=${plugin_dir}/protoc-gen-liquidproto" \
  "--liquidproto_out=module=${module}:${output}" \
  cron/v1/cron.proto

protoc \
  -I "${pkg_root}" \
  -I /usr/local/include \
  "--go_out=module=${module}:${output}" \
  "--plugin=protoc-gen-liquidproto=${plugin_dir}/protoc-gen-liquidproto" \
  "--liquidproto_out=module=${module}:${output}" \
  widget/refinement/v1/refinement.proto

if [[ "${mode}" == check ]]; then
  generated_files=(
    liquidproto/v1/refinement.pb.go
    boundedbuffer/v1/buffer.pb.go
    boundedbuffer/v1/buffer_liquid.pb.go
    cron/v1/cron.pb.go
    cron/v1/cron_liquid.pb.go
    widget/refinement/v1/refinement.pb.go
    widget/refinement/v1/refinement_liquid.pb.go
  )
  for generated_file in "${generated_files[@]}"; do
    diff -u "${pkg_root}/${generated_file}" "${output}/${generated_file}"
  done
fi
