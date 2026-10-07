open Contract
module AST = Datalog_top_down.AST

type kind = Drop_atom | Swap_comparison | Write_knee | Negate_atom | Drop_rule | Swap_head | Flip_label | Move_split
type mutant = { kind : kind; description : string; rules : string; labels : label list }
type outcome = Killed of string | Survived | Excluded of string
type trial = { mutant : mutant; outcome : outcome; changed : string list; labeled : string list }
type report = { miner : string; at : int; trials : trial list; killed : int; survived : int; excluded : int; seconds : float }

let kind_name = function
  | Drop_atom -> "drop atom" | Swap_comparison -> "swap comparison" | Write_knee -> "write knee"
  | Negate_atom -> "negate atom" | Drop_rule -> "drop rule" | Swap_head -> "swap head"
  | Flip_label -> "flip label" | Move_split -> "move split"

(* Measured 2026-10-04 by the template's miner_test over its three fixture
   runs: 16 mutants, 6 excluded (1 equivalent, 5 refused), 10 killed. *)
let template = 10, 10
let floor = float_of_int (fst template) /. float_of_int (snd template)

(* Rules are mutated as the parser's AST, which keeps the author's variable
   names, and printed back as source: a quoted constant keeps its quotes and
   an infix comparison its spelling, both of which the engine's own printer
   loses. *)
let lower_word word = word <> "" && (match word.[0] with 'a'..'z' -> true | _ -> false)
  && String.for_all (function 'a'..'z' | 'A'..'Z' | '0'..'9' | '_' -> true | _ -> false) word
let operator word = word <> "" && String.for_all (fun char -> String.contains "_+<>=*-%^@|:!/" char) word

let rec term_source = function
  | AST.Var name -> name
  | AST.Int number -> string_of_int number
  | AST.Apply (op, [left; right]) when operator op -> term_source left ^ " " ^ op ^ " " ^ term_source right
  | AST.Apply (name, []) -> if lower_word name then name else "\"" ^ name ^ "\""
  | AST.Apply (name, args) -> name ^ "(" ^ String.concat ", " (List.map term_source args) ^ ")"

let literal_source = function
  | AST.LitPos term -> term_source term
  | AST.LitNeg term -> "~" ^ term_source term
  | AST.LitAggr aggregate -> Printf.sprintf "%s := %s %s : %s" (term_source aggregate.ag_left)
      aggregate.ag_constructor aggregate.ag_var (term_source aggregate.ag_guard)

let clause_source (head, body) = match body with
  | [] -> term_source head ^ "."
  | _ -> term_source head ^ " :- " ^ String.concat ", " (List.map literal_source body) ^ "."

let program_source clauses = String.concat "\n" (List.map clause_source clauses)

let ast source =
  let lexbuf = Lexing.from_string source in
  try Datalog_top_down.Parser.parse_file Datalog_top_down.Lexer.token lexbuf with
  | Parsing.Parse_error -> raise (Invalid_rules (AST.error_to_string "rules do not parse" lexbuf))
  | Failure message -> raise (Invalid_rules message)

let replace index value list = List.mapi (fun at item -> if at = index then value else item) list
let remove index list = List.filteri (fun at _ -> at <> index) list

let rec substitute name value = function
  | AST.Var var when var = name -> AST.Int value
  | AST.Apply (f, args) -> AST.Apply (f, List.map (substitute name value) args)
  | term -> term

let map_literal f = function
  | AST.LitPos term -> AST.LitPos (f term)
  | AST.LitNeg term -> AST.LitNeg (f term)
  | AST.LitAggr aggregate -> AST.LitAggr { aggregate with ag_left = f aggregate.ag_left; ag_guard = f aggregate.ag_guard }

let comparisons = ["gt", "ge"; "ge", "gt"; "lt", "le"; "le", "lt"; ">", ">="; ">=", ">"; "<", "<="; "<=", "<"]
let head_name = function AST.Apply (name, _) -> name | _ -> ""

let rule_mutants (miner : miner) scores labels clauses =
  let mutant kind description mutated = { kind; description; rules = program_source mutated; labels } in
  let verdicts = List.map (fun (verdict : verdict) -> verdict.name) miner.verdicts in
  let per_clause = List.concat (List.mapi (fun index (head, body) ->
    let rule = index + 1 in
    let with_body body = replace index (head, body) clauses in
    let drops = List.mapi (fun at literal ->
      mutant Drop_atom (Printf.sprintf "rule %d: drop `%s`" rule (literal_source literal)) (with_body (remove at body))) body in
    let swaps = List.concat (List.mapi (fun at literal -> match literal with
      | AST.LitPos (AST.Apply (op, args)) when List.mem_assoc op comparisons ->
          let swapped = AST.Apply (List.assoc op comparisons, args) in
          [mutant Swap_comparison (Printf.sprintf "rule %d: `%s` for `%s`" rule (term_source swapped) (literal_source literal))
             (with_body (replace at (AST.LitPos swapped) body))]
      | _ -> []) body) in
    let knees = List.concat (List.mapi (fun at literal -> match literal with
      | AST.LitPos (AST.Apply ("knee", [AST.Var name])) -> List.map (fun score ->
          let written = List.map (map_literal (substitute name score)) (remove at body) in
          mutant Write_knee (Printf.sprintf "rule %d: write %d for `knee(%s)`" rule score name)
            (replace index (substitute name score head, written) clauses)) scores
      | _ -> []) body) in
    let negations = List.concat (List.mapi (fun at literal -> match literal with
      | AST.LitPos (AST.Apply (name, _) as term) when not (builtin_name name) ->
          [mutant Negate_atom (Printf.sprintf "rule %d: `~%s` for `%s`" rule (term_source term) (term_source term))
             (with_body (replace at (AST.LitNeg term) body))]
      | _ -> []) body) in
    let dropped = [mutant Drop_rule (Printf.sprintf "drop rule %d: `%s`" rule (term_source head)) (remove index clauses)] in
    drops @ swaps @ knees @ negations @ dropped) clauses) in
  (* Two verdict rules exchange their relation names; the arguments stay. *)
  let heads = List.concat (List.mapi (fun i (head_i, body_i) -> List.concat (List.mapi (fun j (head_j, body_j) ->
    if j <= i || not (List.mem (head_name head_i) verdicts && List.mem (head_name head_j) verdicts)
       || head_name head_i = head_name head_j then []
    else match head_i, head_j with
      | AST.Apply (name_i, args_i), AST.Apply (name_j, args_j) ->
          [mutant Swap_head (Printf.sprintf "rules %d and %d: `%s` for `%s` and back" (i + 1) (j + 1) name_j name_i)
             (replace i (AST.Apply (name_j, args_i), body_i) (replace j (AST.Apply (name_i, args_j), body_j) clauses))]
      | _ -> []) clauses)) clauses) in
  per_clause @ heads

let label_mutants (miner : miner) (labels : label list) =
  let flips = List.map (fun (label : label) ->
    { kind = Flip_label; description = Printf.sprintf "flip %s to %s" label.instance (if label.positive then "−" else "+");
      rules = miner.rules;
      labels = List.map (fun (other : label) ->
        if other.instance = label.instance then { other with positive = not other.positive } else other) labels }) labels in
  (* The split falls after the first (n + 1) / 2 labels in time order, as
     Contract.backtest fits it; the two labels either side exchange starts,
     so one crosses into the fitted half and the other into the judged. *)
  let sorted = List.stable_sort (fun (a : label) b -> compare a.at b.at) labels in
  let fitted = (List.length sorted + 1) / 2 in
  let split = match List.nth_opt sorted (fitted - 1), List.nth_opt sorted fitted with
    | Some last, Some first when last.at <> first.at ->
        [{ kind = Move_split; description = Printf.sprintf "move the split: %s and %s exchange starts" last.instance first.instance;
           rules = miner.rules;
           labels = List.map (fun (other : label) ->
             if other.instance = last.instance then { other with at = first.at }
             else if other.instance = first.instance then { other with at = last.at } else other) labels }]
    | _ -> [] in
  flips @ split

let mutants (miner : miner) facts labels =
  rule_mutants miner (candidates facts) labels (ast miner.rules) @ label_mutants miner labels

(* A verdict is a (rule, instance) row; the knees tried are none and every
   candidate, since findings takes any of them. *)
let fired (miner : miner) facts knee =
  findings ?knee miner facts
  |> List.map (fun (finding : finding) -> finding.rule, match finding.subject with
       | Text text :: _ -> text | Number number :: _ -> string_of_int number | [] -> "")
  |> List.sort_uniq compare

let verdict_changes original mutated facts =
  let differences a b = List.filter (fun row -> not (List.mem row b)) a @ List.filter (fun row -> not (List.mem row a)) b in
  None :: List.map Option.some (candidates facts)
  |> List.concat_map (fun knee -> differences (fired original facts knee) (fired mutated facts knee))
  |> List.map snd |> List.sort_uniq compare

let judge checks = match List.find_map (fun (name, check) -> match check () with () -> None | exception _ -> Some name) checks with
  | Some name -> Killed name
  | None -> Survived

let run ~checks (miner : miner) facts labels =
  let started = Unix.gettimeofday () in
  let named = List.map (fun (label : label) -> label.instance) labels in
  let trial mutant =
    let mutated = { miner with rules = mutant.rules } in
    match mutant.kind with
    | Flip_label | Move_split ->
        let changed = List.filter_map (fun (label : label) -> if List.mem label labels then None else Some label.instance) mutant.labels in
        { mutant; outcome = judge (checks mutated facts mutant.labels); changed = List.sort_uniq compare changed; labeled = [] }
    | _ ->
        match check_rules mutant.rules with
        | exception Invalid_rules reason -> { mutant; outcome = Excluded ("refused: " ^ reason); changed = []; labeled = [] }
        | () ->
            match verdict_changes miner mutated facts with
            | [] -> { mutant; outcome = Excluded "equivalent: the same verdicts under no knee and under every knee candidate"; changed = []; labeled = [] }
            | changed -> { mutant; outcome = judge (checks mutated facts mutant.labels); changed;
                           labeled = List.filter (fun instance -> List.mem instance named) changed }
            | exception exception_ -> { mutant; outcome = Killed ("evaluation: " ^ Printexc.to_string exception_); changed = []; labeled = [] } in
  let trials = List.map trial (mutants miner facts labels) in
  let count outcome = List.length (List.filter (fun trial -> outcome trial.outcome) trials) in
  { miner = miner.name; at = int_of_float started; trials;
    killed = count (function Killed _ -> true | _ -> false); survived = count (( = ) Survived);
    excluded = count (function Excluded _ -> true | _ -> false); seconds = Unix.gettimeofday () -. started }

let score report = match report.killed + report.survived with
  | 0 -> 0.
  | counted -> float_of_int report.killed /. float_of_int counted

let labels_tsv labels = String.concat "\n" (List.map (fun (label : label) ->
  String.concat "\t" [label.instance; (if label.positive then "+" else "-"); timestamp label.at;
    (match label.flagged with Some flagged -> timestamp flagged | None -> "-"); label.source]) labels)

let source mutant = match mutant.kind with
  | Flip_label | Move_split -> labels_tsv mutant.labels
  | _ -> mutant.rules

let survivors report = List.filter (fun trial -> trial.outcome = Survived) report.trials

let rejections report =
  let printed trial = Printf.sprintf "%s (%s)\n%s" trial.mutant.description (kind_name trial.mutant.kind) (source trial.mutant) in
  let below = if score report < floor then
    [Printf.sprintf "mutation score %d/%d = %.2f is below the floor %.2f (the template's %d/%d); surviving:\n%s"
       report.killed (report.killed + report.survived) (score report) floor (fst template) (snd template)
       (String.concat "\n" (List.map printed (survivors report)))] else [] in
  below @ List.filter_map (fun trial -> match trial.labeled with
    | [] -> None
    | labeled -> Some (Printf.sprintf "a surviving mutant changes the verdict of %s: %s" (String.concat ", " labeled) (printed trial)))
    (survivors report)

let outcome_text = function
  | Killed name -> "killed: " ^ name
  | Survived -> "survived"
  | Excluded reason -> "excluded, " ^ (match String.index_opt reason ':' with Some at -> String.sub reason 0 at | None -> reason)

let block ~command report =
  let counted = report.killed + report.survived in
  let excluded prefix = List.length (List.filter (fun trial -> match trial.outcome with
    | Excluded reason -> String.starts_with ~prefix reason | _ -> false) report.trials) in
  let rows = [
    "Mutation score", Printf.sprintf "%d/%d = %.2f; floor %.2f" report.killed counted (score report) floor;
    "Mutation floor", Printf.sprintf "the template miner's measured %d/%d, `Mutate.template`, checked by its own test" (fst template) (snd template);
    "Mutation excluded", Printf.sprintf "%d: %d equivalent, %d refused" report.excluded (excluded "equivalent") (excluded "refused");
    "Mutation surviving", (match survivors report with
      | [] -> "0"
      | some -> Printf.sprintf "%d: %s" (List.length some) (String.concat "; " (List.map (fun trial -> trial.mutant.description) some)));
    "Mutation reproduce", "`" ^ command ^ "`";
  ] in
  let trials = List.mapi (fun index trial -> Printf.sprintf "| %d | %s | %s | %s | %s |" (index + 1) (kind_name trial.mutant.kind)
    trial.mutant.description (outcome_text trial.outcome) (match trial.changed with [] -> "—" | changed -> String.concat ", " changed)) report.trials in
  String.concat "\n" (("| What we measured | Result |" :: "|---|---|"
    :: List.map (fun (what, result) -> Printf.sprintf "| %s | %s |" what result) rows)
    @ ("" :: "| Mutant | Kind | Change | Outcome | Changed instances |" :: "|---|---|---|---|---|" :: trials)) ^ "\n"

let strings values = `List (List.map (fun value -> `String value) values)

let mutant_json trial = `Assoc [
  "kind", `String (kind_name trial.mutant.kind);
  "description", `String trial.mutant.description;
  "source", `String (source trial.mutant);
  "changed", strings trial.changed;
]

let json report = `Assoc [
  "miner", `String report.miner; "at", `Int report.at;
  "killed", `Int report.killed; "survived", `Int report.survived; "excluded", `Int report.excluded;
  "score", `Float (score report); "floor", `Float floor; "accepted", `Bool (rejections report = []);
  "surviving", `List (List.map mutant_json (survivors report));
  "seconds", `Float report.seconds;
]

let usage = "usage: miner.exe facts ITEM... | findings [--knee K] ITEM... | backtest [--json] LABELS ITEM... | mutate [--json] LABELS ITEM... | scope"

let main (miner : miner) =
  match List.tl (Array.to_list Sys.argv) with
  | "mutate" :: rest ->
      let as_json, rest = match rest with "--json" :: rest -> true, rest | rest -> false, rest in
      (match rest with
       | path :: items ->
           let facts = Contract.facts miner items and labels = Contract.labels path in
           let backtest_md = Filename.concat miner.package "backtest.md" in
           let committed = if Sys.file_exists backtest_md then Some { reproduce = command miner ("backtest" :: path :: items);
             block = In_channel.with_open_text backtest_md In_channel.input_all } else None in
           let report = run ~checks:(Contract.checks ?committed) miner facts labels in
           if as_json then print_endline (Yojson.Safe.to_string (json report))
           else print_string (block ~command:(command miner ("mutate" :: path :: items)) report)
       | [] -> prerr_endline usage; exit 2)
  | ("facts" | "findings" | "backtest" | "scope") :: _ -> Contract.main miner
  | _ -> prerr_endline usage; exit 2
