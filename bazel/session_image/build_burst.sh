#!/usr/bin/env bash
# Build the burst image: the session image bazel/session_image.txt pins, plus
# PostgreSQL for csf serve inside a cloud job. It is tagged csf-session-burst
# and, given a registry reference, pushed there for the cloud provider to pull.
#
# Usage: bazel/session_image/build_burst.sh [registry/name:tag]
set -Eeuo pipefail

die() {
  printf 'burst image: %s\n' "$*" >&2
  exit 1
}

command -v docker >/dev/null 2>&1 || die 'docker is required to build the burst image'
module_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)
session_image=$(<"$module_root/bazel/session_image.txt")
docker image inspect "$session_image" >/dev/null 2>&1 ||
  die "the pinned session image $session_image is not on this host; build it with bazel/session_image/build.sh"

# BuildKit resolves FROM by name, not by a bare image ID, so the pin is
# tagged locally first.
docker tag "$session_image" csf-session:pinned

docker build \
  --file "$module_root/bazel/session_image/burst.Dockerfile" \
  --build-arg SESSION_IMAGE=csf-session:pinned \
  --tag csf-session-burst:latest \
  "$module_root/bazel/session_image" >&2

if [[ $# -gt 0 ]]; then
  docker tag csf-session-burst:latest "$1"
  docker push "$1" >&2
fi
docker image inspect --format '{{.Id}}' csf-session-burst:latest
