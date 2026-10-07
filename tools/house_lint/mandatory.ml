open Source

module Names = Set.Make (String)

let prefixed name = String.length name > 1
  && (name.[0] = 'I' || name.[0] = 'i') && exported (String.sub name 1 1)

(* CS-20: the three fields an interface declaration carries travel as one named
   record, never as a bare tuple. *)
type interface_decl = { node : Node.t; name : string; body : Node.t }

let interfaces file = descendants file.root |> List.filter_map (fun node ->
  if not (List.mem (kind node) ["type_spec"; "type_alias"]) then None else
  match field_text file node "name", field node "type" with
  | Some name, Some body when kind body = "interface_type" -> Some { node; name; body }
  | _ -> None)

let parameter_nodes node = children node |> List.filter (fun child ->
  List.mem (kind child) ["parameter_declaration"; "variadic_parameter_declaration"])

let signature_node node = List.mem (kind node)
  ["function_declaration"; "method_declaration"; "function_type"; "func_literal"; "method_elem"]

(* Parameter lists carry both named results and input parameters in the grammar.
   Only a signature's parameters field is subject to CS-2, never its receiver or
   result. Nested function types are visited independently by the shared walk. *)
let signature_findings file = descendants file.root |> List.filter_map (fun node ->
  if not (signature_node node) then None else
  match field node "parameters" with
  | None -> None
  | Some parameters ->
      let unnamed = parameter_nodes parameters
        |> List.filter (fun parameter -> field parameter "name" = None) in
      if unnamed = [] then None else
      let name = Option.value ~default:"function type" (field_text file node "name") in
      Some (issue file node "CS-2" (Printf.sprintf "%s has %d unnamed parameter%s"
        name (List.length unnamed) (if List.length unnamed = 1 then "" else "s"))))

let interface_findings file = interfaces file |> List.filter_map (fun declaration ->
  if prefixed declaration.name then None else
  Some (issue file declaration.node "CS-1" ("interface " ^ declaration.name ^ " lacks the house I/i prefix")))

(* Token spelling is used only to compare already-parsed contract types. It
   ignores comments/formatting while preserving spaces inside string tokens. *)
let rec syntax_text file node =
  if kind node = "comment" then ""
  else if Node.child_count node = 0 then text file node
  else List.init (Node.child_count node) (Node.child node)
    |> List.filter_map Fun.id |> List.map (syntax_text file) |> String.concat ""

let parameter_types file node = parameter_nodes node |> List.concat_map (fun parameter ->
  match field parameter "type" with
  | None -> []
  | Some typ ->
      let count = children parameter
        |> List.filter (fun child -> kind child = "identifier") |> List.length |> max 1 in
      let prefix = if kind parameter = "variadic_parameter_declaration" then "..." else "" in
      List.init count (fun _ -> prefix ^ syntax_text file typ))

let result_nodes node = match field node "result" with
  | None -> []
  | Some result when kind result = "parameter_list" -> parameter_nodes result
      |> List.filter_map (fun parameter -> field parameter "type")
  | Some result -> [result]

let signature file node =
  let parameters = Option.fold ~none:[] ~some:(parameter_types file) (field node "parameters") in
  let results = match field node "result" with
    | None -> []
    | Some result when kind result = "parameter_list" -> parameter_types file result
    | Some result -> [syntax_text file result] in
  "(" ^ String.concat "," parameters ^ ")(" ^ String.concat "," results ^ ")"

type interface_index = { names : Names.t; sealed : Names.t; hooks : Names.t }

let interface_index files =
  let names = ref Names.empty and sealed = ref Names.empty and hooks = ref Names.empty in
  List.iter (fun file ->
    interfaces file |> List.iter (fun declaration ->
      names := Names.add declaration.name !names;
      if exported declaration.name && List.exists (fun child -> kind child = "method_elem" &&
        Option.fold ~none:false ~some:(fun method_name -> String.length method_name > 0 &&
          method_name.[0] >= 'a' && method_name.[0] <= 'z') (field_text file child "name"))
        (children declaration.body) then sealed := Names.add declaration.name !sealed);
    walk (fun node -> if List.mem (kind node) ["function_type"; "func_literal"] then
      hooks := Names.add (signature file node) !hooks) file.root
  ) files;
  {names = !names; sealed = !sealed; hooks = !hooks}

(* These are the established CS-8 wrappers: values of maps and elements of
   slices/arrays hand interfaces back to a caller. Pointers, channels, generic
   instantiations and function contracts remain outside this verdict's scope. *)
let rec result_interface file node = match kind node with
  | "type_identifier" -> let name = text file node in if prefixed name then Some name else None
  | "qualified_type" -> Option.bind (field_text file node "name") (fun name ->
      if prefixed name then Some name else None)
  | "slice_type" | "array_type" -> Option.bind (field node "element") (result_interface file)
  | "map_type" -> Option.bind (field node "value") (result_interface file)
  | _ -> None

let return_findings index file = descendants file.root |> List.concat_map (fun node ->
  if kind node <> "function_declaration" || Names.mem (signature file node) index.hooks then [] else
  let results = result_nodes node in
  results |> List.mapi (fun index result -> index + 1, result) |> List.filter_map (fun (position, result) ->
    match result_interface file result with
    | Some name when Names.mem name index.names && not (Names.mem name index.sealed) ->
        Some (issue file node "CS-8" (Printf.sprintf
          "function %s returns the interface %s as result %d of %d; return a concrete implementation and accept interfaces"
          (Option.value ~default:"?" (field_text file node "name")) (text file result) position (List.length results)))
    | _ -> None))

let assertion_packages = ["github.com/onsi/ginkgo/v2", "ginkgo"; "github.com/onsi/gomega", "gomega"]
type assertion_import = { node : Node.t; imported : string; local : string }

let assertion_imports file = descendants file.root |> List.filter_map (fun node ->
  if kind node <> "import_spec" then None else
  match field_text file node "path" with
  | None -> None
  | Some literal ->
      let imported = Checker.string_value literal in
      match List.assoc_opt imported assertion_packages with
      | None -> None
      | Some default -> Some {node; imported;
          local = Option.value ~default (field_text file node "name")})

let top_level_names file = children file.root |> List.concat_map (fun node ->
  match kind node with
  | "function_declaration" -> Option.to_list (field_text file node "name")
  | "type_declaration" -> children node |> List.filter_map (fun spec -> field_text file spec "name")
  | "var_declaration" | "const_declaration" ->
      descendants node |> List.filter (fun spec -> List.mem (kind spec) ["var_spec"; "const_spec"])
      |> List.concat_map (fun spec -> children spec
          |> List.filter (fun child -> kind child = "identifier") |> List.map (text file))
  | _ -> []) |> Names.of_list

let qualified_uses file local = descendants file.root |> List.filter_map (fun node ->
  let qualifier, member = match kind node with
    | "selector_expression" -> field_text file node "operand", field_text file node "field"
    | "qualified_type" -> field_text file node "package", field_text file node "name"
    | _ -> None, None in
  match qualifier, member with
  | Some qualifier, Some name when qualifier = local && exported name -> Some name
  | _ -> None) |> Names.of_list

let package_key (file : file) = Filename.dirname file.path, package file

(* A collision is evidence about one Go package, not about every file sharing
   its directory: external _test packages do not inherit production names. *)
let collision_index files =
  let declared = Hashtbl.create 64 and used = Hashtbl.create 64 in
  let union table key names = Hashtbl.replace table key
    (Names.union names (Option.value ~default:Names.empty (Hashtbl.find_opt table key))) in
  List.iter (fun file ->
    let key = package_key file in
    union declared key (top_level_names file);
    assertion_imports file |> List.iter (fun imported ->
      if imported.local <> "." && imported.local <> "_" then
        union used (key, imported.imported) (qualified_uses file imported.local))
  ) files;
  fun file imported ->
    let key = package_key file in
    let names = Option.value ~default:Names.empty (Hashtbl.find_opt declared key) in
    let referenced = Option.value ~default:Names.empty (Hashtbl.find_opt used (key, imported)) in
    not (Names.is_empty (Names.inter names referenced))

let assertion_findings collision file =
  if not (is_test file) then [] else assertion_imports file |> List.filter_map (fun imported ->
    if imported.local = "." || (imported.local <> "_" && collision file imported.imported) then None else
    Some (issue file imported.node "CS-11" (Printf.sprintf
      "test imports %s as %s; dot-import the assertion package" imported.imported imported.local)))

let bootstrap_scope file = is_test file &&
  (String.starts_with ~prefix:"pkg/" file.path ||
   String.starts_with ~prefix:"tools/" file.path) &&
  not (String.starts_with ~prefix:"pkg/gotth/" file.path)

let bootstrap_findings file =
  if not (bootstrap_scope file) then [] else
  let tests = children file.root |> List.filter (fun node ->
    kind node = "function_declaration" &&
    Option.fold ~none:false ~some:(String.starts_with ~prefix:"Test") (field_text file node "name") &&
    match field node "parameters" with
    | Some parameters -> syntax_text file parameters = "(t*testing.T)"
    | None -> false) in
  let bootstraps = descendants file.root |> List.filter (fun node ->
    kind node = "call_expression" &&
    Option.fold ~none:false ~some:(fun called -> List.mem (syntax_text file called) ["RunSpecs"; "ginkgo.RunSpecs"])
      (field node "function") &&
    match field node "arguments" with
    | Some arguments -> (match children arguments with first :: _ -> text file first = "t" | [] -> false)
    | None -> false) in
  if List.length tests = List.length bootstraps then [] else
  [issue file (match tests with first :: _ -> first | [] -> file.root) "TEST-BOOTSTRAP"
    (Printf.sprintf "%d Test functions, %d RunSpecs bootstraps; use Ginkgo/Gomega behavior specs"
      (List.length tests) (List.length bootstraps))]

let block_statements block = children block
  |> List.filter (fun node -> kind node <> "comment")
  |> List.concat_map (fun node -> if kind node = "statement_list" then children node else [node])
  |> List.filter (fun node -> kind node <> "comment")

let rec returns_on_every_path node = match kind node with
  | "return_statement" -> true
  | "block" ->
      (match List.rev (block_statements node) with
       | final :: _ -> returns_on_every_path final
       | [] -> false)
  | "if_statement" ->
      (match field node "consequence", field node "alternative" with
       | Some consequence, Some alternative ->
           returns_on_every_path consequence && returns_on_every_path alternative
       | _ -> false)
  | "labeled_statement" ->
      (match List.rev (children node |> List.filter (fun child -> kind child <> "comment")) with
       | final :: _ -> returns_on_every_path final
       | [] -> false)
  | _ -> false

let else_after_return_findings file = descendants file.root |> List.filter_map (fun node ->
  if kind node <> "if_statement" then None else
  match field node "consequence", field node "alternative" with
  | Some consequence, Some alternative when returns_on_every_path consequence ->
      Some (issue file alternative "ELSE-AFTER-RETURN"
        "else follows a branch that returns on every path; remove the nesting and keep an explicit block if the branch needs its lexical scope")
  | _ -> None)

let main_test_findings file =
  if not (is_test file) then [] else
  match Source.package file, children file.root |> List.find_opt (fun node -> kind node = "package_clause") with
  | Some ("main" | "main_test"), Some node ->
      [issue file node "NO-MAIN-TEST"
        ("test file cannot have package main; use the default test package or a named package")]
  | _ -> []

let collect files =
  let selected_files = List.filter selected files in
  let first_party = List.filter (fun (file : file) -> not (List.mem "vendor" (String.split_on_char '/' file.path))) files in
  (* Generated declarations remain contract/collision evidence for handwritten
     consumers, even though findings must never target generated source. *)
  let index = interface_index (List.filter (fun (file : file) -> not (excluded file.path)) files) in
  let collision = collision_index first_party in
  let main_test_violations = files |> List.filter is_test |> List.concat_map main_test_findings in
  List.concat_map (fun file -> interface_findings file @ signature_findings file @ return_findings index file @
    else_after_return_findings file)
    selected_files @
  (first_party |> List.filter (fun file -> not (generated file))
   |> List.concat_map (fun file -> assertion_findings collision file @ bootstrap_findings file)) @
  main_test_violations
