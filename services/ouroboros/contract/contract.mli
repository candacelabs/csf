(** The contract every ouroboros miner fills in.

    A miner is an extractor (one corpus item to typed facts) and a Datalog
    program over those facts. Every row a verdict relation derives is a
    finding, carried with the rule's ground premises as its proof trace.
    [records.proto] beside this file is the wire schema of each record here;
    the JSON encoders below emit its proto3 JSON form. *)

type value = Text of string | Number of int

(** Where a fact was read: a file path, an event log or a ticket reference,
    and its 1-based line (0 when the source has no lines). *)
type span = { source : string; line : int }

type fact = { relation : string; args : value list; span : span }

(** Offense severity (source monorepo issue #373): S0 is a gate escape, S1 cost real
    time or state, S2 was escalated, S3 was found only by mining. *)
type severity = S0 | S1 | S2 | S3

(** A relation the rules derive whose every row is a finding. *)
type verdict = { name : string; arity : int; severity : severity }

type finding = {
  miner : string;
  rule : string;
  subject : value list;
  severity : severity;
  proof : fact list;  (** the ground premises of the rule that derived it *)
}

type miner = {
  name : string;
  package : string;  (** the Bazel package, for the reproduce command *)
  rules : string;  (** Datalog source *)
  verdicts : verdict list;
  extract : string -> fact list;  (** one corpus item: a path or a reference *)
}

(** One labeled instance of Λ. [at] is the instance's start and [flagged]
    when the operator flagged it, both Unix seconds. *)
type label = { instance : string; positive : bool; at : int; flagged : int option; source : string }

(** eval(G, Λ), walk-forward (source monorepo issue #391): κ⋆ is fitted on the
    labels at or before [split] and TP, FP, FN and lead are counted on the
    labels after it. *)
type backtest = {
  labels : label list;
  split : int;
  knee : int option;
  families : int;  (** κ candidates tried, so multiple testing is visible *)
  tp : string list;
  fp : string list;
  fn : string list;
  lead : int option;
      (** the smallest margin, in seconds, by which the gate (firing κ⋆
          after the instance's start) precedes the operator's flag, over the
          flagged TPs *)
}

exception Invalid_rules of string

(** Parses the rules and refuses what the engine evaluates unsoundly: an
    unsafe clause or negation through recursion. *)
val check_rules : string -> unit

(** Evaluates the miner's rules over [facts], plus [knee(K)] when given. *)
val findings : ?knee:int -> miner -> fact list -> finding list

(** The knee candidates are the distinct values [N] of [score(I, N)] facts;
    the instance of a finding is its first subject argument. *)
val backtest : miner -> fact list -> label list -> backtest

val fact_json : fact -> Yojson.Safe.t
val finding_json : finding -> Yojson.Safe.t
val backtest_json : backtest -> Yojson.Safe.t

(** The backtest block a pull request body carries, as Markdown. *)
val backtest_block : command:string -> backtest -> string

(** Λ from a tab-separated file, one label per line: instance, [+] or [-],
    start, flag time or [-], source. Times are RFC 3339 UTC; lines starting
    with [#] are comments. *)
val labels : string -> label list

(** The command that reproduces a backtest of [miner] over these arguments. *)
val command : miner -> string list -> string

(** Every miner's command line:
    [facts ITEM...], [findings [--knee K] ITEM...] (JSON lines) and
    [backtest [--json] LABELS ITEM...]. An ITEM may start with [~/] and hold
    one [/*/] directory segment, expanded in sorted order. *)
val main : miner -> unit
