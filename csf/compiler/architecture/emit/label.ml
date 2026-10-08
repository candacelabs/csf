(* Generated inverse mappings own the display spellings, so every projection
   shows the same keyword the source declared. *)
let spelling encode value = Syntax_cgen.Terminal.name (encode value)

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

let diagram text = "\"" ^ escaped (Printf.sprintf "#%d;") text ^ "\""
let markdown_cell = escaped (Printf.sprintf "&#%d;")
let with_optional prefix = function None -> "" | Some value -> "\n" ^ prefix ^ value

(* Use local identifiers such as c0 in diagram syntax and keep user identifiers
   in escaped labels. These IDs are stable for the same declaration order, not
   permanent identities across insertions or reordering. *)
let numbered prefix values key =
  List.mapi (fun index value -> key value, prefix ^ string_of_int index) values

let component (value : Model.component) =
  value.component_id ^ "\n" ^ spelling Syntax_cgen.role_terminal value.role ^ " | " ^
  spelling Syntax_cgen.state_terminal value.state ^ " | " ^ spelling Syntax_cgen.lifecycle_terminal value.lifecycle ^
  with_optional "source: " value.source

let process (value : Model.process) =
  "process " ^ value.process_id ^ "\nkind: " ^ spelling Syntax_cgen.process_kind_terminal value.kind ^
  with_optional "entrypoint: " value.entrypoint

let connection (value : Model.connection) =
  spelling Syntax_cgen.transport_terminal value.transport ^ " | " ^
  spelling Syntax_cgen.state_terminal value.connection_state ^
  with_optional "boundary: " value.boundary
