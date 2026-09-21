let expect message condition = if not condition then failwith message

let contains text fragment =
  let rec search index =
    index + String.length fragment <= String.length text &&
    (String.sub text index (String.length fragment) = fragment || search (index + 1)) in
  search 0

let generated grammar =
  match Symbol_codegen.generate ~grammar with
  | Ok source -> source
  | Error diagnostics ->
      failwith (String.concat "; " (List.map (fun (diagnostic : Model.diagnostic) -> diagnostic.message) diagnostics))

let rejected grammar code =
  match Symbol_codegen.generate ~grammar with
  | Ok _ -> failwith "invalid generated symbol names accepted"
  | Error diagnostics -> expect "wrong generation diagnostic"
      (List.exists (fun (diagnostic : Model.diagnostic) -> diagnostic.code = code) diagnostics)

let test_actual_grammar grammar =
  let source = generated grammar in
  expect "configured ownership header is first" (String.starts_with
    ~prefix:(Codegen_header.render Codegen_header.Ocaml) source);
  List.iter (fun fragment -> expect ("missing generated API: " ^ fragment) (contains source fragment)) [
    "module Rule = struct";
    "module Terminal = struct";
    "let of_string : string -> t option";
    "let name : t -> string";
    "let constructor_name : t -> string";
    "\"$terminal\" -> Some Terminal";
    "\"identifier\" -> Some Identifier";
    "\"integer\" -> Some Integer";
    "\"string\" -> Some String";
    "\"architecture\" -> Some Architecture";
    "\"->\" -> Some Arrow";
    "\";\" -> Some Semicolon";
    "\"{\" -> Some LeftBrace";
    "\"}\" -> Some RightBrace";
    "let state : Terminal.t -> Model.state option";
    "Terminal.Existing -> Some Model.Existing";
    "Terminal.Planned -> Some Model.Planned";
    "state_terminal : Model.state -> Terminal.t";
    "Model.Existing -> Terminal.Existing";
    "Model.Planned -> Terminal.Planned";
    "let process_kind : Terminal.t -> Model.process_kind option";
    "Terminal.Go -> Some Model.Go";
    "let lifecycle : Terminal.t -> Model.lifecycle option";
    "let role : Terminal.t -> Model.role option";
    "let transport : Terminal.t -> Model.transport option";
  ];
  expect "mixed verification rule is not a plain enum" (not (contains source "let verification :"));
  expect "generation is deterministic" (source = generated grammar)

let test_grammar_drives_symbols () =
  let before = generated "start = state, integer; state = \"existing\" | \"planned\";" in
  let after = generated "start = state, integer; state = \"existing\" | \"future\";" in
  expect "enum edits change generated symbols" (before <> after);
  expect "new enum terminal reaches typed Model reference" (contains after "Terminal.Future -> Some Model.Future");
  expect "new enum terminal reaches inverse mapping" (contains after "Model.Future -> Terminal.Future");
  expect "removed enum terminal disappears" (not (contains after "Planned"));
  let unknown = generated "start = widget, identifier; widget = \"custom\";" in
  expect "unknown grammar enum is discovered" (contains unknown "let widget : Terminal.t -> Model.widget option");
  expect "Model compatibility is left to its compiler" (contains unknown "Terminal.Custom -> Some Model.Custom");
  let symbols = generated "start = \"=\", identifier;" in
  expect "generic punctuation has stable hex name" (contains symbols "\"=\" -> Some Symbol_3D");
  let lexical = generated "start = identifier;" in
  expect "terminal-free grammar has an empty type" (contains lexical "module Terminal = struct\n  type t =\n    |\n");
  expect "empty terminal type has a refutation case" (contains lexical "| _ -> .");
  let reordered = generated "start = state, integer; state = \"planned\" | \"existing\";" in
  expect "enum alternative order does not change output" (before = reordered)

let test_collisions () =
  List.iter (fun grammar -> rejected grammar "CSF_SYMBOL_COLLISION") [
    "start = alpha; alpha = \"a\"; Alpha = \"b\";";
    "start = \"a\"; terminal = \"b\";";
    "start = \"existing\" | \"Existing\";";
    "start = \"->\" | \"arrow\";";
    "start = \"=\" | \"symbol_3D\";";
    "start = state, state_terminal; state = \"existing\"; state_terminal = \"other\";";
  ];
  rejected "start = bad-name; bad-name = \"x\";" "CSF_SYMBOL_NAME";
  rejected "start = \"bad-name\";" "CSF_SYMBOL_NAME";
  rejected "start = missing;" "CSF_GRAMMAR"

let test_reserved_identifiers () =
  List.iter (fun keyword ->
    rejected ("start = " ^ keyword ^ ", identifier; " ^ keyword ^ " = \"value\";") "CSF_SYMBOL_NAME")
    ["type"; "let"; "module"; "match"; "effect"; "nonrec"; "true"; "false"; "land"; "or"];
  let source = generated "start = effect_value, identifier; effect_value = \"value\";" in
  expect "keyword prefix is a valid identifier" (contains source "let effect_value :")

let () =
  let grammar_path = if Array.length Sys.argv > 1 then Sys.argv.(1)
    else "csf/compiler/architecture/language.ebnf" in
  let grammar = In_channel.with_open_bin grammar_path In_channel.input_all in
  test_actual_grammar grammar;
  test_grammar_drives_symbols ();
  test_collisions ();
  test_reserved_identifiers ();
  print_endline "CSF grammar symbol generation tests passed"
