#!/usr/bin/env bash
# Build the session image reproducibly and record its digest.
#
# The base is the pinned build image named by bazel/execution_image.txt, every
# file timestamp is rewritten to SOURCE_DATE_EPOCH, and every download is
# checksum-pinned, so the same tree builds the same image ID. The ID is written
# to bazel/session_image.txt, beside execution_image.txt, as the reference the
# harness starts session containers from.
#
# The image is built by a BuildKit of its own, exported as an archive and then
# loaded: building straight into the daemon's image store recreates the layers
# and loses the rewritten timestamps, while a loaded archive keeps its layers
# and its config digest as the image ID.
#
# Usage: bazel/session_image/build.sh [extra docker buildx build arguments]
#   e.g. --no-cache, to show a cold build reproduces the recorded ID.
set -Eeuo pipefail

die() {
  printf 'session image: %s\n' "$*" >&2
  exit 1
}

command -v docker >/dev/null 2>&1 || die 'docker is required to build the session image'
module_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)
build_image=$(<"$module_root/bazel/execution_image.txt")
# The epoch is fixed rather than taken from git, so the ID depends on the
# tree's content only.
export SOURCE_DATE_EPOCH=0
archive=$(mktemp --suffix=.tar)
trap 'rm -f -- "$archive"' EXIT

# The archive export needs a BuildKit of its own; it runs pinned, as a container.
builder=csf-session-image
buildkit_image=moby/buildkit:v0.25.1@sha256:79cc6476ab1a3371c9afd8b44e7c55610057c43e18d9b39b68e2b0c2475cc1b6
docker buildx inspect "$builder" >/dev/null 2>&1 ||
  docker buildx create --name "$builder" --driver docker-container --driver-opt "image=$buildkit_image" >&2

docker buildx build \
  --builder "$builder" \
  --file "$module_root/bazel/session_image/Dockerfile" \
  --build-arg BUILD_IMAGE="$build_image" \
  --build-arg SOURCE_DATE_EPOCH \
  --output "type=docker,name=csf-session,dest=$archive,rewrite-timestamp=true" \
  "$@" \
  "$module_root" >&2
docker load --quiet --input "$archive" >&2
image_id=$(docker image inspect --format '{{.Id}}' csf-session:latest)
[[ "$image_id" == sha256:* ]] || die "unexpected image id: $image_id"
printf '%s\n' "$image_id" >"$module_root/bazel/session_image.txt.tmp"
mv -- "$module_root/bazel/session_image.txt.tmp" "$module_root/bazel/session_image.txt"
printf '%s\n' "$image_id"
