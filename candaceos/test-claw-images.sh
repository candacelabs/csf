#!/usr/bin/env bash
# Prepare exact CI fixtures; the caller must still run test-claw-chat.sh.
set -Eeuo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
export_root=$(CDPATH= cd -- "$script_dir/.." && pwd -P)

die() {
  printf 'candaceos Claw images: %s\n' "$*" >&2
  exit 1
}

mode=${1:-}
case "$mode" in
  runtime) [[ $# == 2 ]] || die "usage: $0 runtime OUTPUT" ;;
  build) [[ $# == 3 || $# == 4 ]] || die "usage: $0 build core|opencode|warden OUTPUT [RUNTIME_CACHE]" ;;
  load) [[ $# == 3 ]] || die "usage: $0 load INPUT IMAGE_PREFIX" ;;
  *) die "expected runtime, build, or load" ;;
esac

revision=$(git -C "$export_root" rev-parse --verify HEAD)
[[ -z "$(git -C "$export_root" status --porcelain --untracked-files=normal -- .)" ]] || \
  die "image preparation and loading require a clean source tree"
docker info >/dev/null || die "the Docker daemon is not reachable"

image_name() {
  printf 'candaceos-claw-%s-%s:test\n' "$revision" "$1"
}

fingerprint() {
  # Reuse the fleet's portable Config/RootFS/platform identity. Docker image
  # IDs can change between classic and containerd stores after save/load.
  "$script_dir/fleet/node.sh" image-fingerprint .local/share/candaceos-claw-fixture "$1"
}

if [[ "$mode" == runtime ]]; then
  output=$2
  mkdir -p "$output"
  # The hosted Docker daemon has the containerd image store. Export only the
  # content-addressed runtime layers, without unpacking a redundant image.
  docker build --file "$script_dir/Dockerfile.core" --target runtime \
    --cache-to "type=local,dest=$output/cache,mode=min" \
    --output type=cacheonly "$export_root"
  [[ -f "$output/cache/index.json" ]] || die "runtime cache was not exported"
  printf '%s\n' "$revision" >"$output/source-revision"
  exit 0
fi

if [[ "$mode" == build ]]; then
  component=$2
  output=$3
  build_args=()
  case "$component" in
    core)
      [[ $# == 4 ]] || die "Core requires the prepared runtime cache"
      runtime_cache=$4
      [[ -f "$runtime_cache/source-revision" && -f "$runtime_cache/cache/index.json" ]] || \
        die "prepared runtime cache is missing"
      [[ "$(cat "$runtime_cache/source-revision")" == "$revision" ]] || \
        die "prepared runtime cache belongs to another source revision"
      build_args=(--cache-from "type=local,src=$runtime_cache/cache")
      dockerfile="$script_dir/Dockerfile.core"
      context="$export_root"
      ;;
    opencode) dockerfile="$script_dir/Dockerfile.opencode"; context="$script_dir" ;;
    warden) dockerfile="$export_root/app/warden/Dockerfile"; context="$export_root" ;;
    *) die "unknown fixture image: $component" ;;
  esac
  image=$(image_name "$component")
  mkdir -p "$output"
  docker build --file "$dockerfile" --tag "$image" \
    --label "org.opencontainers.image.revision=$revision" \
    "${build_args[@]}" "$context"
  docker save "$image" | gzip -1 -n >"$output/$component.tar.gz"
  checksum=$(sha256sum "$output/$component.tar.gz")
  identity=$(fingerprint "$image")
  printf '%s %s %s\n' "$revision" "${checksum%% *}" "$identity" >"$output/$component.receipt"
  exit 0
fi

input=$2
prefix=$3
[[ "$prefix" =~ ^[a-z0-9][a-z0-9._-]*$ ]] || die "invalid image prefix"
components=(core opencode warden)
# Reject a partial, stale, or damaged artifact set before loading any image.
for component in "${components[@]}"; do
  [[ -f "$input/$component.receipt" && -f "$input/$component.tar.gz" ]] || \
    die "missing prepared $component image"
  read -r source_revision checksum identity extra <"$input/$component.receipt"
  [[ "$source_revision" == "$revision" && "$checksum" =~ ^[a-f0-9]{64}$ && \
    "$identity" =~ ^sha256:[a-f0-9]{64}$ && -z "$extra" ]] || \
    die "invalid source or image receipt for $component"
  actual=$(sha256sum "$input/$component.tar.gz")
  [[ "${actual%% *}" == "$checksum" ]] || die "archive checksum mismatch for $component"
done
for component in "${components[@]}"; do
  read -r source_revision checksum identity <"$input/$component.receipt"
  docker load --input "$input/$component.tar.gz"
  image=$(image_name "$component")
  [[ "$(fingerprint "$image")" == "$identity" ]] || die "loaded $component image fingerprint mismatch"
  docker image tag "$image" "$prefix-$component:test"
done
