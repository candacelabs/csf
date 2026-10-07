(** The JEV-writability census of the CSF EBNF grammars.

    A use-site is one production. A production is [bounded] (JEV-writable) when
    its right-hand side never references a free-text builtin and no choice in it,
    at its top level or inside any group, offers more than [max_options]
    alternatives. The builtins that name text are [identifier], [integer],
    [string] and [text], but a builtin whose slot one pick can write does not
    count as free text: an [identifier] is a reference, a lookup into the
    declared nouns, or a span, a name copied from the operator's tokens, never
    composed; a [string] is a quote -- a verbatim span of the operator's
    tokens -- or a description rendered from the typed fields; and an [integer]
    is a version computed by csfc, or a quantity read from a declared range or
    the data. Only [text] stays free. The scan is textual and tolerant: a
    missing file contributes no use-site rather than raising. *)

val builtins : string list
val max_options : int

type use_site = {
  file : string;
  name : string;
  alternatives : int;
  free_text : string list;
}

(** Whether the site is writable by one JEV pick: no free text, no choice wider
    than sixteen alternatives. *)
val bounded : use_site -> bool

(** One use-site per production, in source order, for the given grammar text. *)
val sites_of_file : file:string -> string -> use_site list

(** The use-sites of every readable file; a missing file is skipped. *)
val census_files : string list -> use_site list

val site_id : use_site -> string

(** [(bounded, total)] over the sites. *)
val share : use_site list -> int * int
