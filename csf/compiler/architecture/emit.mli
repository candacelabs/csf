(** Pure, deterministic projections of validated declarations.
    Supply the successful result of [Validate.resolve]; manually fabricated,
    inconsistent resolved graphs are outside this contract. No function reads
    files, writes output, executes tests or observes a running application.
    Equal values, including declaration order and source locations, produce equal
    bytes; these functions do not canonicalize differently ordered declarations. *)

(** Return compilable OCaml defining [architecture : Model.architecture].
    Strings are escaped as OCaml literals and references retain their declared
    names. Consumers can resolve the resulting value again; it is not a running
    service composition or a serialized cache of the resolved graph. *)
val ocaml : Model.resolved -> string

(** Return Mermaid with process/scope nesting and escaped declaration labels.
    Diagram node IDs are presentation identifiers; edges do not establish active
    communication, goroutine ownership or hardware placement. *)
val mermaid : Model.resolved -> string

(** Return a Markdown review of outstanding obligations, test references and
    declarative start/stop order. A reference does not mean a test passed; an
    ordering does not mean startup or shutdown was executed. *)
val review : Model.resolved -> string
