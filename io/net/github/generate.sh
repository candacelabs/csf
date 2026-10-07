#!/usr/bin/env bash
# Regenerate or verify CSF's GitHub protocol client and its MCP tools from
# GitHub's own OpenAPI description, pinned below by commit and digest.
#
#   write  download the pinned description, filter it to operations.txt into
#          api.github.com.json, then generate client.gen.go (oapi-codegen) and
#          csf/githubtools/tools.gen.go (tools/githubgen) from that file.
#   check  offline: the filtered description holds exactly operations.txt,
#          and regenerating both Go projections from it changes nothing.
set -Eeuo pipefail
die() { printf 'github generate: %s\n' "$*" >&2; exit 2; }

source_commit=a0b077167882b10fe68c96af1703c9b71684637e
source_path=descriptions/api.github.com/api.github.com.2026-03-10.json
source_sha256=1429e93b5cbfa7547aa197e9553a6dacbd8d4aaf28012446ed730b152bcdf284
source_url="https://raw.githubusercontent.com/github/rest-api-description/${source_commit}/${source_path}"
go_image=golang:1.26.5-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599
codegen_image="${GITHUB_CODEGEN_IMAGE:-candace/copilot-adapter-codegen:oapi-v2.8.0-go1.26.5}"

mode="${1:-check}"
[[ "$mode" == write || "$mode" == check ]] || die "usage: generate.sh [write|check]"
package_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
root="$(cd -- "$package_dir/../.." && pwd -P)"
module_cache="$(go env GOMODCACHE 2>/dev/null || printf '%s/go/pkg/mod' "$HOME")"
build_cache="${TMPDIR:-/tmp}/github-generate-gocache"
mkdir -p "$build_cache"

if ! docker image inspect "$codegen_image" >/dev/null 2>&1; then
  docker build --platform linux/amd64 --file "$root/tools/oapi-codegen/Dockerfile" --tag "$codegen_image" "$root/tools/oapi-codegen"
fi

stage="$(mktemp -d "${TMPDIR:-/tmp}/github-generate.XXXXXX")"
trap 'rm -rf -- "$stage"' EXIT

githubgen() {
  docker run --rm --user "$(id -u):$(id -g)" --env HOME=/tmp --env GOCACHE=/gocache --env GOMODCACHE=/gomod \
    --env GOFLAGS=-mod=mod --env GOTELEMETRY=off \
    --volume "$root:/src" --volume "$stage:/stage" --volume "$module_cache:/gomod" --volume "$build_cache:/gocache" \
    --workdir /src "$go_image" go run ./tools/githubgen/cmd "$@"
}

codegen() {
  local directory="$1"
  docker run --rm --platform linux/amd64 --user "$(id -u):$(id -g)" --env HOME=/tmp \
    --volume "$directory:/work" --workdir /work "$codegen_image" --config oapi-codegen.yaml api.github.com.json
}

if [[ "$mode" == write ]]; then
  curl -fsSL -o "$stage/full.json" "$source_url"
  digest="$(sha256sum "$stage/full.json" | cut -d' ' -f1)"
  [[ "$digest" == "$source_sha256" ]] || die "$source_path at $source_commit has digest $digest, pinned $source_sha256"
  githubgen filter -allowlist io/net/github/operations.txt -description /stage/full.json -out io/net/github/api.github.com.json
  codegen "$package_dir"
  githubgen tools -description io/net/github/api.github.com.json -out csf/githubtools/tools.gen.go
  exit 0
fi

githubgen verify -allowlist io/net/github/operations.txt -description io/net/github/api.github.com.json
cp "$package_dir/api.github.com.json" "$package_dir/oapi-codegen.yaml" "$stage/"
codegen "$stage"
githubgen tools -description io/net/github/api.github.com.json -out /stage/tools.gen.go
drift=0
diff -u "$package_dir/client.gen.go" "$stage/client.gen.go" || drift=1
diff -u "$root/csf/githubtools/tools.gen.go" "$stage/tools.gen.go" || drift=1
[[ "$drift" == 0 ]] || die "drift: run bash io/net/github/generate.sh write and commit the result"
printf 'github generate: passed\n'
