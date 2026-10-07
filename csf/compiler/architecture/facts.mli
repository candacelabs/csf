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

(** The census kind of a tracked path: its extension, or its whole basename when
    it has none, reduced to [a-z0-9_]. [go] is a kind here, never a token. *)
val kind_of_path : string -> string

(** Base relations of the tree census: [tracked], [kind_of], [directory],
    [allowed], [covers], [deeper], [directory_role], [child], [under_io] and
    [crosses] for the given tracked paths. Prefix and sibling relations are
    computed here because the rules cannot compare or split strings.
    [directory_role] marks a directory whose path names a [testdata] segment
    (fixtures, not a choice menu); [child] is the immediate-subdirectory
    relation a directory's options come from. [under_io] maps a declared
    directory under [io] to the tier its first segment names ([inproc] reads
    [in_process]); [crosses] is the declared directory's own optional [tier]. *)
val tree_facts : Model.architecture -> string list -> Datalog_top_down.Default.T.t list

(** Base relations of the JEV-writability census: [use_site] for every grammar
    production and [bounded_use_site] for each a single JEV pick can write. *)
val grammar_facts : Grammar.use_site list -> Datalog_top_down.Default.T.t list

(** How many distinct directories the tracked paths occupy, root included. *)
val tracked_directories : string list -> int

val of_architecture : Model.architecture -> Datalog_top_down.Default.T.t list
