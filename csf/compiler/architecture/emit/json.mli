(** Return the declared architecture as one JSON document for tools that do
    not link OCaml: processes, scopes, components (role, process, scope,
    source, state, lifecycle, verification), dependencies, connections, scan
    and generated roots, directories (path and allowed kinds), and outstanding
    obligations, in declaration order,
    each with its declaration location. Paths stay relative to the checked
    root. [format] names this layout and [format_version] changes only when
    an existing field changes meaning. A test reference is not a passed test;
    the document describes declarations, not observed running state. *)
val render : Model.resolved -> string
