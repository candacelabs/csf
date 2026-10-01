(** Interpret an Extended Backus-Naur Form (EBNF) language definition to parse
    a separate source document.
    The EBNF file is executable syntax, not a second description of a parser. *)

(** Named productions have [value = None] and children in source order. Token
    leaves have [Some text] and no children: [identifier], [integer], [string]
    or the synthetic rule [$terminal]. String payloads have decoded escapes.
    Every location points into the original source, with one-based byte columns.

    Rule names stay generic here; [Typed_tree] checks them against the generated
    architecture vocabulary before [Decode] constructs the semantic model. *)
type node = {
  rule : string;
  value : string option;
  children : node list;
  at : Model.location;
}

(** [grammar] contains EBNF; [source] contains the document it describes.
    [filename] labels source diagnostics and is not opened. Grammar diagnostics
    use [<grammar>]. The first production is the start rule. [Ok] requires full
    source consumption and selects the first complete candidate if ambiguous;
    it establishes syntax only, not architecture validity.

    The accepted subset rejects left recursion, nullable repetition bodies and
    terminals incompatible with the fixed lexer. Inputs are capped at 64 KiB
    (kibibytes, 1024 bytes each) for the grammar and 2 MiB (mebibytes,
    1048576 bytes each) and 100000 tokens for the source, with additional rule,
    nesting and parsing-step limits reported as diagnostics. *)
val parse_text : grammar:string -> source:string -> filename:string ->
  (node, Model.diagnostic list) result

(** The same parser with bounded file reads and actual paths in diagnostics.
    File access failures return [CSF_IO] diagnostics; this library does not print
    messages or terminate the calling process. *)
val parse_files : grammar_path:string -> source_path:string ->
  (node, Model.diagnostic list) result
