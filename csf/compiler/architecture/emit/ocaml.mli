(** Return compilable OCaml defining [architecture : Model.architecture].
    Strings are escaped as OCaml literals and references retain their declared
    names. Consumers can resolve the resulting value again; it is not a running
    service composition or a serialized cache of the resolved graph. *)
val render : Model.resolved -> string
