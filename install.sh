#!/usr/bin/env bash
# Build and install the standalone CSF operator from this checkout.

set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly ROOT
readonly BINARY="$ROOT/tools/csf-operator/target/release/csf"

# Editor support ships with this installation, including on machines where
# Neovim is installed later. Never overwrite an unrelated Neovim package.
readonly EDITOR_RUNTIME="$ROOT/tools/csf-operator/target/editor/neovim"
readonly EDITOR_LINK="${XDG_DATA_HOME:-$HOME/.local/share}/nvim/site/pack/csf/start/csf"
if [[ -L "$EDITOR_LINK" ]]; then
  editor_target="$(readlink "$EDITOR_LINK")"
  if [[ "$editor_target" != */tools/csf-operator/target/editor/neovim ]]; then
    printf '[FAIL] Refusing to replace an unrelated Neovim package: %s\n' "$EDITOR_LINK" >&2
    exit 1
  fi
elif [[ -e "$EDITOR_LINK" ]]; then
  printf '[FAIL] Refusing to replace an existing Neovim package: %s\n' "$EDITOR_LINK" >&2
  exit 1
fi
if [[ -n "${CSF_INSTALL_PATH:-}" ]]; then
  INSTALL_PATH="$CSF_INSTALL_PATH"
else
  INSTALL_PATH="$HOME/.local/bin/csf"
fi
readonly INSTALL_PATH

# Refuse collisions before building or changing either installed pointer.
if [[ -L "$INSTALL_PATH" ]]; then
  installed_target="$(readlink "$INSTALL_PATH")"
  if [[ "$installed_target" != */tools/csf-operator/target/release/csf ]]; then
    printf '[FAIL] Refusing to replace an unrelated symlink: %s\n' "$INSTALL_PATH" >&2
    exit 1
  fi
elif [[ -e "$INSTALL_PATH" ]]; then
  printf '[FAIL] Refusing to replace an existing non-symlink: %s\n' "$INSTALL_PATH" >&2
  exit 1
fi

mkdir -p "$ROOT/tools/csf-operator/target"
staging=$(mktemp -d "$ROOT/tools/csf-operator/target/install.XXXXXXXX")
trap 'rm -rf -- "$staging"' EXIT

# The existing Docker-backed Bazel launcher provides all build tools.
printf '[RUN] Building the CSF CLI and editor support with Bazel.\n'
bash "$ROOT/tools/bazel.sh" build -c opt --ignore_dev_dependency --lockfile_mode=error \
  //tools/csf-operator:csf //csf/editor:neovim_runtime
cp -- "$ROOT/bazel-bin/tools/csf-operator/csf" "$staging/csf"
mkdir "$staging/neovim"
tar -xf "$ROOT/bazel-bin/csf/editor/neovim_runtime.tar" -C "$staging/neovim"
"$staging/csf" --help >/dev/null
# Both artifacts are complete before replacing any installed artifact.
install -d -m 0755 "$(dirname -- "$BINARY")" "$(dirname -- "$EDITOR_RUNTIME")"
if [[ -d "$EDITOR_RUNTIME" ]]; then
  mv -- "$EDITOR_RUNTIME" "$staging/previous-neovim"
fi
mv -- "$staging/neovim" "$EDITOR_RUNTIME"
mv -f -- "$staging/csf" "$BINARY"

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
  installed_target="$(readlink "$INSTALL_PATH")"
  if [[ "$installed_target" == "$BINARY" ]]; then
    printf '[PASS] csf already points at this checkout.\n'
  elif [[ "$installed_target" == */tools/csf-operator/target/release/csf ]]; then
    replace_link
    printf '[PASS] Updated csf to this checkout.\n'
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
  printf '[PASS] Installed csf: %s -> %s\n' "$INSTALL_PATH" "$BINARY"
fi

"$INSTALL_PATH" --help >/dev/null
install -d -m 0755 "$(dirname -- "$EDITOR_LINK")"
ln -sfn "$EDITOR_RUNTIME" "$EDITOR_LINK"
printf '[PASS] CSF syntax highlighting installed for Neovim.\n'
printf '[PASS] CSF is ready. Run: csf; start: csf up\n'
if [[ ":$PATH:" != *":$HOME/.local/bin:"* && "$INSTALL_PATH" == "$HOME/.local/bin/"* ]]; then
  printf '[INFO] Add %s to PATH or open a new login shell.\n' "$HOME/.local/bin"
fi
