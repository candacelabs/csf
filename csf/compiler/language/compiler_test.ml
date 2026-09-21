let expect message condition = if not condition then failwith message
let contains = Compiler.contains
let write path source = Out_channel.with_open_bin path (fun channel -> output_string channel source)

let rejects fragment action =
  try action (); failwith ("expected rejection containing: " ^ fragment)
  with Compiler.Error message ->
    expect ("wrong diagnostic: " ^ message) (contains message fragment)

let source = {|# References may precede their definitions.
diagram main LR {
  node input : value existing;
  node output : result planned in boundary;
  group boundary : container;
  edge input existing output label "passes a value";
  edge output planned input;
}
term value "Input value" "A value supplied to this example.";
term result "Result" "The value returned by this example.";
term container "Example boundary" "The named group containing the result.";
document "docs/example.md" { main; }
|}

let block = "Before.\n<!-- csf:diagram main -->\nold generated content\n<!-- /csf:diagram main -->\nAfter.\n"

let test_parser_renderer () =
  let model = Compiler.compile "fixture.csf" source in
  let rendered = Compiler.render_diagram model (List.hd model.diagrams) in
  List.iter (fun fragment -> expect ("missing rendered structure: " ^ fragment) (contains rendered fragment)) [
    "flowchart LR";
    "n_input[\"Input value (existing)\"]";
    "n_output[\"Result (planned)\"]";
    "subgraph g_boundary[\"Example boundary\"]";
    "n_input -->|\"passes a value\"| n_output";
    "n_output -.-> n_input";
    "%% Generated from csf/compiler/language/architecture.csf; do not edit.";
    "%% Documentation model only";
  ];
  let source = {|term value "Quotes \" | < > \\ # `" "A definition with a\nnewline, | <tag> and `code`.";
diagram main TB { node end : value existing; }
document "docs/example.md" { main; }|} in
  let model = Compiler.compile "escaping.csf" source in
  let rendered = Compiler.render_diagram model (List.hd model.diagrams) in
  expect "Mermaid metacharacters encoded" (contains rendered "Quotes #34; #124; #60; #62; #92; #35; #96;");
  expect "reserved Mermaid word receives prefix" (contains rendered "n_end[");
  let ontology = Compiler.render_ontology model in
  expect "dictionary escapes Markdown and HTML" (contains ontology "<br>newline, &#124; &lt;tag&gt; and \\`code\\`.");
  let document = List.hd model.documents in
  let output = Compiler.replace_document model document block in
  expect "surrounding Markdown bytes preserved" (String.starts_with ~prefix:"Before.\n" output && String.ends_with ~suffix:"\nAfter.\n" output);
  expect "old generated content replaced" (not (contains output "old generated content"));
  expect "rendering deterministic" (Compiler.replace_document model document output = output)

let minimal declarations =
  "term value \"Value\" \"The example value.\";\n" ^ declarations ^
  "\ndocument \"docs/example.md\" { main; }"

let test_invalid_source () =
  List.iter (fun (fragment, source) -> rejects fragment (fun () -> ignore (Compiler.compile "invalid.csf" source))) [
    "valid UTF-8", source ^ "term bad \"\255\" \"Invalid encoding.\";";
    "unknown term 'absent'", minimal "diagram main LR { node a : absent existing; }";
    "unknown edge endpoint 'b'", minimal "diagram main LR { node a : value existing; edge a planned b; }";
    "unknown group 'absent'", minimal "diagram main LR { node a : value existing in absent; }";
    "unknown term 'absent'", minimal "diagram main LR { node a : value existing; group g : absent; }";
    "unknown diagram 'absent'", source ^ "document \"docs/second.md\" { absent; }";
    "terms: duplicate 'value'", source ^ "term value \"Again\" \"Duplicate.\";";
    "diagrams: duplicate 'main'", source ^ "diagram main LR { node a : value existing; }";
    "node/group identifiers: duplicate 'a'", minimal "diagram main LR { node a : value existing; node a : value planned; }";
    "node/group identifiers: duplicate 'a'", minimal "diagram main LR { node a : value existing; group a : value; }";
    "edges: duplicate", minimal "diagram main LR { node a : value existing; edge a existing a; edge a planned a; }";
    "documents: duplicate", source ^ "document \"docs/example.md\" { main; }";
    "references: duplicate", source ^ "document \"docs/second.md\" { main; main; }";
    "must not be empty", source ^ "term empty \" \" \"Empty name.\";";
    "must not be empty", source ^ "term empty \"Empty\" \" \";";
    "must not be empty", minimal "diagram main LR { node a : value existing; edge a existing a label \" \"; }";
    "at least one node", minimal "diagram main LR {}";
    "at least one term", "";
    "expected", source ^ "trailing";
    "unexpected character", source ^ ".";
    "expected", minimal "diagram main LR { node a : value existing }";
    "expected", minimal "diagram main RL { node a : value existing; }";
    "unexpected character", source ^ "term Upper \"Name\" \"Definition\";";
    "allowed string escapes", source ^ {|term bad "Bad\t" "Definition";|};
    "unterminated quoted string", source ^ {|term bad "Bad|};
    "unterminated string escape", source ^ "term bad \"Bad\\";
    "control character", source ^ "term bad \"Bad\nname\" \"Definition\";";
    "expected", "term x \"X\" \"Definition\"; diagram main LR {";
  ];
  List.iter (fun path ->
    rejects "invalid repository-relative path" (fun () ->
      ignore (Compiler.compile "invalid.csf" (source ^ Printf.sprintf "document \"%s\" {}" path))))
    [""; "/outside.md"; "../outside.md"; "docs/../outside.md"; "./docs/a.md"; "docs//a.md"; "docs/"; {|docs\\a.md|}; {|docs/line\nbreak.md|}];
  rejects "must be Markdown" (fun () -> ignore (Compiler.compile "invalid.csf" (source ^ "document \"data.txt\" {}")));
  rejects "reserved" (fun () -> ignore (Compiler.compile "invalid.csf" (source ^ "document \"csf/docs/generated/ontology_cgen.md\" {}")))

let test_markers () =
  let model = Compiler.compile "fixture.csf" source in
  let document = List.hd model.documents in
  List.iter (fun (fragment, markdown) ->
    rejects fragment (fun () -> ignore (Compiler.replace_document model document markdown))) [
    "missing diagram marker", "No marker.\n";
    "missing closing marker", "<!-- csf:diagram main -->\n";
    "repeated diagram marker", block ^ block;
    "undeclared diagram marker", "<!-- csf:diagram unknown -->\n<!-- /csf:diagram unknown -->";
    "nested, unmatched, or mismatched", "<!-- csf:diagram main -->\n<!-- /csf:diagram wrong -->";
    "nested, unmatched, or mismatched", "<!-- /csf:diagram main -->\n";
    "nested, unmatched, or mismatched", "<!-- csf:diagram main -->\n<!-- csf:diagram main -->\n";
    "malformed diagram marker", block ^ "<!-- csf:diagram main-->\n";
    "invalid CSF diagram marker", block ^ "<!-- csf:diagram Bad -->\n";
  ];
  List.iter (fun fence -> rejects "handwritten Mermaid" (fun () ->
    ignore (Compiler.replace_document model document (block ^ fence ^ "\nflowchart LR\n```\n"))))
    ["```mermaid"; "  ``` mermaid"; "~~~~mermaid"; "> ```mermaid"; "```MERMAID";
      "```{.mermaid}"; "- ```mermaid"; "+ ```mermaid"; "* ```mermaid";
      "1. ```mermaid"; "20) ```mermaid"; "> - > 1. ```mermaid"];
  List.iter (fun html -> rejects "handwritten Mermaid HTML" (fun () ->
    ignore (Compiler.replace_document model document (block ^ html)))) [
    "<pre class=\"mermaid\">flowchart LR</pre>";
    "<div class='mermaid'>flowchart LR</div>";
    "<pre\nclass=\"mermaid\">flowchart LR</pre>";
    "<pre class=mermaid>flowchart LR</pre>";
    "<div id='x' class='other mermaid'>flowchart LR</div>";
  ];
  let ordinary = block ^ "<div class='note' title='mermaid'>Text</div>\n<!-- <pre class='mermaid'> -->\n" in
  ignore (Compiler.replace_document model document ordinary)

let with_fixture action =
  let root = Filename.temp_file "csf-language-" "" in
  Sys.remove root;
  Unix.mkdir root 0o700;
  let ensure_directory relative =
    String.split_on_char '/' relative
    |> List.fold_left (fun parent component ->
      let path = Filename.concat parent component in
      if not (Sys.file_exists path) then Unix.mkdir path 0o700;
      path) root |> ignore in
  let ensure_parent path = ensure_directory (Filename.dirname path) in
  let rec remove path =
    if (Unix.lstat path).Unix.st_kind = Unix.S_DIR then begin
      Sys.readdir path |> Array.iter (fun name -> remove (Filename.concat path name));
      Unix.rmdir path
    end else Sys.remove path in
  Fun.protect ~finally:(fun () -> remove root) (fun () ->
    List.iter ensure_parent [Compiler.source_path; Compiler.ontology_path; "docs/example.md"];
    write (Filename.concat root Compiler.source_path) source;
    write (Filename.concat root "docs/example.md") block;
    action root)

let test_files () = with_fixture (fun root ->
  let document = Filename.concat root "docs/example.md" in
  let ontology = Filename.concat root Compiler.ontology_path in
  rejects "generated documentation differs" (fun () -> ignore (Compiler.run root Compiler.Check));
  expect "check cannot create ontology" (not (Sys.file_exists ontology));
  expect "check cannot change document" (Compiler.read_file document = block);
  let generated = Compiler.run root Compiler.Write in
  expect "write generates both files" (List.length generated = 2);
  expect "check accepts generated files" (Compiler.run root Compiler.Check = []);
  expect "second write is a no-op" (Compiler.run root Compiler.Write = []);
  let expected = Compiler.read_file document in
  let edited = String.concat "\n" (List.map (fun line ->
    if contains line "n_input[" then "  n_input[\"Hand edit\"]" else line)
    (String.split_on_char '\n' expected)) in
  write document edited;
  rejects "docs/example.md" (fun () -> ignore (Compiler.run root Compiler.Check));
  expect "drift check preserves edited bytes" (Compiler.read_file document = edited);
  expect "write repairs edited generated block" (Compiler.run root Compiler.Write = ["docs/example.md"]);
  expect "repair is deterministic" (Compiler.read_file document = expected);
  write ontology "edited ontology\n";
  rejects "ontology_cgen.md" (fun () -> ignore (Compiler.run root Compiler.Check));
  ignore (Compiler.run root Compiler.Write);
  expect "repaired outputs pass" (Compiler.run root Compiler.Check = []))

let test_preflight () = with_fixture (fun root ->
  let document = Filename.concat root "docs/example.md" in
  let ontology = Filename.concat root Compiler.ontology_path in
  let source_file = Filename.concat root Compiler.source_path in
  write source_file (source ^ "document \"docs/missing.md\" { main; }");
  let missing_rejected = try ignore (Compiler.run root Compiler.Write); false with
    | Unix.Unix_error (Unix.ENOENT, _, _) -> true in
  expect "missing document rejected" missing_rejected;
  expect "missing later document leaves all outputs untouched"
    (Compiler.read_file document = block && not (Sys.file_exists ontology));
  write (Filename.concat root "docs/missing.md") "No generated block.\n";
  rejects "missing diagram marker" (fun () -> ignore (Compiler.run root Compiler.Write));
  expect "invalid later document leaves first output untouched" (Compiler.read_file document = block);
  write source_file source;
  Unix.symlink "example.md" (Filename.concat root "docs/alias.md");
  write source_file (source ^ "document \"docs/alias.md\" { main; }");
  rejects "symlinks are not allowed" (fun () -> ignore (Compiler.run root Compiler.Write));
  Unix.symlink "docs" (Filename.concat root "alias");
  write source_file (source ^ "document \"alias/example.md\" { main; }");
  rejects "symlinks are not allowed" (fun () -> ignore (Compiler.run root Compiler.Write));
  write source_file source;
  Unix.symlink "../../docs/example.md" ontology;
  rejects "symlinks are not allowed" (fun () -> ignore (Compiler.run root Compiler.Write)))

let () =
  test_parser_renderer ();
  test_invalid_source ();
  test_markers ();
  test_files ();
  test_preflight ();
  print_endline "CSF documentation compiler tests passed"
