module Datalog = Datalog_top_down.Default
module T = Datalog.T

type value = Text of string | Number of int
type span = { source : string; line : int }
type fact = { relation : string; args : value list; span : span }
type severity = S0 | S1 | S2 | S3
type verdict = { name : string; arity : int; severity : severity }
type finding = { miner : string; rule : string; subject : value list; severity : severity; proof : fact list }
type miner = { name : string; package : string; rules : string; verdicts : verdict list; extract : string -> fact list }
type label = { instance : string; positive : bool; at : int; flagged : int option; source : string }
type backtest = {
  labels : label list; split : int; knee : int option; families : int;
  tp : string list; fp : string list; fn : string list; lead : int option;
}

exception Invalid_rules of string

let constant = function Text text -> Datalog_top_down.String text | Number number -> Datalog_top_down.Int number
let value = function Datalog_top_down.String text -> Text text | Datalog_top_down.Int number -> Number number
let term relation args = T.mk_apply_l (Datalog_top_down.String relation) (List.map (fun arg -> T.mk_const (constant arg)) args)
let symbol term = match T.head_symbol term with Datalog_top_down.String name -> name | Datalog_top_down.Int number -> string_of_int number

let builtins = lazy (let db = Datalog.DB.create () in Datalog.setup_default db; db)
let builtin term = Datalog.DB.is_interpreted (Lazy.force builtins) (T.head_symbol term)

(* The engine checks neither safety nor stratification (csf/compiler/
   third_party/datalog/README.md); these are the checks the architecture
   rules_test holds rules.dl to. Reading a body left to right, a variable is
   bound once a positive literal or an aggregate has named it. *)
let unsafe (clause : Datalog.C.t) =
  let covered bound term = List.for_all (fun var -> List.mem var bound) (T.vars term) in
  let bound, problems = List.fold_left (fun (bound, problems) literal -> match literal with
    | Datalog.Lit.LitPos term when builtin term ->
        bound, if covered bound term then problems else "builtin before its arguments are bound" :: problems
    | Datalog.Lit.LitPos term -> T.vars term @ bound, problems
    | Datalog.Lit.LitNeg term ->
        bound, if covered bound term then problems else "negation before its variables are bound" :: problems
    | Datalog.Lit.LitAggr aggregate ->
        T.vars aggregate.left @ T.vars aggregate.guard @ bound,
        if List.length clause.body = 1 then problems else "an aggregate is not the whole body" :: problems)
    ([], []) clause.body in
  if covered bound clause.head then problems else "head variable not bound by the body" :: problems

let unstratified clauses =
  let edge relation head body = T.mk_apply_l (Datalog_top_down.String relation)
    [T.mk_const (Datalog_top_down.String (symbol head)); T.mk_const (Datalog_top_down.String (symbol body))] in
  let edges = List.concat_map (fun (clause : Datalog.C.t) -> List.concat_map (function
    | Datalog.Lit.LitPos term when builtin term -> []
    | Datalog.Lit.LitPos term -> [edge "depends" clause.head term]
    | Datalog.Lit.LitNeg term -> [edge "depends" clause.head term; edge "negatively" clause.head term]
    | Datalog.Lit.LitAggr aggregate ->
        [edge "depends" clause.head aggregate.guard; edge "negatively" clause.head aggregate.guard]) clause.body) clauses in
  let db = Datalog.DB.create () in
  Datalog.DB.add_facts db edges;
  List.iter (fun rule -> Datalog.DB.add_clause db (Datalog.clause_of_string rule)) [
    "reaches(X, Y) :- depends(X, Y).";
    "reaches(X, Y) :- reaches(X, Z), depends(Z, Y).";
    "unstratified(H, P) :- negatively(H, P), reaches(P, H).";
  ];
  Datalog.ask db (Datalog.term_of_string "unstratified(H, P)") |> List.map T.to_string

let parse rules = match Datalog.parse_string rules with
  | `Error message -> raise (Invalid_rules ("rules do not parse: " ^ message))
  | `Ok clauses ->
      List.iter (fun clause -> match unsafe clause with
        | [] -> ()
        | problem :: _ -> raise (Invalid_rules (problem ^ ": " ^ Datalog.C.to_string clause))) clauses;
      (match unstratified clauses with
       | [] -> ()
       | cycles -> raise (Invalid_rules ("negation through recursion: " ^ String.concat ", " cycles)));
      clauses

let check_rules rules = ignore (parse rules)

let rec ground binding term = match term with
  | T.Var var -> (match List.assoc_opt var binding with Some value -> value | None -> term)
  | T.Apply (head, args) -> T.mk_apply head (Array.map (ground binding) args)

let arguments term = match term with
  | T.Apply (_, args) -> Array.to_list args |> List.map (function
      | T.Apply (constant, [||]) -> value constant
      | other -> invalid_arg ("non-ground argument: " ^ T.to_string other))
  | T.Var _ -> invalid_arg "unbound answer"

(* The proof of one answer: the first clause for its relation whose body
   holds with the head bound to it, and that body's ground positive
   literals. Input facts keep their span; derived premises name the rules. *)
let proof db clauses read (miner : miner) answer =
  let premise term = match T.Tbl.find_opt read term with
    | Some fact -> fact
    | None -> { relation = symbol term; args = arguments term; span = { source = miner.package ^ "/rules.dl"; line = 0 } } in
  List.find_map (fun (clause : Datalog.C.t) ->
    match Datalog.unify clause.head 0 answer 1 with
    | exception Datalog.UnifFail -> None
    | subst ->
        let body = Datalog.Subst.eval_lits subst ~renaming:(Datalog.Subst.create_renaming ()) clause.body 0 in
        let positive = List.filter_map (function
          | Datalog.Lit.LitPos term when not (builtin term) -> Some term
          | _ -> None) body in
        let vars = List.sort_uniq compare (List.concat_map T.vars positive) in
        match Datalog.ask_lits db (List.map T.mk_var vars) body with
        | [] -> None
        | T.Apply (_, values) :: _ ->
            let binding = List.combine vars (Array.to_list values) in
            Some (List.map (fun term -> premise (ground binding term)) positive)
        | T.Var _ :: _ -> None) clauses
  |> Option.value ~default:[]

let findings ?knee (miner : miner) facts =
  let clauses = parse miner.rules in
  let db = Datalog.DB.create () in
  Datalog.setup_default db;
  Datalog.DB.add_clauses db clauses;
  let read = T.Tbl.create 64 in
  List.iter (fun (fact : fact) -> T.Tbl.replace read (term fact.relation fact.args) fact) facts;
  Datalog.DB.add_facts db (List.of_seq (T.Tbl.to_seq_keys read));
  Option.iter (fun knee -> Datalog.DB.add_fact db (term "knee" [Number knee])) knee;
  List.concat_map (fun (verdict : verdict) ->
    let query = T.mk_apply (Datalog_top_down.String verdict.name) (Array.init verdict.arity T.mk_var) in
    let derived = List.filter (fun (clause : Datalog.C.t) -> symbol clause.head = verdict.name) clauses in
    Datalog.ask db query
    |> List.map (fun answer -> { miner = miner.name; rule = verdict.name; subject = arguments answer;
         severity = verdict.severity; proof = proof db derived read miner answer })
    |> List.sort compare) miner.verdicts

let text = function Text text -> text | Number number -> string_of_int number
let instance (finding : finding) = match finding.subject with first :: _ -> text first | [] -> ""

let backtest miner facts labels =
  let labels = List.stable_sort (fun (a : label) b -> compare a.at b.at) labels in
  let fitted = (List.length labels + 1) / 2 in
  let fit = List.filteri (fun index _ -> index < fitted) labels
  and accept = List.filteri (fun index _ -> index >= fitted) labels in
  let fired knee = findings ?knee miner facts |> List.map instance |> List.sort_uniq compare in
  let outcome labels fired =
    let named keep = List.filter_map (fun (label : label) -> if keep label then Some label.instance else None) labels in
    named (fun label -> label.positive && List.mem label.instance fired),
    named (fun label -> not label.positive && List.mem label.instance fired),
    named (fun label -> label.positive && not (List.mem label.instance fired)) in
  let candidates = List.filter_map (fun (fact : fact) -> match fact.relation, fact.args with
    | "score", [_; Number score] -> Some score
    | _ -> None) facts |> List.sort_uniq compare in
  (* κ⋆ maximizes precision on the fitted labels among the knees with no
     false negative there; ties keep the smallest knee, which fires earliest. *)
  let precision (tp, fp, _) = match List.length tp + List.length fp with
    | 0 -> 0.
    | fired -> float_of_int (List.length tp) /. float_of_int fired in
  let knee, fired = match candidates with
    | [] -> None, fired None
    | _ ->
        let tried = List.map (fun knee -> let fired = fired (Some knee) in knee, fired, outcome fit fired) candidates in
        let admissible = match List.filter (fun (_, _, (_, _, fn)) -> fn = []) tried with [] -> tried | some -> some in
        let best = List.fold_left (fun best candidate ->
          let _, _, counts = candidate and _, _, best_counts = best in
          if precision counts > precision best_counts then candidate else best) (List.hd admissible) admissible in
        let knee, fired, _ = best in Some knee, fired in
  let tp, fp, fn = outcome accept fired in
  let lead = List.filter_map (fun (label : label) -> match label.flagged with
    | Some flagged when List.mem label.instance tp -> Some (flagged - (label.at + Option.value knee ~default:0))
    | _ -> None) accept |> List.sort compare |> function [] -> None | smallest :: _ -> Some smallest in
  { labels; split = (match List.rev fit with last :: _ -> last.at | [] -> 0);
    knee; families = max 1 (List.length candidates); tp; fp; fn; lead }

let value_json = function Text text -> `Assoc ["text", `String text] | Number number -> `Assoc ["number", `Int number]
let severity_json severity = `String (match severity with
  | S0 -> "SEVERITY_S0" | S1 -> "SEVERITY_S1" | S2 -> "SEVERITY_S2" | S3 -> "SEVERITY_S3")
let strings values = `List (List.map (fun value -> `String value) values)
let optional name = function Some number -> [name, `Int number] | None -> []

let fact_json (fact : fact) = `Assoc [
  "relation", `String fact.relation;
  "args", `List (List.map value_json fact.args);
  "span", `Assoc ["source", `String fact.span.source; "line", `Int fact.span.line];
]

let finding_json (finding : finding) = `Assoc [
  "miner", `String finding.miner;
  "rule", `String finding.rule;
  "subject", `List (List.map value_json finding.subject);
  "severity", severity_json finding.severity;
  "proof", `List (List.map fact_json finding.proof);
]

let label_json (label : label) = `Assoc ([
  "instance", `String label.instance; "positive", `Bool label.positive; "at", `Int label.at;
] @ optional "flagged" label.flagged @ ["source", `String label.source])

let backtest_json backtest = `Assoc ([
  "labels", `List (List.map label_json backtest.labels); "split", `Int backtest.split;
] @ optional "knee" backtest.knee @ [
  "families", `Int backtest.families; "tp", strings backtest.tp; "fp", strings backtest.fp; "fn", strings backtest.fn;
] @ optional "lead" backtest.lead)

let timestamp seconds =
  let time = Unix.gmtime (float_of_int seconds) in
  Printf.sprintf "%04d-%02d-%02dT%02d:%02d:%02dZ" (time.tm_year + 1900) (time.tm_mon + 1) time.tm_mday
    time.tm_hour time.tm_min time.tm_sec

let backtest_block ~command backtest =
  let count names = match names with
    | [] -> "0"
    | _ -> Printf.sprintf "%d: %s" (List.length names) (String.concat ", " names) in
  let positives = List.length (List.filter (fun (label : label) -> label.positive) backtest.labels) in
  let fitted = List.length (List.filter (fun (label : label) -> label.at <= backtest.split) backtest.labels) in
  let rows = [
    "Backtest $\\Lambda$", Printf.sprintf "%d labeled: %d +, %d −; labeled by %s" (List.length backtest.labels) positives
      (List.length backtest.labels - positives)
      (String.concat ", " (List.sort_uniq compare (List.map (fun (label : label) -> label.source) backtest.labels)));
    "Backtest split $t$", Printf.sprintf "%s: fit on %d, accept on %d" (timestamp backtest.split) fitted
      (List.length backtest.labels - fitted);
    "Backtest $\\kappa^\\star$", (match backtest.knee with
      | Some knee -> Printf.sprintf "%d (argmax precision on $\\Lambda_{\\le t}$ s.t. FN = 0; %d candidates from `score`)" knee backtest.families
      | None -> "none (no `score` facts)");
    "Backtest TP", count backtest.tp;
    "Backtest FP", count backtest.fp;
    "Backtest FN", count backtest.fn;
    "Backtest lead", (match backtest.lead with Some lead -> Printf.sprintf "%d s" lead | None -> "none flagged");
    "Backtest reproduce", "`" ^ command ^ "`";
  ] in
  String.concat "\n" ("| What we measured | Result |" :: "|---|---|"
    :: List.map (fun (what, result) -> Printf.sprintf "| %s | %s |" what result) rows) ^ "\n"

let labels path =
  In_channel.with_open_text path In_channel.input_lines
  |> List.filter (fun line -> String.trim line <> "" && not (String.starts_with ~prefix:"#" line))
  |> List.map (fun line -> match String.split_on_char '\t' line with
    | [instance; sign; at; flagged; source] when sign = "+" || sign = "-" ->
        { instance; positive = sign = "+"; at = Corpus.seconds at; source;
          flagged = if flagged = "-" then None else Some (Corpus.seconds flagged) }
    | _ -> failwith (path ^ ": a label is instance, +/-, start, flag time or -, source: " ^ line))

let shell argument =
  let plain = function 'A'..'Z' | 'a'..'z' | '0'..'9' | '_' | '.' | '/' | '-' -> true | _ -> false in
  if argument <> "" && String.for_all plain argument then argument else Filename.quote argument

let command (miner : miner) arguments =
  Printf.sprintf "tools/bazel.sh build //%s:miner && bazel-bin/%s/miner.exe %s"
    miner.package miner.package (String.concat " " (List.map shell arguments))

(* A leading ~/ and one /*/ segment, so a corpus is named by its pattern. *)
let expand item =
  let item = if String.starts_with ~prefix:"~/" item
    then Filename.concat (Sys.getenv "HOME") (String.sub item 2 (String.length item - 2)) else item in
  let marker = "/*/" in
  let rec find at = if at + 3 > String.length item then None
    else if String.sub item at 3 = marker then Some at else find (at + 1) in
  match find 0 with
  | None -> [item]
  | Some at ->
      let directory = String.sub item 0 at and rest = String.sub item (at + 3) (String.length item - at - 3) in
      Sys.readdir directory |> Array.to_list |> List.sort compare
      |> List.map (fun entry -> String.concat "/" [directory; entry; rest])
      |> List.filter Sys.file_exists

let main (miner : miner) =
  let facts items = List.concat_map expand items |> List.concat_map miner.extract in
  let lines encode values = List.iter (fun value -> print_endline (Yojson.Safe.to_string (encode value))) values in
  match List.tl (Array.to_list Sys.argv) with
  | "facts" :: items -> lines fact_json (facts items)
  | "findings" :: "--knee" :: knee :: items -> lines finding_json (findings ~knee:(int_of_string knee) miner (facts items))
  | "findings" :: items -> lines finding_json (findings miner (facts items))
  | "backtest" :: "--json" :: path :: items ->
      print_endline (Yojson.Safe.to_string (backtest_json (backtest miner (facts items) (labels path))))
  | "backtest" :: path :: items as arguments ->
      print_string (backtest_block ~command:(command miner arguments)
        (backtest miner (facts items) (labels path)))
  | _ ->
      prerr_endline "usage: miner.exe facts ITEM... | findings [--knee K] ITEM... | backtest [--json] LABELS ITEM...";
      exit 2
