#!/usr/bin/env bash
# Build and install the standalone Candace operator from this checkout.

set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly ROOT
readonly MANIFEST="$ROOT/tools/csf-operator/Cargo.toml"
readonly BINARY="$ROOT/tools/csf-operator/target/release/candace"

if ! command -v cargo >/dev/null 2>&1; then
  printf '[FAIL] Rust 1.91 or newer is required to build the Candace operator.\n' >&2
  exit 1
fi

cargo build --release --locked --manifest-path "$MANIFEST"

if [[ -n "${CANDACE_INSTALL_PATH:-}" ]]; then
  INSTALL_PATH="$CANDACE_INSTALL_PATH"
else
  INSTALL_PATH="$HOME/.local/bin/candace"
fi
readonly INSTALL_PATH

install_parent="$(dirname -- "$INSTALL_PATH")"
readonly install_parent
if [[ ! -d "$install_parent" ]]; then
  install -d -m 0755 "$install_parent"
fi

link_binary() {
  ln -s "$BINARY" "$INSTALL_PATH"
}

replace_link() {
  ln -sfn "$BINARY" "$INSTALL_PATH"
}

if [[ -L "$INSTALL_PATH" ]]; then
  installed_target="$(readlink -f "$INSTALL_PATH")"
  if [[ "$installed_target" == "$BINARY" ]]; then
    printf '[PASS] candace already points at this checkout.\n'
  elif [[ "$installed_target" == */tools/csf-operator/target/release/candace ]]; then
    replace_link
    printf '[PASS] Updated candace to this checkout.\n'
  else
    printf '[FAIL] Refusing to replace an unrelated symlink: %s -> %s\n' \
      "$INSTALL_PATH" "$installed_target" >&2
    exit 1
  fi
elif [[ -e "$INSTALL_PATH" ]]; then
  printf '[FAIL] Refusing to replace an existing non-symlink: %s\n' \
    "$INSTALL_PATH" >&2
  exit 1
else
  link_binary
  printf '[PASS] Installed candace: %s -> %s\n' "$INSTALL_PATH" "$BINARY"
fi

"$INSTALL_PATH" --help >/dev/null
printf '[PASS] Candace is ready. Run: candace csf; start: candace csf up\n'
if [[ ":$PATH:" != *":$HOME/.local/bin:"* && "$INSTALL_PATH" == "$HOME/.local/bin/"* ]]; then
  printf '[INFO] Add %s to PATH or open a new login shell.\n' "$HOME/.local/bin"
fi
