#!/usr/bin/env bash
set -Eeuo pipefail

native_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)

exec cargo run --quiet --locked --release \
  --manifest-path "$native_root/receipt-rs/Cargo.toml" -- "$@"
