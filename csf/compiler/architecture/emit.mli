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

(** Return the declared architecture as one JSON document for tools that do
    not link OCaml: processes, scopes, components (role, process, scope,
    source, state, lifecycle, verification), dependencies, connections, scan
    and generated roots, directories (path and allowed kinds), and outstanding
    obligations, in declaration order,
    each with its declaration location. Paths stay relative to the checked
    root. [format] names this layout and [format_version] changes only when
    an existing field changes meaning. A test reference is not a passed test;
    the document describes declarations, not observed running state. *)
val json : Model.resolved -> string
