(** Return a Markdown review of outstanding obligations, test references and
    declarative start/stop order. A reference does not mean a test passed; an
    ordering does not mean startup or shutdown was executed. *)
val render : Model.resolved -> string
