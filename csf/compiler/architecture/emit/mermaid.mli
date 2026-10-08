(** Return Mermaid with process/scope nesting and escaped declaration labels.
    Diagram node IDs are presentation identifiers; edges do not establish active
    communication, goroutine ownership or hardware placement. *)
val render : Model.resolved -> string
