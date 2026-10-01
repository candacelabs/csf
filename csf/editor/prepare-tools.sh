#!/usr/bin/env bash
# Optional Linux x86-64 CI bootstrap; installs only into the supplied directory.
set -Eeuo pipefail
[[ $# == 1 ]] || { echo 'usage: prepare-tools.sh DIRECTORY' >&2; exit 2; }
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || {
  echo 'Use Tree-sitter 0.25.10 and Neovim 0.11.4 for your platform.' >&2; exit 1;
}
mkdir -p -- "$1"
tools_root=$(cd -- "$1" && pwd -P)
curl --fail --location --silent --show-error --retry 3 \
  https://github.com/tree-sitter/tree-sitter/releases/download/v0.25.10/tree-sitter-linux-x64.gz \
  -o "$tools_root/tree-sitter.gz"
printf '%s  %s\n' 8283ddba69253c698f6e987ba0e2f9285e079c8db4d36ebe1394b5bb3a0ebdfd "$tools_root/tree-sitter.gz" | sha256sum --check -
gzip -dc "$tools_root/tree-sitter.gz" > "$tools_root/tree-sitter"
chmod +x "$tools_root/tree-sitter"
curl --fail --location --silent --show-error --retry 3 \
  https://github.com/neovim/neovim/releases/download/v0.11.4/nvim-linux-x86_64.tar.gz \
  -o "$tools_root/neovim.tar.gz"
printf '%s  %s\n' a74740047e73b2b380d63a474282814063d10650cd6cc95efa16d1713c7e616c "$tools_root/neovim.tar.gz" | sha256sum --check -
tar -xzf "$tools_root/neovim.tar.gz" -C "$tools_root"
