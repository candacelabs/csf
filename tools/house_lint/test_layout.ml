(* Copyright 2026 Candace Labs *)

open Source

(* CS-18, the test layout (go-rules.md § CS-18). Three advisory locators, each
   its own policy ID so one part can block at zero while the others still
   carry a backlog:

   CS-18-MOCKGEN   an exported interface no //go:generate mockgen directive
                   covers, or whose directive's destination is not a tracked
                   MockGen-generated file.
   CS-18-CROSSING  a test that makes a real crossing (socket, subprocess,
                   database pool, container) instead of using gomock or pgmem.
                   A file whose build constraint names `acceptance` is the
                   labelled opt-in acceptance suite and is not reported.
   CS-18-EXTERNAL  a package with exported API and no external `<name>_test`
                   package holding a spec beside it.

   None of the three exempts test files: the crossing rule exists to read
   them, and the other two read them as the evidence a package is covered. *)

let directory path = Filename.dirname path

(* Lexical path normalization for directive-relative paths: `.` and `..`
   segments are resolved without touching the filesystem. *)
let normalize path =
  let segments = String.split_on_char '/' path |> List.fold_left (fun acc segment ->
    match segment, acc with
    | ("" | "."), _ -> acc
    | "..", previous :: rest when previous <> ".." -> rest
    | _ -> segment :: acc) [] in
  match List.rev segments with [] -> "." | segments -> String.concat "/" segments

let join base relative = normalize (if base = "." then relative else base ^ "/" ^ relative)

let leading_comments file =
  children file.root |> List.take_while (fun node -> kind node = "comment") |> List.map (text file)

let package_line file = children file.root
  |> List.find_opt (fun node -> kind node = "package_clause")
  |> Option.fold ~none:1 ~some:(fun node -> (Node.start_point node).row + 1)

(* ---------------------------------------------------------------- crossing *)

let crossing_calls = [
  "net", ["Listen"; "ListenPacket"; "ListenTCP"; "ListenUDP"; "ListenUnix"; "ListenUnixgram";
          "ListenIP"; "ListenMulticastUDP"; "Dial"; "DialTimeout"; "DialTCP"; "DialUDP";
          "DialUnix"; "DialIP"];
  "net/http", ["ListenAndServe"; "ListenAndServeTLS"; "Serve"; "ServeTLS"];
  "net/http/httptest", ["NewServer"; "NewTLSServer"; "NewUnstartedServer"];
  "os/exec", ["Command"; "CommandContext"];
  "github.com/jackc/pgx/v5/pgxpool", ["New"; "NewWithConfig"];
  "github.com/jackc/pgx/v5", ["Connect"; "ConnectConfig"];
]
(* database/sql opens a real PostgreSQL pool only through a network driver;
   the driver name is the first argument. *)
let network_drivers = ["pgx"; "postgres"; "pgx/v5"]
let crossing_types = ["net", ["Dialer"; "ListenConfig"]]
let container_module = "github.com/testcontainers/testcontainers-go"

(* The labelled acceptance suite: a `//go:build` constraint above the package
   clause in which `acceptance` appears as a positive tag. `!acceptance`, a
   plain comment, or a tag in the file body is not the label. *)
let acceptance_suite file =
  leading_comments file |> List.exists (fun comment ->
    String.starts_with ~prefix:"//go:build " comment &&
    String.sub comment 11 (String.length comment - 11)
    |> String.map (function '(' | ')' | '&' | '|' -> ' ' | character -> character)
    |> String.split_on_char ' ' |> List.mem "acceptance")

let crossing_advice =
  "use a gomock mock of the exported interface, or pgmem as the database substrate; a real-infrastructure suite carries //go:build acceptance"

let crossings (file : Source.file) =
  if not (is_test file) || acceptance_suite file then [] else
  let imports = Source.imports file in
  let report node what = issue file node "CS-18-CROSSING" ("test crosses a real boundary through " ^ what ^ ": " ^ crossing_advice) in
  descendants file.root |> List.filter_map (fun node ->
    match kind node with
    | "import_spec" ->
        (match Option.map Checker.string_value (field_text file node "path") with
         | Some path when path = container_module || String.starts_with ~prefix:(container_module ^ "/") path ->
             Some (report node path)
         | _ -> None)
    | "call_expression" ->
        (match Option.bind (field_text file node "function") (Placement.qualified imports) with
         | Some (path, name) when Placement.member crossing_calls (path, name) -> Some (report node (path ^ "." ^ name))
         | Some ("database/sql", "Open") ->
             (match Option.map children (field node "arguments") with
              | Some (driver :: _) when List.mem (kind driver) ["interpreted_string_literal"; "raw_string_literal"] &&
                                        List.mem (Checker.string_value (text file driver)) network_drivers ->
                  Some (report node ("database/sql.Open with the " ^ Checker.string_value (text file driver) ^ " driver"))
              | _ -> None)
         | _ -> None)
    | "composite_literal" ->
        (match Option.bind (field_text file node "type") (Placement.qualified imports) with
         | Some (path, name) when Placement.member crossing_types (path, name) -> Some (report node (path ^ "." ^ name ^ " literal"))
         | _ -> None)
    | _ -> None)

(* ----------------------------------------------------------------- mockgen *)

type directive = {
  at : string;                 (* directory of the file holding the directive *)
  source : string option;      (* -source, resolved against [at] *)
  destination : string option; (* -destination, resolved against [at] *)
  package_path : string option; (* package-mode import path, "." for [at] *)
  names : string list;         (* package-mode interface names *)
}

let valued_flags = ["source"; "destination"; "package"; "mock_names"; "aux_files"; "self_package";
                    "imports"; "build_flags"; "copyright_file"; "exclude_interfaces"; "model_file"]

let mockgen_command token =
  let base = Filename.basename token in
  base = "mockgen" || String.starts_with ~prefix:"mockgen@" base

(* Arguments after the mockgen command token: `-flag=value`, `-flag value`
   for the flags that take one, and positional arguments. *)
let directive_of at comment =
  if not (String.starts_with ~prefix:"//go:generate " comment) then None else
  let tokens = String.split_on_char ' ' comment |> List.concat_map (String.split_on_char '\t')
    |> List.filter (( <> ) "") in
  let rec after = function [] -> None | token :: rest -> if mockgen_command token then Some rest else after rest in
  match after tokens with
  | None -> None
  | Some arguments ->
      let flag token = let bare = if String.starts_with ~prefix:"--" token then String.sub token 2 (String.length token - 2)
        else String.sub token 1 (String.length token - 1) in
        match String.index_opt bare '=' with
        | Some index -> String.sub bare 0 index, Some (String.sub bare (index + 1) (String.length bare - index - 1))
        | None -> bare, None in
      let rec parse flags positional = function
        | [] -> List.rev flags, List.rev positional
        | token :: rest when String.length token > 1 && token.[0] = '-' ->
            (match flag token with
             | name, Some value -> parse ((name, value) :: flags) positional rest
             | name, None when List.mem name valued_flags ->
                 (match rest with value :: rest -> parse ((name, value) :: flags) positional rest
                  | [] -> parse flags positional [])
             | _, None -> parse flags positional rest)
        | token :: rest -> parse flags (token :: positional) rest in
      let flags, positional = parse [] [] arguments in
      let resolved name = Option.map (join at) (List.assoc_opt name flags) in
      let package_path, names = match positional with
        | path :: names :: _ -> Some path, String.split_on_char ',' names
        | _ -> None, [] in
      Some {at; source = resolved "source"; destination = resolved "destination"; package_path; names}

let directives files = files |> List.concat_map (fun (file : Source.file) ->
  descendants file.root |> List.filter_map (fun node ->
    if kind node = "comment" then directive_of (directory file.path) (text file node) else None))

let mockgen_output (file : Source.file) =
  standard_generated file.source &&
  String.split_on_char '\n' file.source |> List.exists (String.starts_with ~prefix:"// Code generated by MockGen.")

(* Module roots from go.mod files, longest module path first, so a package
   import path resolves to the directory that holds it. *)
let modules root paths = paths |> List.filter (fun path -> Filename.basename path = "go.mod")
  |> List.filter_map (fun path ->
    let file = Filename.concat root path in
    if not (Sys.file_exists file) then None else
    Checker.read_file file |> String.split_on_char '\n' |> List.find_map (fun line ->
      match String.split_on_char ' ' (String.trim line) |> List.filter (( <> ) "") with
      | ["module"; name] -> Some (directory path, String.concat "" (String.split_on_char '"' name))
      | _ -> None))
  |> List.sort (fun (_, a) (_, b) -> compare (String.length b) (String.length a))

let package_directory modules at import_path =
  if import_path = "." then Some at else
  modules |> List.find_map (fun (root, name) ->
    if import_path = name then Some root
    else if String.starts_with ~prefix:(name ^ "/") import_path then
      Some (join root (String.sub import_path (String.length name + 1) (String.length import_path - String.length name - 1)))
    else None)

(* A constraint interface (a type set with `|` or `~`) cannot be mocked, and an
   empty interface has nothing to mock. *)
let mockable file body =
  let members = children body in
  members <> [] && not (List.exists (fun member ->
    kind member = "type_elem" && String.exists (fun character -> character = '|' || character = '~') (text file member)) members)

let exported_interfaces (file : Source.file) =
  if is_test file || not (selected file) || package file = Some "main" then [] else
  descendants file.root |> List.filter_map (fun node ->
    if kind node <> "type_spec" then None else
    match field_text file node "name", field node "type" with
    | Some name, Some body when exported name && kind body = "interface_type" && mockable file body -> Some (node, name)
    | _ -> None)

let unmocked ~modules files =
  let generated_mocks = files |> List.filter mockgen_output |> List.map (fun (file : Source.file) -> file.path) in
  let effective = directives files |> List.filter (fun directive ->
    match directive.destination with Some path -> List.mem path generated_mocks | None -> false) in
  let covers (file : Source.file) name directive =
    directive.source = Some file.path ||
    (List.mem name directive.names &&
     Option.bind directive.package_path (package_directory modules directive.at) = Some (directory file.path)) in
  files |> List.concat_map (fun (file : Source.file) -> exported_interfaces file |> List.filter_map (fun (node, name) ->
    if List.exists (covers file name) effective then None else
    Some (issue file node "CS-18-MOCKGEN"
      ("exported interface " ^ name ^ " has no //go:generate mockgen directive with a tracked generated mock; add the directive and commit its output"))))

(* ---------------------------------------------------------------- external *)

let exported_api (file : Source.file) = children file.root |> List.exists (fun node ->
  match kind node with
  | "function_declaration" | "method_declaration" ->
      Option.fold ~none:false ~some:exported (field_text file node "name")
  | "type_declaration" | "var_declaration" | "const_declaration" ->
      descendants node |> List.exists (fun spec ->
        List.mem (kind spec) ["type_spec"; "type_alias"; "var_spec"; "const_spec"] &&
        (match field spec "name" with
         | Some name -> exported (text file name)
         | None -> false) ||
        (List.mem (kind spec) ["var_spec"; "const_spec"] &&
         children spec |> List.exists (fun child -> kind child = "identifier" && exported (text file child))))
  | _ -> false)

let test_entry name = List.exists (fun prefix -> String.starts_with ~prefix name) ["Test"; "Example"; "Fuzz"]

(* A spec, not an empty file that only declares the external package name. *)
let holds_spec (file : Source.file) = descendants file.root |> List.exists (fun node ->
  match kind node with
  | "function_declaration" -> Option.fold ~none:false ~some:test_entry (field_text file node "name")
  | "call_expression" ->
      (match field_text file node "function" with
       | Some ("Describe" | "DescribeTable") -> true
       | _ -> false)
  | _ -> false)

let missing_external files =
  let tests = files |> List.filter (fun (file : Source.file) -> is_test file && selected file) in
  let production = files |> List.filter (fun (file : Source.file) -> not (is_test file) && selected file)
    |> List.sort (fun (a : Source.file) (b : Source.file) -> compare a.path b.path) in
  let packages = production |> List.filter_map (fun (file : Source.file) ->
    match package file with
    | Some name when name <> "main" -> Some (directory file.path, name)
    | _ -> None) |> List.sort_uniq compare in
  packages |> List.filter_map (fun (at, name) ->
    let members = production |> List.filter (fun (file : Source.file) ->
      directory file.path = at && package file = Some name) in
    match List.find_opt exported_api members with
    | None -> None
    | Some first ->
        let external_suite = tests |> List.exists (fun (file : Source.file) ->
          directory file.path = at && package file = Some (name ^ "_test") && holds_spec file) in
        if external_suite then None else
        Some {path = first.path; line = package_line first; rule = "CS-18-EXTERNAL";
              message = "package " ^ name ^ " exports API but has no external " ^ name ^
                        "_test package with specs; integration specs drive the exported API with gomock dependencies, including misuse"})

let collect ~modules files =
  List.concat_map crossings (List.filter selected files) @ unmocked ~modules files @ missing_external files
