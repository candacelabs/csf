(* Pure projections of a resolved declaration graph. These functions return
   strings; Compiler owns file reads/writes and drift checks. Stable input list order and
   retained locations make output deterministic without claiming observed state. *)

(* [%S] writes an OCaml string literal with escapes, so a path containing a
   quote or newline remains data in the generated source. *)
let quoted = Printf.sprintf "%S"
let optional encode = function None -> "None" | Some value -> "Some (" ^ encode value ^ ")"
let sequence encode values = "[" ^ String.concat "; " (List.map encode values) ^ "]"
let record fields =
  "{ " ^ String.concat "; " (List.map (fun (name, value) -> name ^ " = " ^ value) fields) ^ " }"

(* Generated inverse mappings are the vocabulary owner for both OCaml variant
   names and display spellings. Record field labels below belong to Model's
   destination types; the emitted module is compiled against those types. *)
let constructor encode value = "Model." ^ Syntax_cgen.Terminal.constructor_name (encode value)
let spelling encode value = Syntax_cgen.Terminal.name (encode value)
let verification = function
  | Model.Pending -> "Model.Pending"
  | Model.Test_reference path -> "Model.Test_reference " ^ quoted path

let location (at : Model.location) = record [
  "Model.file", quoted at.file; "line", string_of_int at.line; "column", string_of_int at.column;
]

let process (value : Model.process) = record [
  "Model.process_id", quoted value.process_id; "kind", constructor Syntax_cgen.process_kind_terminal value.kind;
  "entrypoint", optional quoted value.entrypoint; "process_at", location value.process_at;
]

let scope (value : Model.scope) = record [
  "Model.scope_id", quoted value.scope_id; "parent", quoted value.parent;
  "scope_at", location value.scope_at;
]

let component (value : Model.component) = record [
  "Model.component_id", quoted value.component_id; "role", constructor Syntax_cgen.role_terminal value.role;
  "process", quoted value.process; "scope", quoted value.scope;
  "source", optional quoted value.source; "state", constructor Syntax_cgen.state_terminal value.state;
  "lifecycle", constructor Syntax_cgen.lifecycle_terminal value.lifecycle;
  "verification", verification value.verification; "component_at", location value.component_at;
]

let dependency (value : Model.dependency) = record [
  "Model.consumer", quoted value.consumer; "provider", quoted value.provider;
  "dependency_at", location value.dependency_at;
]

let connection (value : Model.connection) = record [
  "Model.caller", quoted value.caller; "callee", quoted value.callee;
  "transport", constructor Syntax_cgen.transport_terminal value.transport; "boundary", optional quoted value.boundary;
  "connection_state", constructor Syntax_cgen.state_terminal value.connection_state;
  "connection_at", location value.connection_at;
]

let source_root (value : Model.source_root) = record [
  "Model.path", quoted value.path; "path_at", location value.path_at;
]

(** Emit a reconstructible [Model.architecture] value, preserving declarations
    and their locations. Resolution caches, observations and executed lifecycle
    hooks are not serialized. A consumer resolves this value again before use. *)
let ocaml (resolved : Model.resolved) =
  let value = resolved.architecture in
  let fields = [
    "name", quoted value.name; "version", string_of_int value.version; "at", location value.at;
    "processes", sequence process value.processes; "scopes", sequence scope value.scopes;
    "components", sequence component value.components;
    "dependencies", sequence dependency value.dependencies;
    "connections", sequence connection value.connections;
    "scan_roots", sequence source_root value.scan_roots;
    "generated_roots", sequence source_root value.generated_roots;
  ] in
  Codegen_header.render Codegen_header.Ocaml ^
  "(* Declared architecture; this value does not attest to execution behavior. *)\n" ^
  "let architecture : Model.architecture = {\n" ^
  String.concat "" (List.map (fun (name, value) -> "  " ^ name ^ " = " ^ value ^ ";\n") fields) ^
  "}\n"

(* Mermaid decimal entities keep declaration text out of diagram syntax.
   The only raw markup is the renderer-owned line break. *)
let escaped entity text =
  let output = Buffer.create (String.length text) in
  String.iter (fun character -> match character with
    | 'a'..'z' | 'A'..'Z' | '0'..'9' | ' ' -> Buffer.add_char output character
    | '\n' -> Buffer.add_string output "<br/>"
    | character when Char.code character >= 128 -> Buffer.add_char output character
    | character -> Buffer.add_string output (entity (Char.code character))) text;
  Buffer.contents output

let diagram_label text = "\"" ^ escaped (Printf.sprintf "#%d;") text ^ "\""
let markdown_cell = escaped (Printf.sprintf "&#%d;")
let with_optional prefix = function None -> "" | Some value -> "\n" ^ prefix ^ value
(* Use local identifiers such as c0 in diagram syntax and keep user identifiers
   in escaped labels. These IDs are stable for the same declaration order, not
   permanent identities across insertions or reordering. *)
let numbered prefix values key =
  List.mapi (fun index value -> key value, prefix ^ string_of_int index) values

let component_label (value : Model.component) =
  value.component_id ^ "\n" ^ spelling Syntax_cgen.role_terminal value.role ^ " | " ^
  spelling Syntax_cgen.state_terminal value.state ^ " | " ^ spelling Syntax_cgen.lifecycle_terminal value.lifecycle ^
  with_optional "source: " value.source

let process_label (value : Model.process) =
  "process " ^ value.process_id ^ "\nkind: " ^ spelling Syntax_cgen.process_kind_terminal value.kind ^
  with_optional "entrypoint: " value.entrypoint

let connection_label (value : Model.connection) =
  spelling Syntax_cgen.transport_terminal value.transport ^ " | " ^
  spelling Syntax_cgen.state_terminal value.connection_state ^
  with_optional "boundary: " value.boundary

(** Render process and scope containment from the validated graph. Subgraphs
    mean declared lifetimes, not package layout, core affinity or live processes.
    Dashed arrows show consumer-to-provider dependencies; solid arrows show
    connections whose declared transport and state remain explicit in labels. *)
let mermaid (resolved : Model.resolved) =
  let architecture = resolved.architecture in
  let process_ids = numbered "p" architecture.processes (fun (p : Model.process) -> p.process_id) in
  let scope_ids = numbered "s" resolved.scopes (fun (s : Model.resolved_scope) -> s.scope.scope_id) in
  let component_ids = numbered "c" resolved.components
    (fun (c : Model.resolved_component) -> c.component.component_id) in
  let output = Buffer.create 2048 in
  let line depth value = Buffer.add_string output (String.make (2 * depth) ' ' ^ value ^ "\n") in
  let node depth (value : Model.resolved_component) =
    line depth (List.assoc value.component.component_id component_ids ^
      "[" ^ diagram_label (component_label value.component) ^ "]") in
  let rec scopes depth parent =
    resolved.scopes |> List.iter (fun (value : Model.resolved_scope) ->
      if value.scope.parent = parent then begin
        line depth ("subgraph " ^ List.assoc value.scope.scope_id scope_ids ^
          "[" ^ diagram_label ("scope " ^ value.scope.scope_id) ^ "]");
        resolved.components |> List.iter (fun (component : Model.resolved_component) ->
          if component.lifetime.scope.scope_id = value.scope.scope_id then node (depth + 1) component);
        scopes (depth + 1) value.scope.scope_id;
        line depth "end"
      end) in
  Buffer.add_string output (Codegen_header.render Codegen_header.Mermaid);
  line 0 "flowchart TB";
  line 1 "%% Declared architecture, not observed running state.";
  architecture.processes |> List.iter (fun (value : Model.process) ->
    line 1 ("subgraph " ^ List.assoc value.process_id process_ids ^
      "[" ^ diagram_label (process_label value) ^ "]");
    scopes 2 value.process_id;
    line 1 "end");
  architecture.dependencies |> List.iter (fun (value : Model.dependency) ->
    line 1 (List.assoc value.consumer component_ids ^ " -.->|" ^ diagram_label "requires" ^ "| " ^
      List.assoc value.provider component_ids));
  resolved.connections |> List.iter (fun (value : Model.resolved_connection) ->
    line 1 (List.assoc value.source.component.component_id component_ids ^ " -->|" ^
      diagram_label (connection_label value.connection) ^ "| " ^
      List.assoc value.target.component.component_id component_ids));
  Buffer.contents output

let row cells = "| " ^ String.concat " | " (List.map markdown_cell cells) ^ " |\n"
let table headings rows =
  row headings ^ "| " ^ String.concat " | " (List.map (fun _ -> "---") headings) ^ " |\n" ^
  String.concat "" (List.map row rows)

(* An evidence path stays a reference in the human view. Rendering it must not
   change pending work into a success claim just because a path was supplied. *)
let evidence = function
  | None -> "Pending; no evidence reference supplied"
  | Some reference -> "Reference only; not executed: " ^ reference

let verification_row (value : Model.component) =
  let status = match value.verification with
    | Model.Pending -> "Pending"
    | Model.Test_reference reference -> "Test reference only; not executed: " ^ reference in
  [value.component_id; status]

let order values = match values with
  | [] -> "None declared"
  | _ -> String.concat " -> "
      (List.map (fun (value : Model.resolved_component) -> value.component.component_id) values)

(** Present obligations and declarative ordering with their evidence limits.
    Even the empty-obligation case makes no execution or cleanup claim. *)
let review (resolved : Model.resolved) =
  let obligations = List.map (fun (value : Model.obligation) ->
    [value.subject; value.requirement; evidence value.evidence]) resolved.obligations in
  let obligations = match obligations with
    | [] -> [["None listed"; "No pending obligations in this resolved model"; "No execution claim"]]
    | values -> values in
  Codegen_header.render Codegen_header.Markdown ^ "\n" ^
  table ["Architecture"; "Version"; "Interpretation"] [[resolved.architecture.name;
    string_of_int resolved.architecture.version; "Declared architecture; not observed running state"]] ^ "\n" ^
  table ["Subject"; "Pending obligation"; "Evidence"] obligations ^ "\n" ^
  table ["Component"; "Verification declaration"]
    (List.map verification_row resolved.architecture.components) ^ "\n" ^
  table ["Lifecycle"; "Declarative order"; "Meaning"] [
    ["Start"; order resolved.start_order; "Declared ordering only; not execution"];
    ["Stop"; order resolved.stop_order; "Declared ordering only; not cleanup or shutdown evidence"];
  ]
