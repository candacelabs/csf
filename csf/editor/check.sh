#!/usr/bin/env bash
# Test shipped parser generation, source distribution, Python and Neovim consumers.
# Native EBNF projection drift is separately checked by highlight_codegen_test.
set -Eeuo pipefail
editor_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
module_root=$(cd -- "$editor_root/../.." && pwd -P)
python=${CSF_HIGHLIGHT_PYTHON:-python3}
tree_sitter=${TREE_SITTER:-tree-sitter}
neovim=${NVIM:-nvim}
parser_root="$editor_root/tree-sitter-csf"
[[ $("$tree_sitter" --version) == 'tree-sitter 0.25.10 '* ]] || {
  echo 'Tree-sitter CLI 0.25.10 required' >&2; exit 1;
}
scratch=$(mktemp -d)
trap 'rm -rf -- "$scratch"' EXIT
mkdir -p "$scratch/parser/src"
cp -- "$parser_root/tree-sitter.json" "$scratch/parser/"
cp -- "$parser_root/src/grammar.json" "$scratch/parser/src/"
(cd -- "$scratch/parser" && "$tree_sitter" generate src/grammar.json --abi 14)
for path in src/parser.c src/node-types.json src/tree_sitter/parser.h src/tree_sitter/alloc.h src/tree_sitter/array.h; do
  cmp -- "$scratch/parser/$path" "$parser_root/$path"
done
# Build an sdist, then install from that archive, never an editable checkout.
"$python" -m build --sdist --no-isolation --outdir "$scratch/dist" "$parser_root"
"$python" -m pip install --no-index --no-deps --no-build-isolation --force-reinstall "$scratch"/dist/*.tar.gz
(cd -- "$scratch" && "$python" -m pytest "$editor_root/tests" -q)
cc -std=c11 -O2 -fPIC -shared -I "$parser_root/src" \
  "$parser_root/src/parser.c" "$parser_root/src/scanner.c" -o "$scratch/csf.so"
XDG_DATA_HOME="$scratch/data" XDG_CONFIG_HOME="$scratch/config" \
XDG_STATE_HOME="$scratch/state" XDG_CACHE_HOME="$scratch/cache" NVIM_LOG_FILE="$scratch/nvim.log" \
CSF_MODULE_ROOT="$module_root" CSF_PARSER="$scratch/csf.so" \
  "$neovim" --headless -u NONE -i NONE -n -c "luafile $editor_root/tests/neovim.lua"
