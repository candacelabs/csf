(** Reusable architecture compilation. No console output or process exits. *)
type mode = Check | Emit | Check_generated
type config = {
  grammar_path : string;
  source_path : string;
  root : string;
  output_path : string;
  require_closed : bool;
}
type report = {
  architecture_name : string;
  mode : mode;
  obligations : int;
  directories_declared : int;
  directories_tracked : int;
}

val mode_name : mode -> string
val compile : config -> (Model.resolved, Model.diagnostic list) result

(** Parse, decode and resolve one declaration with no tree census, so a source
    checked out from another revision resolves wherever it is. *)
val resolve_source : grammar_path:string -> source_path:string -> (Model.resolved, Model.diagnostic list) result

(** [resolve_source] over the grammar and source as they stood at the merge
    base of [revision] and HEAD in the repository at [root]. Paths are relative
    to [root]; nothing in the checkout is written. *)
val resolve_revision : root:string -> revision:string -> grammar_path:string -> source_path:string ->
  (Model.resolved, Model.diagnostic list) result

(** The resolved graph and its tree census, before obligation closure. The
    census reads the tracked inventory, so this is where the checkout is read. *)
val analyze : config -> (Model.resolved * Validate.tree_report, Model.diagnostic list) result

(** Declared directories over tracked directories, for the panel. *)
val census : root:string -> Model.architecture -> int * int

(** Pure projections, as artifact basenames paired with their complete contents.
    Consumers can inspect the exact files [Emit] would write without accessing files. *)
val projections : Model.resolved -> (string * string) list

(** Only [Emit] writes files. Failed compilation never starts emission.
    Each artifact is replaced atomically; the group is not a transaction. *)
val run : mode -> config -> (report, Model.diagnostic list) result

(** Check exactly as [run Emit] does, then return [Emit.Json.render] of the checked
    model instead of writing projections. Nothing is written. *)
val json : config -> (string, Model.diagnostic list) result
