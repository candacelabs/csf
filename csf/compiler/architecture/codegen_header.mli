(** One header configuration for the architecture compiler's generated files.
    Edit [text] in the implementation and rebuild/regenerate to change it; there
    are no environment variables or command-line overrides. *)

type syntax = Ocaml | Mermaid | Markdown

(** Stable generator identity shared with generated-file ownership checks. *)
val generator_name : string

(** The comment body, including the configured development version.
    It is not a release claim or a source revision. Preserve [Code generated],
    [generator_name], the [_cgen] suffix explanation and [DO NOT EDIT.] markers.

    This trusted build-time literal must stay on one line. Wrappers do not
    escape it: avoid double quotes, OCaml comment delimiters and consecutive
    hyphens, which can invalidate OCaml or Hypertext Markup Language (HTML)
    comments. Source-document text
    never enters this value. *)
val text : string

(** Render the configured body in the selected comment syntax, with one trailing
    newline. Callers put this first and append their own content/blank lines.
    There is deliberately no arbitrary-text argument to this renderer. *)
val render : syntax -> string
