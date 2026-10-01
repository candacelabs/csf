(* Protobuf owns RPCs and Go packages; OpenAPI owns transport schemas. *)
open Yojson.Safe.Util

let fail format = Printf.ksprintf failwith format
let field name json = member name json
let string name json = field name json |> to_string
let list name json = match field name json with `Null -> [] | value -> to_list value
let optional_string name json = match field name json with `Null -> "" | value -> to_string value
let assoc json = to_assoc json
let sorted_fields json = assoc json |> List.sort (fun (a, _) (b, _) -> String.compare a b)
let quote value = Yojson.Safe.to_string (`String value)
let last_name value = String.split_on_char '.' value |> List.rev |> List.hd

let rec canonical = function
  | `Assoc fields -> `Assoc (List.map (fun (key, value) -> key, canonical value)
      (List.sort (fun (a, _) (b, _) -> String.compare a b) fields))
  | `List values -> `List (List.map canonical values)
  | value -> value

let json_string value = Yojson.Safe.to_string (canonical value)

let validate_operation method_ =
  let name = string "name" method_ in
  let request = last_name (string "inputType" method_) in
  let response = last_name (string "outputType" method_) in
  if request <> name ^ "Request" || response <> name ^ "Response" then
    fail "RPCs require operation-specific *Request/*Response messages: %s: %s -> %s" name request response

let schema_for_mcp schema components =
  let required = Hashtbl.create 16 in
  let prefix = "#/components/schemas/" in
  let rec collect = function
    | `Assoc fields as value ->
        (match field "$ref" value with
        | `String reference when String.starts_with ~prefix reference ->
            let name = String.sub reference (String.length prefix) (String.length reference - String.length prefix) in
            if not (Hashtbl.mem required name) then (
              let definition = match List.assoc_opt name (assoc components) with
                | Some value -> value | None -> fail "missing referenced schema %s" name in
              Hashtbl.add required name definition;
              collect definition)
        | _ -> ());
        List.iter (fun (_, value) -> collect value) fields
    | `List values -> List.iter collect values
    | _ -> () in
  collect schema;
  let schema = if Hashtbl.length required = 0 then schema else
      `Assoc (assoc schema @ ["$defs", `Assoc (Hashtbl.fold (fun key value acc -> (key, value) :: acc) required [])]) in
  let rec rewrite = function
    | `Assoc fields -> `Assoc (List.map (fun (key, value) ->
        key, match key, value with
        | "$ref", `String reference when String.starts_with ~prefix reference ->
            `String ("#/$defs/" ^ String.sub reference (String.length prefix) (String.length reference - String.length prefix))
        | _ -> rewrite value) fields)
    | `List values -> `List (List.map rewrite values)
    | value -> value in
  rewrite schema

type operation = {
  name : string; request : string; response : string; verb : string;
  path : string; comment : string; schema : Yojson.Safe.t;
}

let messages files =
  List.concat_map (fun file -> List.map (fun message ->
    "." ^ string "package" file ^ "." ^ string "name" message, (file, message))
    (list "messageType" file)) files

let package_path file =
  let package = optional_string "goPackage" (field "options" file) in
  let path = List.hd (String.split_on_char ';' package) in
  if path = "" then fail "RPC message owner requires go_package";
  path

let go_types files methods =
  let messages = messages files in
  let required = List.concat_map (fun method_ -> [string "inputType" method_; string "outputType" method_]) methods
    |> List.sort_uniq String.compare in
  let owner name = match List.assoc_opt name messages with
    | Some value -> value | None -> fail "RPC message not found: %s" name in
  let paths = List.map (fun name -> package_path (fst (owner name))) required |> List.sort_uniq String.compare in
  let imports = List.mapi (fun index path -> path, "contract" ^ string_of_int index) paths in
  let types = List.map (fun name ->
    let file, message = owner name in
    name, List.assoc (package_path file) imports ^ "." ^ string "name" message) required in
  imports, types

let method_comments source methods =
  let locations = match field "sourceCodeInfo" source with
    | `Null -> [] | value -> list "location" value in
  List.mapi (fun index method_ ->
    let comment = List.find_opt (fun location ->
      field "path" location = `List [`Int 6; `Int 0; `Int 2; `Int index]) locations in
    let text = match comment with None -> "" | Some location -> optional_string "leadingComments" location in
    let words = String.map (function '\n' | '\r' | '\t' -> ' ' | ch -> ch) text
      |> String.split_on_char ' ' |> List.filter (fun word -> word <> "") in
    string "name" method_, String.concat " " words) methods

let operations descriptor api =
  let files = list "file" descriptor in
  let source = match List.find_opt (fun file -> string "name" file = "adapter.proto") files with
    | Some file -> file | None -> fail "adapter.proto descriptor required" in
  let service = match list "service" source with
    | [service] -> service | _ -> fail "exactly one adapter service required" in
  let methods = list "method" service in
  List.iter validate_operation methods;
  let imports, types = go_types files methods in
  let comments = method_comments source methods in
  let remaining = Hashtbl.create 32 in
  List.iter (fun method_ -> Hashtbl.add remaining (string "name" service ^ "_" ^ string "name" method_) method_) methods;
  let all_messages = messages files in
  let components = field "schemas" (field "components" api) in
  let entries = List.concat_map (fun (path, routes) ->
    List.map (fun (verb, route) ->
      let operation_id = string "operationId" route in
      let method_ = match Hashtbl.find_opt remaining operation_id with
        | Some method_ -> method_ | None -> fail "unknown or duplicate operation %s" operation_id in
      Hashtbl.remove remaining operation_id;
      let request_name = string "inputType" method_ in
      let input_schema = match verb with
        | "get" when last_name request_name = "GetSnapshotRequest" ->
            let _, message = List.assoc request_name all_messages in
            if list "field" message <> [] then fail "GET route %s requires an empty request message" path;
            `Assoc ["type", `String "object"; "properties", `Assoc []; "additionalProperties", `Bool false]
        | "post" -> field "requestBody" route |> field "content" |> field "application/json" |> field "schema"
        | _ -> fail "unsupported contract route %s %s" verb path in
      {name = string "name" method_; request = List.assoc request_name types;
       response = List.assoc (string "outputType" method_) types;
       verb = String.uppercase_ascii verb; path;
       comment = (match field "summary" route with `String value -> value
         | _ -> List.assoc (string "name" method_) comments);
       schema = schema_for_mcp input_schema components}) (sorted_fields routes))
    (sorted_fields (field "paths" api)) in
  if Hashtbl.length remaining <> 0 then fail "missing routes for %d RPCs" (Hashtbl.length remaining);
  imports, entries

let generate descriptor api =
  let imports, entries = operations descriptor api in
  let output = Buffer.create 32768 in
  let line format = Printf.ksprintf (fun value -> Buffer.add_string output (value ^ "\n")) format in
  line "// Code generated by Candacegen (csf/compiler/api_codegen). DO NOT EDIT.";
  line "package csf";
  line "import (\"context\"; \"encoding/json\"; \"fmt\"; \"strings\";";
  line "\"google.golang.org/protobuf/encoding/protojson\";";
  List.iter (fun (path, alias) -> line "%s %s;" alias (quote path)) imports;
  line ")";
  line "func (service *Service) registerOperations() {";
  List.iter (fun op ->
    line "registerOperation(service, %s, %s, %s, %s, json.RawMessage(%s), func() *%s {return &%s{}}, service.%s)"
      (quote op.name) (quote op.verb) (quote op.path) (quote op.comment)
      (quote (json_string op.schema)) op.request op.request op.name) entries;
  line "}";
  List.iter (fun op ->
    line "func (client *Client) %s(ctx context.Context, request *%s) (*%s, error) {" op.name op.request op.response;
    line "response := &%s{}" op.response;
    line "if err := client.call(ctx, %s, %s, request, response); err != nil { return nil, err }" (quote op.verb) (quote op.path);
    line "return response, nil"; line "}") entries;
  line "type HumanOperation struct { Name, Description string }";
  line "func HumanOperations() []HumanOperation { return []HumanOperation{%s} }"
    (String.concat "," (List.map (fun op -> "{Name: " ^ quote op.name ^ ", Description: " ^ quote op.comment ^ "}") entries));
  line "func CLIOperations() []string { return []string{%s} }"
    (String.concat "," (List.map (fun op -> quote op.name) entries));
  line "func (client *Client) CallOperation(ctx context.Context, operation string, input []byte) ([]byte, error) {";
  line "switch strings.ToLower(operation) {";
  List.iter (fun op ->
    line "case %s:" (quote (String.lowercase_ascii op.name));
    line "request := &%s{}" op.request;
    line "if err := protojson.Unmarshal(input, request); err != nil { return nil, err }";
    line "response, err := client.%s(ctx, request)" op.name;
    line "if err != nil { return nil, err }";
    line "return (protojson.MarshalOptions{UseProtoNames: true}).Marshal(response)") entries;
  line "default: return nil, fmt.Errorf(\"unknown operation %%q; choose %%v\", operation, CLIOperations())";
  line "}"; line "}";
  Buffer.contents output
