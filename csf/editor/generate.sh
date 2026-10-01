#!/usr/bin/env bash
# Regenerate/check the EBNF projection and the pinned upstream C parser.
set -Eeuo pipefail
editor_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
module_root=$(cd -- "$editor_root/../.." && pwd -P)
mode=${1:-check}
[[ "$mode" == write || "$mode" == check ]] || { echo 'usage: generate.sh [write|check]' >&2; exit 2; }
parser_root="$editor_root/tree-sitter-csf"
# Callers may reuse an already built generator and CLI; their versions are checked.
if [[ -z "${CSF_HIGHLIGHT_CODEGEN:-}" ]]; then
  bash "$module_root/tools/bazel.sh" --batch build //csf/compiler/architecture:highlight_codegen --lockfile_mode=error
  CSF_HIGHLIGHT_CODEGEN="$module_root/bazel-bin/csf/compiler/architecture/highlight_codegen.exe"
fi
"$CSF_HIGHLIGHT_CODEGEN" "$mode" "$module_root/csf/compiler/architecture/language.ebnf" "$parser_root"
tree_sitter=${TREE_SITTER:-tree-sitter}
[[ $("$tree_sitter" --version) == 'tree-sitter 0.25.10 '* ]] || {
  echo 'Tree-sitter CLI 0.25.10 is required (see README.md).' >&2; exit 1;
}
if [[ "$mode" == write ]]; then
  (cd -- "$parser_root" && "$tree_sitter" generate src/grammar.json --abi 14)
else
  scratch=$(mktemp -d)
  trap 'rm -rf -- "$scratch"' EXIT
  cp -- "$parser_root/tree-sitter.json" "$scratch/"
  mkdir -p "$scratch/src"
  cp -- "$parser_root/src/grammar.json" "$scratch/src/"
  (cd -- "$scratch" && "$tree_sitter" generate src/grammar.json --abi 14)
  for path in src/parser.c src/node-types.json src/tree_sitter/parser.h src/tree_sitter/alloc.h src/tree_sitter/array.h; do
    cmp -- "$scratch/$path" "$parser_root/$path"
  done
fi
