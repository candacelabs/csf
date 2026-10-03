(* rules.dl must stay within what the engine evaluates soundly. The engine
   checks neither safety nor stratification (third_party/datalog/README.md),
   so this test does, and it holds the predicate blocks to their keys. *)
module Datalog = Datalog_top_down.Default

let clauses = match Datalog.parse_string Rules_cgen.source with
  | `Ok clauses -> clauses
  | `Error message -> failwith ("rules.dl does not parse: " ^ message)

let builtins = let db = Datalog.DB.create () in Datalog.setup_default db; db
let symbol term = Datalog_top_down.(match Datalog.T.head_symbol term with String name -> name | Int value -> string_of_int value)
let builtin term = Datalog.DB.is_interpreted builtins (Datalog.T.head_symbol term)
let check condition message = if not condition then failwith message

(* Reading the body left to right, as the engine does: a variable is bound once
   a positive literal or an aggregate has named it. *)
let unsafe (clause : Datalog.C.t) =
  let covered bound term = List.for_all (fun var -> List.mem var bound) (Datalog.T.vars term) in
  let bound, problems = List.fold_left (fun (bound, problems) literal -> match literal with
    | Datalog.Lit.LitPos term when builtin term ->
        bound, if covered bound term then problems else "builtin before its arguments are bound" :: problems
    | Datalog.Lit.LitPos term -> Datalog.T.vars term @ bound, problems
    | Datalog.Lit.LitNeg term ->
        bound, if covered bound term then problems else "negation before its variables are bound" :: problems
    | Datalog.Lit.LitAggr aggregate ->
        Datalog.T.vars aggregate.left @ Datalog.T.vars aggregate.guard @ bound,
        if List.length clause.body = 1 then problems else "an aggregate is not the whole body" :: problems)
    ([], []) clause.body in
  if covered bound clause.head then problems else "head variable not bound by the body" :: problems

(* Stratification in Datalog: no predicate depends negatively, or through an
   aggregate, on anything that depends on it. *)
let unstratified () =
  let edge relation head term = Datalog.T.mk_apply_l (Datalog_top_down.String relation)
    [Datalog.T.mk_const (Datalog_top_down.String (symbol head)); Datalog.T.mk_const (Datalog_top_down.String (symbol term))] in
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
  Datalog.ask db (Datalog.term_of_string "unstratified(H, P)") |> List.map Datalog.T.to_string

(* A block is the lines from one starting with "/*" to a line holding only
   its closing marker; its first line names the block kind and subject. *)
let blocks =
  let rec collect blocks current = function
    | [] -> List.rev blocks
    | line :: rest -> let line = String.trim line in match current with
      | None when String.starts_with ~prefix:"/*" line -> collect blocks (Some [line]) rest
      | None -> collect blocks None rest
      | Some lines when line = "*/" -> collect (List.rev lines :: blocks) None rest
      | Some lines -> collect blocks (Some (line :: lines)) rest in
  collect [] None (String.split_on_char '\n' Rules_cgen.source)
  |> List.filter_map (function
    | header :: lines -> (match String.split_on_char ' ' header with
      | ["/*"; kind; name] ->
          let key line = List.hd (String.split_on_char ' ' line) in
          Some (kind, name, List.sort_uniq String.compare (List.map key lines))
      | _ -> None)
    | [] -> None)

let named kind = List.filter_map (fun (k, name, keys) -> if k = kind then Some (name, keys) else None) blocks
let heads = List.map (fun (clause : Datalog.C.t) -> symbol clause.head) clauses

let tests = [
  "every clause is safe", (fun () ->
    List.iter (fun clause -> match unsafe clause with
      | [] -> ()
      | problem :: _ -> failwith (problem ^ ": " ^ Datalog.C.to_string clause)) clauses);
  "negation and counting are stratified", (fun () ->
    match unstratified () with
    | [] -> ()
    | cycles -> failwith ("negation through recursion: " ^ String.concat ", " cycles));
  "every finding has a formal predicate", (fun () ->
    let predicates = named "predicate" in
    List.iter (fun code -> check (List.mem_assoc code predicates) ("no predicate block for " ^ code)) Validate.finding_codes;
    List.iter (fun (name, keys) ->
      check (keys = ["clause"; "finding"; "relation"; "universe"]) ("predicate " ^ name ^ " needs universe, relation, clause and finding");
      check (List.mem name Validate.finding_codes || List.mem name heads) ("predicate " ^ name ^ " names no finding")) predicates);
  "every derived relation block defines a relation", (fun () ->
    List.iter (fun (name, keys) ->
      check (keys = ["definition"]) ("relation " ^ name ^ " needs exactly a definition");
      check (List.mem name heads) ("relation " ^ name ^ " is not derived by any rule")) (named "relation"));
]

let () =
  List.iter (fun (name, test) ->
    try test () with exception_ ->
      Printf.eprintf "FAIL %s: %s\n" name (Printexc.to_string exception_);
      exit 1) tests;
  Printf.printf "Architecture rules: %d tests passed\n" (List.length tests)
