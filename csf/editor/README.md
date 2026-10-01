# CSF syntax highlighting

CSF architecture files can be highlighted in Neovim and in the rendered
repository documentation. Both consumers use the same Tree-sitter parser and
highlight queries, generated from the compiler's executable
[`language.ebnf`](../compiler/architecture/language.ebnf).

```csf
// One declared host and one service in its root scope.
architecture example version 1 {
  process host kind go entrypoint "app/cmd";
  scope root under host;
  service worker in host scope root source "services/worker"
    state existing lifecycle scoped verification pending;
  scan "services";
}
```

This highlights syntax; it does not validate names, ownership, lifetimes, source
coverage, or pending verification. Run `csfc check` for architecture validation.
The separate documentation DSL (`term`, `diagram`, `document`, and `#` comments)
is not this language. Neovim detection leaves those files alone even though
both dialects use `.csf`.

## Included in the CSF installation

Run the normal `./install.sh` from the CSF repository root. It installs the
`csf` operator and builds/registers the Neovim parser and highlighting
runtime together. There is no separate plugin or Python package to install.
The installer requires a running Docker daemon; Bazel supplies the pinned build tools inside Docker. It preserves
existing editor configuration and refuses to replace an unrelated package.

Restart Neovim after installing CSF, then open an architecture `.csf` file.
The runtime is registered under
`${XDG_DATA_HOME:-~/.local/share}/nvim/site/pack/csf/start/csf`, linked to the
installed checkout, just like the `csf` launcher. Keep that checkout in
place; rerun the same installer after updating it. Neovim 0.9 or newer is
required; acceptance runs 0.11.4, with parser ABI 14. Linux is the tested
installation platform. No `nvim-treesitter` plugin or `init.lua` edit is needed.

Filetype detection looks for `architecture` after leading whitespace and `//`
comments. For a new empty buffer, use `:setfiletype csf`. Standard theme
captures color keywords, identifiers, numbers, strings, comments, arrows and
punctuation. Highlighting also tolerates unfinished edits.

## Rendered documentation

Fenced `csf` blocks in the repository's rendered documentation use the same
parser and query. No browser script or external highlighting service is
needed. The docs build packages its internal Python binding automatically;
that binding is not a separate public CSF installation or published package.
The rendered HTML escapes source text, including invalid or unfinished code.
The docs image must be rebuilt to incorporate a changed parser.

Tree-sitter checks JSON escape syntax in strings but does not enforce the
compiler's Unicode surrogate-pair validation or input-size limits. Its
successful parse is not compiler acceptance. Strings currently receive one
capture, without separate escape coloring.

## Ownership and regeneration

| Input | Derived artifacts |
|---|---|
| `compiler/architecture/language.ebnf` | Tree-sitter productions and literal-keyword highlight queries |
| `compiler/architecture/frontend_lexer.ml` | Identifier character sets |
| `compiler/architecture/highlight_codegen.ml` | Translation and fixed lexical/highlight policy |
| Tree-sitter CLI 0.25.10, ABI 14 | C parser, node types and required headers |

The external identifier scanner preserves `worker->provider` and globally
reserved keywords. The editor never replaces the compiler's parser.
Generated files are checked in so consumers need only a C compiler; edit the
inputs and regenerate rather than patching `src/` or the query.

From the CSF root, using the pinned Bazel launcher and Tree-sitter CLI 0.25.10:

```sh
bash csf/editor/generate.sh write
bash csf/editor/generate.sh check
bash tools/bazel.sh --batch test //csf/compiler/architecture:highlight_codegen_test \
  --lockfile_mode=error --test_output=errors
```

The native test checks EBNF translation and committed projection drift. The
consumer test separately regenerates the C parser, checks its bytes, builds and
installs the Python source archive, tests highlighting and incremental parsing,
and loads the parser/query in a real headless Neovim process:

```sh
python3 -m venv .cache/csf-highlighting/venv
.cache/csf-highlighting/venv/bin/python -m pip install -r csf/editor/requirements-test.txt
bash csf/editor/prepare-tools.sh .cache/csf-highlighting/tools
CSF_HIGHLIGHT_PYTHON="$PWD/.cache/csf-highlighting/venv/bin/python" \
TREE_SITTER="$PWD/.cache/csf-highlighting/tools/tree-sitter" \
NVIM="$PWD/.cache/csf-highlighting/tools/nvim-linux-x86_64/bin/nvim" \
  bash csf/editor/check.sh
```

`prepare-tools.sh` downloads checksum-pinned Linux x86-64 test tools into the
chosen directory. It does not install system packages or change editor
configuration. Other platforms can supply `TREE_SITTER` and `NVIM` directly.
