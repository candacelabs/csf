(** Declaration text as the projections show it: generated keyword spellings,
    entity-escaped Mermaid and Markdown text, and the labels every diagram
    draws for a process, component and connection. *)

(** The source keyword for a generated vocabulary value. *)
val spelling : ('a -> Syntax_cgen.Terminal.t) -> 'a -> string

(** A quoted Mermaid label; declaration text never reaches diagram syntax. *)
val diagram : string -> string

(** One Markdown table cell, entity-escaped. *)
val markdown_cell : string -> string

(** Local diagram identifiers such as [c0], paired with each value's key.
    Stable for the same declaration order, not across reordering. *)
val numbered : string -> 'a list -> ('a -> 'key) -> ('key * string) list

val process : Model.process -> string
val component : Model.component -> string
val connection : Model.connection -> string
