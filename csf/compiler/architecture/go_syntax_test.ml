module Node = Tree_sitter.Node

let expect message condition = if not condition then failwith message
let source expression = "package example\nvar value = " ^ expression ^ "\n"

let raw_parse text =
  Tree_sitter.Parser.parse_string (Lazy.force Checker.go_parser) (text ^ "\n")
  |> Tree_sitter.Tree.root_node

let nodes kind root =
  let found = ref [] in
  Checker.walk (fun node -> if Node.kind node = kind then found := node :: !found) root;
  List.rev !found

let function_names text root =
  nodes "call_expression" root |> List.filter_map (fun node ->
    Option.map (Checker.node_source text) (Node.child_by_field_name node "function"))

let accepted expression =
  let text = source expression in
  let root = Go_syntax.parse text in
  expect (expression ^ ": adaptation left a parse error") (not (Node.has_error root));
  expect (expression ^ ": new function no longer points to original source")
    (List.mem "new" (function_names text root));
  text, root

let test_expression_forms () =
  List.iter (fun expression ->
    let text, root = accepted expression in
    let raw = raw_parse text in
    (* The pinned upstream grammar already accepts these Go 1.26 forms. *)
    expect (expression ^ ": supported expression changed")
      (Node.to_sexp root = Node.to_sexp raw))
    ["new(3)"; "new(1.25)"; "new(\"x\")"; "new(int(value))"; "new(new(3))"]

let test_erroneous_call_adaptation () =
  let text = source "new(3 +)" in
  let raw = raw_parse text in
  expect "adapter fixture must contain a parse error" (Node.has_error raw);
  let adapted = Bytes.of_string text in
  expect "erroneous new call was not adapted" (Go_syntax.adapt_calls text adapted raw);
  expect "adapter must only replace the call function, preserving source spans"
    (Bytes.to_string adapted = source "cgn(3 +)");
  expect "adapter repeated the same replacement"
    (not (Go_syntax.adapt_calls text adapted raw));
  let root = Go_syntax.parse text in
  expect "adapter hid the malformed argument"
    (Node.has_error root);
  let call = List.hd (nodes "call_expression" root) in
  let fn = Option.get (Node.child_by_field_name call "function") in
  expect "adapted function no longer points to original source"
    (Checker.node_source text fn = "new");
  let position = Node.start_point fn in
  expect "adapted function byte location changed"
    (Node.start_byte fn = String.length "package example\nvar value = " &&
     position.row = 1 && position.column = String.length "var value = ")

let test_unchanged_type_forms () =
  List.iter (fun expression ->
    let text, root = accepted expression in
    expect (expression ^ ": previously accepted type form changed")
      (Node.to_sexp root = Node.to_sexp (raw_parse text)))
    ["new([]byte)"; "new(http.Server)"; "new(struct { Value int })"];
  let text, root = accepted "new(http.Server)" in
  let call = List.hd (nodes "call_expression" root) in
  let arguments = Option.get (Node.child_by_field_name call "arguments") in
  let target = Option.get (Node.named_child arguments 0) in
  expect "allocation policy retains qualified type" (Node.kind target = "qualified_type");
  expect "allocation policy reads original type" (Checker.node_source text target = "http.Server")

let test_nested_boundaries () =
  List.iter (fun expression ->
    let text, root = accepted expression in
    let selectors = nodes "selector_expression" root |> List.map (Checker.node_source text) in
    expect "nested child-process API remains visible" (List.mem "exec.Command" selectors);
    expect "nested API invocation remains visible" (List.mem "exec.Command" (function_names text root)))
    ["new(exec.Command(\"helper\"))"; "new(new(exec.Command(\"helper\")))"];
  let text, root = accepted "new(int(os.Getenv(\"PORT\")))" in
  expect "nested process configuration API remains visible"
    (List.mem "os.Getenv" (function_names text root));
  let selector = List.find (fun node -> Checker.node_source text node = "os.Getenv")
    (nodes "selector_expression" root) in
  let position = Node.start_point selector in
  expect "nested API byte location unchanged"
    (position.row = 1 && position.column = String.length "var value = new(int(")

let test_syntax_errors () =
  List.iter (fun expression ->
    expect (expression ^ ": malformed arguments accepted")
      (Node.has_error (Go_syntax.parse (source expression))))
    ["new(3 +)"; "new(, 3)"; "new(int(value)"; "new(\"unterminated)"; "new(3,,4)"];
  let text = source "new(3)" ^ "func broken( { }\n" in
  expect "unrelated syntax error was hidden" (Node.has_error (Go_syntax.parse text));
  let text = "package example\nvar value = other(3 +)\n" in
  expect "ordinary malformed call was changed"
    (Node.to_sexp (Go_syntax.parse text) = Node.to_sexp (raw_parse text))

let test_comments_and_literals () =
  let text = "package example\n// new(3) stays a comment\nvar text = `new(3)`\nvar value = new(\"new(3)\")\n" in
  let root = Go_syntax.parse text in
  expect "comment/string fixture parses" (not (Node.has_error root));
  expect "comment text unchanged"
    (List.map (Checker.node_source text) (nodes "comment" root) = ["// new(3) stays a comment"]);
  expect "raw string unchanged"
    (List.map (Checker.node_source text) (nodes "raw_string_literal" root) = ["`new(3)`"]);
  expect "quoted string unchanged"
    (List.map (Checker.node_source text) (nodes "interpreted_string_literal" root) = ["\"new(3)\""])

let () =
  test_expression_forms ();
  test_erroneous_call_adaptation ();
  test_unchanged_type_forms ();
  test_nested_boundaries ();
  test_syntax_errors ();
  test_comments_and_literals ();
  print_endline "CSF Go syntax compatibility tests passed"
