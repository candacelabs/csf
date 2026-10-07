(* CS-21 "Code of one type lives together" (operator, 2026-10-06: "PUT CODE OF
   SIMILAR TYPES TOGETHER IT MAKES IT EASIER TO REASON ABOUT THE RUNTIME"). A
   file named for a role (options.go, metrics.go, errors.go, ...) collects
   unrelated types that share only their role, and a method living away from
   its receiver's file forces a reader to hold several files in mind at once.
   Code of one type belongs in one place: a file named for the type, or the
   file that declares it.

   The file a type is named for is <snake(type)>.go, or a concern split still
   sorted beside it: <snake(type)>_<concern>.go. *)

open Source

let role_names = [
  "options.go"; "contracts.go"; "interfaces.go"; "metrics.go"; "errors.go";
  "types.go"; "models.go"; "helpers.go"; "utils.go"; "constants.go"; "config.go";
]

let role_named path = List.mem (Filename.basename path) role_names

(* snake(GrammarName) = grammar_name: a lower-case run is not interrupted, an
   upper-case run takes a separator before its last name and before a
   lower-case neighbour. *)
let snake name =
  let buffer = Buffer.create (String.length name + 4) in
  String.iteri (fun index char ->
    if char >= 'A' && char <= 'Z' then begin
      let previous = if index = 0 then '\000' else name.[index - 1] in
      let after_lower = (previous >= 'a' && previous <= 'z') || (previous >= '0' && previous <= '9') in
      let before_lower = index + 1 < String.length name &&
        (let next = name.[index + 1] in next >= 'a' && next <= 'z') in
      if index > 0 && (after_lower || before_lower) then Buffer.add_char buffer '_';
      Buffer.add_char buffer (Char.lowercase_ascii char)
    end else Buffer.add_char buffer char) name;
  Buffer.contents buffer

let named_for path type_name =
  let base = Filename.basename path in
  let stem = snake type_name in
  base = stem ^ ".go" || String.starts_with ~prefix:(stem ^ "_") base

let top_level_types file =
  children file.root |> List.concat_map (fun node ->
    if kind node <> "type_declaration" then []
    else children node |> List.filter_map (fun spec ->
      if List.mem (kind spec) ["type_spec"; "type_alias"] then field_text file spec "name" else None))

(* role_named_file_mixing_types *)
let mixes_findings (file : file) =
  if not (role_named file.path) then [] else
  match List.sort_uniq compare (top_level_types file) with
  | [] | [_] -> []
  | names -> [issue file file.root "CS-21" (Printf.sprintf
      "role-named file declares %d unrelated types (%s); give each type its own file named for it"
      (List.length names) (String.concat ", " names))]

(* method_outside_its_type_files: an index from a type's declaring file, keyed
   by the directory and package it lives in. *)
let index_key (file : file) name = Filename.dirname file.path, name

let declaring_files files =
  let table = Hashtbl.create 256 in
  List.iter (fun file ->
    if selected file then
      top_level_types file |> List.iter (fun name ->
        let key = index_key file name in
        if not (Hashtbl.mem table key) then Hashtbl.replace table key file.path)) files;
  table

let receiver_type file node =
  match field node "receiver" with
  | None -> None
  | Some receiver ->
      descendants receiver |> List.find_map (fun child ->
        if kind child = "type_identifier" then Some (text file child) else None)

let away_findings declaring (file : file) =
  descendants file.root |> List.filter_map (fun node ->
    if kind node <> "method_declaration" then None else
    match receiver_type file node with
    | None -> None
    | Some type_name ->
        if named_for file.path type_name then None else
        (match Hashtbl.find_opt declaring (index_key file type_name) with
         | Some home when home <> file.path ->
             Some (issue file node "CS-21" (Printf.sprintf
               "method on %s is not where %s lives; move it beside the type (or into a file named %s.go)"
               type_name type_name (snake type_name)))
         | _ -> None))

(* generated_layout_by_role: a generated file named for a role proves a
   generator emitted a layout by role. The generator declares that layout where
   it is configured -- a `Layout:` line in the config that names its output
   directory -- and that declaration, not the file, is what exempts it. *)
let declares_layout text =
  String.split_on_char '\n' text |> List.exists (fun line ->
    let line = String.trim line in
    let body =
      if String.starts_with ~prefix:"//" line then Some (String.sub line 2 (String.length line - 2))
      else if String.starts_with ~prefix:"#" line then Some (String.sub line 1 (String.length line - 1))
      else None in
    match body with
    | Some body -> String.starts_with ~prefix:"Layout:" (String.trim body)
    | None -> false)

(* Resolve `.` and `..` the way the tracked file list spells a directory, so an
   output path written relative to the config lands where the parser looks. *)
let normalize path =
  let parts = List.fold_left (fun parts part -> match part, parts with
    | ("." | ""), parts -> parts
    | "..", _ :: rest -> rest
    | "..", [] -> []
    | part, parts -> part :: parts) [] (String.split_on_char '/' path) in
  String.concat "/" (List.rev parts)

let unquote value =
  String.to_seq value |> Seq.filter (fun character -> character <> '"' && character <> '\'') |> String.of_seq

(* The output directories a configured generator declares a layout for. sqlc
   names each with `out:`, and its config sits beside its input rather than its
   output, so the path resolves against the config's own directory. *)
let declared_layout_dirs root tracked =
  tracked |> List.filter (fun path -> Filename.basename path = "sqlc.yaml")
  |> List.concat_map (fun path ->
    try
      let text = Checker.read_file (Checker.checked_path root path) in
      if not (declares_layout text) then [] else
      let directory = Filename.dirname path in
      String.split_on_char '\n' text |> List.filter_map (fun line ->
        let line = String.trim line in
        if not (String.starts_with ~prefix:"out:" line) then None
        else Some (normalize (Filename.concat directory
          (unquote (String.trim (String.sub line 4 (String.length line - 4)))))))
    with Sys_error _ -> [])

let layout_findings declared (file : file) =
  if not (role_named file.path && generated file) then [] else
  if List.mem (Filename.dirname file.path) declared then [] else
  [issue file file.root "CS-21"
    "generated role-named file; the generator emits a layout by role without declaring it (declare the layout in the generator's configuration)"]

let collect ~declared files =
  let declaring = declaring_files files in
  files |> List.concat_map (fun file ->
    if generated file then layout_findings declared file
    else if selected file then mixes_findings file @ away_findings declaring file
    else [])
