(** The base relations of [rules.dl], extracted from typed [Model] records.

    Processes, scopes and components share one declaration index space, in that
    order and in source order within each list; [declarations] returns it.
    Requires and connection edges are indexed by their position in their own
    lists. Enumerations are spelled with the grammar's terminals, so the rules
    read [process(P, go, I)] and [state(C, existing)].

    Facts describe declarations only. A [sourced] fact says a nonempty source
    path was declared, not that the path exists. *)

val nonempty : string -> bool
val present : string option -> bool

(** Identifier and location of every declaration, at its declaration index. *)
val declarations : Model.architecture -> (string * Model.location) array

val of_architecture : Model.architecture -> Datalog_top_down.Default.T.t list
