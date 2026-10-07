(* Acceptance for slice kinds_declared (#487): CSF declares its own kinds in
   CSF. meta.csf names the productions of the documentation and architecture
   grammars, plus the meta language's own kind and relation, and this suite
   holds those names to the grammars they mirror. A kind added, renamed or
   dropped in either grammar, or in meta.csf alone, fails here instead of
   drifting silently — the count grows with the grammars. *)

let expect message condition = if not condition then failwith message

let read path = In_channel.with_open_bin path In_channel.input_all

let describe diagnostics =
  List.map (fun (diagnostic : Model.diagnostic) ->
    Printf.sprintf "%s:%d:%d %s: %s" diagnostic.at.file diagnostic.at.line
      diagnostic.at.column diagnostic.code diagnostic.message) diagnostics
  |> String.concat "\n"

(* Text scan of an executable EBNF grammar: the ordered rule names on one
   production's right-hand side. Comments are dropped first, a statement is
   the text between ';' separators, and its left-hand side is the rule name.
   This reads the grammar the frontend actually compiles, so a vocabulary
   change cannot pass without appearing here. *)
let strip_comments text =
  let buffer = Buffer.create (String.length text) in
  let length = String.length text in
  let rec skip i =
    if i + 1 >= length then length
    else if text.[i] = '*' && text.[i + 1] = ')' then i + 2
    else skip (i + 1) in
  let rec go i =
    if i >= length then ()
    else if i + 1 < length && text.[i] = '(' && text.[i + 1] = '*' then go (skip (i + 2))
    else (Buffer.add_char buffer text.[i]; go (i + 1)) in
  go 0;
  Buffer.contents buffer

let alternatives production grammar =
  let body =
    strip_comments grammar |> String.split_on_char ';'
    |> List.find_map (fun statement ->
      match String.index_opt statement '=' with
      | Some equals when String.trim (String.sub statement 0 equals) = production ->
          Some (String.sub statement (equals + 1) (String.length statement - equals - 1))
      | _ -> None) in
  match body with
  | None -> failwith ("grammar production not found: " ^ production)
  | Some body ->
      String.map (function '{' | '}' | '|' | '\n' | '\t' | '\r' -> ' ' | c -> c) body
      |> String.split_on_char ' ' |> List.filter (fun name -> name <> "")

let rec descendants rule (node : Frontend.node) =
  (if node.rule = rule then [ node ] else []) @
  List.concat_map (descendants rule) node.children

let values rule root =
  descendants rule root |> List.filter_map (fun (node : Frontend.node) -> node.value)

(* The measured kinds, grouped by the home grammar whose production each names,
   in that grammar's declaration order. The documentation grammar grew from ten
   to twelve when the LANG batch added relation and policy; relation names a
   documentation kind here and the meta language's own edge kind below, so the
   two homes share the name and the differential holds each home separately. *)
let documentation_kinds =
  [ "term"; "literature"; "relation"; "retired"; "jargon"; "section";
    "linked"; "diagram"; "north_star"; "document"; "policy"; "failure_code" ]

let architecture_kinds =
  [ "process"; "scope"; "component"; "dependency"; "connection"; "scan"; "generated"; "directory" ]

let names kinds = List.map (fun (kind : Meta.kind) -> kind.Meta.name) kinds

let by_home home (meta : Meta.t) =
  List.filter (fun (kind : Meta.kind) -> kind.Meta.home = home) meta.Meta.kinds

let meta_of grammar_path source_path =
  match Meta.parse_files ~grammar_path ~source_path with
  | Ok meta -> meta
  | Error diagnostics -> failwith (source_path ^ " did not parse:\n" ^ describe diagnostics)

let architecture_of grammar_path source_path =
  match Frontend.parse_files ~grammar_path ~source_path with
  | Ok root -> root
  | Error diagnostics -> failwith (source_path ^ " did not parse:\n" ^ describe diagnostics)

(* Acceptance 1: meta.csf declares every kind the two grammars name, and closes
   over its own kind and relation. *)
let test_meta_csf_declares_the_kinds meta =
  expect "documentation home declares the twelve documentation kinds in order"
    (names (by_home Meta.Documentation meta) = documentation_kinds);
  expect "architecture home declares the eight architecture kinds in order"
    (names (by_home Meta.Architecture meta) = architecture_kinds);
  expect "the twenty declared kinds are all present"
    (List.length (by_home Meta.Documentation meta) + List.length (by_home Meta.Architecture meta) = 20);
  (* Closure: the meta language declares its own kind and relation, and #520
     adds the decision vocabulary a question projects into the directory tree,
     the decision tree and the CLI. The meta home is exactly these four. *)
  expect "the meta home declares kind, relation, question and option, and nothing else"
    (List.sort String.compare (names (by_home Meta.Meta meta)) = [ "kind"; "option"; "question"; "relation" ]);
  let declared = names meta.Meta.kinds in
  List.iter (fun (relation : Meta.relation) ->
    expect (relation.Meta.from_ ^ " -> " ^ relation.Meta.to_ ^ " names declared kinds")
      (List.mem relation.Meta.from_ declared && List.mem relation.Meta.to_ declared))
    meta.Meta.relations

(* Acceptance 2: the meta-driven vocabulary is identical to the vocabulary the
   two grammars hardcode, for every declared kind, and the checked-in instances
   the old frontend accepts parse to the same place. *)
let test_differential_outputs_identical_for_all_declared meta doc_grammar arch_grammar_path arch_grammar source_paths =
  let doc_alternatives = alternatives "source" doc_grammar in
  let arch_alternatives = alternatives "declaration" arch_grammar in
  expect "documentation kinds are identical to the documentation grammar's source alternatives"
    (names (by_home Meta.Documentation meta) = doc_alternatives);
  expect "architecture kinds are identical to the architecture grammar's declaration alternatives"
    (names (by_home Meta.Architecture meta) = arch_alternatives);
  (* Uniqueness is per home: a name may name a kind in two homes (relation is a
     documentation kind and the meta language's own edge kind), but never twice
     in one. *)
  let declared_once home kind =
    let in_home = names (by_home home meta) in
    List.length (List.filter (fun name -> name = kind) in_home) = 1 in
  List.iter (fun kind ->
    expect (kind ^ " is declared exactly once in the documentation home")
      (declared_once Meta.Documentation kind)) doc_alternatives;
  List.iter (fun kind ->
    expect (kind ^ " is declared exactly once in the architecture home")
      (declared_once Meta.Architecture kind)) arch_alternatives;
  List.iter (fun path ->
    let root = architecture_of arch_grammar_path path in
    expect (path ^ " parses as an architecture") (root.Frontend.rule = "architecture")) source_paths

(* Acceptance 3: the meta language parses its own kind and relation
   declarations, and an instance of the declared kinds parses too. *)
let test_kind_and_instance_parsed meta_grammar arch_grammar_path architecture_path =
  let parse source = Meta.parse_text ~grammar:meta_grammar ~source ~filename:"meta-fixture.csf" in
  (match parse {|kind "widget" in documentation;|} with
   | Ok meta ->
       expect "a bare kind declaration parses" (names meta.Meta.kinds = [ "widget" ]);
       expect "its home is decoded" ((List.hd meta.Meta.kinds).Meta.home = Meta.Documentation)
   | Error diagnostics -> failwith ("bare kind rejected:\n" ^ describe diagnostics));
  (match parse {|relation "document" -> "diagram";|} with
   | Ok meta ->
       expect "a bare relation declaration parses"
         (List.map (fun (relation : Meta.relation) -> relation.Meta.from_ ^ " -> " ^ relation.Meta.to_)
            meta.Meta.relations = [ "document -> diagram" ])
   | Error diagnostics -> failwith ("bare relation rejected:\n" ^ describe diagnostics));
  (* #520: the decision vocabulary parses as meta declarations too, so the
     record projects from the same source the CSF language, the decision tree,
     the directory tree and the CLI share. *)
  (match parse {|kind "question" in meta; kind "option" in meta; relation "question" -> "option"; relation "option" -> "question";|}
   with
   | Ok meta ->
       expect "the #520 record parses as meta declarations"
         (List.for_all (fun name -> List.mem name (names meta.Meta.kinds)) [ "question"; "option" ])
   | Error diagnostics -> failwith ("#520 record rejected:\n" ^ describe diagnostics));
  (* The grammar enumerates the three homes, so an unknown home is rejected as
     syntax before the decoder sees it. *)
  (match parse {|kind "widget" in nonsense;|} with
   | Ok _ -> failwith "an unknown kind home was accepted"
   | Error diagnostics ->
       expect "an unknown home is rejected as syntax"
         (List.exists (fun (diagnostic : Model.diagnostic) -> diagnostic.code = "CSF_SYNTAX") diagnostics));
  let instance = architecture_of arch_grammar_path architecture_path in
  expect "the kind and its instance both parse" (instance.Frontend.rule = "architecture")

(* Acceptance 4: the rest client golden parses, and every kind it uses is one
   meta.csf declares. *)
let test_rest_client_golden_parses arch_grammar_path rest_client_path =
  let root = architecture_of arch_grammar_path rest_client_path in
  expect "the rest client golden parses as an architecture" (root.Frontend.rule = "architecture");
  expect "the rest client names its architecture" (List.mem "rest_client" (values "identifier" root));
  expect "the rest client declares version 1" (values "integer" root = [ "1" ]);
  let used =
    descendants "declaration" root
    |> List.concat_map (fun (declaration : Frontend.node) ->
      List.map (fun (child : Frontend.node) -> child.rule) declaration.children)
    |> List.sort_uniq String.compare in
  expect "the rest client uses every architecture kind"
    (used = List.sort String.compare architecture_kinds);
  expect "every kind the rest client uses is declared in meta.csf"
    (List.for_all (fun kind -> List.mem kind architecture_kinds) used)

let () =
  let argument index name =
    if Array.length Sys.argv <= index then failwith ("missing test argument: " ^ name);
    Sys.argv.(index) in
  let meta_grammar_path = argument 1 "meta.ebnf" in
  let meta_source_path = argument 2 "meta.csf" in
  let doc_grammar_path = argument 3 "grammar.ebnf" in
  let arch_grammar_path = argument 4 "language.ebnf" in
  let architecture_path = argument 5 "architecture.csf" in
  let rest_client_path = argument 6 "rest_client.csf" in
  let meta = meta_of meta_grammar_path meta_source_path in
  test_meta_csf_declares_the_kinds meta;
  test_differential_outputs_identical_for_all_declared meta (read doc_grammar_path)
    arch_grammar_path (read arch_grammar_path) [ architecture_path; rest_client_path ];
  test_kind_and_instance_parsed (read meta_grammar_path) arch_grammar_path architecture_path;
  test_rest_client_golden_parses arch_grammar_path rest_client_path;
  print_endline "CSF meta language tests passed"
