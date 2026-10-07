(* Copyright 2026 Candace Labs *)

open Source

(* Placement rules from the CSF ontology (csf/compiler/language/
   architecture.csf, term `placement`). CS-15, CS-16 and ONTOLOGY-DIRS are
   advisory locators: their retrofit backlog is the repository as it stood
   before runtime/ and ipc/ existed, and a finding names a site
   to move. CS-16-DB blocks: it read 0, tests included, when it was split out.
   Test files are in scope for both CS-16 parts: a test crosses a boundary
   through the same capability as production, or uses a gomock double or pgmem.

   CS-15     a goroutine with no visible owner: nothing joins it and nothing
             cancels it. Services may start goroutines; the starter owns cleanup.
   CS-16     network listen/dial and gRPC client creation only under io/net;
             fork/exec (os/exec, the PTY starter, os.StartProcess and the
             syscall fork/exec calls) only under io/ipc/proc.
   CS-16-DB  PostgreSQL pool and connection creation only under
             io/ipc/db/csfpg.
   CS-17     the process environment is read only by the config capability
             (runtime/config) or a binary's own app/<name>/config.
   ATOMIC-WRITE  a file is replaced whole only through pkg/atomicfile; it
             blocks, tests included.
   ONTOLOGY-DIRS  directories are named by ontology terms;
          `internal` is a transparent visibility marker. *)

(* The crossing mechanisms: io/net reaches another machine, io/ipc another
   process here and io/kernel a syscall here. Each owns the transport it
   crosses with, so a socket or gRPC call inside one is that mechanism's own
   crossing, not a consumer reaching around it; io/inproc crosses nothing. *)
let mechanism_roots = ["io/net/"; "io/ipc/"; "io/kernel/"]
let csfpg_root = "io/ipc/db/csfpg/"
let under prefix path = String.starts_with ~prefix path

(* CS-15 (operator, 2026-10-01): "go routines do not start only in runtime
   services can start goroutines, they just have to be responsible for cleanup
   and, if you have inter-service things, then YOU'RE responsible for
   lifecycle management". The locator reports a `go` statement whose goroutine
   shows no owner in its enclosing function or method:
   - no join there: no `.Wait()` call (WaitGroup, errgroup, conc, a runtime
     scope) and no receive or range over a channel the goroutine sends on or
     closes;
   - and no context-driven exit: the goroutine's call mentions no `.Done()` or
     `.Err()` selector and no context value (an identifier named ctx, or
     ending in ctx, Ctx or Context).
   This is syntax, so it is a locator: ownership can live elsewhere, and a
   finding is answered at its site. *)
let significant node = children node |> List.filter (fun child -> kind child <> "comment")

let within outer inner =
  Node.start_byte outer <= Node.start_byte inner && Node.end_byte inner <= Node.end_byte outer

let context_name name =
  name = "ctx" || List.exists (fun suffix -> String.ends_with ~suffix name) ["ctx"; "Ctx"; "Context"]

let identifier_text (file : Source.file) = function
  | Some node when kind node = "identifier" -> Some (text file node)
  | _ -> None

let signalled_channels (file : Source.file) goroutine =
  descendants goroutine |> List.filter_map (fun node ->
    match kind node with
    | "send_statement" -> identifier_text file (field node "channel")
    | "call_expression" when field_text file node "function" = Some "close" ->
        (match Option.map significant (field node "arguments") with
         | Some [argument] -> identifier_text file (Some argument)
         | _ -> None)
    | _ -> None)

let received_channels (file : Source.file) nodes =
  nodes |> List.concat_map (fun node ->
    match kind node with
    | "unary_expression" when String.starts_with ~prefix:"<-" (text file node) ->
        Option.to_list (identifier_text file (field node "operand"))
    | "range_clause" -> Option.to_list (identifier_text file (field node "right"))
    (* Handed to a waiter: Eventually(done), or a helper that drains it. *)
    (* Returned to the caller, who owns the wait. *)
    | "return_statement" ->
        significant node |> List.concat_map (fun result ->
          if kind result = "expression_list" then significant result else [result])
        |> List.filter_map (fun result -> identifier_text file (Some result))
    | "call_expression" ->
        (match field node "arguments" with
         | Some arguments -> significant arguments |> List.filter_map (fun argument -> identifier_text file (Some argument))
         | None -> [])
    | _ -> [])

let owned (file : Source.file) body goroutine =
  let outside = descendants body |> List.filter (fun node -> not (within goroutine node)) in
  let joins = outside |> List.exists (fun node ->
    kind node = "call_expression" &&
    (match field node "function" with
     | Some callee when kind callee = "selector_expression" -> field_text file callee "field" = Some "Wait"
     | _ -> false)) in
  let cancellable = descendants goroutine |> List.exists (fun node ->
    (kind node = "selector_expression" &&
      List.mem (Option.value (field_text file node "field") ~default:"") ["Done"; "Err"]) ||
    (kind node = "identifier" && context_name (text file node))) in
  let received = received_channels file outside in
  joins || cancellable || List.exists (fun channel -> List.mem channel received) (signalled_channels file goroutine)

(* The function a goroutine is started from: the smallest function
   declaration, method or function literal around the go statement, other
   than the goroutine's own literal. Specs start goroutines inside the
   literals they hand to Describe and It, so literals count. *)
let enclosing_body (file : Source.file) goroutine =
  descendants file.root
  |> List.filter (fun node ->
    List.mem (kind node) ["function_declaration"; "method_declaration"; "func_literal"] &&
    within node goroutine && not (within goroutine node))
  |> List.filter_map (fun node -> field node "body")
  |> List.fold_left (fun smallest body -> match smallest with
    | Some current when Node.end_byte current - Node.start_byte current <= Node.end_byte body - Node.start_byte body -> smallest
    | _ -> Some body) None

let goroutine_starts (file : Source.file) =
  (* Tests are held to the same rule (operator, 2026-10-01: "WHY THE FUCK
     DOES THE GATE EXEMPT TESTS"): a goroutine a spec starts is joined too. *)
  descendants file.root |> List.filter_map (fun node ->
    if kind node <> "go_statement" then None else
    match enclosing_body file node with
    | Some body when owned file body node -> None
    | _ ->
        Some (issue file node "CS-15"
          "goroutine with no visible owner: nothing here joins it (Wait, or a receive on a channel it signals) and it has no context-driven exit; whoever starts a goroutine is responsible for its cleanup"))

let network_calls = [
  "net", ["Listen"; "ListenPacket"; "ListenTCP"; "ListenUDP"; "ListenUnix"; "ListenUnixgram";
          "ListenIP"; "ListenMulticastUDP"; "Dial"; "DialTimeout"; "DialTCP"; "DialUDP";
          "DialUnix"; "DialIP"];
  "net/http", ["ListenAndServe"; "ListenAndServeTLS"; "Serve"; "ServeTLS"];
  "google.golang.org/grpc", ["NewClient"; "Dial"; "DialContext"];
]
let boundary_types = ["net", ["Dialer"; "ListenConfig"]]

(* A pool or connection to PostgreSQL is a kernel I/O crossing owned by one
   package, narrower than io/ as a whole. *)
let database_calls = [
  "github.com/jackc/pgx/v5/pgxpool", ["New"; "NewWithConfig"];
  "github.com/jackc/pgx/v5", ["Connect"; "ConnectConfig"];
]

let qualified imports spelling = match String.split_on_char '.' spelling with
  | [alias; name] -> Option.map (fun path -> path, name) (List.assoc_opt alias imports)
  | _ -> None

let member table (path, name) =
  List.mem name (Option.value (List.assoc_opt path table) ~default:[])

let boundary_crossings (file : Source.file) =
  (* Tests are in scope for both parts: a spec reaches a socket through io/net
     and a database through csfpg, or uses a gomock double or pgmem (operator,
     2026-10-01: "WHY THE FUCK DOES THE GATE EXEMPT TESTS"). *)
  let network = not (List.exists (fun prefix -> under prefix file.path) mechanism_roots)
                and database = not (under csfpg_root file.path) in
  let imports = Source.imports file in
  let report node what = issue file node "CS-16"
    (what ^ " outside the io/ crossing mechanisms: take the socket or gRPC capability (io/net, io/net/http, io/net/grpc) in the constructor") in
  let report_database node what = issue file node "CS-16-DB"
    (what ^ " outside io/ipc/db/csfpg: the binary opens the pool with csfpg.OpenPool and a service takes csfpg.IDB in its constructor") in
  descendants file.root |> List.filter_map (fun node ->
    match kind node with
    | "call_expression" ->
        (match Option.bind (field_text file node "function") (qualified imports) with
         | Some (path, name) when network && member network_calls (path, name) -> Some (report node (path ^ "." ^ name))
         | Some (path, name) when database && member database_calls (path, name) -> Some (report_database node (path ^ "." ^ name))
         | _ -> None)
    | "composite_literal" ->
        (match Option.bind (field_text file node "type") (qualified imports) with
         | Some (path, name) when network && member boundary_types (path, name) -> Some (report node (path ^ "." ^ name ^ " literal"))
         | _ -> None)
    | _ -> None)

(* CS-16, process part: crossing into another address space happens only in
   the one subprocess gateway, io/ipc/proc. Importing os/exec or the PTY
   starter is the crossing (exec.Cmd values, LookPath and ExitError included),
   and the lower-level fork/exec calls are matched where they are spelled.
   Tests are not exempt (operator, 2026-10-01: "tests are not exempt from
   the capability gates"): a consumer's spec doubles ILauncher with gomock,
   and only the gateway's own package starts real children. *)
let proc_root = "io/ipc/proc/"
let process_imports = ["os/exec"; "github.com/creack/pty"]
let process_calls = ["os", ["StartProcess"]; "syscall", ["Exec"; "ForkExec"; "StartProcess"]]

let process_crossings (file : Source.file) =
  if under proc_root file.path then [] else
  let imports = Source.imports file in
  let report node what = issue file node "CS-16"
    (what ^ " outside io/ipc/proc: take the process capability (proc.ILauncher) in the constructor") in
  descendants file.root |> List.filter_map (fun node ->
    match kind node with
    | "import_spec" ->
        (match Option.map Checker.string_value (field_text file node "path") with
         | Some path when List.mem path process_imports -> Some (report node ("import of " ^ path))
         | _ -> None)
    | "call_expression" ->
        (match Option.bind (field_text file node "function") (qualified imports) with
         | Some (path, name) when member process_calls (path, name) -> Some (report node (path ^ "." ^ name))
         | _ -> None)
    | _ -> None)

(* CS-17. Configuration is a capability: a binary obtains
   runtimeconfig.OSEnvironment() once, in its own config package, and hands
   values to what it constructs. The capability itself and each app/ binary's
   config package are the only readers of the process environment. Tests are
   not exempt (operator, 2026-10-01: tests are not exempt from the capability
   gates): a spec hands the code under test a runtimeconfig.NewEnvironment
   over a map, and sets real variables with GinkgoT().Setenv, which writes. *)
let config_root = "runtime/config/"
let environment_calls = ["os", ["Getenv"; "LookupEnv"; "Environ"]]

let binary_config path = match String.split_on_char '/' path with
  | "app" :: _ :: "config" :: _ -> true
  | _ -> false

let environment_reads (file : Source.file) =
  if under config_root file.path || binary_config file.path then [] else
  let imports = Source.imports file in
  (* The selector, not the call: os.LookupEnv handed to a lookup parameter is
     the same read, one indirection later. *)
  descendants file.root |> List.filter_map (fun node ->
    if kind node <> "selector_expression" then None else
    match qualified imports (text file node) with
    | Some (path, name) when member environment_calls (path, name) ->
        Some (issue file node "CS-17" (path ^ "." ^ name ^
          " outside the config capability: read the environment through runtime/config in the binary's config package and pass values to constructors"))
    | _ -> None)

(* ATOMIC-WRITE (operator, 2026-10-05, reading writeAtomically: "WHAT A
   MISSED OPPORTUNITY TO WRITE A BEAUTIFUL OPINIONATED ATOMICS LIBRARY OR
   CHOOSE ONE OFF THE SHELF INSTEAD OF AN UNEXPORTED PIECE OF SHIT
   UNNECESSARY HELPER"). A file is replaced whole only through
   pkg/atomicfile. A function that creates or writes a file and also calls
   os.Rename is a hand-rolled copy; 21 were migrated when the rule landed,
   most without an fsync and several on a fixed .tmp name two writers share.
   A rename with no write beside it is a move (a directory installed, a
   binary swapped back) and passes. Tests are in scope: a spec replaces a
   fixture through the same primitive as production. *)
let atomic_write_root = "pkg/atomicfile/"
let file_writes = ["os", ["WriteFile"; "Create"; "CreateTemp"; "OpenFile"]]
let renames = ["os", ["Rename"]]

let atomic_writes (file : Source.file) =
  if under atomic_write_root file.path then [] else
  let imports = Source.imports file in
  let calls table node = kind node = "call_expression" &&
    (match Option.bind (field_text file node "function") (qualified imports) with
     | Some call -> member table call
     | None -> false) in
  descendants file.root |> List.filter_map (fun node ->
    if not (calls renames node) then None else
    match enclosing_body file node with
    | Some body when List.exists (calls file_writes) (descendants body) ->
        Some (issue file node "ATOMIC-WRITE"
          "file written and renamed into place by hand: use atomicfile.WriteFile(path, content, mode) from pkg/atomicfile (unique temporary beside the target, fsync, rename, directory fsync, exact mode)")
    | _ -> None)

let collect files = files |> List.filter selected |> List.concat_map (fun file ->
  goroutine_starts file @ boundary_crossings file @ process_crossings file @ environment_reads file @
  atomic_writes file)

(* CS-20, one runtime per application (ticket #527): the process and runtime
   boundaries of an application, counted statically. Four rows, each its own
   policy ID (advisory under the 2026-10-06 operator ruling recorded in
   policy.ml), read over selected non-test Go so they match the
   operator's meter (tests build their own runtime and are not an application's).

   CS-20-HOST     an application (app/<name>/cmd) builds exactly one
                  runtime.NewHostRuntime; a count other than 1 is the offense.
   CS-20-RUNTIME  runtime.NewHostRuntime is called only in a package-main file:
                  the one runtime of a process is the binary's own.
   CS-20-SPAWN    os/exec is imported only under io/ipc/proc/: every other
                  subprocess spawn takes proc.ILauncher in the constructor.
   CS-20-LISTEN   a socket is bound only under io/ (in the io/net tier) or in
                  a package-main file: a library serves a listener it is handed.
                  A bind is an import-resolved net.Listen* / net/http.ListenAndServe*,
                  or a `.ListenAndServe`/`.ListenAndServeTLS` method call; a
                  `net.ListenConfig` literal and a `.Listen`/`.Serve` method
                  that serves a bound listener are not binds. *)
let host_runtime_root = "github.com/candacelabs/csf/runtime"

let host_runtime_calls (file : Source.file) =
  let imports = Source.imports file in
  descendants file.root |> List.filter (fun node ->
    kind node = "call_expression" &&
    match Option.bind (field_text file node "function") (qualified imports) with
    | Some (path, name) -> path = host_runtime_root && name = "NewHostRuntime"
    | None -> false)

let runtime_only_in_binary (file : Source.file) =
  if is_test file || package file = Some "main" then [] else
  host_runtime_calls file |> List.map (fun node ->
    issue file node "CS-20-RUNTIME"
      "runtime.NewHostRuntime outside a package-main file: the one host runtime of a process is built in its binary's main package")

let spawn_through_proc (file : Source.file) =
  if is_test file || under proc_root file.path then [] else
  descendants file.root |> List.filter_map (fun node ->
    if kind node <> "import_spec" then None else
    match Option.map Checker.string_value (field_text file node "path") with
    | Some "os/exec" ->
        Some (issue file node "CS-20-SPAWN"
          "import of os/exec outside io/ipc/proc: route subprocess spawn through proc.ILauncher, taken in the constructor")
    | _ -> None)

let listen_calls = [
  "net", ["Listen"; "ListenPacket"; "ListenTCP"; "ListenUDP"; "ListenUnix"; "ListenUnixgram";
          "ListenIP"; "ListenMulticastUDP"];
  "net/http", ["ListenAndServe"; "ListenAndServeTLS"];
]
let listen_methods = ["ListenAndServe"; "ListenAndServeTLS"]
let listen_tiers = ["io/"]

let listens (file : Source.file) =
  if is_test file || package file = Some "main" ||
     List.exists (fun prefix -> under prefix file.path) listen_tiers then []
  else
  let imports = Source.imports file in
  descendants file.root |> List.filter_map (fun node ->
    if kind node <> "call_expression" then None else
    match field node "function" with
    | Some callee when kind callee = "selector_expression" ->
        (match qualified imports (text file callee) with
         | Some (path, name) when member listen_calls (path, name) ->
             Some (issue file node "CS-20-LISTEN" (path ^ "." ^ name ^
               " binds a socket outside io/ and outside a package-main file: bind through io/net, or let the binary's main package own the listener"))
         | Some _ -> None
         | None ->
             (match field_text file callee "field" with
              | Some field when List.mem field listen_methods ->
                  Some (issue file node "CS-20-LISTEN" ("." ^ field ^
                    " binds a socket outside io/ and outside a package-main file: serve a listener the caller bound through io/net"))
              | _ -> None))
    | _ -> None)

let application path = match String.split_on_char '/' path with
  | "app" :: name :: "cmd" :: _ -> Some ("app/" ^ name ^ "/cmd")
  | _ -> None

let consistency_rows files =
  let sources = files |> List.filter selected |> List.filter (fun file -> not (is_test file)) in
  let applications = sources |> List.filter_map (fun (file : Source.file) -> application file.path)
    |> List.sort_uniq String.compare in
  let host_row = applications |> List.filter_map (fun name ->
    let count = sources |> List.filter (fun (file : Source.file) -> application file.path = Some name)
      |> List.concat_map host_runtime_calls |> List.length in
    if count = 1 then None else
    Some { path = name; line = 1; rule = "CS-20-HOST";
      message = Printf.sprintf
        "application %s builds %d host runtimes; exactly one runtime.NewHostRuntime is required" name count }) in
  host_row @ (sources |> List.concat_map runtime_only_in_binary) @
  (sources |> List.concat_map spawn_through_proc) @ (sources |> List.concat_map listens)

(* ONTOLOGY-DIRS. The terms are read from the ontology source itself, so the
   rule cannot drift from the dictionary it enforces. A directory name matches
   a term by its identifier or the identifier's plural (services, widgets).
   Only role positions are checked: the first directory under the repository root, and
   every directory below the layered roots (ipc, runtime, web), whose children
   are themselves ontology terms. A service, package or app below its root is
   named by what it does. Catch-all names are reported at any depth. *)
let ontology_source = "csf/compiler/language/architecture.csf"
let layered_roots = ["ipc"; "runtime"; "web"]
let catch_all = ["engine"; "util"; "utils"; "common"; "core"; "helpers"; "misc"]
let transparent = "internal"

let ontology_terms source =
  String.split_on_char '\n' source |> List.filter_map (fun line ->
    match String.split_on_char ' ' (String.trim line) with
    | "term" :: identifier :: _ -> Some identifier
    | _ -> None)

let names_term terms segment =
  List.mem segment terms ||
  (String.length segment > 1 && String.ends_with ~suffix:"s" segment &&
   List.mem (String.sub segment 0 (String.length segment - 1)) terms)

type position = Top | Layered | Free

let directory_violation terms directory =
  let rec walk prefix position = function
    | [] -> None
    | segment :: rest ->
        let prefix = if prefix = "" then segment else prefix ^ "/" ^ segment in
        if segment = transparent then walk prefix position rest
        else if List.mem segment catch_all then
          Some (prefix, segment ^ " is a catch-all name; name the directory by the ontology term or by what it does")
        else if position <> Free && not (names_term terms segment) then
          Some (prefix, segment ^ " is not a CSF ontology term; rename the directory or define the term in " ^ ontology_source)
        else
          let next = match position with
            | Top when List.mem segment layered_roots -> Layered
            | Layered -> Layered
            | _ -> Free in
          walk prefix next rest in
  if directory = "." then None else walk "" Top (String.split_on_char '/' directory)

let directories ~terms paths =
  if terms = [] then [] else
  paths |> List.filter (fun path -> Filename.check_suffix path ".go" && not (excluded path))
  |> List.map Filename.dirname |> List.sort_uniq String.compare
  |> List.filter_map (directory_violation terms)
  |> List.sort_uniq compare
  |> List.map (fun (path, message) -> {path; line = 1; rule = "ONTOLOGY-DIRS"; message})

let check_directories root paths =
  let source = Filename.concat root ontology_source in
  if not (Sys.file_exists source) then [] else
  directories ~terms:(ontology_terms (Checker.read_file source)) paths
