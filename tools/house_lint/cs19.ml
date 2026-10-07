open Source

(* CS-19 names the four shapes the typed ask refuses: a reflect import, a
   decoder into an anonymous struct, a map[string]string carrying typed fields,
   and a local generic helper that belongs among the shared primitives.

   This module is the declare half of the ask's golden_first method: the shapes
   become countable and reviewable here, before the exports each message points
   at exist. A following slice adds pkg/collections and rewrites the site, and
   only then does local_generic_helper gain its "an export exists but is not
   called" arm. The rule is registered Advisory until that rewrite lands,
   because its detectors fire on the not-yet-rewritten site and a pre-existing
   backlog: handmade source is not exempt from reflected structure just because
   the fix has not shipped. *)

let decoded literal = try Some (Checker.string_value literal) with Invalid_argument _ -> None

let import_specs file =
  descendants file.root |> List.filter (fun node -> kind node = "import_spec")

let import_path file node = Option.bind (field_text file node "path") decoded

(* reflect_import *)
let reflect_findings file =
  import_specs file |> List.filter_map (fun node ->
    if import_path file node = Some "reflect" then
      Some (issue file node "CS-19"
        "imports reflect; use type parameters and generated decoders instead of runtime reflection")
    else None)

(* decode_into_anonymous_struct: a variable declared with an anonymous struct
   type that a decoder fills in. *)
let anonymous_struct_names file =
  descendants file.root |> List.filter_map (fun node ->
    if kind node <> "var_spec" then None else
    match field node "type" with
    | Some typ when kind typ = "struct_type" ->
        let names = children node
          |> List.filter (fun child -> kind child = "identifier")
          |> List.map (text file) in
        if names = [] then None else Some names
    | _ -> None)
  |> List.concat

let rec target_identifier file node =
  match kind node with
  | "unary_expression" ->
      (match field node "operand" with Some operand -> target_identifier file operand | None -> None)
  | "identifier" -> Some (text file node)
  | _ -> None

let decode_findings file =
  let names = anonymous_struct_names file in
  if names = [] then [] else
  descendants file.root |> List.filter_map (fun node ->
    if kind node <> "call_expression" then None else
    match field node "function" with
    | Some called when kind called = "selector_expression" ->
        (match field_text file called "field" with
         | Some "Unmarshal" ->
             (match field node "arguments" with
              | Some arguments ->
                  let arguments = children arguments
                    |> List.filter (fun child -> kind child <> "comment") in
                  (match arguments with
                   | _ :: target :: _ ->
                       (match target_identifier file target with
                        | Some name when List.mem name names ->
                            Some (issue file node "CS-19"
                              "a decoder fills an anonymous struct; declare a named record type and decode into it")
                        | _ -> None)
                   | _ -> None)
              | None -> None)
         | _ -> None)
    | _ -> None)

let alias_of file path = imports file
  |> List.find_opt (fun (_, imported) -> imported = path) |> Option.map fst

let strconv_method file alias node =
  if kind node <> "call_expression" then None else
  match field node "function" with
  | Some called when kind called = "selector_expression" ->
      (match field_text file called "operand", field_text file called "field" with
       | Some operand, Some member when operand = alias -> Some member
       | _ -> None)
  | _ -> None

let format_methods = ["Itoa"; "FormatInt"; "FormatUint"; "FormatFloat"]
let parse_methods = ["Atoi"; "ParseInt"; "ParseUint"; "ParseFloat"]

let has_strconv_method file alias methods root =
  descendants root |> List.exists (fun node ->
    match strconv_method file alias node with
    | Some member -> List.mem member methods
    | None -> false)

let parses_strconv file =
  match alias_of file "strconv" with
  | None -> false
  | Some alias -> has_strconv_method file alias parse_methods file.root

let directory (file : file) = Filename.dirname file.path

(* string_map_for_typed_fields: a map[string]string literal whose values are
   stringified typed fields, in a package that parses strings back with strconv
   somewhere. The typed value survives only as its decimal text; the round trip
   through strconv is what makes the map a lossy encoding of a declared field. *)
let is_string_map file node =
  kind node = "map_type" &&
  field_text file node "key" = Some "string" &&
  field_text file node "value" = Some "string"

let string_map_findings file parse_dirs =
  if not (List.mem (directory file) parse_dirs) then [] else
  match alias_of file "strconv" with
  | None -> []
  | Some alias ->
      descendants file.root |> List.filter_map (fun node ->
        if kind node <> "composite_literal" then None else
        match field node "type" with
        | Some typ when is_string_map file typ &&
                        has_strconv_method file alias format_methods node ->
            Some (issue file node "CS-19"
              "typed fields are stringified into a map[string]string and parsed back; carry typed fields, not their string form")
        | _ -> None)

(* local_generic_helper: a local helper that reimplements a pkg/collections
   primitive by hand. Two shapes: membership toggle (slices.Index + slices.Delete)
   and lookup by key (a (T, bool) result over slices.IndexFunc). *)
let slices_member file alias member node =
  kind node = "call_expression" &&
  match field node "function" with
  | Some called when kind called = "selector_expression" ->
      field_text file called "operand" = Some alias && field_text file called "field" = Some member
  | _ -> false

let body_uses file alias member fn =
  match field fn "body" with
  | Some body -> descendants body |> List.exists (slices_member file alias member)
  | None -> false

let two_results file fn =
  match field fn "result" with
  | Some result when kind result = "parameter_list" ->
      let parameters = children result |> List.filter (fun child -> kind child = "parameter_declaration") in
      (match parameters with
       | [_; second] -> field_text file second "type" = Some "bool"
       | _ -> false)
  | _ -> false

let functions file =
  descendants file.root |> List.filter (fun node ->
    List.mem (kind node) ["function_declaration"; "method_declaration"])

(* The exported primitive's own home implements the shapes this arm refuses
   (Set.Toggle is slices.Index+slices.Delete; KeyedList.Get is a (V, bool)
   IndexFunc lookup), so it is exempt: the offense is reimplementing the
   primitive elsewhere, never defining it where it is exported. *)
let primitive_home = "pkg/collections"

let generic_helper_findings file =
  if directory file = primitive_home then [] else
  match alias_of file "slices" with
  | None -> []
  | Some alias ->
      functions file |> List.filter_map (fun fn ->
        if body_uses file alias "Index" fn && body_uses file alias "Delete" fn then
          Some (issue file fn "CS-19"
            "local set toggle; export pkg/collections Set[T comparable].Toggle and call it")
        else if two_results file fn && body_uses file alias "IndexFunc" fn then
          Some (issue file fn "CS-19"
            "local lookup by key; export pkg/collections KeyedList[K comparable, V any].Get and call it")
        else None)

let collect files =
  let selected_files = List.filter selected files in
  let parse_dirs = selected_files |> List.filter parses_strconv
    |> List.map directory |> List.sort_uniq compare in
  selected_files |> List.concat_map (fun file ->
    reflect_findings file @ decode_findings file @
    string_map_findings file parse_dirs @ generic_helper_findings file)
