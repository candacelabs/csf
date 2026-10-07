(** Tests for the testers: a miner's test must fail when the miner is wrong
    (candace-server#317). Each mutant is one small change to the miner's
    rules or labels; the miner's own checks run on it, and a mutant that
    passes them has survived. The mutation score is killed over counted,
    and the gate holds every miner to the floor the template miner measured.

    Mutants change only rule text and labels. The extractor that ran under
    the test's sandbox runs no further, so the whole analysis is in-process
    and costs milliseconds per miner. *)

open Contract

type kind =
  | Drop_atom  (** one body literal removed *)
  | Swap_comparison  (** [gt] for [ge], [lt] for [le], and back *)
  | Write_knee  (** [knee(K)] removed and a fixture score written for [K] *)
  | Negate_atom  (** a positive atom negated *)
  | Drop_rule  (** a whole clause removed *)
  | Swap_head  (** two verdict relations exchange their rules *)
  | Flip_label  (** one label's sign flipped *)
  | Move_split  (** the labels either side of the split exchange starts *)

val kind_name : kind -> string

type mutant = { kind : kind; description : string; rules : string; labels : label list }

(** Killed names the check that failed; Excluded gives the reason a mutant
    is left out of the denominator: refused by [check_rules] (stillborn), or
    equivalent, deriving the same verdicts as the original under no knee and
    under every knee candidate. *)
type outcome = Killed of string | Survived | Excluded of string

(** One mutant's trial: [changed] are the instances whose verdict the mutant
    changes under some knee candidate, [labeled] those of them that are
    labeled. A label mutant changes no verdict; [changed] names the labels
    it changed. *)
type trial = { mutant : mutant; outcome : outcome; changed : string list; labeled : string list }

type report = {
  miner : string;
  at : int;  (** when the analysis ran, Unix seconds *)
  trials : trial list;
  killed : int;
  survived : int;
  excluded : int;
  seconds : float;  (** the analysis's wall time *)
}

(** The template miner's measured score, killed over counted, as its own
    test checks it (services/ouroboros/miners/_template/mutation.md). *)
val template : int * int

(** The floor every miner's score must reach: the template's score. Derived,
    not picked; the template's test fails when [template] drifts from its
    measurement. *)
val floor : float

(** Every mutant of the miner over this corpus and these labels. *)
val mutants : miner -> fact list -> label list -> mutant list

(** Runs [checks] (the miner's test, as [Contract.checks] builds it) on every
    mutant. *)
val run : checks:(miner -> fact list -> label list -> (string * (unit -> unit)) list) -> miner -> fact list -> label list -> report

(** Killed over killed plus survived; 0 when nothing counted. *)
val score : report -> float

(** Why the gate rejects the miner, each with the surviving mutant printed:
    a score below [floor], and every surviving mutant that changes the
    verdict of a labeled instance. Empty means accepted. *)
val rejections : report -> string list

(** A mutant's text: its rules, or its labels as the TSV the fixtures use. *)
val source : mutant -> string

(** The Markdown block mutation.md holds: the measurements and one row per
    mutant with its outcome. *)
val block : command:string -> report -> string

(** The series record, [Mutation] in records.proto. *)
val json : report -> Yojson.Safe.t

(** [Contract.main] plus [mutate [--json] LABELS ITEM...]: the block, or the
    series record as one JSON line to append to the ops view's
    mutation.jsonl. *)
val main : miner -> unit
