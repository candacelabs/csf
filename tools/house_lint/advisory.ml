open Source

(* These rules identify review sites. None of their findings is a verdict or
   changes the mandatory gate's exit status. The shapes and exemptions follow
   the house rules, including the mechanical constructor-name follow-up. *)
let significant node = children node |> List.filter (fun child -> kind child <> "comment")
let named (file : Source.file) node = Option.value (field_text file node "name") ~default:"<anonymous>"
let without_pointer node =
  if kind node = "pointer_type" then (match significant node with child :: _ -> Some child | [] -> None) else Some node
let imports file = Source.imports file |> List.map (fun (alias, path) ->
  let canonical = match path with
    | "database/sql" -> "sql"
    | "github.com/google/uuid" | "github.com/gofrs/uuid" | "github.com/gofrs/uuid/v5" -> "uuid"
    | path -> path in
  alias, canonical)
let canonical_name aliases spelling = match String.split_on_char '.' spelling with
  | [qualifier; name] -> Option.value (List.assoc_opt qualifier aliases) ~default:qualifier ^ "." ^ name
  | _ -> spelling
let bare_text (file : Source.file) aliases node =
  Option.map (fun node -> canonical_name aliases (text file node)) (without_pointer node)
let has_kind kinds node = List.mem (kind node) kinds
let declarations (file : Source.file) = descendants file.root |> List.filter (has_kind ["type_spec"; "type_alias"])
let parameters node = significant node |> List.filter (has_kind ["parameter_declaration"; "variadic_parameter_declaration"])
let parameter_types node = parameters node |> List.filter_map (fun parameter ->
  if kind parameter = "variadic_parameter_declaration" then None else field parameter "type")
let result_types node = match field node "result" with
  | None -> []
  | Some result when kind result = "parameter_list" -> parameter_types result
  | Some result -> [result]
let one_type node = match parameters node with
  | [parameter] when kind parameter = "parameter_declaration" ->
      let names = significant parameter |> List.filter (fun child -> kind child = "identifier") in
      if List.length names <= 1 then field parameter "type" else None
  | _ -> None
let single_result node = match field node "result" with
  | Some result when kind result = "parameter_list" -> one_type result
  | result -> result

let mutexes (file : Source.file) =
  let aliases = imports file in
  let is_mutex node = match bare_text file aliases node with
    | Some ("sync.Mutex" | "sync.RWMutex") -> true | _ -> false in
  let rec mutex_initializer node = match kind node with
    | "expression_list" -> (match significant node with [value] -> mutex_initializer value | _ -> None)
    | "unary_expression" -> (match significant node with [value] -> mutex_initializer value | _ -> None)
    | "composite_literal" -> field node "type"
    | _ -> None in
  descendants file.root |> List.filter_map (fun node ->
    let typ = match kind node with
      | "field_declaration" -> field node "type"
      | "short_var_declaration" -> Option.bind (field node "right") mutex_initializer
      | "var_spec" -> (match field node "type" with
          | Some typ -> Some typ
          | None -> Option.bind (field node "value") mutex_initializer)
      | _ -> None in
    match typ with
    | Some typ when is_mutex typ ->
        let subject = if kind node = "short_var_declaration" then
            "short variable " ^ Option.value (field_text file node "left") ~default:"<anonymous>"
          else (if kind node = "var_spec" then "var " else "struct field ") ^ named file node in
        Some (issue file node "CS-5" (subject ^ " (" ^ text file typ ^
          "): prefer CSP and one owning goroutine unless this is a leaf critical section with no cross-goroutine protocol"))
    | _ -> None)

let dispatches (file : Source.file) =
  let sibling receiver statement =
    match significant statement with
    | [call] when kind statement = "expression_statement" && kind call = "call_expression" ->
        (match field call "function", field call "arguments" with
         | Some callee, Some arguments when kind callee = "selector_expression" && significant arguments = [] ->
             if field_text file callee "operand" = Some receiver then field_text file callee "field" else None
         | _ -> None)
    | _ -> None in
  descendants file.root |> List.filter_map (fun node ->
    if kind node <> "method_declaration" then None else
    match field node "receiver", field node "body" with
    | Some receiver, Some body ->
        let receiver_name = parameters receiver |> List.filter_map (fun parameter -> field_text file parameter "name") in
        let statements = significant body |> List.concat_map (fun node ->
          if kind node = "statement_list" then significant node else [node]) in
        (match receiver_name with
         | [receiver] when receiver <> "_" ->
             let calls = List.filter_map (sibling receiver) statements in
             if List.length calls >= 3 && List.length calls = List.length statements then
               Some (issue file node "CS-6" (Printf.sprintf
                 "method %s dispatches %d sibling methods and does nothing else (%s): register a family as named function values; a fixed sequence with ordering dependencies may stay"
                 (named file node) (List.length calls) (String.concat ", " calls)))
             else None
         | _ -> None)
    | _ -> None)

let erased_boundaries (file : Source.file) =
  if List.mem "internal" (String.split_on_char '/' file.path) then [] else
  let directly_erased node =
    text file node = "any" || (kind node = "interface_type" && significant node = []) in
  let declarations = declarations file in
  let aliases = declarations |> List.filter_map (fun declaration ->
    match field declaration "type" with
    | Some typ when directly_erased typ -> field_text file declaration "name"
    | _ -> None) in
  (* A generic API instantiated with [any] at a public boundary erases exactly
     what its type parameter exists to carry (operator, 2026-10-01: "why do we
     need any if we have parameterizable types"). *)
  let instantiated_with_any node =
    (kind node = "type_arguments" || kind node = "generic_type" || kind node = "pointer_type" ||
     kind node = "slice_type" || kind node = "qualified_type") &&
    descendants node |> List.exists (fun argument ->
      kind argument = "type_arguments" &&
      significant argument |> List.exists (fun parameter ->
        let typ = match significant parameter with [inner] when kind parameter = "type_elem" -> inner | _ -> parameter in
        directly_erased typ || List.mem (text file typ) aliases)) in
  let erased node = directly_erased node || List.mem (text file node) aliases || instantiated_with_any node in
  let describe holder member position typ =
    Printf.sprintf "exported %s.%s has %s typed %s: carry the type in a type parameter, or document why this public boundary genuinely handles unknown shapes"
      holder member position (text file typ) in
  let functions = significant file.root |> List.concat_map (fun node ->
    if kind node <> "function_declaration" || not (exported (named file node)) then [] else
    let parameters = Option.fold ~none:[] ~some:parameter_types (field node "parameters") in
    List.map (fun typ -> "a parameter", typ) parameters @ List.map (fun typ -> "a result", typ) (result_types node)
    |> List.filter_map (fun (position, typ) -> if instantiated_with_any typ then
      Some (issue file node "CS-7" (Printf.sprintf
        "exported function %s has %s typed %s: a generic API instantiated with any erases what its type parameter carries; instantiate it with the concrete contract"
        (named file node) position (text file typ))) else None)) in
  functions @ (declarations |> List.concat_map (fun declaration ->
    let holder = named file declaration in
    if not (exported holder) then [] else
    match field declaration "type" with
    | Some typ when kind typ = "interface_type" ->
        significant typ |> List.concat_map (fun member ->
          if kind member <> "method_elem" || not (exported (named file member)) then [] else
          let parameter_types = Option.fold ~none:[] ~some:parameter_types (field member "parameters") in
          List.map (fun typ -> "a parameter", typ) parameter_types @
          List.map (fun typ -> "a result", typ) (result_types member)
          |> List.filter_map (fun (position, typ) -> if erased typ then
            Some (issue file member "CS-7" (describe holder (named file member) position typ)) else None))
    | Some typ when kind typ = "struct_type" ->
        significant typ |> List.concat_map (fun node -> if kind node = "field_declaration_list" then significant node else [node])
        |> List.filter_map (fun member ->
          match field member "type" with
          | Some typ when kind member = "field_declaration" && exported (named file member) && erased typ ->
              Some (issue file member "CS-7" (describe holder (named file member) "a field" typ))
          | _ -> None)
    | _ -> []))

let sleep_loops (file : Source.file) =
  if not (is_test file) then [] else
  let aliases = imports file in
  let rec visit in_loop node =
    let finding = if in_loop && kind node = "call_expression" &&
      Option.map (canonical_name aliases) (field_text file node "function") = Some "time.Sleep" then
      [issue file node "CS-9" "time.Sleep in a test loop: use the datum owner's typed await or eventually; load generation, pacing and observation windows may stay with their reason documented"]
      else [] in
    finding @ (significant node |> List.concat_map (fun child ->
      let in_loop = in_loop || (kind node = "for_statement" && kind child = "block") in
      visit in_loop child)) in
  visit false file.root

let generic_constructor_names = ["New"; "new"; "NewStore"; "newStore"; "NewClient"; "newClient"; "NewService"; "newService"]

let constructors (file : Source.file) =
  significant file.root |> List.filter_map (fun node ->
    if kind node = "function_declaration" && List.mem (named file node) generic_constructor_names then
      Some (issue file node "CS-12" ("function " ^ named file node ^ " is a generic constructor: name the concrete thing it builds; generated and mirrored third-party names retain their contracts"))
    else None)

let message_first = ["panic"; "errors.New"; "fmt.Errorf"; "fmt.Print"; "fmt.Printf";
  "fmt.Println"; "fmt.Sprint"; "fmt.Sprintf"; "fmt.Sprintln"; "log.Fatal";
  "log.Fatalf"; "log.Fatalln"; "log.Panic"; "log.Panicf"; "log.Print";
  "log.Printf"; "log.Println"]
let message_second = ["fmt.Fprint"; "fmt.Fprintf"; "fmt.Fprintln"]
let message_methods = ["Debug"; "DebugContext"; "Debugf"; "Error"; "ErrorContext";
  "Errorf"; "Fatal"; "Fatalf"; "Info"; "InfoContext"; "Infof"; "Log"; "Logf";
  "Panic"; "Panicf"; "Print"; "Printf"; "Println"; "Skip"; "Skipf"; "Warn";
  "WarnContext"; "Warnf"]
let message_fields = ["Message"; "Msg"; "Detail"; "Description"; "Reason"]
let message_argument callee index =
  (index = 0 && List.mem callee message_first) ||
  (index = 1 && List.mem callee message_second) ||
  (match List.rev (String.split_on_char '.' callee) with
   | method_name :: _ :: _ -> List.mem method_name message_methods
   | _ -> false)

let magic_strings (file : Source.file) =
  if is_test file then [] else
  let aliases = imports file in
  let separator inner =
    inner = "" || (String.length inner = 2 && inner.[0] = '\\') ||
    (String.length inner > 0 && Uchar.utf_decode_length (String.get_utf_8_uchar inner 0) = String.length inner) in
  let rec visit context node =
    match kind node with
    | "const_declaration" -> []
    | "interpreted_string_literal" | "raw_string_literal" ->
        let literal = text file node in
        let inner = String.sub literal 1 (max 0 (String.length literal - 2)) in
        if separator inner || String.contains inner ' ' ||
          Option.fold ~none:false ~some:(fun (callee, index) -> message_argument callee index) context then []
        else
          let where = Option.fold ~none:"an expression" ~some:(fun (callee, index) ->
            Printf.sprintf "argument %d of %s(...)" (index + 1) callee) context in
          [issue file node "CS-13" (Printf.sprintf
            "the literal %s stands in %s: declare the semantic value once in its owning package; human-facing messages and separators may stay inline"
            literal where)]
    | "call_expression" ->
        let callee = canonical_name aliases (Option.value (field_text file node "function") ~default:"") in
        let function_findings = Option.fold ~none:[] ~some:(visit None) (field node "function") in
        function_findings @ Option.fold ~none:[] ~some:(fun arguments ->
          significant arguments |> List.mapi (fun index argument -> visit (Some (callee, index)) argument)
          |> List.concat) (field node "arguments")
    | "keyed_element" ->
        let key = field_text file node "key" in
        significant node |> List.concat_map (fun child ->
          if Option.fold ~none:false ~some:(fun key -> List.mem key message_fields) key &&
            Option.fold ~none:false ~some:(fun value ->
              Node.start_byte child = Node.start_byte value &&
              match significant value with
              | [literal] -> has_kind ["interpreted_string_literal"; "raw_string_literal"] literal
              | _ -> false) (field node "value") then []
          else visit None child)
    | "field_declaration" ->
        significant node |> List.filter (fun child -> not (has_kind ["interpreted_string_literal"; "raw_string_literal"] child))
        |> List.concat_map (visit context)
    | "composite_literal" | "literal_value" | "index_expression" | "parenthesized_expression" ->
        significant node |> List.concat_map (visit None)
    | _ -> significant node |> List.concat_map (visit context) in
  significant file.root |> List.filter (has_kind ["function_declaration"; "method_declaration"])
  |> List.concat_map (fun declaration -> Option.fold ~none:[] ~some:(visit None) (field declaration "body"))

let optional_values = [
  "sql.NullString", ["string"], "null.String";
  "sql.NullTime", ["time.Time"], "null.Time";
  "sql.NullBool", ["bool"], "null.Bool";
  "sql.NullInt16", ["int"; "int8"; "int16"], "null.Int16";
  "sql.NullInt32", ["int"; "int16"; "int32"], "null.Int32";
  "sql.NullInt64", ["int"; "int32"; "int64"], "null.Int64";
  "sql.NullFloat64", ["float32"; "float64"], "null.Float";
  "uuid.NullUUID", ["uuid.UUID"], "*uuid.UUID";
]

(* CS-20: the five fields of a null-twin conversion travel as one named
   record, never as a bare tuple. *)
type null_conversion = {
  direction : string; typ : string; relation : string; plain : Node.t; replacement : string }

let null_twins (file : Source.file) =
  if is_test file then [] else
  let aliases = imports file in
  let optional node = Option.bind (bare_text file aliases node) (fun typ ->
    List.find_opt (fun (name, _, _) -> name = typ) optional_values) in
  significant file.root |> List.filter_map (fun node ->
    if kind node <> "function_declaration" then None else
    match Option.bind (field node "parameters") one_type, single_result node with
    | Some parameter, Some result ->
        let conversion = match optional parameter, optional result with
          | Some (typ, values, replacement), None when Option.fold ~none:false ~some:(fun typ -> List.mem typ values) (bare_text file aliases result) ->
              Some { direction = "reads"; typ; relation = "as"; plain = result; replacement }
          | None, Some (typ, values, replacement) when Option.fold ~none:false ~some:(fun typ -> List.mem typ values) (bare_text file aliases parameter) ->
              Some { direction = "builds"; typ; relation = "from"; plain = parameter; replacement }
          | _ -> None in
        Option.map (fun conversion -> issue file node "CS-14"
          (Printf.sprintf "function %s %s %s %s %s by hand: emit %s with sqlc go_type overrides and use the library's constructors and accessors"
            (named file node) conversion.direction conversion.typ conversion.relation
            (text file conversion.plain) conversion.replacement)) conversion
    | _ -> None)

(* CS-14, second shape (operator, 2026-10-01, reading runtime/host.go:
   "is this really idiomatic in go for async loop shit"): a buffered
   `make(chan error, 1)` filled by a non-blocking `select { case ch <- err:
   default: }` is a hand-rolled first-error latch. context.WithCancelCause is
   the library primitive: the first cause wins by construction, and the waiter
   reads <-ctx.Done() and context.Cause(ctx). Reported at the select. *)
let first_error_latches (file : Source.file) =
  if is_test file then [] else
  let error_channel node =
    kind node = "call_expression" && field_text file node "function" = Some "make" &&
    (match Option.map significant (field node "arguments") with
     | Some [typ; capacity] ->
         String.concat "" (String.split_on_char ' ' (text file typ)) = "chanerror" && text file capacity = "1"
     | _ -> false) in
  let single node = match significant node with [value] -> Some value | _ -> None in
  let latches = descendants file.root |> List.filter_map (fun node ->
    let binding = match kind node with
      | "short_var_declaration" | "assignment_statement" -> Some (field node "left", field node "right")
      | "var_spec" -> Some (field node "name", field node "value")
      | _ -> None in
    match binding with
    | Some (Some left, Some right) ->
        let value = if kind right = "expression_list" then single right else Some right in
        (match value with
         | Some value when error_channel value -> Some (text file left)
         | _ -> None)
    | _ -> None) in
  if latches = [] then [] else
  descendants file.root |> List.filter_map (fun node ->
    if kind node <> "select_statement" then None else
    let cases = significant node in
    let has_default = List.exists (fun case -> kind case = "default_case") cases in
    let sends = cases |> List.filter_map (fun case ->
      if kind case <> "communication_case" then None else
      match field case "communication" with
      | Some send when kind send = "send_statement" -> field_text file send "channel"
      | _ -> None) in
    match List.find_opt (fun channel -> List.mem channel latches) sends with
    | Some channel when has_default ->
        Some (issue file node "CS-14" (Printf.sprintf
          "%s is a hand-rolled first-error latch (make(chan error, 1) plus a non-blocking send): use context.WithCancelCause, whose first cause wins, and read <-ctx.Done() with context.Cause"
          channel))
    | _ -> None)

let handler_name name =
  let contains word =
    let rec search offset =
      offset + String.length word <= String.length name &&
      (String.sub name offset (String.length word) = word || search (offset + 1)) in
    search 0 in
  contains "handler" || contains "middleware"

let handler_file path =
  let parts = String.split_on_char '/' (String.lowercase_ascii path) in
  List.exists (fun component -> List.mem component ["handler"; "handlers"; "middleware"; "middlewares"]) parts ||
  handler_name (List.hd (List.rev parts))

let storage_name name =
  let name = String.lowercase_ascii name in
  List.mem name ["store"; "queries"; "db"; "database"; "pool";
    "tx"; "transaction"; "conn"; "connection"; "repository"; "repo"] ||
  String.ends_with ~suffix:"store" name || String.ends_with ~suffix:"queries" name ||
  String.ends_with ~suffix:"repository" name

let rec receiver_segments (file : Source.file) node = match kind node with
  | "identifier" -> [text file node]
  | "selector_expression" ->
      Option.fold ~none:[] ~some:(receiver_segments file) (field node "operand") @
      Option.to_list (field_text file node "field")
  | "call_expression" ->
      (match field node "function" with
       | Some callee when kind callee = "selector_expression" ->
           Option.fold ~none:[] ~some:(receiver_segments file) (field callee "operand")
       | Some callee -> receiver_segments file callee
       | None -> [])
  | "parenthesized_expression" | "unary_expression" | "index_expression" ->
      significant node |> List.concat_map (receiver_segments file)
  | _ -> []

let path_leaf path = List.rev (String.split_on_char '/' path) |> List.hd
let path_is_pgx path =
  path = "github.com/jackc/pgx/v5" ||
  String.starts_with ~prefix:"github.com/jackc/pgx/v5/" path
let path_is_store path =
  let leaf = path_leaf path |> String.lowercase_ascii in
  leaf = "store" || leaf = "sqlc" || leaf = "db" || String.ends_with ~suffix:"db" leaf

let local_type_aliases (file : Source.file) =
  declarations file |> List.filter_map (fun declaration ->
    match field declaration "type" with
    | Some typ when kind declaration = "type_alias" -> Some (named file declaration, typ)
    | _ -> None)

let rec handle_type (file : Source.file) imports aliases seen node =
  let node = Option.value (without_pointer node) ~default:node in
  let spelling = text file node in
  let names = String.split_on_char '.' spelling in
  match names with
  | [qualifier; name] ->
      let path = List.assoc_opt qualifier imports in
      let name = String.lowercase_ascii name in
      Option.fold ~none:false ~some:(fun path ->
        (path = "database/sql" && List.mem name ["db"; "tx"; "conn"; "stmt"]) ||
        (path_is_pgx path &&
          ((path = "github.com/jackc/pgx/v5" && List.mem name ["conn"; "tx"; "rows"]) ||
           (path_leaf path = "pgxpool" && List.mem name ["pool"; "conn"]))) ||
        (path_is_store path && List.mem name ["queries"; "querier"; "dbtx"; "db"; "store"; "postgresstore"]) ||
        (String.ends_with ~suffix:"/store" path &&
          (String.ends_with ~suffix:"store" name || List.mem name ["querier"; "dbtx"]))
      ) path
  | [name] ->
      let lowered = String.lowercase_ascii name in
      if List.mem lowered seen then false
      else (match List.assoc_opt name aliases with
        | Some alias -> handle_type file imports aliases (lowered :: seen) alias
        | None ->
            List.mem lowered ["db"; "tx"; "conn"; "queries"; "querier"; "dbtx"] ||
            String.ends_with ~suffix:"store" lowered)
  | _ -> false

let binding_names (file : Source.file) declaration typ =
  let type_start = Node.start_byte typ in
  descendants declaration
  |> List.filter (fun child -> kind child = "identifier" && Node.start_byte child < type_start)
  |> List.map (text file)

let parameter_bindings (file : Source.file) imports aliases node =
  parameters node |> List.concat_map (fun parameter ->
    match field parameter "type" with
    | Some typ when handle_type file imports aliases [] typ ->
        binding_names file parameter typ
    | _ -> [])

let handler_receiver_type (file : Source.file) function_node =
  match field function_node "receiver" with
  | None -> None
  | Some receiver ->
      parameters receiver |> List.find_map (fun parameter ->
        Option.map (fun typ ->
          String.split_on_char '.' (text file typ) |> List.rev |> List.hd
          |> String.split_on_char '*' |> List.rev |> List.hd) (field parameter "type"))

let receiver_handle_fields (file : Source.file) imports aliases function_node =
  let receiver_type = handler_receiver_type file function_node in
  Option.fold ~none:[] ~some:(fun wanted ->
    declarations file |> List.concat_map (fun declaration ->
      if named file declaration <> wanted then [] else
      match field declaration "type" with
      | Some typ when kind typ = "struct_type" ->
          descendants typ |> List.filter (fun member -> kind member = "field_declaration")
          |> List.filter_map (fun member ->
            match field member "type" with
            | Some field_type when handle_type file imports aliases [] field_type ->
                Some (member, text file field_type)
            | _ -> None)
      | _ -> [])) receiver_type

let http_type (file : Source.file) imports node =
  let spelling = text file (Option.value (without_pointer node) ~default:node) in
  match String.split_on_char '.' spelling with
  | [qualifier; name] ->
      (match List.assoc_opt qualifier imports with
       | Some "net/http" -> List.mem name ["Request"; "ResponseWriter"; "Handler"; "HandlerFunc"]
       | Some "github.com/gin-gonic/gin" -> List.mem name ["Context"; "HandlerFunc"]
       | _ -> false)
  | _ -> false

let signature_types function_node =
  let parameters = Option.fold ~none:[] ~some:parameter_types (field function_node "parameters") in
  parameters @ result_types function_node

let strict_operation (file : Source.file) function_node =
  let parameters = Option.fold ~none:[] ~some:parameter_types (field function_node "parameters") in
  let has_suffix suffix typ =
    descendants typ |> List.exists (fun child -> List.mem (kind child) ["identifier"; "type_identifier"] &&
      String.ends_with ~suffix (text file child)) in
  List.exists (has_suffix "RequestObject") parameters &&
  List.exists (has_suffix "ResponseObject") (result_types function_node)

let handler_receiver (file : Source.file) function_node =
  match field function_node "receiver" with
  | None -> false
  | Some receiver ->
      parameters receiver |> List.exists (fun parameter ->
        match field parameter "type" with
        | None -> false
        | Some typ -> handler_name (String.lowercase_ascii (text file typ)))

let handler_scope (file : Source.file) imports function_node =
  handler_file file.path || handler_receiver file function_node ||
  strict_operation file function_node ||
  List.exists (http_type file imports) (signature_types function_node)

let scope_nodes body =
  let rec visit node =
    node :: (significant node |> List.concat_map visit) in
  significant body |> List.concat_map visit

let expression_list node =
  if kind node = "expression_list" then significant node else [node]

let direct_storage_call (file : Source.file) imports call =
  match field call "function" with
  | Some callee when kind callee = "selector_expression" ->
      (match field callee "operand", field_text file callee "field" with
       | Some operand, Some method_name when kind operand = "identifier" ->
           (match List.assoc_opt (text file operand) imports with
            | Some path when path = "database/sql" -> List.mem method_name ["Open"; "OpenDB"]
            | Some path when path_is_pgx path ->
                List.mem method_name ["Connect"; "ConnectConfig"; "New"; "NewWithConfig"]
            | Some path when path_is_store path -> List.mem method_name ["New"; "NewStore"; "NewPostgresStore"]
            | _ -> false)
       | _ -> false)
  | _ -> false

let storage_receiver file imports handle_names receiver =
  let names = receiver_segments file receiver in
  let is_package name = List.mem_assoc name imports in
  List.exists (fun name -> not (is_package name) && storage_name name) names ||
  List.exists (fun name -> List.mem name handle_names) names

let handler_db_io (file : Source.file) =
  if is_test file then [] else
  let imports = Source.imports file in
  let aliases = local_type_aliases file in
  let function_nodes = descendants file.root |> List.filter (fun node ->
    List.mem (kind node) ["function_declaration"; "method_declaration"; "func_literal"]) in
  let explicit_scopes = function_nodes |> List.filter (handler_scope file imports) in
  let contains_function outer inner =
    match field outer "body" with
    | Some body -> Node.start_byte body <= Node.start_byte inner && Node.end_byte inner <= Node.end_byte body
    | None -> false in
  let scope_roots = explicit_scopes |> List.filter (fun function_node ->
    not (List.exists (fun outer -> outer != function_node && contains_function outer function_node) explicit_scopes)) in
  scope_roots |> List.concat_map (fun function_node ->
    let body = field function_node "body" in
    let nodes = Option.fold ~none:[] ~some:scope_nodes body in
    let functions = function_node :: (nodes |> List.filter (fun node ->
      List.mem (kind node) ["func_literal"; "function_declaration"; "method_declaration"])) in
    let signature_handle_types = functions |> List.concat_map signature_types
      |> List.filter (handle_type file imports aliases []) in
    let receiver_fields = receiver_handle_fields file imports aliases function_node in
    let receiver_field_names = List.map (fun (member, _) ->
      descendants member |> List.find_opt (fun child -> kind child = "identifier")
      |> Option.map (text file) |> Option.value ~default:"") receiver_fields in
    let typed_names =
      let parameters = functions |> List.concat_map (fun nested ->
        Option.fold ~none:[] ~some:(parameter_bindings file imports aliases) (field nested "parameters") @
        Option.fold ~none:[] ~some:(parameter_bindings file imports aliases) (field nested "receiver")) in
      let locals = nodes |> List.filter (fun node -> kind node = "var_spec") |> List.concat_map (fun declaration ->
        match field declaration "type" with
        | Some typ when handle_type file imports aliases [] typ ->
            binding_names file declaration typ
        | _ -> []) in
      parameters @ receiver_field_names @ locals in
    let rec alias_names names =
      let additions = nodes |> List.filter_map (fun node ->
        let left, right = match kind node with
          | "short_var_declaration" | "assignment_statement" -> field node "left", field node "right"
          | "var_spec" -> field node "name", field node "value"
          | _ -> None, None in
        match left, right with
        | Some left, Some right ->
            let right_values = expression_list right in
            (match right_values with
             | [value] when direct_storage_call file imports value ||
                List.exists (fun expression -> storage_receiver file imports names expression)
                  (descendants value |> List.filter (fun child -> kind child = "selector_expression")) ->
                 let left_names = descendants left |> List.filter (fun child -> kind child = "identifier") |> List.map (text file) in
                 (match left_names with first :: _ when not (List.mem first names) -> Some first | _ -> None)
             | _ -> None)
        | _ -> None) in
      let expanded = List.fold_left (fun acc name -> if List.mem name acc then acc else name :: acc) names additions in
      if List.length expanded = List.length names then names else alias_names expanded in
    let handle_names = alias_names typed_names in
    let handle_findings =
      let signature_findings = signature_handle_types |> List.map (fun typ ->
        issue file function_node "HANDLER-DB-IO"
          (Printf.sprintf "handler or middleware %s accepts or returns database handle %s: pass an operation through the service boundary"
            (named file function_node) (text file typ))) in
      let receiver_findings = receiver_fields |> List.map (fun (member, typ) ->
        issue file member "HANDLER-DB-IO"
          (Printf.sprintf "handler receiver %s stores database handle %s: keep the handle behind its service"
            (Option.value (handler_receiver_type file function_node) ~default:"<unknown>") typ)) in
      let local_findings = nodes |> List.filter (fun node -> kind node = "var_spec") |> List.filter_map (fun node ->
        match field node "type" with
        | Some typ when handle_type file imports aliases [] typ ->
            Some (issue file node "HANDLER-DB-IO"
              (Printf.sprintf "handler or middleware %s declares database handle %s: pass an operation through the service boundary"
                (named file function_node) (text file typ)))
        | _ -> None) in
      signature_findings @ receiver_findings @ local_findings in
    let call_findings = nodes |> List.filter (fun node -> kind node = "call_expression") |> List.filter_map (fun call ->
      if direct_storage_call file imports call then
        Some (issue file call "HANDLER-DB-IO"
          (Printf.sprintf "handler or middleware %s opens or constructs a database/store handle: move persistence setup behind the service boundary"
            (named file function_node)))
      else match field call "function" with
        | Some callee when kind callee = "selector_expression" ->
            (match field callee "operand" with
             | Some receiver when storage_receiver file imports handle_names receiver ->
                 Some (issue file call "HANDLER-DB-IO"
                   (Printf.sprintf "handler or middleware %s calls %s through a database/store handle: call a service operation instead"
                     (named file function_node) (Option.value (field_text file callee "field") ~default:"<unknown>")))
             | _ -> None)
        | _ -> None) in
    handle_findings @ call_findings)

let rules = [mutexes; dispatches; erased_boundaries; sleep_loops; constructors; magic_strings; null_twins; first_error_latches]
let collect files =
  (* A store name alone is not evidence of database I/O: a package may own
     ordinary in-memory maps. Include sibling/generated SQLC imports when
     deciding whether the package declares a database boundary. *)
  let database_packages = files |> List.filter (fun file ->
    not (is_test file) && (Source.imports file |> List.exists (fun (_, path) ->
      path = "database/sql" || path_is_pgx path || path_is_store path)))
    |> List.map (fun (file : Source.file) -> Filename.dirname file.path)
    |> List.sort_uniq String.compare in
  files |> List.filter selected |> List.concat_map (fun file ->
    let ordinary = rules |> List.concat_map (fun rule -> rule file) in
    if List.mem (Filename.dirname file.path) database_packages then ordinary @ handler_db_io file else ordinary)
