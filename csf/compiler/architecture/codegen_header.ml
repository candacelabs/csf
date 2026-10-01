type syntax = Ocaml | Mermaid | Markdown | Hash | C | Sexp

let generator_name = "CandaceCodegen"

(* This module is the one build-time header configuration. The renderer and
   generated-file checker share these markers; border/wording edits do not
   require a matching list of banner strings elsewhere. Keep the legacy name
   here only for existing Candacegen outputs, without changing their generators. *)
let generated_marker = "generated"
let disclaimer = "DO NOT EDIT"
let ownership_markers =
  List.map (fun name -> generated_marker ^ " by " ^ name)
    [generator_name; "Candacegen"]

let text =
  "Code " ^ generated_marker ^ " by " ^ generator_name ^
  " v0.1.0-dev; the _cgen suffix marks generated files. " ^ disclaimer ^ "."

(* Change this one definition to change the banner on every output owned by
   this compiler. Equals signs also remain valid inside Markdown HTML comments,
   where a dashed separator would contain the forbidden sequence "--". *)
let banner = [
  String.make 60 '/';
  text;
  "===== " ^ disclaimer ^ " =====";
  "Edit the source definition or generator, then regenerate this file.";
  String.make 60 '/';
]

type provenance = {
  sources : string list;
  generator : string;
  owner : string;
  regenerate : string;
}

let contains text piece =
  let width = String.length piece in
  let rec scan at = at + width <= String.length text &&
    (String.sub text at width = piece || scan (at + 1)) in
  scan 0

let validate_metadata value =
  let absolute token = String.starts_with ~prefix:"/" token &&
    not (String.starts_with ~prefix:"//" token) in
  if value = "" || String.trim value <> value ||
     String.exists (fun character -> Char.code character < 32 || Char.code character = 127) value ||
     List.exists (contains value) ["(*"; "*)"; "/*"; "*/"; "--"; "\""] ||
     List.exists absolute (String.split_on_char ' ' value) then
    invalid_arg "generated provenance must use single-line relative paths or Bazel labels without comment delimiters"

let provenance_lines provenance =
  if provenance.sources = [] then invalid_arg "generated provenance requires a source";
  List.iter validate_metadata
    (provenance.sources @ [provenance.generator; provenance.owner; provenance.regenerate]);
  List.map (fun source -> "Source: " ^ source) provenance.sources @ [
    "Generator: " ^ provenance.generator;
    "Regeneration owner: " ^ provenance.owner;
    "Regenerate: " ^ provenance.regenerate;
  ]

let render ?provenance syntax =
  let comment line = match syntax with
    | Ocaml -> "(* " ^ line ^ " *)\n"
    | Mermaid -> "%% " ^ line ^ "\n"
    | Markdown -> "<!-- " ^ line ^ " -->\n"
    | Hash -> "# " ^ line ^ "\n"
    | C -> "/* " ^ line ^ " */\n"
    | Sexp -> ";; " ^ line ^ "\n" in
  let lines = match provenance with
    | None -> banner
    | Some provenance ->
        match List.rev banner with
        | border :: body -> List.rev body @ provenance_lines provenance @ [border]
        | [] -> assert false in
  String.concat "" (List.map comment lines)
