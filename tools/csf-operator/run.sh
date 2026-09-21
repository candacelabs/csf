#!/usr/bin/env bash

set -Eeuo pipefail

OPERATOR_ROOT="$(cd -- "${BASH_SOURCE[0]%/*}" && pwd -P)"
readonly OPERATOR_ROOT

for binary in \
  "$OPERATOR_ROOT/target/release/candace" \
  "$OPERATOR_ROOT/target/debug/candace"
do
  if [[ -x "$binary" ]]; then
    exec "$binary" "$@"
  fi
done

if ! command -v cargo >/dev/null 2>&1; then
  printf '[FAIL] CSF operator is not built and Cargo is unavailable. Install Rust 1.91 or newer, then rerun `candace csf`.\n' >&2
  exit 127
fi

exec cargo run \
  --quiet \
  --locked \
  --manifest-path "$OPERATOR_ROOT/Cargo.toml" \
  -- \
  "$@"
