exception Error of string

(** Python [str.splitlines] semantics, without retaining separators. Inputs to
    this module must already be validated UTF-8. Unicode classification follows
    the checked-in Unicode 15.0 compatibility table. *)
val physical_lines : string -> string list

(** Recognize the Python metric's explicit markers in leading comments only. *)
val is_generated : string -> bool

(** Count whole generated documents or diagram interiors. Invalid diagram
    regions raise [Error], including in documents with a generated header. *)
val documentation_generated_lines : name:string -> string -> int
