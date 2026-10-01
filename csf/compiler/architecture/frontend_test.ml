let expect message condition = if not condition then failwith message

let describe diagnostics =
  List.map (fun (diagnostic : Model.diagnostic) ->
    Printf.sprintf "%s:%d:%d %s: %s" diagnostic.at.file diagnostic.at.line
      diagnostic.at.column diagnostic.code diagnostic.message) diagnostics |> String.concat "\n"

let valid grammar source =
  match Frontend.parse_text ~grammar ~source ~filename:"example.csf" with
  | Ok node -> node
  | Error diagnostics -> failwith ("unexpected rejection:\n" ^ describe diagnostics)

let invalid ?(code = "CSF_SYNTAX") grammar source =
  match Frontend.parse_text ~grammar ~source ~filename:"example.csf" with
  | Ok _ -> failwith ("unexpected acceptance: " ^ source)
  | Error diagnostics ->
      expect ("wrong diagnostic: " ^ describe diagnostics)
        (List.exists (fun (diagnostic : Model.diagnostic) -> diagnostic.code = code) diagnostics);
      List.hd diagnostics

let rec descendants rule (node : Frontend.node) =
  (if node.rule = rule then [node] else []) @ List.concat_map (descendants rule) node.children

let values nodes = List.filter_map (fun (node : Frontend.node) -> node.value) nodes

let architecture_example = {|// syntax fixture; no execution claim
architecture example version 1 {
  process daemon kind go entrypoint "app/daemon/main.go";
  process peer kind external;
  scope application under daemon;
  scope request under application;
  manager coordinator in daemon scope application state planned lifecycle scoped verification pending;
  service worker in daemon scope request source "services/worker" state existing lifecycle scoped verification test "worker_test.go";
  resource database in peer scope application state planned lifecycle borrowed verification pending;
  requires worker->database;
  connect worker -> database via remote boundary database state planned;
  scan "services";
  generated "generated";
}
|}

let test_real_grammar grammar =
  let root = valid grammar architecture_example in
  expect "start rule is architecture" (root.rule = "architecture");
  expect "root source location" (root.at = { Model.file = "example.csf"; line = 2; column = 1 });
  expect "named declarations preserved" (List.length (descendants "declaration" root) = 11);
  expect "process rules preserved" (List.length (descendants "process" root) = 2);
  expect "component roles preserved" (List.length (descendants "role" root) = 3);
  expect "identifier values decoded" (List.mem "worker" (values (descendants "identifier" root)));
  expect "integer value preserved" (values (descendants "integer" root) = ["1"]);
  expect "sequence children remain ordered and flat"
    (List.map (fun (node : Frontend.node) -> node.rule) (List.hd (descendants "process" root)).children
     = ["$terminal"; "identifier"; "$terminal"; "process_kind"; "$terminal"; "string"; "$terminal"]);
  let process = List.hd (descendants "process" root) in
  expect "declaration source location" (process.at.line = 3 && process.at.column = 3);
  expect "terminals retained" (List.mem "->" (values (descendants "$terminal" root)))

let test_json_strings grammar =
  let source = {|architecture strings version 1 {
  scan "a\"b\\c\/d\b\f\n\r\t\u263a\uD83D\uDE00"; // braces {} ignored
  generated "utf8-λ";
}|} in
  let root = valid grammar source in
  expect "JSON escapes and Unicode decoded"
    (values (descendants "string" root) = ["a\"b\\c/d\b\012\n\r\t☺😀"; "utf8-λ"]);
  let bad_strings = [
    "\"unterminated"; "\"bad\\q\""; "\"bad\\u123\""; "\"bad\\uXXXX\"";
    "\"bad\\uD800\""; "\"bad\\uDC00\""; "\"bad\\uD800\\u1234\"";
    "\"raw\nnewline\""; "\"\255\""; "\"backslash\\";
  ] in
  List.iter (fun literal -> ignore (invalid grammar
    ("architecture bad version 1 { scan " ^ literal ^ "; }"))) bad_strings

let test_invalid_architecture grammar =
  List.iter (fun source -> ignore (invalid grammar source)) [
    "";
    "architecture example version 1 {";
    "architecture example version 1 {} extra";
    "architecture architecture version 1 {}";
    "architecture example version -1 {}";
    "architecture example version 1 { process p kind go }";
    "architecture example version 1 { process p kind nope; }";
    "architecture example version 1 { scope s below p; }";
    "architecture example version 1 { requires a b; }";
    "architecture example version 1 { connect a -> b via call; }";
    "architecture example version 1 { @ }";
    "architecture example version 1 { /* comment */ }";
  ];
  let diagnostic = invalid grammar "architecture example version 1 {\n  process p kind ;\n}" in
  expect "failure location identifies unexpected token"
    (diagnostic.at.file = "example.csf" && diagnostic.at.line = 2 && diagnostic.at.column = 18)

let test_executable_grammar () =
  let grammar = "(* grammar comment (* nested *) *)\nstart = (\"alpha\" | 'beta'), [integer], {\"!\", identifier};" in
  let root = valid grammar "beta 42 ! item ! other-item // source comment" in
  expect "EBNF operators interpreted" (values root.children = ["beta"; "42"; "!"; "item"; "!"; "other-item"]);
  ignore (valid "start = \"x\", [\"a\"], \"a\";" "x a");
  ignore (valid "start = \"x\", {\"a\"}, \"a\";" "x a a");
  ignore (valid "start = (\"x\", \"y\") | (\"x\", \"z\");" "x z");
  ignore (valid "start = \"replacement\", identifier;" "replacement custom");
  ignore (invalid "start = \"replacement\", identifier;" "architecture custom");
  ignore (invalid "start = \"replacement\", identifier;" "replacement replacement")

let test_grammar_safety () =
  List.iter (fun grammar -> ignore (invalid ~code:"CSF_GRAMMAR" grammar "")) [
    "";
    "start = unknown;";
    "start = \"x\"; start = \"y\";";
    "identifier = \"x\";";
    "start = start, \"x\";";
    "start = [\"x\"], other; other = start;";
    "start = { [\"x\"] };";
    "start = {empty}; empty = \"\";";
    "start = \"x\"";
    "start = ();";
    "start = \"x\" | ;";
    "start = \"x\"; (* unterminated";
    "start = \"\\q\";";
  ];
  let root = valid "start = \"\";" "" in
  expect "empty terminal has empty children" (root.children = []);
  ignore (invalid ~code:"CSF_LIMIT" "start = \"x\", [start];" (String.concat " " (List.init 300 (fun _ -> "x"))));
  ignore (invalid ~code:"CSF_LIMIT" "start = \"x\";" (String.make (2 * 1024 * 1024 + 1) ' '));
  let nested = "start = " ^ String.make 140 '(' ^ "\"x\"" ^ String.make 140 ')' ^ ";" in
  ignore (invalid ~code:"CSF_LIMIT" nested "x")

let test_terminal_forms () =
  List.iter (fun literal ->
    ignore (invalid ~code:"CSF_GRAMMAR" ("start = " ^ literal ^ ";") "")) [
    {|"hello!"|}; {|"x y"|}; {|"//"|}; {|" abc"|}; {|"123abc"|};
    {|"\"x\""|}; {|"x\n"|};
  ];
  ignore (valid {|start = "hello", "!";|} "hello!");
  ignore (valid {|start = "x", "y";|} "x y");
  ignore (valid {|start = "42", "->", "other-item";|} "42->other-item");
  ignore (valid {|start = string;|} {|"literal with spaces!"|})

let test_files grammar_path =
  let source_path = Filename.temp_file "csf-frontend-" ".csf" in
  Fun.protect ~finally:(fun () -> Sys.remove source_path) (fun () ->
    Out_channel.with_open_bin source_path (fun channel -> output_string channel architecture_example);
    match Frontend.parse_files ~grammar_path ~source_path with
    | Ok root -> expect "file source location" (root.at.file = source_path)
    | Error diagnostics -> failwith (describe diagnostics));
  match Frontend.parse_files ~grammar_path ~source_path with
  | Ok _ -> failwith "missing source file accepted"
  | Error diagnostics -> expect "file I/O diagnostic" ((List.hd diagnostics).code = "CSF_IO")

let () =
  let grammar_path = if Array.length Sys.argv > 1 then Sys.argv.(1) else "csf/compiler/architecture/language.ebnf" in
  let grammar = In_channel.with_open_bin grammar_path In_channel.input_all in
  test_real_grammar grammar;
  test_json_strings grammar;
  test_invalid_architecture grammar;
  test_executable_grammar ();
  test_grammar_safety ();
  test_terminal_forms ();
  test_files grammar_path;
  print_endline "CSF executable EBNF frontend tests passed"
