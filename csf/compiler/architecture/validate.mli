(** Resolve architecture references and reject inconsistent declared ownership.

    The result contains actual process, scope and component records. For example,
    an application-scoped provider may serve a request-scoped consumer; a provider
    in a sibling scope cannot. Missing references and containment/dependency cycles
    are errors, and diagnostics preserve source locations.

    Every check that relates records (a join, closure or count) is a Datalog
    finding relation in [rules.dl] with its formal predicate beside it; checks on
    a single record stay in OCaml. Diagnostics come in phases (declarations,
    ownership, relationships); the first phase with findings is the result, in
    source order.

    Startup ordering follows explicit [requires] relationships, with declaration
    order breaking ties; shutdown reverses that order. Neither list executes hooks.
    Only Service requires Scoped by role; a conceptual Manager may be Borrowed.

    [Ok resolved] validates declarations, not source files or execution behavior.
    It may contain outstanding obligations. Naming a test never proves it passed
    or establishes automatic cleanup, cancellation or joining. *)
val resolve : Model.architecture -> (Model.resolved, Model.diagnostic list) result

(** Diagnostic codes derived by [rules.dl]; each has a predicate block there. *)
val finding_codes : string list
