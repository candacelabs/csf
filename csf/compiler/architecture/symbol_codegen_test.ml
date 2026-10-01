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
  (* Exercise the compiled projection against the current grammar's inventory.
     Grammar edits must not require maintaining a second vocabulary in this test.
     Fixed fixtures below own tests of the generator's individual behaviors. *)
  let parsed = Frontend_grammar.parse ~filename:"language.ebnf" grammar in
  let roundtrip namespace of_string name spelling =
    match of_string spelling with
    | None -> failwith ("missing generated " ^ namespace ^ ": " ^ spelling)
    | Some symbol -> expect ("generated " ^ namespace ^ " round trip: " ^ spelling)
        (name symbol = spelling) in
  let rules = Hashtbl.to_seq_keys parsed.rules |> List.of_seq in
  List.iter (roundtrip "rule" Syntax_cgen.Rule.of_string Syntax_cgen.Rule.name)
    (rules @ Frontend_grammar.builtin_names);
  List.iter (roundtrip "terminal" Syntax_cgen.Terminal.of_string Syntax_cgen.Terminal.name)
    parsed.terminals;
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
  let mixed = generated "start = entry; entry = \"named\", identifier | \"anonymous\";" in
  expect "mixed rule is not a plain enum" (not (contains mixed "let entry :"));
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
