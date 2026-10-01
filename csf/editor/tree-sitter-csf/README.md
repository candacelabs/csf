# Internal CSF highlighting parser

Generated syntax and highlights for the CSF architecture language. The source
of truth is `csf/compiler/architecture/language.ebnf`. This directory is a build
input of the single CSF installation and the documentation image, not a
separate public distribution. See [CSF highlighting](../README.md).

The internal Python binding exposes `language()` and `HIGHLIGHTS_QUERY` to the
docs renderer. Its source-archive installation is tested as a packaging boundary;
users do not install it separately. The parser does not validate architecture
semantics or implement the `term`/`diagram` documentation DSL.
