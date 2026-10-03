open Source

let service_path path = String.starts_with ~prefix:"services/" path

let qualified_call file imports node =
  match field node "function" with
  | Some selector when kind selector = "selector_expression" ->
      (match field_text file selector "operand", field_text file selector "field" with
       | Some alias, Some name -> Option.map (fun path -> path, name) (List.assoc_opt alias imports)
       | _ -> None)
  | _ -> None

let process_calls = [
  "os", ["Getenv"; "LookupEnv"; "Exit"];
  "flag", ["Parse"];
  "os/signal", ["Notify"; "NotifyContext"];
  "net", ["Listen"; "ListenPacket"; "ListenUnix"; "ListenTCP"];
  "net/http", ["ListenAndServe"; "ListenAndServeTLS"; "Serve"; "ServeTLS"];
  "github.com/gin-gonic/gin", ["New"; "Default"];
]

let sql_literal value =
  let words = Checker.words (String.map (function '\n' -> ' ' | c -> c) (String.uppercase_ascii (String.trim value))) in
  match words with
  | "SELECT" :: _ -> List.mem "FROM" words
  | "INSERT" :: "INTO" :: _ | "DELETE" :: "FROM" :: _ -> true
  | "UPDATE" :: _ -> List.mem "SET" words
  | "CREATE" :: ("TABLE" | "INDEX") :: _ | "ALTER" :: "TABLE" :: _ -> true
  | _ -> false

let collect files = files |> List.concat_map (fun file ->
  if not (selected file) || is_test file then [] else
  let findings = ref [] in
  let report node rule message = findings := issue file node rule message :: !findings in
  let imported = imports file in
  if List.exists (fun suffix -> Filename.check_suffix file.path suffix) [".pb.go"; ".gen.go"; "_generated.go"] then
    report file.root "CS-4" "generated-looking filename lacks a recognized generated header; verify its generator";
  walk (fun node ->
    (match kind node with
    | "interpreted_string_literal" | "raw_string_literal" ->
        if sql_literal (Checker.string_value (text file node)) then
          report node "CS-4" "handwritten SQL literal; put owned queries in SQLC inputs (review parser/fixture literals)"
    | "type_spec" ->
        (match field_text file node "name", field node "type" with
         | Some name, Some body when String.starts_with ~prefix:"Mock" name && kind body = "struct_type" ->
             report node "CS-4" "handwritten mock-shaped struct; verify whether mockgen owns this double"
         | _ -> ())
    | _ -> ());
    if service_path file.path then
      match kind node with
      | "function_declaration" when field_text file node "name" = Some "main" ->
          report node "CS-10" "service directory owns a main function; move process composition into app/"
      | "call_expression" ->
          (match qualified_call file imported node with
           | Some (path, name) when List.mem name (Option.value (List.assoc_opt path process_calls) ~default:[]) ->
               report node "CS-10" ("service owns " ^ path ^ "." ^ name ^ "; receive configuration/router/lifetime from its caller")
           | _ -> ())
      | _ -> ()
  ) file.root;
  List.rev !findings)

let artifacts files paths =
  let generated_paths = files |> List.filter generated |> List.map (fun (file : file) -> file.path) in
  paths |> List.filter_map (fun path ->
  let parts = String.split_on_char '/' path in
  if service_path path && not (List.mem path generated_paths) &&
    (List.mem "cmd" parts || Filename.basename path = "Dockerfile" ||
     String.starts_with ~prefix:"Dockerfile." (Filename.basename path)) then
    Some {path; line=1; rule="CS-10"; message="service directory contains process/deployment artifact; app/ owns the binary"}
  else None)
