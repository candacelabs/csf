(** Reusable architecture compilation. No console output or process exits. *)
type mode = Check | Emit | Check_generated
type config = {
  grammar_path : string;
  source_path : string;
  root : string;
  output_path : string;
  require_closed : bool;
}
type report = { architecture_name : string; mode : mode; obligations : int }

val mode_name : mode -> string
val compile : config -> (Model.resolved, Model.diagnostic list) result

(** Pure projections, as artifact basenames paired with their complete contents.
    Consumers can inspect the exact files [Emit] would write without accessing files. *)
val projections : Model.resolved -> (string * string) list

(** Only [Emit] writes files. Failed compilation never starts emission.
    Each artifact is replaced atomically; the group is not a transaction. *)
val run : mode -> config -> (report, Model.diagnostic list) result
