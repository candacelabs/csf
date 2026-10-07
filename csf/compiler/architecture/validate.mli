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

(** Tree-census result for the panel: every offense a tracked file or the io
    shape draws, the declared-versus-tracked directory counts, and the
    directories whose fanout (immediate-subdirectory count) exceeds sixteen.
    [tree_diagnostics] adds the tree facts to a fresh database over
    [architecture], so it is independent of [resolve]; an empty [paths] yields
    no offense rather than a false one. Offense codes are the file offenses
    (tree_undeclared, tree_kind_not_allowed, tree_csf_outside_csf), the io
    shape (tree_io_children_stray, tree_io_children_missing) and a directory's
    declared tier disagreeing with the io segment it lives under
    (tree_tier_mismatch). *)
type tree_report = {
  offenses : Model.diagnostic list;
  directories_declared : int;
  directories_tracked : int;
  fanout : (string * int) list;
}

val tree_diagnostics : Model.architecture -> string list -> tree_report

(** JEV-writability result for the gate: every grammar use-site a single JEV
    pick cannot write, and the census the meter [bounded_use_sites / use_sites]
    reads. The same [offense] relation answers it, tagged with the [grammar]
    tree; [grammar_diagnostics] adds the census facts to a fresh database, so it
    is independent of both [resolve] and [tree_diagnostics]. An empty [sites]
    yields no offense rather than a false one. *)
type grammar_report = {
  offenses : Model.diagnostic list;
  use_sites : int;
  bounded_use_sites : int;
}

val grammar_diagnostics : Grammar.use_site list -> grammar_report
