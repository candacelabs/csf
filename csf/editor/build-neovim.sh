#!/usr/bin/env bash
# Internal build step of the single CSF installer; not a second installation.
set -Eeuo pipefail
[[ $# == 1 ]] || { echo 'usage: build-neovim.sh OUTPUT_DIRECTORY' >&2; exit 2; }
editor_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
parser_root="$editor_root/tree-sitter-csf"
output=$1
command -v cc >/dev/null || { echo 'A C compiler (cc) is required by the CSF installation.' >&2; exit 1; }
mkdir -p -- "$output/parser" "$output/queries/csf" "$output/ftdetect" "$output/ftplugin"
scratch=$(mktemp -d "$output/.build.XXXXXX")
trap 'rm -rf -- "$scratch"' EXIT
cc -std=c11 -O2 -fPIC -shared -I "$parser_root/src" \
  "$parser_root/src/parser.c" "$parser_root/src/scanner.c" -o "$scratch/csf.so"
install -m 0644 "$parser_root/queries/highlights.scm" "$output/queries/csf/highlights.scm"
install -m 0644 "$parser_root/neovim/ftdetect/csf.lua" "$output/ftdetect/csf.lua"
install -m 0644 "$parser_root/neovim/ftplugin/csf.lua" "$output/ftplugin/csf.lua"
mv -f -- "$scratch/csf.so" "$output/parser/csf.so"
