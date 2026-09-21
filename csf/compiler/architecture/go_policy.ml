(** Bounded Go syntax checks, not a Go type checker or an effect system.

    A selector such as [runner.Command] is matched through its file's import
    aliases. Thus [runner "os/exec"] cannot evade the gateway rule by renaming
    the import. A local variable shadowing [runner] can cause a false positive:
    resolving that distinction requires lexical scope/type information that
    this pass does not provide. Wrappers and third-party effects are likewise
    outside its coverage. The selected application programming interfaces
    (APIs) are listed explicitly in the tables below. *)
open Model
module Node = Tree_sitter.Node

let source_extension = ".go"
let test_suffix = "_test.go"
let entrypoint_name = "main"
let is_source path = Filename.check_suffix path source_extension
let is_test path = Filename.check_suffix path test_suffix

module Code = struct
  let import = "CSF_GO_IMPORT"
  let syntax = "CSF_GO_SYNTAX"
  let process_owner = "CSF_PROCESS_OWNER"
  let process_boundary = "CSF_PROCESS_BOUNDARY"
end

type context = {
  source : string;
  mutable imports : (string * string) list;
  entrypoint : bool;
  entrypoint_package : bool;
  gateway : bool;
  test : bool;
  mutable main_package : bool;
  mutable main_function : bool;
  report : Node.t -> string -> string -> unit;
}

(* These tables are the explicit coverage of the policy, not an exhaustive
   inventory of ways Go can listen, read configuration or start processes.
   Selecting a function value is checked even before a call, so assigning
   [exec.Command] to a variable does not hide that direct reference. *)
let process_apis = [
  "flag", ["Parse"];
  "os", ["Getenv"; "LookupEnv"];
  "os/signal", ["Notify"; "NotifyContext"];
  "net", ["Listen"; "ListenPacket"];
  "net/http", ["ListenAndServe"; "ListenAndServeTLS"; "NewServeMux"];
  "github.com/gin-gonic/gin", ["New"; "Default"];
  "github.com/candace-server/pkg/httpserver", ["NewEngine"; "NewStreamingServer"; "Serve"];
]

let child_apis = [
  "os/exec", ["Command"; "CommandContext"];
  "os", ["StartProcess"];
  "syscall", ["Exec"; "ForkExec"; "StartProcess"];
  "github.com/creack/pty", ["Start"; "StartWithSize"; "StartWithAttrs"];
]

let is_api table package member =
  match List.assoc_opt package table with
  | Some names -> List.mem member names
  | None -> false

let report_diagnostic path diagnostics node code message =
  let position = Node.start_point node in
  diagnostics := { at = {file = path; line = position.row + 1; column = position.column + 1};
    code; message } :: !diagnostics

let field context node name =
  Option.map (Checker.node_source context.source) (Node.child_by_field_name node name)

(* Dot imports remove the alias needed to identify these selected APIs, so
   boundary packages must use a named import. Other imports are left to Go. *)
let inspect_import context node =
  match field context node "path" with
  | None -> context.report node Code.import "import has no path"
  | Some literal ->
      let package = try Checker.string_value literal with Invalid_argument message ->
        context.report node Code.import message; "" in
      let alias = Option.value (field context node "name") ~default:(Filename.basename package) in
      if alias = "." && (List.mem_assoc package process_apis || List.mem_assoc package child_apis) then
        context.report node Code.import "boundary APIs require a named import";
      context.imports <- (alias, package) :: context.imports

let inspect_syntax_and_imports context node =
  if Node.is_error node || Node.is_missing node then
    context.report node Code.syntax "upstream Go grammar could not parse source";
  if Node.kind node = "import_spec" then inspect_import context node

let imported_selector context node =
  let left, right = if Node.kind node = "qualified_type"
    then "package", "name" else "operand", "field" in
  match field context node left, field context node right with
  | Some alias, Some member ->
      Option.map (fun package -> package, member) (List.assoc_opt alias context.imports)
  | _ -> None

(* Recognize both [&http.Server{}] and [new(http.Server)] syntactically.
   This does not resolve aliases of the Server type or inspect constructors. *)
let allocated_server context node =
  let target = match Node.kind node with
    | "composite_literal" -> Node.child_by_field_name node "type"
    | "call_expression" when field context node "function" = Some "new" ->
        Option.bind (Node.child_by_field_name node "arguments") (fun args -> Node.named_child args 0)
    | _ -> None in
  Option.bind target (imported_selector context) = Some ("net/http", "Server")

let inspect_package context node =
  if Node.kind node = "package_clause" then
    match Node.named_child node 0 with
    | Some name when Checker.node_source context.source name = entrypoint_name ->
        context.main_package <- true;
        if not context.entrypoint_package then
          context.report node Code.process_owner "package main requires a declared process entrypoint"
    | _ -> ()

let inspect_process_ownership context node =
  let kind = Node.kind node in
  if kind = "function_declaration" && field context node "name" = Some entrypoint_name then begin
    context.main_function <- true;
    if not context.entrypoint then
      context.report node Code.process_owner "main must be defined in the declared entrypoint"
  end;
  if not context.entrypoint && allocated_server context node then
    context.report node Code.process_owner "HTTP server allocation belongs to the process entrypoint";
  if not context.entrypoint && kind = "selector_expression" &&
     List.mem (field context node "field") [Some "ListenAndServe"; Some "ListenAndServeTLS"] &&
     imported_selector context node = None then
    context.report node Code.process_owner "listener method belongs to the process entrypoint"

(* Tests may create process fixtures and read test environment. That narrow
   exception does not turn every test into an application entrypoint. *)
let inspect_imported_api context node =
  if Node.kind node = "selector_expression" || Node.kind node = "qualified_type" then
    match imported_selector context node with
    | Some (package, member) ->
        let test_fixture = context.test && (member = "NewEngine" ||
          (package = "os" && List.mem member ["Getenv"; "LookupEnv"])) in
        if not context.entrypoint && not test_fixture && is_api process_apis package member then
          context.report node Code.process_owner (package ^ "." ^ member ^ " belongs to the process entrypoint");
        if not context.gateway && not context.test && is_api child_apis package member then
          context.report node Code.process_boundary (package ^ "." ^ member ^ " requires a declared gateway source")
    | None -> ()

let inspect_policy context node =
  inspect_package context node;
  inspect_process_ownership context node;
  inspect_imported_api context node

(* Collect aliases before inspecting references, rather than making the verdict
   depend on syntax tree traversal order. The second walk uses the original source
   bytes even if Go_syntax adapted its parser copy for new(expression). *)
let inspect ~entrypoint ~entrypoint_package ~gateway ~test path source =
  let root = Go_syntax.parse source in
  let diagnostics = ref [] in
  let context = {
    source; imports = []; entrypoint; entrypoint_package; gateway; test;
    main_package = false; main_function = false;
    report = report_diagnostic path diagnostics;
  } in
  Checker.walk (inspect_syntax_and_imports context) root;
  Checker.walk (inspect_policy context) root;
  if entrypoint && not (context.main_package && context.main_function) then
    context.report root Code.process_owner "Go entrypoint requires package main and a main function declaration";
  List.rev !diagnostics
