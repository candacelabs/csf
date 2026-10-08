(* [%S] writes an OCaml string literal with escapes, so a path containing a
   quote or newline remains data in the generated source. *)
let quoted = Printf.sprintf "%S"
let optional encode = function None -> "None" | Some value -> "Some (" ^ encode value ^ ")"
let sequence encode values = "[" ^ String.concat "; " (List.map encode values) ^ "]"
let record fields =
  "{ " ^ String.concat "; " (List.map (fun (name, value) -> name ^ " = " ^ value) fields) ^ " }"

(* Generated inverse mappings are the vocabulary owner for OCaml variant
   names. Record field labels below belong to Model's destination types; the
   emitted module is compiled against those types. *)
let constructor encode value = "Model." ^ Syntax_cgen.Terminal.constructor_name (encode value)
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

let directory (value : Model.directory) = record [
  "Model.path", quoted value.path; "allowed", sequence quoted value.allowed;
  "tier", optional (constructor Syntax_cgen.tier_terminal) value.tier;
  "directory_at", location value.directory_at;
]

(* Declarations and their locations only. Resolution caches, observations and
   executed lifecycle hooks are not serialized. *)
let render (resolved : Model.resolved) =
  let value = resolved.architecture in
  let fields = [
    "name", quoted value.name; "version", string_of_int value.version; "at", location value.at;
    "processes", sequence process value.processes; "scopes", sequence scope value.scopes;
    "components", sequence component value.components;
    "dependencies", sequence dependency value.dependencies;
    "connections", sequence connection value.connections;
    "scan_roots", sequence source_root value.scan_roots;
    "generated_roots", sequence source_root value.generated_roots;
    "directories", sequence directory value.directories;
  ] in
  Codegen_header.render Codegen_header.Ocaml ^
  "(* Declared architecture; this value does not attest to execution behavior. *)\n" ^
  "let architecture : Model.architecture = {\n" ^
  String.concat "" (List.map (fun (name, value) -> "  " ^ name ^ " = " ^ value ^ ";\n") fields) ^
  "}\n"
