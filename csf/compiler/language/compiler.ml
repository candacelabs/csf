exception Error of string

let fail format = Printf.ksprintf (fun message -> raise (Error message)) format
let source_path = "csf/compiler/language/architecture.csf"
let ontology_path = "csf/docs/generated/ontology_cgen.md"

type token_kind = Word of string | Quoted of string | Symbol of char | End
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

type state = Existing | Planned
type term = { term_id : string; name : string; definition : string }
type node = { node_id : string; term : string; state : state; group : string option }
type group = { group_id : string; term : string }
type edge = { from_node : string; to_node : string; state : state; label : string option }
type diagram = { diagram_id : string; direction : string; nodes : node list;
  groups : group list; edges : edge list }
type document = { path : string; diagrams : string list }
type model = { terms : term list; diagrams : diagram list; documents : document list }

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

let parse path source =
  let parser = { path; tokens = lex path source; next = 0 } in
  let rec declarations terms diagrams documents = match (peek parser).kind with
    | End -> { terms = List.rev terms; diagrams = List.rev diagrams;
        documents = List.rev documents }
    | Word "term" -> ignore (take parser);
        let term_id = identifier parser in
        let name = quoted parser in
        let definition = quoted parser in
        symbol parser ';';
        declarations ({ term_id; name; definition } :: terms) diagrams documents
    | Word "diagram" -> ignore (take parser);
        let diagram = parse_diagram parser in declarations terms (diagram :: diagrams) documents
    | Word "document" -> ignore (take parser);
        let document = parse_document parser in declarations terms diagrams (document :: documents)
    | _ -> expected parser "term, diagram, document, or end of input" in
  declarations [] [] []

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

let validate (model : model) =
  let terms = List.map (fun term -> term.term_id) model.terms in
  let diagrams = List.map (fun diagram -> diagram.diagram_id) model.diagrams in
  unique "terms" terms;
  unique "diagrams" diagrams;
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
    valid_path document.path;
    if not (Filename.check_suffix document.path ".md" || Filename.check_suffix document.path ".markdown") then
      fail "%s: document must be Markdown (.md or .markdown)" document.path;
    if document.path = ontology_path then fail "%s is reserved for the generated ontology" ontology_path;
    unique ("document " ^ document.path ^ " references") document.diagrams;
    List.iter (require document.path "diagram" diagrams) document.diagrams) model.documents;
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

let render_diagram model (diagram : diagram) =
  let buffer = Buffer.create 512 in
  let line format = Printf.ksprintf (fun value -> Buffer.add_string buffer (value ^ "\n")) format in
  line "```mermaid";
  line "%%%% Generated from %s; do not edit." source_path;
  line "%%%% Documentation model only; status labels do not establish runtime verification.";
  line "flowchart %s" diagram.direction;
  let node indent (node : node) =
    line "%sn_%s[\"%s (%s)\"]" indent node.node_id
      (mermaid_text (term_name model node.term)) (state_name node.state) in
  List.iter (node "  ") (List.filter (fun (node : node) -> node.group = None) diagram.nodes);
  List.iter (fun (group : group) ->
    line "  subgraph g_%s[\"%s\"]" group.group_id (mermaid_text (term_name model group.term));
    List.iter (node "    ") (List.filter (fun (node : node) -> node.group = Some group.group_id) diagram.nodes);
    line "  end") diagram.groups;
  List.iter (fun edge ->
    let arrow = match edge.state with Existing -> "-->" | Planned -> "-.->" in
    let label = match edge.label with None -> "" | Some value -> "|\"" ^ mermaid_text value ^ "\"|" in
    line "  n_%s %s%s n_%s" edge.from_node arrow label edge.to_node) diagram.edges;
  Buffer.add_string buffer "```";
  Buffer.contents buffer

let render_ontology model =
  let rows = List.map (fun term -> Printf.sprintf "| `%s` | %s | %s |"
    term.term_id (markdown_text term.name) (markdown_text term.definition)) model.terms in
  String.concat "\n" ([
    "<!-- Generated by csf/compiler/language; do not edit. -->";
    "# CSF shared vocabulary";
    "";
    "Source: [architecture.csf](architecture.csf). This dictionary defines the names used in generated diagrams.";
    "It describes a documentation model; it does not verify controller execution or deployed behavior.";
    "";
    "| Identifier | Name | Definition |";
    "|---|---|---|";
  ] @ rows @ [""])

let contains value fragment =
  let rec search index =
    index + String.length fragment <= String.length value &&
    (String.sub value index (String.length fragment) = fragment || search (index + 1)) in
  search 0

let marker prefix line =
  let line = String.trim line in
  let suffix = " -->" in
  if String.starts_with ~prefix line && String.ends_with ~suffix line then begin
    let length = String.length line - String.length prefix - String.length suffix in
    if length < 1 then fail "empty CSF diagram marker";
    let id = String.sub line (String.length prefix) length in
    if not (lowercase id.[0] && String.for_all word_char id) then fail "invalid CSF diagram marker '%s'" id;
    Some id
  end else None

let opening = marker "<!-- csf:diagram "
let closing = marker "<!-- /csf:diagram "

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

let replace_document model (document : document) source =
  let seen = Hashtbl.create 8 in
  let outside = Buffer.create (String.length source) in
  let rendered id =
    if not (List.mem id document.diagrams) then fail "%s: undeclared diagram marker '%s'" document.path id;
    if Hashtbl.mem seen id then fail "%s: repeated diagram marker '%s'" document.path id;
    Hashtbl.add seen id ();
    List.find (fun diagram -> diagram.diagram_id = id) model.diagrams |> render_diagram model in
  let rec lines active output number = function
    | [] ->
        Option.iter (fun id -> fail "%s: missing closing marker for '%s'" document.path id) active;
        List.iter (fun id -> if not (Hashtbl.mem seen id) then
          fail "%s: missing diagram marker '%s'" document.path id) document.diagrams;
        reject_html_mermaid document.path (Buffer.contents outside);
        String.concat "\n" (List.rev output)
    | line :: remaining ->
        if active = None then Buffer.add_string outside line;
        Buffer.add_char outside '\n';
        let open_id = opening line and close_id = closing line in
        begin match active, open_id, close_id with
        | None, Some id, None ->
            let content = rendered id in
            lines (Some id) (content :: line :: output) (number + 1) remaining
        | Some id, None, Some ended when id = ended ->
            lines None (line :: output) (number + 1) remaining
        | _, Some _, _ | _, _, Some _ -> fail "%s:%d: nested, unmatched, or mismatched diagram marker" document.path number
        | _, None, None ->
            if contains line "<!-- csf:diagram" || contains line "<!-- /csf:diagram" then
              fail "%s:%d: malformed diagram marker; markers must occupy their own lines" document.path number;
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
let checked_path root relative allow_missing =
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
        | Some stats when remaining = [] && stats.Unix.st_kind <> Unix.S_REG -> fail "%s: expected a regular file" relative
        | _ -> ()
        end;
        walk path remaining in
  walk root (String.split_on_char '/' relative)

type mode = Write | Check
type output = { relative : string; absolute : string; current : string option; generated : string }

let prepare root =
  let source = checked_path root source_path false |> read_file in
  let model = compile source_path source in
  let documents = List.map (fun (document : document) ->
    let absolute = checked_path root document.path false in
    let current = read_file absolute in
    { relative = document.path; absolute; current = Some current;
      generated = replace_document model document current }) model.documents in
  let absolute = checked_path root ontology_path true in
  let current = if Sys.file_exists absolute then Some (read_file absolute) else None in
  { relative = ontology_path; absolute; current; generated = render_ontology model } :: documents

let write_output output =
  let permissions = match output.current with
    | None -> 0o644 | Some _ -> (Unix.stat output.absolute).Unix.st_perm in
  let temporary, channel = Filename.open_temp_file ~temp_dir:(Filename.dirname output.absolute) ".csf-" ".tmp" in
  Fun.protect ~finally:(fun () -> close_out_noerr channel; if Sys.file_exists temporary then Sys.remove temporary)
    (fun () -> output_string channel output.generated; close_out channel;
      Unix.chmod temporary permissions; Unix.rename temporary output.absolute)

let run root mode =
  let outputs = prepare (Unix.realpath root) in
  let changed = List.filter (fun output -> output.current <> Some output.generated) outputs in
  if mode = Check && changed <> [] then fail "generated documentation differs: %s"
    (String.concat ", " (List.map (fun output -> output.relative) changed));
  if mode = Write then List.iter write_output changed;
  List.map (fun output -> output.relative) changed
