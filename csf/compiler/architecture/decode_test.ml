let expect message condition = if not condition then failwith message

let parse grammar source =
  match Frontend.parse_text ~grammar ~source ~filename:"decode.csf" with
  | Ok node -> node
  | Error diagnostics -> failwith (String.concat "; "
      (List.map (fun (error : Model.diagnostic) -> error.message) diagnostics))

let accepted node = match Decode.architecture node with
  | Ok architecture -> architecture
  | Error diagnostics -> failwith (String.concat "; "
      (List.map (fun (error : Model.diagnostic) -> error.message) diagnostics))

let rejected node = match Decode.architecture node with
  | Ok _ -> failwith "malformed or unconsumed syntax was accepted"
  | Error diagnostics -> expect "decoder did not report a model diagnostic"
      (List.for_all (fun (error : Model.diagnostic) -> error.code = "CSF_MODEL") diagnostics)

let source = {|architecture example version 1 {
  process daemon kind go entrypoint "cmd/main.go";
  process peer kind external;
  scope application under daemon;
  service worker in daemon scope application source "services/worker" state existing lifecycle scoped verification test "worker_test.go";
  resource database in peer scope application state planned lifecycle borrowed verification pending;
  requires worker -> database;
  connect worker -> database via remote boundary database state planned;
  connect worker -> worker via call state existing;
  scan "services";
  generated "generated";
}|}

let test_canonical root =
  let architecture = accepted root in
  expect "architecture header lost" (architecture.name = "example" && architecture.version = 1);
  expect "process or optional entrypoint lost"
    (List.map (fun (value : Model.process) -> value.process_id, value.entrypoint) architecture.processes
     = ["daemon", Some "cmd/main.go"; "peer", None]);
  expect "component source or evidence lost"
    (List.map (fun (value : Model.component) -> value.source, value.verification) architecture.components
     = [Some "services/worker", Model.Test_reference "worker_test.go"; None, Model.Pending]);
  expect "connection boundary lost"
    (List.map (fun (value : Model.connection) -> value.transport, value.boundary) architecture.connections
     = [Model.Remote, Some "database"; Model.Call, None]);
  expect "declaration kinds lost"
    (List.length architecture.scopes = 1 && List.length architecture.dependencies = 1 &&
     List.length architecture.scan_roots = 1 && List.length architecture.generated_roots = 1);
  expect "source location changed" ((List.hd architecture.processes).process_at.line = 2)

let test_grammar_drift () =
  List.iter (fun (grammar, source) -> rejected (parse grammar source)) [
    {|architecture = "architecture", identifier, "version", integer, "{", metadata, "}";
      metadata = "metadata", string;|},
    {|architecture example version 1 { metadata "do not drop me" }|};
    {|architecture = "architecture", identifier, "version", integer, "{", {declaration}, "}";
      declaration = process;
      process = "process", identifier, "kind", process_kind, [entrypoint], ";";
      process_kind = "go" | "external"; entrypoint = "entrypoint", string;|},
    {|architecture example version 1 { process daemon kind go entrypoint "main.go"; }|};
    {|architecture = "architecture", identifier, "version", integer, "{", (metadata | declaration), "}";
      metadata = "process", identifier, "kind", process_kind, ";";
      declaration = process; process = "process", identifier, "kind", process_kind, ";";
      process_kind = "go" | "external";|},
    {|architecture example version 1 { process daemon kind go; }|};
    {|architecture = "architecture", identifier, "version", integer, "{", "ignored", "}";|},
    {|architecture example version 1 { ignored }|};
  ]

(* Corrupt one node at a time, so every concrete-tree boundary is checked
   independently instead of one early failure hiding later skipped nodes. *)
let rec mutations change (node : Frontend.node) =
  let local = match change node with None -> [] | Some changed -> [changed] in
  let nested = List.mapi (fun index child ->
    mutations change child |> List.map (fun changed ->
      { node with children = List.mapi (fun current original ->
          if current = index then changed else original) node.children })) node.children in
  local @ List.concat nested

let test_complete_consumption root =
  let extra : Frontend.node = { rule = "$terminal"; value = Some "unexpected";
    children = []; at = root.Frontend.at } in
  let named change node = match node.Frontend.value with
    | None -> Some (change node) | Some _ -> None in
  let corruptions = [
    named (fun node -> { node with children = node.children @ [extra] });
    named (fun node -> { node with children = node.children @ [{ extra with rule = "unknown"; value = None }] });
    named (fun node -> { node with value = Some "unexpected" });
    (fun node -> if node.Frontend.rule = "$terminal"
      then Some { node with value = Some "unexpected" } else None);
    (fun node -> match node.Frontend.value with
      | Some _ -> Some { node with children = [extra] } | None -> None);
    (fun node -> match node.Frontend.value with
      | Some _ -> Some { node with value = None } | None -> None);
  ] in
  List.iter (fun change ->
    let candidates = mutations change root in
    expect "mutation check exercised no nodes" (candidates <> []);
    List.iter rejected candidates) corruptions

let () =
  let grammar_path = if Array.length Sys.argv > 1 then Sys.argv.(1)
    else "csf/compiler/architecture/language.ebnf" in
  let grammar = In_channel.with_open_bin grammar_path In_channel.input_all in
  let root = parse grammar source in
  test_canonical root;
  test_grammar_drift ();
  test_complete_consumption root;
  print_endline "CSF complete-tree decoder tests passed"
