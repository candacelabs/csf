exception Error of string

let fail format = Printf.ksprintf (fun message -> raise (Error message)) format
let source_path = "csf/compiler/language/architecture.csf"
let ontology_path = "csf/docs/generated/ontology_cgen.md"
let glossary_path = "docs/GLOSSARY.md"

type token_kind = Word of string | Quoted of string | Number of int | Symbol of char | End
type token = { kind : token_kind; line : int; column : int }
type parser = { path : string; tokens : token array; mutable next : int }

let lowercase = function 'a' .. 'z' -> true | _ -> false
let digit = function '0' .. '9' -> true | _ -> false
let word_char character = lowercase character || digit character || character = '_'
let whitespace = function ' ' | '\t' | '\r' | '\n' -> true | _ -> false

let lex path source =
  if not (String.is_valid_utf_8 source) then fail "%s: source must be valid UTF-8" path;
  let length = String.length source in
  let index = ref 0 and line = ref 1 and column = ref 1 in
  let advance () =
    let character = source.[!index] in
    incr index;
    if character = '\n' then (incr line; column := 1) else incr column;
    character in
  let error message = fail "%s:%d:%d: %s" path !line !column message in
  let quoted () =
    ignore (advance ());
    let buffer = Buffer.create 32 in
    let rec loop () =
      if !index = length then error "unterminated quoted string";
      match advance () with
      | '"' -> Buffer.contents buffer
      | '\\' ->
          if !index = length then error "unterminated string escape";
          let character = match advance () with
            | '"' -> '"' | '\\' -> '\\' | 'n' -> '\n'
            | _ -> error "allowed string escapes are quote, backslash, and \\n" in
          Buffer.add_char buffer character; loop ()
      | character when Char.code character < 32 || Char.code character = 127 ->
          error "control character in quoted string; use \\n for a newline"
      | character -> Buffer.add_char buffer character; loop () in
    loop () in
  let rec loop tokens =
    if !index = length then
      Array.of_list (List.rev ({ kind = End; line = !line; column = !column } :: tokens))
    else if whitespace source.[!index] then (ignore (advance ()); loop tokens)
    else if source.[!index] = '#' then begin
      while !index < length && source.[!index] <> '\n' do ignore (advance ()) done;
      loop tokens
    end else begin
      let token_line = !line and token_column = !column in
      let kind = match source.[!index] with
        | '"' -> Quoted (quoted ())
        | ('{' | '}' | ':' | ';') as character -> ignore (advance ()); Symbol character
        | character when digit character ->
            let start = !index in
            while !index < length && digit source.[!index] do ignore (advance ()) done;
            let value = String.sub source start (!index - start) in
            if value.[0] = '0' then error "numbers are positive decimals without leading zeros";
            (match int_of_string_opt value with
             | Some number -> Number number
             | None -> error "number out of range")
        | character when lowercase character || character = 'L' || character = 'T' ->
            let start = !index in
            ignore (advance ());
            while !index < length &&
              (word_char source.[!index] || source.[!index] = 'R' || source.[!index] = 'B') do
              ignore (advance ())
            done;
            let value = String.sub source start (!index - start) in
            if value <> "LR" && value <> "TB" &&
              not (lowercase value.[0] && String.for_all word_char value) then
              error "identifiers use lowercase ASCII letters, digits, and underscores";
            Word value
        | _ -> error "unexpected character" in
      loop ({ kind; line = token_line; column = token_column } :: tokens)
    end in
  loop []

let peek parser = parser.tokens.(parser.next)
let take parser = let token = peek parser in parser.next <- parser.next + 1; token
let expected parser description =
  let token = peek parser in
  fail "%s:%d:%d: expected %s" parser.path token.line token.column description
let symbol parser character =
  if (peek parser).kind <> Symbol character then expected parser (Printf.sprintf "'%c'" character);
  ignore (take parser)
let keyword parser word =
  if (peek parser).kind <> Word word then expected parser word;
  ignore (take parser)
let identifier parser = match (peek parser).kind with
  | Word value when lowercase value.[0] -> ignore (take parser); value
  | _ -> expected parser "lowercase identifier"
let quoted parser = match (peek parser).kind with
  | Quoted value -> ignore (take parser); value
  | _ -> expected parser "quoted string"
let number parser = match (peek parser).kind with
  | Number value -> ignore (take parser); value
  | _ -> expected parser "number"

type state = Existing | Planned
type term = { term_id : string; name : string; definition : string; forms : string list }
type literature = { lit_id : string; lit_name : string; meaning : string; usage : string;
  citation : string; url : string; lit_forms : string list }
type retired = { retired_id : string; word : string; replacement : string }
type section = { section_id : string; title : string; summary : string; example : string;
  members : string list }
type node = { node_id : string; term : string; state : state; group : string option }
type group = { group_id : string; term : string }
type edge = { from_node : string; to_node : string; state : state; label : string option }
type diagram = { diagram_id : string; direction : string; nodes : node list;
  groups : group list; edges : edge list }
(* A north star: one goal and the ordered milestones toward it. Status is
   typed so a done claim must carry evidence the checker can verify. *)
type status = Done | In_progress | Not_started
type evidence = Path of string | Pull_request of int | Issue of int
type milestone = { milestone_id : string; status : status; statement : string;
  uses : string list; evidence : evidence list; cites : string list }
type north_star = { star_id : string; goal : string; milestones : milestone list }
(* [diagrams] names every generated block the document holds: diagrams and
   north stars share one marker form and one identifier space. *)
type document = { path : string; diagrams : string list }
type model = { terms : term list; diagrams : diagram list; documents : document list;
  literature : literature list; retired : retired list; jargon : string list;
  sections : section list; linked : string list; north_stars : north_star list }

let state parser = match (peek parser).kind with
  | Word "existing" -> ignore (take parser); Existing
  | Word "planned" -> ignore (take parser); Planned
  | _ -> expected parser "existing or planned"

let parse_node parser =
  let node_id = identifier parser in
  symbol parser ':';
  let term = identifier parser in
  let state = state parser in
  let group = if (peek parser).kind = Word "in" then
    (ignore (take parser); Some (identifier parser)) else None in
  symbol parser ';';
  { node_id; term; state; group }

let parse_group parser =
  let group_id = identifier parser in
  symbol parser ':';
  let term = identifier parser in
  symbol parser ';';
  { group_id; term }

let parse_edge parser =
  let from_node = identifier parser in
  let state = state parser in
  let to_node = identifier parser in
  let label = if (peek parser).kind = Word "label" then
    (ignore (take parser); Some (quoted parser)) else None in
  symbol parser ';';
  { from_node; to_node; state; label }

let parse_diagram parser =
  let diagram_id = identifier parser in
  let direction = match (peek parser).kind with
    | Word ("LR" | "TB" as value) -> ignore (take parser); value
    | _ -> expected parser "LR or TB" in
  symbol parser '{';
  let rec declarations nodes groups edges = match (peek parser).kind with
    | Symbol '}' -> ignore (take parser);
        { diagram_id; direction; nodes = List.rev nodes;
          groups = List.rev groups; edges = List.rev edges }
    | Word "node" -> ignore (take parser);
        let node = parse_node parser in declarations (node :: nodes) groups edges
    | Word "group" -> ignore (take parser);
        let group = parse_group parser in declarations nodes (group :: groups) edges
    | Word "edge" -> ignore (take parser);
        let edge = parse_edge parser in declarations nodes groups (edge :: edges)
    | _ -> expected parser "node, group, edge, or '}'" in
  declarations [] [] []

let parse_document parser =
  let path = quoted parser in
  symbol parser '{';
  let rec references diagrams = match (peek parser).kind with
    | Symbol '}' -> ignore (take parser); { path; diagrams = List.rev diagrams }
    | _ -> let diagram = identifier parser in
        symbol parser ';'; references (diagram :: diagrams) in
  references []

(* Optional surface forms: other exact spellings a linked document uses for the
   same entry, such as a lowercase or plural noun. *)
let parse_forms parser =
  if (peek parser).kind <> Word "forms" then [] else begin
    ignore (take parser);
    let rec loop forms = match (peek parser).kind with
      | Quoted value -> ignore (take parser); loop (value :: forms)
      | _ -> List.rev forms in
    match loop [] with [] -> expected parser "quoted string" | forms -> forms
  end

let parse_section parser =
  let section_id = identifier parser in
  let title = quoted parser in
  let summary = quoted parser in
  let example = quoted parser in
  symbol parser '{';
  let rec members acc = match (peek parser).kind with
    | Symbol '}' -> ignore (take parser); List.rev acc
    | _ -> let member = identifier parser in symbol parser ';'; members (member :: acc) in
  { section_id; title; summary; example; members = members [] }

let parse_milestone parser =
  let milestone_id = identifier parser in
  let status = match (peek parser).kind with
    | Word "done" -> ignore (take parser); Done
    | Word "in_progress" -> ignore (take parser); In_progress
    | Word "planned" -> ignore (take parser); Not_started
    | _ -> expected parser "done, in_progress or planned" in
  let statement = quoted parser in
  symbol parser '{';
  let rec items uses evidence cites = match (peek parser).kind with
    | Symbol '}' -> ignore (take parser);
        { milestone_id; status; statement; uses = List.rev uses;
          evidence = List.rev evidence; cites = List.rev cites }
    | Word "uses" -> ignore (take parser);
        let term = identifier parser in symbol parser ';'; items (term :: uses) evidence cites
    | Word "cites" -> ignore (take parser);
        let entry = identifier parser in symbol parser ';'; items uses evidence (entry :: cites)
    | Word "evidence" -> ignore (take parser);
        let item = match (peek parser).kind with
          | Word "path" -> ignore (take parser); Path (quoted parser)
          | Word "pr" -> ignore (take parser); Pull_request (number parser)
          | Word "issue" -> ignore (take parser); Issue (number parser)
          | _ -> expected parser "path, pr or issue" in
        symbol parser ';'; items uses (item :: evidence) cites
    | _ -> expected parser "uses, evidence, cites or '}'" in
  items [] [] []

let parse_north_star parser =
  let star_id = identifier parser in
  symbol parser '{';
  keyword parser "goal";
  let goal = quoted parser in
  symbol parser ';';
  let rec milestones acc = match (peek parser).kind with
    | Symbol '}' -> ignore (take parser); List.rev acc
    | Word "milestone" -> ignore (take parser); milestones (parse_milestone parser :: acc)
    | _ -> expected parser "milestone or '}'" in
  { star_id; goal; milestones = milestones [] }

let parse path source =
  let parser = { path; tokens = lex path source; next = 0 } in
  let terms = ref [] and diagrams = ref [] and documents = ref [] and literature = ref []
  and retired = ref [] and jargon = ref [] and sections = ref [] and linked = ref []
  and north_stars = ref [] in
  let push cell value = cell := value :: !cell in
  let rec declarations () = match (peek parser).kind with
    | End -> { terms = List.rev !terms; diagrams = List.rev !diagrams; documents = List.rev !documents;
        literature = List.rev !literature; retired = List.rev !retired; jargon = List.rev !jargon;
        sections = List.rev !sections; linked = List.rev !linked; north_stars = List.rev !north_stars }
    | Word "term" -> ignore (take parser);
        let term_id = identifier parser in
        let name = quoted parser in
        let definition = quoted parser in
        let forms = parse_forms parser in
        symbol parser ';';
        push terms { term_id; name; definition; forms }; declarations ()
    | Word "literature" -> ignore (take parser);
        let lit_id = identifier parser in
        let lit_name = quoted parser in
        let meaning = quoted parser in
        let usage = quoted parser in
        let citation = quoted parser in
        let url = quoted parser in
        let lit_forms = parse_forms parser in
        symbol parser ';';
        push literature { lit_id; lit_name; meaning; usage; citation; url; lit_forms }; declarations ()
    | Word "retired" -> ignore (take parser);
        let retired_id = identifier parser in
        let word = quoted parser in
        let replacement = quoted parser in
        symbol parser ';';
        push retired { retired_id; word; replacement }; declarations ()
    | Word "jargon" -> ignore (take parser);
        let word = quoted parser in symbol parser ';'; push jargon word; declarations ()
    | Word "section" -> ignore (take parser); push sections (parse_section parser); declarations ()
    | Word "linked" -> ignore (take parser);
        let path = quoted parser in symbol parser ';'; push linked path; declarations ()
    | Word "diagram" -> ignore (take parser); push diagrams (parse_diagram parser); declarations ()
    | Word "north_star" -> ignore (take parser); push north_stars (parse_north_star parser); declarations ()
    | Word "document" -> ignore (take parser); push documents (parse_document parser); declarations ()
    | _ -> expected parser
        "term, literature, retired, jargon, section, linked, diagram, north_star, document, or end of input" in
  declarations ()

let nonempty context value =
  if String.trim value = "" then fail "%s must not be empty" context

let unique context values =
  let seen = Hashtbl.create 16 in
  List.iter (fun value ->
    if Hashtbl.mem seen value then fail "%s: duplicate '%s'" context value;
    Hashtbl.add seen value ()) values

let require context kind values value =
  if not (List.mem value values) then fail "%s: unknown %s '%s'" context kind value

let valid_path path =
  if path = "" || not (Filename.is_relative path) ||
    String.exists (fun character -> character = '\\' || Char.code character < 32) path ||
    List.exists (fun part -> part = "" || part = "." || part = "..") (String.split_on_char '/' path)
  then fail "invalid repository-relative path '%s'" path

let markdown_path context path =
  valid_path path;
  if not (Filename.check_suffix path ".md" || Filename.check_suffix path ".markdown") then
    fail "%s: %s must be Markdown (.md or .markdown)" path context;
  if path = ontology_path then fail "%s is reserved for the generated ontology" ontology_path;
  if path = glossary_path then fail "%s is reserved for the generated glossary" glossary_path

let validate_vocabulary (model : model) =
  let terms = List.map (fun term -> term.term_id) model.terms in
  unique "literature" (List.map (fun entry -> entry.lit_id) model.literature);
  unique "retired" (List.map (fun entry -> entry.retired_id) model.retired);
  unique "sections" (List.map (fun section -> section.section_id) model.sections);
  unique "linked documents" model.linked;
  List.iter (fun term -> List.iter (nonempty ("term " ^ term.term_id ^ " form")) term.forms) model.terms;
  List.iter (fun entry ->
    let context = "literature " ^ entry.lit_id in
    List.iter (fun (field, value) -> nonempty (context ^ " " ^ field) value)
      ["name", entry.lit_name; "meaning", entry.meaning; "usage", entry.usage; "citation", entry.citation];
    List.iter (nonempty (context ^ " form")) entry.lit_forms;
    if not (String.starts_with ~prefix:"https://" entry.url) || String.exists whitespace entry.url then
      fail "%s: url must be one https:// link" context) model.literature;
  List.iter (fun entry ->
    nonempty ("retired " ^ entry.retired_id ^ " word") entry.word;
    nonempty ("retired " ^ entry.retired_id ^ " replacement") entry.replacement) model.retired;
  List.iter (nonempty "jargon candidate") model.jargon;
  (* One surface spelling identifies one entry, so a match is never ambiguous. *)
  unique "surface spellings" (List.concat [
    List.concat_map (fun term -> term.name :: term.forms) model.terms;
    List.concat_map (fun entry -> entry.lit_name :: entry.lit_forms) model.literature;
    List.map (fun entry -> entry.word) model.retired; model.jargon ]);
  List.iter (fun section ->
    let context = "section " ^ section.section_id in
    List.iter (fun (field, value) -> nonempty (context ^ " " ^ field) value)
      ["title", section.title; "summary", section.summary; "example", section.example];
    if section.members = [] then fail "%s: at least one term is required" context;
    List.iter (require context "term" terms) section.members) model.sections;
  if model.sections <> [] then begin
    let members = List.concat_map (fun section -> section.members) model.sections in
    unique "section members" members;
    List.iter (fun term -> if not (List.mem term members) then
      fail "term %s is not in any glossary section" term) terms
  end;
  List.iter (markdown_path "linked document") model.linked

let evidence_paths milestone =
  List.filter_map (function Path path -> Some path | Pull_request _ | Issue _ -> None) milestone.evidence

let validate_north_stars (model : model) =
  let terms = List.map (fun term -> term.term_id) model.terms in
  let literature = List.map (fun entry -> entry.lit_id) model.literature in
  unique "north stars" (List.map (fun star -> star.star_id) model.north_stars);
  List.iter (fun star ->
    let context = "north_star " ^ star.star_id in
    nonempty (context ^ " goal") star.goal;
    if star.milestones = [] then fail "%s: at least one milestone is required" context;
    unique (context ^ " milestones") (List.map (fun milestone -> milestone.milestone_id) star.milestones);
    List.iter (fun milestone ->
      let context = context ^ " milestone " ^ milestone.milestone_id in
      nonempty (context ^ " statement") milestone.statement;
      if milestone.uses = [] then fail "%s: at least one used term is required" context;
      unique (context ^ " terms") milestone.uses;
      List.iter (require context "term" terms) milestone.uses;
      unique (context ^ " citations") milestone.cites;
      List.iter (require context "literature entry" literature) milestone.cites;
      List.iter (fun evidence -> match evidence with
        | Path path -> valid_path path
        | Pull_request _ | Issue _ -> ()) milestone.evidence;
      unique (context ^ " evidence") (List.map (function
        | Path path -> "path " ^ path
        | Pull_request number -> Printf.sprintf "pr %d" number
        | Issue number -> Printf.sprintf "issue %d" number) milestone.evidence);
      match milestone.status with
      | Done when evidence_paths milestone = [] ->
          fail "%s: a done milestone needs path evidence the checker can verify" context
      | In_progress when milestone.evidence = [] ->
          fail "%s: an in_progress milestone needs evidence" context
      | Done | In_progress | Not_started -> ()) star.milestones) model.north_stars

let validate (model : model) =
  validate_vocabulary model;
  validate_north_stars model;
  let terms = List.map (fun term -> term.term_id) model.terms in
  let diagrams = List.map (fun diagram -> diagram.diagram_id) model.diagrams in
  unique "terms" terms;
  unique "diagrams" diagrams;
  unique "generated blocks" (diagrams @ List.map (fun star -> star.star_id) model.north_stars);
  let blocks = diagrams @ List.map (fun star -> star.star_id) model.north_stars in
  unique "documents" (List.map (fun (document : document) -> document.path) model.documents);
  List.iter (fun term ->
    nonempty ("term " ^ term.term_id ^ " name") term.name;
    nonempty ("term " ^ term.term_id ^ " definition") term.definition) model.terms;
  List.iter (fun (diagram : diagram) ->
    let context = "diagram " ^ diagram.diagram_id in
    let nodes = List.map (fun node -> node.node_id) diagram.nodes in
    let groups = List.map (fun group -> group.group_id) diagram.groups in
    unique (context ^ " node/group identifiers") (nodes @ groups);
    if nodes = [] then fail "%s: at least one node is required" context;
    List.iter (fun (group : group) -> require context "term" terms group.term) diagram.groups;
    List.iter (fun (node : node) ->
      require context "term" terms node.term;
      Option.iter (require context "group" groups) node.group) diagram.nodes;
    List.iter (fun edge ->
      require context "edge endpoint" nodes edge.from_node;
      require context "edge endpoint" nodes edge.to_node;
      Option.iter (nonempty (context ^ " edge label")) edge.label) diagram.edges;
    unique (context ^ " edges") (List.map (fun edge ->
      edge.from_node ^ " -> " ^ edge.to_node) diagram.edges)) model.diagrams;
  List.iter (fun (document : document) ->
    markdown_path "document" document.path;
    unique ("document " ^ document.path ^ " references") document.diagrams;
    List.iter (require document.path "diagram or north star" blocks) document.diagrams) model.documents;
  if model.terms = [] || model.diagrams = [] || model.documents = [] then
    fail "at least one term, diagram, and document are required";
  model

let compile path source = parse path source |> validate

let mermaid_text value =
  let buffer = Buffer.create (String.length value) in
  String.iter (fun character ->
    if lowercase character || digit character ||
      (character >= 'A' && character <= 'Z') || character = ' ' || Char.code character >= 128 then
      Buffer.add_char buffer character
    else Buffer.add_string buffer (Printf.sprintf "#%d;" (Char.code character))) value;
  Buffer.contents buffer

let markdown_text value =
  let buffer = Buffer.create (String.length value) in
  String.iter (fun character -> Buffer.add_string buffer (match character with
    | '\n' -> "<br>"
    | '&' -> "&amp;" | '<' -> "&lt;" | '>' -> "&gt;" | '|' -> "&#124;"
    | '\\' | '`' | '*' | '_' | '[' | ']' -> "\\" ^ String.make 1 character
    | _ -> String.make 1 character)) value;
  Buffer.contents buffer

let term_name model id = (List.find (fun term -> term.term_id = id) model.terms).name
let state_name = function Existing -> "existing" | Planned -> "planned"

(* These direct Mermaid styles keep the generated diagrams legible in light
   renderers: teal existing nodes use white text, amber planned nodes use dark
   text, and indigo groups distinguish the shared process boundary. *)
let existing_class = "csf_existing"
let planned_class = "csf_planned"
let group_style = "fill:#EEF2FF,stroke:#4338CA,stroke-width:2px,color:#1E1B4B"

let state_class = function Existing -> existing_class | Planned -> planned_class
let edge_style = function
  | Existing -> "stroke:#0F766E,stroke-width:2px"
  | Planned -> "stroke:#B45309,stroke-width:2px,stroke-dasharray:5 5"

let render_palette buffer =
  Buffer.add_string buffer "  classDef csf_existing fill:#0F766E,stroke:#115E59,stroke-width:2px,color:#FFFFFF;\n";
  Buffer.add_string buffer "  classDef csf_planned fill:#FEF3C7,stroke:#B45309,stroke-width:2px,color:#78350F;\n"

let render_diagram model (diagram : diagram) =
  let buffer = Buffer.create 512 in
  let line format = Printf.ksprintf (fun value -> Buffer.add_string buffer (value ^ "\n")) format in
  line "```mermaid";
  line "%%%% Generated from %s; do not edit." source_path;
  line "%%%% Documentation model only; status labels do not establish runtime verification.";
  line "flowchart %s" diagram.direction;
  render_palette buffer;
  let node indent (node : node) =
    line "%sn_%s[\"%s (%s)\"]:::%s" indent node.node_id
      (mermaid_text (term_name model node.term)) (state_name node.state) (state_class node.state) in
  List.iter (node "  ") (List.filter (fun (node : node) -> node.group = None) diagram.nodes);
  List.iter (fun (group : group) ->
    line "  subgraph g_%s[\"%s\"]" group.group_id (mermaid_text (term_name model group.term));
    List.iter (node "    ") (List.filter (fun (node : node) -> node.group = Some group.group_id) diagram.nodes);
    line "  end";
    line "  style g_%s %s" group.group_id group_style) diagram.groups;
  List.iter (fun edge ->
    let arrow = match edge.state with Existing -> "-->" | Planned -> "-.->" in
    let label = match edge.label with None -> "" | Some value -> "|\"" ^ mermaid_text value ^ "\"|" in
    line "  n_%s %s%s n_%s" edge.from_node arrow label edge.to_node) diagram.edges;
  List.iteri (fun index edge -> line "  linkStyle %d %s" index (edge_style edge.state)) diagram.edges;
  Buffer.add_string buffer "```";
  Buffer.contents buffer

let generated_header = "<!-- Generated by csf/compiler/language; do not edit. -->"

(* Every term has the stable anchor #term-<id>; linked documents point at it. *)
let term_anchor id = "term-" ^ id
let literature_anchor id = "lit-" ^ id
let section_anchor id = "section-" ^ id

let render_ontology model =
  let rows = List.map (fun term -> Printf.sprintf "| <a id=\"%s\"></a>`%s` | %s | %s |"
    (term_anchor term.term_id) term.term_id (markdown_text term.name) (markdown_text term.definition)) model.terms in
  let retired = if model.retired = [] then [] else [
    "";
    "## Retired vocabulary";
    "";
    "These words are no longer CSF vocabulary. The documentation checker reports them in linked documents.";
    "";
    "| Retired word | Use instead |";
    "|---|---|";
  ] @ List.map (fun entry -> Printf.sprintf "| %s | %s |"
    (markdown_text entry.word) (markdown_text entry.replacement)) model.retired in
  String.concat "\n" ([
    generated_header;
    "# CSF shared vocabulary";
    "";
    "Source: [architecture.csf](architecture.csf). This dictionary defines the names used in generated diagrams.";
    "It describes a documentation model; it does not verify controller execution or deployed behavior.";
    "Each term has the stable anchor `#term-<identifier>`; linked documents point to it.";
    "";
    "| Identifier | Name | Definition |";
    "|---|---|---|";
  ] @ rows @ retired @ [""])

(* The relative Markdown link from one root-relative (or absolute) file to another. *)
let relative_link from_file target =
  let directories = match List.rev (String.split_on_char '/' from_file) with
    | _ :: parents -> List.rev parents | [] -> [] in
  let rec strip from target = match from, target with
    | x :: from', y :: (_ :: _ as target') when x = y -> strip from' target'
    | _ -> from, target in
  let up, rest = strip directories (String.split_on_char '/' target) in
  String.concat "/" (List.map (fun _ -> "..") up @ rest)

let glossary_banner =
  "Human reference — agents: do not read this file; use csf/docs/generated/ontology_cgen.md \
   (the canonical dictionary) instead."

let render_glossary model =
  let ontology = relative_link glossary_path ontology_path in
  let term id = List.find (fun term -> term.term_id = id) model.terms in
  let sections = if model.sections <> [] then model.sections else [
    { section_id = "terms"; title = "Terms"; summary = "Every term the architecture model defines.";
      example = "See the canonical dictionary."; members = List.map (fun term -> term.term_id) model.terms } ] in
  let lines = ref [] in
  let line value = lines := value :: !lines in
  List.iter line [
    generated_header;
    "> **" ^ glossary_banner ^ "**";
    "";
    "# CSF glossary";
    "";
    "A plain-language guide to the words CSF documentation uses. Part 1 groups the terms CSF defines";
    "itself; each links to its exact wording in the [canonical dictionary](" ^ ontology ^ "). Part 2 lists";
    "words CSF borrows from papers and other fields, with a citation for each. This page is generated";
    "from [architecture.csf](../csf/compiler/language/architecture.csf); change that file, not this one.";
    "";
    "## Contents";
    "";
  ];
  List.iter (fun section -> line (Printf.sprintf "- [%s](#%s)" (markdown_text section.title)
    (section_anchor section.section_id))) sections;
  if model.literature <> [] then line "- [Literature terms](#literature-terms)";
  if model.retired <> [] then line "- [Retired words](#retired-words)";
  line ""; line "## Part 1: CSF terms";
  List.iter (fun section ->
    List.iter line [ "";
      Printf.sprintf "<a id=\"%s\"></a>" (section_anchor section.section_id); "";
      "### " ^ markdown_text section.title; "";
      markdown_text section.summary; "";
      "*Example:* " ^ markdown_text section.example; "";
      "| Term | What it means |"; "|---|---|" ];
    List.iter (fun id -> let term = term id in
      line (Printf.sprintf "| **%s** ([definition](%s#%s)) | %s |" (markdown_text term.name)
        ontology (term_anchor id) (markdown_text term.definition))) section.members) sections;
  if model.literature <> [] then begin
    List.iter line [ ""; "<a id=\"literature-terms\"></a>"; ""; "## Part 2: Literature terms"; "";
      "These words come from published work. CSF uses them but does not define them; the citation is";
      "the source of the meaning given here." ];
    List.iter (fun entry -> List.iter line [ "";
      Printf.sprintf "<a id=\"%s\"></a>" (literature_anchor entry.lit_id); "";
      "### " ^ markdown_text entry.lit_name; "";
      "**Meaning.** " ^ markdown_text entry.meaning; "";
      "**How CSF uses it.** " ^ markdown_text entry.usage; "";
      "**Source.** " ^ markdown_text entry.citation ^ " <" ^ entry.url ^ ">" ]) model.literature
  end;
  if model.retired <> [] then begin
    List.iter line [ ""; "<a id=\"retired-words\"></a>"; ""; "## Retired words"; "";
      "Older documents may still use these words. They are no longer CSF vocabulary."; "";
      "| Retired word | Use instead |"; "|---|---|" ];
    List.iter (fun entry -> line (Printf.sprintf "| %s | %s |"
      (markdown_text entry.word) (markdown_text entry.replacement))) model.retired
  end;
  line "";
  String.concat "\n" (List.rev !lines)

let contains value fragment =
  let rec search index =
    index + String.length fragment <= String.length value &&
    (String.sub value index (String.length fragment) = fragment || search (index + 1)) in
  search 0

let marker kind prefix line =
  let line = String.trim line in
  let suffix = " -->" in
  if String.starts_with ~prefix line && String.ends_with ~suffix line then begin
    let length = String.length line - String.length prefix - String.length suffix in
    if length < 1 then fail "empty CSF %s marker" (Generated_block.name kind);
    let id = String.sub line (String.length prefix) length in
    if not (lowercase id.[0] && String.for_all word_char id) then
      fail "invalid CSF %s marker '%s'" (Generated_block.name kind) id;
    Some (kind, id)
  end else None

(* The opening or closing line of a generated block of any kind. *)
let block_marker ~close line =
  List.find_map (fun kind ->
    marker kind (Printf.sprintf "<!-- %scsf:%s " (if close then "/" else "") (Generated_block.name kind)) line)
    Generated_block.kinds

let opening = block_marker ~close:false
let closing = block_marker ~close:true

(* A line that starts like a block marker but names no known kind. *)
let unknown_block_marker line =
  let line = String.trim line in
  (String.starts_with ~prefix:"<!-- csf:" line || String.starts_with ~prefix:"<!-- /csf:" line) &&
  not (List.exists (fun kind ->
    let name = Generated_block.name kind in
    String.starts_with ~prefix:("<!-- csf:" ^ name ^ " ") line ||
    String.starts_with ~prefix:("<!-- /csf:" ^ name ^ " ") line) Generated_block.kinds)

let mermaid_fence line =
  let line = String.trim (String.lowercase_ascii line) in
  let length = String.length line in
  let rec prefix index =
    if index = length then index
    else if whitespace line.[index] || line.[index] = '>' then prefix (index + 1)
    else if (line.[index] = '-' || line.[index] = '+' || line.[index] = '*') &&
      index + 1 < length && whitespace line.[index + 1] then prefix (index + 2)
    else if digit line.[index] then begin
      let rec digits finish = if finish < length && digit line.[finish] then digits (finish + 1) else finish in
      let finish = digits index in
      if finish + 1 < length && (line.[finish] = '.' || line.[finish] = ')') &&
        whitespace line.[finish + 1] then prefix (finish + 2) else index
    end else index in
  let start = prefix 0 in
  if start = length || (line.[start] <> '`' && line.[start] <> '~') then false else
  let rec fence index = if index < length && line.[index] = line.[start] then fence (index + 1) else index in
  let finish = fence start in
  if finish - start < 3 then false else
  let info = String.sub line finish (length - finish) |> String.trim in
  let word = String.split_on_char ' ' (String.map (fun character -> if whitespace character then ' ' else character) info)
    |> List.hd in
  word = "mermaid" || word = "{.mermaid}"

let reject_html_mermaid path source =
  let length = String.length source in
  let rec skip_space index = if index < length && whitespace source.[index] then skip_space (index + 1) else index in
  let rec name_end index =
    if index < length && not (whitespace source.[index] || List.mem source.[index] ['='; '>'; '/'])
    then name_end (index + 1) else index in
  let rec value_end quote index =
    if index = length || (match quote with Some quote -> source.[index] = quote
      | None -> whitespace source.[index] || source.[index] = '>') then index
    else value_end quote (index + 1) in
  let reject index =
    let line = ref 1 in
    for cursor = 0 to index do if source.[cursor] = '\n' then incr line done;
    fail "%s:%d: handwritten Mermaid HTML outside a generated block" path !line in
  let rec attributes index =
    let index = skip_space index in
    if index = length then index
    else if source.[index] = '>' then index + 1
    else if source.[index] = '/' then attributes (index + 1)
    else begin
      let stop = name_end index in
      let name = String.sub source index (stop - index) |> String.lowercase_ascii in
      let after = skip_space stop in
      if after < length && source.[after] = '=' then begin
        let start = skip_space (after + 1) in
        let quote = if start < length && List.mem source.[start] ['\''; '"'] then Some source.[start] else None in
        let start = if quote = None then start else start + 1 in
        let stop = value_end quote start in
        let value = String.sub source start (stop - start) in
        let classes = String.map (fun character -> if whitespace character then ' ' else character) value
          |> String.split_on_char ' ' in
        if name = "class" && List.mem "mermaid" classes then reject index;
        attributes (if quote <> None && stop < length then stop + 1 else stop)
      end else attributes (max (index + 1) after)
    end in
  let rec comment index =
    if index + 3 > length then length
    else if String.sub source index 3 = "-->" then index + 3 else comment (index + 1) in
  let rec scan index =
    if index + 1 < length then
      if index + 4 <= length && String.sub source index 4 = "<!--" then scan (comment (index + 4))
      else if source.[index] = '<' && lowercase (Char.lowercase_ascii source.[index + 1]) then
        scan (attributes (name_end (index + 1)))
      else scan (index + 1) in
  scan 0

(* Vocabulary links.

   A linked document must link every occurrence of a defined term to its
   dictionary anchor and every literature term to its glossary entry. Matching
   is exact and case-sensitive against the display name and the declared
   forms: CSF names such as "Check" or "Context" are also ordinary English and
   Go words, so only spellings the model declares are vocabulary. A space in a
   spelling matches any run of whitespace, including a line break. Matches
   start and end at word boundaries, where letters, digits, '_', '-' and '/'
   continue a word: "agent-native" and "HTTP/MCP" contain no "agent" or "MCP".
   An inline code span counts when its whole content is a term's display name
   or an underscored term identifier.

   Not scanned for links: fenced code, headings (their anchors would change),
   raw HTML blocks, link and image text and targets, inline HTML, autolinks and
   bare URLs. Retired words are reported everywhere except fenced code, which
   shows literal identifiers, including package paths that still exist. *)

type finding_kind = Unlinked | Blocking
type finding = { offset : int; kind : finding_kind; message : string }
type replacement = { start : int; stop : int; text : string }

type entry = Term_entry of term | Literature_entry of literature | Jargon_entry

let is_word_char character =
  lowercase character || digit character || (character >= 'A' && character <= 'Z') ||
  character = '_' || character = '-' || character = '/'

(* Returns the end of [phrase] at [index], treating each space as whitespace. *)
let match_phrase source index phrase =
  let length = String.length source and size = String.length phrase in
  let rec loop index position =
    if position = size then Some index
    else if phrase.[position] = ' ' then begin
      let rec spaces cursor = if cursor < length && whitespace source.[cursor] then spaces (cursor + 1) else cursor in
      let next = spaces index in
      if next = index then None else loop next (position + 1)
    end else if index < length && source.[index] = phrase.[position] then loop (index + 1) (position + 1)
    else None in
  loop index 0

let boundary_before source index = index = 0 || not (is_word_char source.[index - 1])
let boundary_after source index = index >= String.length source || not (is_word_char source.[index])

let starts_at source index prefix =
  index + String.length prefix <= String.length source &&
  String.sub source index (String.length prefix) = prefix

let fence_start line =
  let length = String.length line in
  let rec prefix index =
    if index = length then index
    else if whitespace line.[index] || line.[index] = '>' then prefix (index + 1)
    else if (line.[index] = '-' || line.[index] = '+' || line.[index] = '*') &&
      index + 1 < length && whitespace line.[index + 1] then prefix (index + 2)
    else if digit line.[index] then begin
      let rec digits finish = if finish < length && digit line.[finish] then digits (finish + 1) else finish in
      let finish = digits index in
      if finish + 1 < length && (line.[finish] = '.' || line.[finish] = ')') &&
        whitespace line.[finish + 1] then prefix (finish + 2) else index
    end else index in
  let start = prefix 0 in
  if start = length || (line.[start] <> '`' && line.[start] <> '~') then None else
  let rec run index = if index < length && line.[index] = line.[start] then run (index + 1) else index in
  let finish = run start in
  if finish - start < 3 then None
  else Some (line.[start], finish - start, String.trim (String.sub line finish (length - finish)))

(* Blocks of consecutive non-blank lines outside fenced code, with their byte
   ranges and whether they are prose that takes links. *)
let blocks source =
  let length = String.length source in
  let result = ref [] in
  let emit start stop prose = if stop > start then result := (start, stop, prose) :: !result in
  let rec lines offset fence block =
    if offset >= length then
      Option.iter (fun (start, stop) -> emit start stop true) block
    else begin
      let stop = match String.index_from_opt source offset '\n' with Some index -> index | None -> length in
      let line = String.sub source offset (stop - offset) in
      let next = stop + 1 in
      let flush () = Option.iter (fun (start, finish) -> emit start finish true) block in
      match fence with
      | Some (character, count) ->
          let closed = match fence_start line with
            | Some (closing, run, info) -> closing = character && run >= count && info = ""
            | None -> false in
          lines next (if closed then None else fence) None
      | None ->
          let trimmed = String.trim line in
          match fence_start line with
          | Some (character, count, _) -> flush (); lines next (Some (character, count)) None
          | None when trimmed = "" -> flush (); lines next None None
          | None when trimmed.[0] = '#' -> flush (); emit offset stop false; lines next None None
          | None -> lines next None (Some (match block with Some (start, _) -> (start, stop) | None -> (offset, stop)))
    end in
  lines 0 None None;
  List.rev_map (fun (start, stop, prose) ->
    let first = String.trim (String.sub source start (min (stop - start) 256)) in
    (start, stop, prose && not (String.starts_with ~prefix:"<" first))) !result

let line_of source offset =
  let line = ref 1 in
  for index = 0 to min offset (String.length source) - 1 do
    if source.[index] = '\n' then incr line done;
  !line

(* The end of a link or image starting with '[' at [index], and its target. *)
let link_end source index =
  let length = String.length source in
  let rec closing index depth opening closing_character =
    if index >= length then None
    else match source.[index] with
      | '\\' -> closing (index + 2) depth opening closing_character
      | character when character = opening -> closing (index + 1) (depth + 1) opening closing_character
      | character when character = closing_character ->
          if depth = 0 then Some index else closing (index + 1) (depth - 1) opening closing_character
      | _ -> closing (index + 1) depth opening closing_character in
  match closing (index + 1) 0 '[' ']' with
  | Some text_end when text_end + 1 < length && source.[text_end + 1] = '(' ->
      Option.map (fun target_end ->
        let target = String.sub source (text_end + 2) (target_end - text_end - 2) |> String.trim in
        let target = match String.index_opt target ' ' with Some cut -> String.sub target 0 cut | None -> target in
        (target_end + 1, Some target)) (closing (text_end + 2) 0 '(' ')')
  | Some text_end when text_end + 1 < length && source.[text_end + 1] = '[' ->
      Option.map (fun stop -> (stop + 1, None)) (closing (text_end + 2) 0 '[' ']')
  | _ -> None

let vocabulary model =
  let spellings = List.concat [
    List.concat_map (fun term -> List.map (fun form -> form, Term_entry term) (term.name :: term.forms)) model.terms;
    List.concat_map (fun entry -> List.map (fun form -> form, Literature_entry entry)
      (entry.lit_name :: entry.lit_forms)) model.literature;
    List.map (fun word -> word, Jargon_entry) model.jargon ] in
  List.stable_sort (fun (left, _) (right, _) -> compare (String.length right) (String.length left)) spellings

(* Scan one Markdown file. [ontology] and [glossary] are the relative links
   from that file to the generated dictionary and glossary. *)
let scan_vocabulary model ~ontology ~glossary source =
  let spellings = vocabulary model in
  let findings = ref [] and replacements = ref [] in
  let report offset kind format = Printf.ksprintf (fun message ->
    findings := { offset; kind; message } :: !findings) format in
  let replace start stop text = replacements := { start; stop; text } :: !replacements in
  let term_link term = ontology ^ "#" ^ term_anchor term.term_id in
  let literature_link entry = glossary ^ "#" ^ literature_anchor entry.lit_id in
  let check_target offset target =
    let path, anchor = match String.index_opt target '#' with
      | Some cut -> String.sub target 0 cut, Some (String.sub target (cut + 1) (String.length target - cut - 1))
      | None -> target, None in
    let known prefix ids anchor = String.starts_with ~prefix anchor &&
      List.mem (String.sub anchor (String.length prefix) (String.length anchor - String.length prefix)) ids in
    let term_ids = List.map (fun term -> term.term_id) model.terms in
    let literature_ids = List.map (fun entry -> entry.lit_id) model.literature in
    match anchor with
    | Some anchor when Filename.basename path = Filename.basename ontology_path ->
        if not (known "term-" term_ids anchor) then report offset Blocking "link to an undefined ontology term '#%s'" anchor
        else if path <> ontology then report offset Blocking "ontology link path '%s' should be '%s'" path ontology
    | Some anchor when Filename.basename path = Filename.basename glossary_path &&
        String.starts_with ~prefix:"lit-" anchor ->
        if not (known "lit-" literature_ids anchor) then report offset Blocking "link to an undeclared literature term '#%s'" anchor
        else if path <> glossary then report offset Blocking "glossary link path '%s' should be '%s'" path glossary
    | _ -> () in
  let retired_scan start stop =
    for index = start to stop - 1 do
      if boundary_before source index then
        List.iter (fun entry -> match match_phrase source index entry.word with
          | Some finish when finish <= stop &&
              (boundary_after source finish || (source.[finish] = 's' && boundary_after source (finish + 1))) ->
              report index Blocking "retired word '%s': %s" entry.word entry.replacement
          | _ -> ()) model.retired
    done in
  (* A bare identifier such as `config` is usually a package name; only
     underscored identifiers are unambiguous in code spans. *)
  let code_term content = List.find_opt (fun term -> term.name = content ||
    (term.term_id = content && String.contains content '_')) model.terms in
  let rec scan index stop =
    if index < stop then
      let character = source.[index] in
      if character = '\\' then scan (index + 2) stop
      else if character = '`' then begin
        let rec run cursor = if cursor < stop && source.[cursor] = '`' then run (cursor + 1) else cursor in
        let opening_end = run index in
        let size = opening_end - index in
        let rec closing cursor =
          if cursor >= stop then None
          else if source.[cursor] = '`' then
            let finish = run cursor in if finish - cursor = size then Some cursor else closing finish
          else closing (cursor + 1) in
        match closing opening_end with
        | None -> scan opening_end stop
        | Some close ->
            let content = String.sub source opening_end (close - opening_end) |> String.trim in
            Option.iter (fun term ->
              report index Unlinked "unlinked ontology term '%s' (term %s)" content term.term_id;
              replace index (close + size)
                (Printf.sprintf "[%s](%s)" (String.sub source index (close + size - index)) (term_link term)))
              (code_term content);
            scan (close + size) stop
      end
      else if character = '[' || (character = '!' && index + 1 < stop && source.[index + 1] = '[') then begin
        let opening = if character = '!' then index + 1 else index in
        match link_end source opening with
        | Some (finish, target) when finish <= stop ->
            Option.iter (check_target index) target; scan finish stop
        | _ -> scan (opening + 1) stop
      end
      else if character = '<' && index + 1 < stop &&
        (is_word_char source.[index + 1] || source.[index + 1] = '!') then begin
        match String.index_from_opt source index '>' with
        | Some close when close < stop -> scan (close + 1) stop
        | _ -> scan (index + 1) stop
      end else if starts_at source index "http://" || starts_at source index "https://" then begin
        let rec url cursor = if cursor < stop && not (whitespace source.[cursor]) &&
          source.[cursor] <> ')' && source.[cursor] <> '>' then url (cursor + 1) else cursor in
        scan (url index) stop
      end
      else if boundary_before source index then begin
        let rec first = function
          | [] -> None
          | (spelling, entry) :: remaining ->
              if spelling.[0] <> character then first remaining else
              match match_phrase source index spelling with
              | Some finish when finish <= stop && boundary_after source finish -> Some (finish, spelling, entry)
              | _ -> first remaining in
        match first spellings with
        | None -> scan (index + 1) stop
        | Some (finish, spelling, entry) ->
            let text = String.sub source index (finish - index) in
            (match entry with
            | Term_entry term ->
                report index Unlinked "unlinked ontology term '%s' (term %s)" spelling term.term_id;
                replace index finish (Printf.sprintf "[%s](%s)" text (term_link term))
            | Literature_entry entry ->
                report index Unlinked "unlinked literature term '%s' (literature %s)" spelling entry.lit_id;
                replace index finish (Printf.sprintf "[%s](%s)" text (literature_link entry))
            | Jargon_entry ->
                report index Blocking "undeclared jargon '%s': declare it as a term or literature entry" spelling);
            scan finish stop
      end else scan (index + 1) stop in
  List.iter (fun (start, stop, prose) ->
    retired_scan start stop;
    if prose then scan start stop) (blocks source);
  let linked = List.fold_left (fun text { start; stop; text = replacement } ->
    String.sub text 0 start ^ replacement ^ String.sub text stop (String.length text - stop))
    source (List.sort (fun left right -> compare right.start left.start) !replacements) in
  let findings = List.sort_uniq (fun left right -> compare (left.offset, left.message) (right.offset, right.message)) !findings in
  linked, List.map (fun finding -> { finding with offset = line_of source finding.offset }) findings

let format_findings path findings =
  List.map (fun finding -> Printf.sprintf "%s:%d: %s" path finding.offset finding.message) findings

let status_name = function Done -> "done" | In_progress -> "in progress" | Not_started -> "planned"

(* A north star renders as Markdown whose ontology terms, literature and
   evidence are links relative to [path], the document that holds it. The
   vocabulary scan runs over the rendered text, so prose in a goal or statement
   is linked exactly as the document's own prose is, and a retired word or
   undeclared jargon in the source fails generation. *)
let render_north_star model ~path star =
  let ontology = relative_link path ontology_path and glossary = relative_link path glossary_path in
  let term id = Printf.sprintf "[%s](%s#%s)" (markdown_text (term_name model id)) ontology (term_anchor id) in
  let cite id =
    let entry = List.find (fun entry -> entry.lit_id = id) model.literature in
    Printf.sprintf "[%s](%s) ([glossary](%s#%s))" (markdown_text entry.lit_name) entry.url
      glossary (literature_anchor id) in
  let evidence = function
    | Path target -> Printf.sprintf "[`%s`](%s)" target (relative_link path target)
    | Pull_request number -> Printf.sprintf "PR #%d" number
    | Issue number -> Printf.sprintf "issue #%d" number in
  let count status = List.length (List.filter (fun milestone -> milestone.status = status) star.milestones) in
  let joined render = function [] -> "—" | values -> String.concat ", " (List.map render values) in
  let rows = List.mapi (fun index milestone -> Printf.sprintf "| %d | %s | %s | %s | %s | %s |" (index + 1)
    (markdown_text milestone.statement) (status_name milestone.status) (joined term milestone.uses)
    (joined evidence milestone.evidence) (joined cite milestone.cites)) star.milestones in
  let text = String.concat "\n" ([
    "## North star";
    "";
    Printf.sprintf "*Generated from [architecture.csf](%s); change that file, not this section.*"
      (relative_link path source_path);
    "";
    "**Goal.** " ^ markdown_text star.goal;
    "";
    Printf.sprintf "**Milestones.** %d done, %d in progress, %d planned. A done milestone names a \
      path in this repository that the generator checks exists; PR and issue numbers refer to the \
      source monorepo." (count Done) (count In_progress) (count Not_started);
    "";
    "| # | Milestone | Status | Terms | Evidence | Builds on |";
    "|---|---|---|---|---|---|";
  ] @ rows @ [""]) in
  let linked, findings = scan_vocabulary model ~ontology ~glossary text in
  List.iter (fun (finding : finding) -> if finding.kind = Blocking then
    fail "north_star %s: %s" star.star_id finding.message) findings;
  linked

(* The kind of block a declared identifier renders. *)
let block_kind model id =
  if List.exists (fun diagram -> diagram.diagram_id = id) model.diagrams then Generated_block.Diagram
  else Generated_block.North_star

let replace_document model (document : document) source =
  let seen = Hashtbl.create 8 in
  let outside = Buffer.create (String.length source) in
  let rendered (kind, id) =
    let name = Generated_block.name kind in
    if not (List.mem id document.diagrams) then fail "%s: undeclared %s marker '%s'" document.path name id;
    if block_kind model id <> kind then
      fail "%s: '%s' is a %s, not a %s" document.path id (Generated_block.name (block_kind model id)) name;
    if Hashtbl.mem seen id then fail "%s: repeated %s marker '%s'" document.path name id;
    Hashtbl.add seen id ();
    match kind with
    | Generated_block.Diagram ->
        List.find (fun diagram -> diagram.diagram_id = id) model.diagrams |> render_diagram model
    | Generated_block.North_star ->
        List.find (fun star -> star.star_id = id) model.north_stars
        |> render_north_star model ~path:document.path in
  let rec lines active output number = function
    | [] ->
        Option.iter (fun (_, id) -> fail "%s: missing closing marker for '%s'" document.path id) active;
        List.iter (fun id -> if not (Hashtbl.mem seen id) then
          fail "%s: missing %s marker '%s'" document.path (Generated_block.name (block_kind model id)) id)
          document.diagrams;
        reject_html_mermaid document.path (Buffer.contents outside);
        String.concat "\n" (List.rev output)
    | line :: remaining ->
        if active = None then Buffer.add_string outside line;
        Buffer.add_char outside '\n';
        let open_marker = opening line and close_marker = closing line in
        begin match active, open_marker, close_marker with
        | None, Some block, None ->
            let content = rendered block in
            lines (Some block) (content :: line :: output) (number + 1) remaining
        | Some block, None, Some ended when block = ended ->
            lines None (line :: output) (number + 1) remaining
        | _, Some (kind, _), _ | _, _, Some (kind, _) ->
            fail "%s:%d: nested, unmatched, or mismatched %s marker" document.path number (Generated_block.name kind)
        | _, None, None ->
            List.iter (fun kind -> let name = Generated_block.name kind in
              if contains line ("<!-- csf:" ^ name) || contains line ("<!-- /csf:" ^ name) then
                fail "%s:%d: malformed %s marker; markers must occupy their own lines" document.path number name)
              Generated_block.kinds;
            if unknown_block_marker line then
              fail "%s:%d: unknown generated block kind; known kinds: %s" document.path number
                (String.concat ", " (List.map Generated_block.name Generated_block.kinds));
            match active with
            | Some _ -> lines active output (number + 1) remaining
            | None ->
                if mermaid_fence line then fail "%s:%d: handwritten Mermaid outside a generated block" document.path number;
                lines None (line :: output) (number + 1) remaining
        end in
  lines None [] 1 (String.split_on_char '\n' source)

let read_file path = In_channel.with_open_bin path In_channel.input_all

(* Check every component, including parents: document declarations cannot redirect
   writes outside the checkout through a symlink. The operator owns ROOT. *)
let checked_path ?(directory = false) root relative allow_missing =
  valid_path relative;
  let rec walk parent = function
    | [] -> parent
    | component :: remaining ->
        let path = Filename.concat parent component in
        let stats = try Some (Unix.lstat path) with
          | Unix.Unix_error (Unix.ENOENT, _, _) when remaining = [] && allow_missing -> None in
        begin match stats with
        | Some stats when stats.Unix.st_kind = Unix.S_LNK -> fail "%s: symlinks are not allowed" relative
        | Some stats when remaining <> [] && stats.Unix.st_kind <> Unix.S_DIR -> fail "%s: parent is not a directory" relative
        | Some stats when remaining = [] && stats.Unix.st_kind <> Unix.S_REG &&
            not (directory && stats.Unix.st_kind = Unix.S_DIR) -> fail "%s: expected a regular file" relative
        | _ -> ()
        end;
        walk path remaining in
  walk root (String.split_on_char '/' relative)

type mode = Write | Check
type output = { relative : string; absolute : string; current : string option; generated : string }

let load_model root = checked_path root source_path false |> read_file |> compile source_path

(* Every path a milestone cites as evidence names an existing file or
   directory under ROOT, reached without symlinks. *)
let check_evidence root model =
  List.iter (fun star -> List.iter (fun milestone -> List.iter (fun path ->
    try ignore (checked_path ~directory:true root path false)
    with Unix.Unix_error (Unix.ENOENT, _, _) ->
      fail "north_star %s milestone %s: evidence path '%s' does not exist"
        star.star_id milestone.milestone_id path) (evidence_paths milestone)) star.milestones)
    model.north_stars

(* Outputs plus vocabulary findings for every linked document. Findings use
   [offset] as a line number. *)
let prepare root =
  let model = load_model root in
  check_evidence root model;
  let paths = List.map (fun (document : document) -> document.path) model.documents in
  let paths = paths @ List.filter (fun path -> not (List.mem path paths)) model.linked in
  let findings = ref [] in
  let documents = List.map (fun path ->
    let absolute = checked_path root path false in
    let current = read_file absolute in
    let generated = match List.find_opt (fun (document : document) -> document.path = path) model.documents with
      | Some document -> replace_document model document current
      | None -> current in
    let generated = if not (List.mem path model.linked) then generated else begin
      let linked, found = scan_vocabulary model ~ontology:(relative_link path ontology_path)
        ~glossary:(relative_link path glossary_path) generated in
      findings := !findings @ List.map (fun finding -> path, finding) found;
      linked
    end in
    { relative = path; absolute; current = Some current; generated }) paths in
  let generated relative generated =
    let absolute = checked_path root relative true in
    let current = if Sys.file_exists absolute then Some (read_file absolute) else None in
    { relative; absolute; current; generated } in
  generated ontology_path (render_ontology model) :: generated glossary_path (render_glossary model) ::
  generated Generated_block.python_path (Generated_block.render_python ()) :: documents,
  !findings

let write_output output =
  let permissions = match output.current with
    | None -> 0o644 | Some _ -> (Unix.stat output.absolute).Unix.st_perm in
  let temporary, channel = Filename.open_temp_file ~temp_dir:(Filename.dirname output.absolute) ".csf-" ".tmp" in
  Fun.protect ~finally:(fun () -> close_out_noerr channel; if Sys.file_exists temporary then Sys.remove temporary)
    (fun () -> output_string channel output.generated; close_out channel;
      Unix.chmod temporary permissions; Unix.rename temporary output.absolute)

let fail_findings findings =
  if findings <> [] then fail "vocabulary findings:\n%s" (String.concat "\n"
    (List.concat_map (fun (path, finding) -> format_findings path [finding]) findings))

let run root mode =
  let outputs, findings = prepare (Unix.realpath root) in
  fail_findings (if mode = Check then findings
    else List.filter (fun (_, finding) -> finding.kind = Blocking) findings);
  let changed = List.filter (fun output -> output.current <> Some output.generated) outputs in
  if mode = Check && changed <> [] then fail "generated documentation differs: %s"
    (String.concat ", " (List.map (fun output -> output.relative) changed));
  if mode = Write then List.iter write_output changed;
  List.map (fun output -> output.relative) changed

(* Report vocabulary findings for any Markdown files, linked or not, without
   writing. Links are computed from each file's real location. *)
let lint root paths =
  let root = Unix.realpath root in
  let model = load_model root in
  List.concat_map (fun path ->
    let absolute = Unix.realpath path in
    let _, findings = scan_vocabulary model ~ontology:(relative_link absolute (Filename.concat root ontology_path))
      ~glossary:(relative_link absolute (Filename.concat root glossary_path)) (read_file absolute) in
    format_findings path findings) paths
