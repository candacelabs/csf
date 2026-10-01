#!/usr/bin/env bash
# Share one pinned git-xet compile recipe between Docker and bounded CI jobs.
set -Eeuo pipefail

die() { printf 'workbench client: %s\n' "$*" >&2; exit 1; }

mode=${1:-}
if [[ "$mode" == dependencies || "$mode" == download-dependencies ]]; then
  [[ $# == 1 || $# == 2 ]] || die 'dependencies takes an optional package directory'
  # git2 uses OpenSSL; transitive -sys crates need cmake and pkg-config.
  if [[ "$mode" == download-dependencies ]]; then
    [[ $# == 2 ]] || die 'download-dependencies requires an output directory'
    mkdir -p "$2/packages"
    apt-get update
    apt-get -o "Dir::Cache::archives=$2/packages" --download-only \
      install -y --no-install-recommends cmake libssl-dev pkg-config
    rm -rf "$2/packages/partial" "$2/packages/lock"
  elif [[ $# == 2 ]]; then
    # The pinned base is identical to the downloader's base. Install its
    # complete missing/upgraded package set without another registry lookup.
    dpkg --install "$2"/packages/*.deb
  else
    apt-get update
    apt-get install -y --no-install-recommends cmake libssl-dev pkg-config
  fi
  rm -rf /var/lib/apt/lists/*
  exit 0
fi
if [[ "$mode" == compile ]]; then
  [[ $# == 2 || $# == 3 ]] || die 'compile requires an output directory and optional prepared dependencies'
  if [[ $# == 3 ]]; then
    bash "${BASH_SOURCE[0]}" dependencies "$3"
  fi
  : "${XET_CORE_REPO:?}" "${XET_CORE_REV:?}"
  cargo install --locked --root "$2" \
    --git "$XET_CORE_REPO" --rev "$XET_CORE_REV" git_xet
  "$2/bin/git-xet" --version
  exit 0
fi

component=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
dockerfile="$component/docker/Dockerfile.workbench"
input_digest=$(
  cd "$component"
  sha256sum docker/Dockerfile.workbench docker/workbench-client.sh | sha256sum | cut -d ' ' -f 1
)
input_key="xetcas-workbench-v2-$(uname -s)-$(uname -m)-$input_digest"

if [[ "$mode" == key ]]; then
  [[ $# == 1 ]] || die 'key takes no arguments'
  printf '%s\n' "$input_key"
  exit 0
fi
case "$mode:$#" in
  prepare:3|verify:2|prepare-toolchain:2|verify-toolchain:2) ;;
  *) die 'usage: workbench-client.sh key | prepare OUTPUT TOOLCHAIN | verify OUTPUT | {prepare,verify}-toolchain OUTPUT' ;;
esac
output=$(realpath -m "$2")

verify_inputs() {
  [[ -f "$1/input-key" && "$(cat "$1/input-key")" == "$input_key" ]] || \
    die 'build inputs or architecture do not match this checkout'
}

verify_client() {
  verify_inputs "$output"
  [[ -s "$output/out/bin/git-xet" && -x "$output/out/bin/git-xet" && -f "$output/git-xet.sha256" ]] || \
    die 'compiled client or checksum is missing'
  local expected actual
  expected=$(cat "$output/git-xet.sha256")
  [[ "$expected" =~ ^[0-9a-f]{64}$ ]] || die 'invalid client checksum'
  actual=$(sha256sum "$output/out/bin/git-xet" | cut -d ' ' -f 1)
  [[ "$actual" == "$expected" ]] || die 'compiled client checksum mismatch'
  printf 'verified %s\n' "$input_key"
}

verify_toolchain() {
  verify_inputs "$1"
  [[ -s "$1/packages.sha256" ]] || die 'dependency checksums are missing'
  # Re-enumeration rejects additional packages as well as missing/changed
  # bytes. The manifest is relative to the transferred artifact directory.
  [[ "$(cd "$1" && sha256sum packages/*.deb)" == "$(cat "$1/packages.sha256")" ]] || \
    die 'prepared dependency package set or checksum mismatch'
}

if [[ "$mode" == verify-toolchain || "$mode" == prepare-toolchain && -e "$output/input-key" ]]; then
  verify_toolchain "$output"
  exit 0
fi
if [[ "$mode" == verify || "$mode" == prepare && -e "$output/input-key" ]]; then
  verify_client
  exit 0
fi

default_arg() {
  local value
  value=$(sed -n "s/^ARG $1=//p" "$dockerfile")
  [[ -n "$value" && "$value" != *$'\n'* ]] || die "missing or duplicate Dockerfile argument: $1"
  printf '%s\n' "$value"
}
image=$(default_arg GIT_XET_BUILD_IMAGE)
revision=$(default_arg XET_CORE_REV)
repository=$(default_arg XET_CORE_REPO)
[[ "$image" =~ @sha256:[0-9a-f]{64}$ && "$revision" =~ ^[0-9a-f]{40}$ ]] || \
  die 'the builder image and upstream revision must be immutable pins'
if [[ "$mode" == prepare-toolchain ]]; then
  [[ ! -e "$output/packages" ]] || die 'unverified dependency packages already exist'
  mkdir -p "$output/packages"
  docker run --rm \
    --volume "$component/docker/workbench-client.sh:/recipe/workbench-client.sh:ro" \
    --volume "$output:/out" \
    "$image" bash /recipe/workbench-client.sh download-dependencies /out
  (cd "$output" && sha256sum packages/*.deb) > "$output/packages.sha256"
  printf '%s\n' "$input_key" > "$output/input-key"
  verify_toolchain "$output"
  exit 0
fi
[[ ! -e "$output/out/bin/git-xet" ]] || die 'unverified client already exists'
toolchain=$(realpath -m "$3")
verify_toolchain "$toolchain"
mkdir -p "$output/out/bin"
# Install the small prepared package set in the pinned base and compile.
# Temporary Cargo files stay in bounded memory; only the client is retained.
docker run --rm \
  --tmpfs /tmp:rw,exec,mode=1777,size=3g \
  --tmpfs /usr/local/cargo/registry:rw,exec,mode=1777,size=1g \
  --tmpfs /usr/local/cargo/git:rw,exec,mode=1777,size=256m \
  --env "XET_CORE_REPO=$repository" --env "XET_CORE_REV=$revision" \
  --volume "$component/docker/workbench-client.sh:/recipe/workbench-client.sh:ro" \
  --volume "$toolchain:/toolchain:ro" \
  --volume "$output/out:/out" \
  "$image" bash /recipe/workbench-client.sh compile /out /toolchain
sha256sum "$output/out/bin/git-xet" | cut -d ' ' -f 1 > "$output/git-xet.sha256"
printf '%s\n' "$input_key" > "$output/input-key"
verify_client
