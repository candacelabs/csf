#!/usr/bin/env bash
# Exercise the real unified installer in an isolated user-data prefix.
set -Eeuo pipefail
editor_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
module_root=$(cd -- "$editor_root/../.." && pwd -P)
scratch=$(mktemp -d)
trap 'rm -rf -- "$scratch"' EXIT
export CSF_INSTALL_PATH="$scratch/bin/csf"
export XDG_DATA_HOME="$scratch/data"
export XDG_CONFIG_HOME="$scratch/config"
export XDG_STATE_HOME="$scratch/state"
export XDG_CACHE_HOME="$scratch/cache"
export NVIM_LOG_FILE="$scratch/nvim.log"
export CSF_MODULE_ROOT="$module_root"
export CANDACE_CSF_STATE_DIR="$scratch/runtime-state"
# Caller Cargo settings must not redirect the installed operator away from its
# source checkout or select a stale binary from a previous build.
export CARGO_TARGET_DIR="$scratch/unrelated-target"
bash "$module_root/install.sh"
"$CSF_INSTALL_PATH" --help >/dev/null
"$CSF_INSTALL_PATH" docs --help >/dev/null
"$CSF_INSTALL_PATH" up --dry >/dev/null
[[ ! -e "$CANDACE_CSF_STATE_DIR" ]]
"${NVIM:-nvim}" --headless -u NORC -i NONE -c "luafile $editor_root/tests/installed_neovim.lua"
# Failed Bazel build must preserve both installed artifacts and links.
old_binary=$(sha256sum "$(readlink "$CSF_INSTALL_PATH")")
package="$XDG_DATA_HOME/nvim/site/pack/csf/start/csf"
old_parser=$(sha256sum "$package/parser/csf.so")
mkdir "$scratch/failing-tools"
printf '#!/bin/sh\nexit 42\n' > "$scratch/failing-tools/docker"
chmod +x "$scratch/failing-tools/docker"
if PATH="$scratch/failing-tools:$PATH" bash "$module_root/install.sh" > "$scratch/build-failure.log" 2>&1; then
  echo 'installer accepted failed Bazel build' >&2; exit 1
fi
[[ $(sha256sum "$(readlink "$CSF_INSTALL_PATH")") == "$old_binary" ]]
[[ $(sha256sum "$package/parser/csf.so") == "$old_parser" ]]
[[ ! -e "$CARGO_TARGET_DIR" ]]
# A repeat of the same installation updates its own links without another setup.
bash "$module_root/install.sh"
package="$XDG_DATA_HOME/nvim/site/pack/csf/start/csf"
[[ -L "$package" ]] || { echo 'installer did not register the Neovim runtime' >&2; exit 1; }
rm -- "$package"
mkdir -p "$package"
printf 'preserve me\n' > "$package/unrelated"
if bash "$module_root/install.sh" > "$scratch/refusal.log" 2>&1; then
  echo 'installer overwrote an unrelated Neovim package' >&2; exit 1
fi
[[ $(cat "$package/unrelated") == 'preserve me' ]]
grep -q 'Refusing to replace an existing Neovim package' "$scratch/refusal.log"
echo 'Single CSF installation and unrelated-package preservation passed'
