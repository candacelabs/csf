#!/usr/bin/env bash
# Copyright 2026 Candace Labs
#
# Install the CSF kit: the csf binary (the harness host app and its client)
# and the rrsi-mine miner into a prefix, then, with --repo, run `csf init` in
# that repository. Every toolchain runs in a pinned container as the invoking
# user; nothing is built with a host Go or Rust. tools/kit/README.md is the
# guide.
#
#   tools/kit/install.sh [--repo /abs/path/to/repo] [--prefix DIR]

set -Eeuo pipefail

die() {
  printf 'kit install: %s\n' "$*" >&2
  exit 1
}

usage() {
  printf 'usage: tools/kit/install.sh [--repo /abs/path/to/repo] [--prefix DIR]\n' >&2
  exit 1
}

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly ROOT
readonly GO_IMAGE="golang:1.26.5-bookworm"
readonly RUST_IMAGE="rust:1.91-bookworm"
readonly RRSI_URL="https://github.com/candacelabs/rrsi.git"
readonly RRSI_REVISION="e741cf3"
readonly RRSI_CRATE="tools/rrsi-mine"
readonly CSF_PACKAGE="./app/csf/cmd"

repo=""
prefix="$HOME/.local/bin"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo) [[ $# -ge 2 ]] || usage; repo="$2"; shift 2 ;;
    --prefix) [[ $# -ge 2 ]] || usage; prefix="$2"; shift 2 ;;
    *) usage ;;
  esac
done
if [[ -n "$repo" ]]; then
  [[ "$repo" == /* ]] || die "--repo must be an absolute path: $repo"
  [[ -d "$repo" ]] || die "--repo is not a directory: $repo"
fi
prefix="$(mkdir -p -- "$prefix" && cd -- "$prefix" && pwd -P)"
readonly repo prefix
readonly state="$prefix/.csf-kit"

for tool in docker git; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool is required on PATH"
done

# Refuse collisions before building: an installed name must be absent or a
# symlink this kit made (same refusal shape as the root install.sh).
refuse_collision() {
  local link="$prefix/$1"
  if [[ -L "$link" ]]; then
    local target
    target="$(readlink "$link")"
    [[ "$target" == */.csf-kit/bin/"$1" ]] || die "Refusing to replace an unrelated symlink: $link -> $target"
  elif [[ -e "$link" ]]; then
    die "Refusing to replace an existing non-symlink: $link"
  fi
}
refuse_collision csf
refuse_collision rrsi-mine

install -d -m 0755 "$state" "$state/bin" "$state/go/mod" "$state/go/build" "$state/go/path" \
  "$state/cargo/home" "$state/cargo/target" "$state/src"
staging="$(mktemp -d "$state/install.XXXXXXXX")"
trap 'rm -rf -- "$staging"' EXIT
user="$(id -u):$(id -g)"
readonly staging user

printf '[RUN] Building csf in %s (module cache %s).\n' "$GO_IMAGE" "$state/go"
docker run --rm --user "$user" \
  -e HOME=/tmp/kit-home -e GOTOOLCHAIN=local -e CGO_ENABLED=0 -e GOFLAGS=-modcacherw \
  -e GOMODCACHE=/state/mod -e GOCACHE=/state/build -e GOPATH=/state/path \
  -v "$ROOT:/src:ro" -v "$state/go:/state" -v "$staging:/out" -w /src \
  "$GO_IMAGE" go build -trimpath -buildvcs=false -o /out/csf "$CSF_PACKAGE"
csf_usage="$("$staging/csf" 2>&1 || true)"
[[ "$csf_usage" == *"usage: csf init"* ]] || die "the built csf does not print its usage"

printf '[RUN] Fetching rrsi at %s.\n' "$RRSI_REVISION"
rrsi_src="$state/src/rrsi"
if [[ ! -d "$rrsi_src/.git" ]]; then
  git clone --quiet "$RRSI_URL" "$rrsi_src"
fi
if ! git -C "$rrsi_src" cat-file -e "$RRSI_REVISION^{commit}" 2>/dev/null; then
  git -C "$rrsi_src" fetch --quiet origin
fi
git -C "$rrsi_src" checkout --quiet --detach "$RRSI_REVISION"

printf '[RUN] Building rrsi-mine in %s (cargo home %s).\n' "$RUST_IMAGE" "$state/cargo"
docker run --rm --user "$user" \
  -e HOME=/tmp/kit-home -e CARGO_HOME=/state/home -e CARGO_TARGET_DIR=/state/target \
  -v "$rrsi_src:/src" -v "$state/cargo:/state" -w "/src/$RRSI_CRATE" \
  "$RUST_IMAGE" cargo build --release --locked --bin rrsi-mine
cp -- "$state/cargo/target/release/rrsi-mine" "$staging/rrsi-mine"
"$staging/rrsi-mine" miners >/dev/null || die "the built rrsi-mine does not list its miners"

# Both binaries are complete before either installed name changes.
install_binary() {
  mv -f -- "$staging/$1" "$state/bin/$1"
  ln -sfn "$state/bin/$1" "$prefix/$1"
  printf '[PASS] Installed %s: %s -> %s\n' "$1" "$prefix/$1" "$state/bin/$1"
}
install_binary csf
install_binary rrsi-mine

if [[ -n "$repo" ]]; then
  printf '[RUN] csf init in %s.\n' "$repo"
  (cd -- "$repo" && "$prefix/csf" init)
else
  printf '\nNext, in the root of any git repository of yours:\n\n  %s/csf init\n' "$prefix"
fi
if [[ ":$PATH:" != *":$prefix:"* ]]; then
  printf '\n[INFO] %s is not on PATH; add it, or call %s/csf by its full path.\n' "$prefix" "$prefix"
fi
