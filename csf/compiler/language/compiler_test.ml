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
    "classDef csf_existing fill:#0F766E,stroke:#115E59,stroke-width:2px,color:#FFFFFF;";
    "classDef csf_planned fill:#FEF3C7,stroke:#B45309,stroke-width:2px,color:#78350F;";
    "n_input[\"Input value (existing)\"]:::csf_existing";
    "n_output[\"Result (planned)\"]:::csf_planned";
    "subgraph g_boundary[\"Example boundary\"]";
    "style g_boundary fill:#EEF2FF,stroke:#4338CA,stroke-width:2px,color:#1E1B4B";
    "n_input -->|\"passes a value\"| n_output";
    "n_output -.-> n_input";
    "linkStyle 0 stroke:#0F766E,stroke-width:2px";
    "linkStyle 1 stroke:#B45309,stroke-width:2px,stroke-dasharray:5 5";
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
    "unknown diagram or north star 'absent'", source ^ "document \"docs/second.md\" { absent; }";
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
    List.iter ensure_parent [Compiler.source_path; Compiler.ontology_path; Generated_block.python_path; "docs/example.md"];
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
  expect "write generates the ontology, glossary, Python block kinds and document" (List.length generated = 4);
  expect "Python oracle receives the generator's block kinds"
    (contains (Compiler.read_file (Filename.concat root Generated_block.python_path))
      "BLOCK_KINDS = (\"diagram\", \"north_star\")");
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

(* Vocabulary links: one fixture per rule. *)
let vocabulary_source = {|term service "Service" "A mounted lifecycle part." forms "service" "services";
term save_evidence "Save evidence" "Keep the result.";
term check "Check" "Apply the admission rules.";
literature hidden_physics "Hidden physics" "Unknown dynamics." "Held back from the agent." "A. Author. A title. A venue, 2018." "https://doi.org/10.1/x"
  forms "hidden physics";
retired candaceos "CandaceOS" "Name the part by its function.";
jargon "noise band";
section all "Everything" "All terms." "An example." { service; save_evidence; check; }
diagram main LR { node a : service existing; }
document "docs/diagram.md" { main; }
linked "docs/linked.md";
|}

let scan text =
  let model = Compiler.compile "vocabulary.csf" vocabulary_source in
  Compiler.scan_vocabulary model ~ontology:"../csf/docs/generated/ontology_cgen.md"
    ~glossary:"GLOSSARY.md" text

let messages text = List.map (fun (finding : Compiler.finding) ->
  Printf.sprintf "%d: %s" finding.offset finding.message) (snd (scan text))

let has_message text fragment = List.exists (fun message -> contains message fragment) (messages text)

let test_vocabulary () =
  let ontology = "../csf/docs/generated/ontology_cgen.md" in
  (* Rule: an unlinked term in prose is a finding, and write links it. *)
  let linked, findings = scan "One service here.\n" in
  expect "unlinked term reported with its line" (has_message "One service here.\n" "1: unlinked ontology term 'service' (term service)");
  expect "unlinked term is not blocking" (List.for_all (fun (finding : Compiler.finding) -> finding.kind = Compiler.Unlinked) findings);
  expect "write links the term" (linked = "One [service](" ^ ontology ^ "#term-service) here.\n");
  expect "linked output passes" (snd (scan linked) = []);
  (* Rule: multi-word names match across a line break and win over shorter names. *)
  let linked, _ = scan "Then Save\nevidence.\n" in
  expect "multi-word name across a line" (linked = "Then [Save\nevidence](" ^ ontology ^ "#term-save_evidence).\n");
  (* Rule: an underscored identifier in a code span is linked; a bare one is not. *)
  let linked, _ = scan "Use `save_evidence` and `check`.\n" in
  expect "underscored identifier code span linked"
    (linked = "Use [`save_evidence`](" ^ ontology ^ "#term-save_evidence) and `check`.\n");
  (* Rule: matching is exact-case against declared spellings and word-bounded. *)
  List.iter (fun text -> expect ("no finding in: " ^ text) (messages text = [])) [
    "We check the result.\n"; "Servicing and microservices.\n"; "An agent-service and HTTP/service.\n";
    "```\nservice CandaceOS\n```\n"; "# A service heading\n"; "<div>service</div>\n";
    "A [service link](elsewhere.md) and ![a service](image.png).\n"; "See <https://example.invalid/service>.\n";
    "Bare https://example.invalid/service URL.\n"; "Text `service` code.\n";
    "Correct [service](" ^ ontology ^ "#term-service).\n";
  ];
  (* Rule: a literature term links to its glossary entry. *)
  let linked, _ = scan "Under hidden physics.\n" in
  expect "literature term links to the glossary" (linked = "Under [hidden physics](GLOSSARY.md#lit-hidden_physics).\n");
  (* Rule: links to undefined terms, undeclared literature or the wrong path are blocking. *)
  expect "undefined term link" (has_message ("[x](" ^ ontology ^ "#term-absent)\n") "link to an undefined ontology term '#term-absent'");
  expect "undeclared literature link" (has_message "[x](GLOSSARY.md#lit-absent)\n" "undeclared literature term");
  expect "wrong ontology path" (has_message "[x](ontology_cgen.md#term-service)\n" "ontology link path");
  (* Rule: retired words are reported in prose, headings, HTML and code spans, not fences. *)
  List.iter (fun text -> expect ("retired word in: " ^ text) (has_message text "retired word 'CandaceOS'"))
    ["Run CandaceOS.\n"; "# CandaceOS\n"; "<b>CandaceOS</b>\n"; "Use `CandaceOS`.\n"; "Two CandaceOSs.\n"];
  expect "retired word is blocking" (List.exists (fun (finding : Compiler.finding) ->
    finding.kind = Compiler.Blocking) (snd (scan "CandaceOS\n")));
  (* Rule: a declared jargon candidate is reported until it is declared. *)
  expect "undeclared jargon" (has_message "Inside the noise band.\n" "undeclared jargon 'noise band'");
  (* Glossary and ontology anchors. *)
  let model = Compiler.compile "vocabulary.csf" vocabulary_source in
  let glossary = Compiler.render_glossary model in
  expect "glossary banner" (contains glossary Compiler.glossary_banner);
  expect "glossary literature anchor and citation" (contains glossary "<a id=\"lit-hidden_physics\"></a>" &&
    contains glossary "A. Author. A title. A venue, 2018. <https://doi.org/10.1/x>");
  expect "glossary section links the dictionary" (contains glossary "(../csf/docs/generated/ontology_cgen.md#term-save_evidence)");
  let dictionary = Compiler.render_ontology model in
  expect "ontology anchors" (contains dictionary "<a id=\"term-service\"></a>`service`");
  expect "ontology retired table" (contains dictionary "| CandaceOS | Name the part by its function. |");
  expect "relative links" (Compiler.relative_link "README.md" Compiler.ontology_path = "csf/docs/generated/ontology_cgen.md" &&
    Compiler.relative_link "csf/README.md" Compiler.ontology_path = "docs/generated/ontology_cgen.md" &&
    Compiler.relative_link Compiler.glossary_path Compiler.ontology_path = "../csf/docs/generated/ontology_cgen.md")

let test_vocabulary_source () =
  let base = vocabulary_source in
  List.iter (fun (fragment, source) -> rejects fragment (fun () -> ignore (Compiler.compile "invalid.csf" source))) [
    "surface spellings: duplicate 'Service'", base ^ "jargon \"Service\";";
    "surface spellings: duplicate 'CandaceOS'", base ^ "retired again \"CandaceOS\" \"Duplicate.\";";
    "retired: duplicate 'candaceos'", base ^ "retired candaceos \"Other\" \"Duplicate.\";";
    "literature: duplicate", base ^ "literature hidden_physics \"Other\" \"m\" \"u\" \"c\" \"https://x.invalid\";";
    "url must be one https:// link", base ^ "literature other \"Other\" \"m\" \"u\" \"c\" \"http://x.invalid\";";
    "must not be empty", base ^ "literature other \"Other\" \"m\" \" \" \"c\" \"https://x.invalid\";";
    "expected quoted string", base ^ "term bare \"Bare\" \"Definition.\" forms;";
    "not in any glossary section", base ^ "term orphan \"Orphan\" \"Definition.\";";
    "section members: duplicate", base ^ "section again \"Again\" \"s\" \"e\" { check; }";
    "unknown term 'absent'", base ^ "section again \"Again\" \"s\" \"e\" { absent; }";
    "reserved for the generated glossary", base ^ "linked \"docs/GLOSSARY.md\";";
    "must be Markdown", base ^ "linked \"docs/linked.txt\";";
    "linked documents: duplicate", base ^ "linked \"docs/linked.md\";";
  ]

(* Check mode reports file:line findings; write links and check then passes. *)
let test_vocabulary_files () =
  let root = Filename.temp_file "csf-vocabulary-" "" in
  Sys.remove root;
  Unix.mkdir root 0o700;
  let file relative contents =
    let path = Filename.concat root relative in
    let rec parents path = let parent = Filename.dirname path in
      if not (Sys.file_exists parent) then (parents parent; Unix.mkdir parent 0o700) in
    parents path; write path contents in
  let directory relative = file (Filename.concat relative ".keep") "" in
  directory (Filename.dirname Compiler.ontology_path);
  directory (Filename.dirname Compiler.glossary_path);
  directory (Filename.dirname Generated_block.python_path);
  file Compiler.source_path vocabulary_source;
  file "docs/diagram.md" "<!-- csf:diagram main -->\n<!-- /csf:diagram main -->\n";
  file "docs/linked.md" "Intro.\n\nA service and hidden physics.\n";
  rejects "docs/linked.md:3: unlinked ontology term 'service'" (fun () -> ignore (Compiler.run root Compiler.Check));
  ignore (Compiler.run root Compiler.Write);
  expect "write inserts links" (Compiler.read_file (Filename.concat root "docs/linked.md") =
    "Intro.\n\nA [service](../csf/docs/generated/ontology_cgen.md#term-service) and \
     [hidden physics](GLOSSARY.md#lit-hidden_physics).\n");
  expect "glossary generated" (Sys.file_exists (Filename.concat root Compiler.glossary_path));
  expect "check passes after write" (Compiler.run root Compiler.Check = []);
  file "docs/linked.md" "CandaceOS returns.\n";
  rejects "docs/linked.md:1: retired word 'CandaceOS'" (fun () -> ignore (Compiler.run root Compiler.Write));
  expect "write refuses blocking findings without writing"
    (Compiler.read_file (Filename.concat root "docs/linked.md") = "CandaceOS returns.\n");
  expect "lint reports any file" (List.exists (fun line -> contains line "retired word")
    (Compiler.lint root [Filename.concat root "docs/linked.md"]))

(* North star: one goal, typed milestone status, checked terms, citations and
   evidence. The fixture's README holds it between generated-block markers. *)
let star_source = {|term value "Value" "The example value.";
term check "Check" "Apply the admission rules.";
literature proof "Proof kernel" "A verified kernel." "Named as the target." "A. Author. A title. A venue, 2009." "https://example.invalid/proof";
diagram main LR { node a : value existing; }
north_star goal {
  goal "Correct end to end, checked before every Value is used.";
  milestone shipped done "The value ships." {
    uses value; evidence path "src/value.go"; evidence pr 7;
  }
  milestone moving in_progress "The check is monitored." {
    uses check; uses value; evidence issue 12; cites proof;
  }
  milestone later planned "Everything is proven." { uses value; }
}
document "README.md" { goal; main; }
linked "README.md";
|}

let star_block = "Intro.\n\n<!-- csf:north_star goal -->\n<!-- /csf:north_star goal -->\n\n\
  <!-- csf:diagram main -->\n<!-- /csf:diagram main -->\n"

let test_north_star () =
  let model = Compiler.compile "star.csf" star_source in
  let star = List.hd model.north_stars in
  let rendered = Compiler.render_north_star model ~path:"README.md" star in
  List.iter (fun fragment -> expect ("missing north star structure: " ^ fragment) (contains rendered fragment)) [
    "## North star";
    "*Generated from [architecture.csf](csf/compiler/language/architecture.csf)";
    "**Milestones.** 1 done, 1 in progress, 1 planned.";
    "| 1 | The value ships. | done | [Value](csf/docs/generated/ontology_cgen.md#term-value) | [`src/value.go`](src/value.go), PR #7 | — |";
    "[Proof kernel](https://example.invalid/proof) ([glossary](docs/GLOSSARY.md#lit-proof))";
    "| 3 | Everything is proven. | planned |";
  ];
  (* Prose in the goal is linked exactly as the document's own prose is. *)
  expect "goal prose is vocabulary-linked"
    (contains rendered "every [Value](csf/docs/generated/ontology_cgen.md#term-value) is used");
  expect "links are relative to the holding document"
    (contains (Compiler.render_north_star model ~path:"docs/x.md" star) "[`src/value.go`](../src/value.go)");
  let with_star declaration = String.concat "\n" [
    "term value \"Value\" \"The example value.\";";
    "literature proof \"Proof kernel\" \"m\" \"u\" \"c\" \"https://example.invalid/proof\";";
    "diagram main LR { node a : value existing; }";
    "north_star goal { goal \"The goal.\"; " ^ declaration ^ " }";
    "document \"README.md\" { goal; main; }" ] in
  List.iter (fun (fragment, source) -> rejects fragment (fun () -> ignore (Compiler.compile "star.csf" source))) [
    (* Rule: every used term exists. *)
    "milestone m: unknown term 'absent'", with_star "milestone m planned \"S.\" { uses absent; }";
    (* Rule: a done milestone carries path evidence the checker verifies. *)
    "a done milestone needs path evidence", with_star "milestone m done \"S.\" { uses value; }";
    "a done milestone needs path evidence", with_star "milestone m done \"S.\" { uses value; evidence pr 4; }";
    "an in_progress milestone needs evidence", with_star "milestone m in_progress \"S.\" { uses value; }";
    (* Rule: a cited literature entry exists and has its https link. *)
    "unknown literature entry 'absent'", with_star "milestone m planned \"S.\" { uses value; cites absent; }";
    "expected quoted string", "literature bare \"Bare\" \"m\" \"u\" \"c\";";
    "url must be one https:// link", with_star "milestone m planned \"S.\" { uses value; cites plain; }"
      ^ "\nliterature plain \"Plain\" \"m\" \"u\" \"c\" \"http://example.invalid\";";
    "at least one used term", with_star "milestone m planned \"S.\" { evidence issue 3; }";
    "at least one milestone", with_star "";
    "milestones: duplicate 'm'", with_star "milestone m planned \"S.\" { uses value; } milestone m planned \"T.\" { uses value; }";
    "terms: duplicate 'value'", with_star "milestone m planned \"S.\" { uses value; uses value; }";
    "evidence: duplicate 'pr 4'", with_star "milestone m planned \"S.\" { uses value; evidence pr 4; evidence pr 4; }";
    "generated blocks: duplicate 'main'",
      String.concat "" [with_star "milestone m planned \"S.\" { uses value; }"; "\nnorth_star main { goal \"G.\"; milestone m planned \"S.\" { uses value; } }"];
    "invalid repository-relative path", with_star "milestone m done \"S.\" { uses value; evidence path \"../x\"; }";
    "leading zeros", with_star "milestone m planned \"S.\" { uses value; evidence pr 07; }";
    "expected done, in_progress or planned", with_star "milestone m finished \"S.\" { uses value; }";
    "must not be empty", with_star "milestone m planned \" \" { uses value; }";
  ];
  (* Retired words in north-star prose fail generation like any linked prose. *)
  let retired = Compiler.compile "star.csf" (star_source ^ "retired old \"Legacy\" \"Use the new name.\";") in
  let legacy = { star with goal = "Legacy goal." } in
  rejects "retired word 'Legacy'" (fun () -> ignore (Compiler.render_north_star retired ~path:"README.md" legacy));
  (* Files: evidence paths exist under ROOT; write renders and check passes. *)
  let root = Filename.temp_file "csf-star-" "" in
  Sys.remove root;
  Unix.mkdir root 0o700;
  let file relative contents =
    let path = Filename.concat root relative in
    let rec parents path = let parent = Filename.dirname path in
      if not (Sys.file_exists parent) then (parents parent; Unix.mkdir parent 0o700) in
    parents path; write path contents in
  List.iter (fun relative -> file (Filename.concat (Filename.dirname relative) ".keep") "")
    [Compiler.ontology_path; Compiler.glossary_path; Generated_block.python_path];
  file Compiler.source_path star_source;
  file "README.md" star_block;
  rejects "north_star goal milestone shipped: evidence path 'src/value.go' does not exist"
    (fun () -> ignore (Compiler.run root Compiler.Write));
  expect "missing evidence writes nothing" (Compiler.read_file (Filename.concat root "README.md") = star_block);
  file "src/value.go" "package value\n";
  ignore (Compiler.run root Compiler.Write);
  let readme = Compiler.read_file (Filename.concat root "README.md") in
  expect "README holds the rendered north star" (contains readme "**Goal.** Correct end to end");
  expect "check passes after write" (Compiler.run root Compiler.Check = []);
  file "README.md" (String.concat "\n" (List.map (fun line ->
    if contains line "**Milestones.**" then "**Milestones.** 3 done, 0 in progress, 0 planned." else line)
    (String.split_on_char '\n' readme)));
  rejects "generated documentation differs: README.md" (fun () -> ignore (Compiler.run root Compiler.Check));
  ignore (Compiler.run root Compiler.Write);
  expect "write repairs a hand edit" (Compiler.read_file (Filename.concat root "README.md") = readme);
  (* Markers name their kind: a north star is not a diagram, and an unknown
     kind is rejected rather than silently left as prose. *)
  let document = List.find (fun (document : Compiler.document) -> document.path = "README.md") model.documents in
  List.iter (fun (fragment, markdown) ->
    rejects fragment (fun () -> ignore (Compiler.replace_document model document markdown))) [
    "'goal' is a north_star, not a diagram", "<!-- csf:diagram goal -->\n<!-- /csf:diagram goal -->\n";
    "'main' is a diagram, not a north_star", "<!-- csf:north_star main -->\n<!-- /csf:north_star main -->\n";
    "unknown generated block kind", star_block ^ "<!-- csf:nort_star goal -->\n";
    "mismatched diagram marker", "<!-- csf:north_star goal -->\n<!-- /csf:diagram goal -->\n";
    "malformed north_star marker", star_block ^ "Text <!-- csf:north_star goal -->\n";
    "missing north_star marker 'goal'", "<!-- csf:diagram main -->\n<!-- /csf:diagram main -->\n";
  ]

let () =
  test_north_star ();
  test_parser_renderer ();
  test_vocabulary ();
  test_vocabulary_source ();
  test_vocabulary_files ();
  test_invalid_source ();
  test_markers ();
  test_files ();
  test_preflight ();
  print_endline "CSF documentation compiler tests passed"
