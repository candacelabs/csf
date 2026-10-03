#!/usr/bin/env bash
set -euo pipefail

store_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# sqlc writes the generated package into ../internal/storedb, so the mount has
# to span the whole service directory rather than just the store package.
service_dir="$(cd "${store_dir}/.." && pwd)"
sqlc_image="sqlc/sqlc:1.31.1@sha256:70f53171d27b2424e9358869975455a6e955a5aa8e58a998a270a6e34e525537"

docker run --rm \
  --user "$(id -u):$(id -g)" \
  --volume "${service_dir}:/src" \
  --workdir /src/store \
  "${sqlc_image}" generate -f sqlc.yaml
