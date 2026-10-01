let expect message condition = if not condition then failwith message

let contains text fragment =
  let rec search offset =
    offset + String.length fragment <= String.length text &&
    (String.sub text offset (String.length fragment) = fragment || search (offset + 1)) in
  search 0

let at = { Model.file = "declarations\"\\\n\000\127\195\169.csf"; line = 12; column = 7 }
let injection = "source\"]\nend\nclick c0 \"https://example.invalid\"\n%%{init: {}}%%<script>|#35;`"

let host = {
  Model.process_id = "host"; kind = Model.Go; entrypoint = Some "cmd/host\"\nmain.go";
  process_at = at;
}
let peer = { Model.process_id = "peer"; kind = Model.External; entrypoint = None; process_at = at }
let lifetime id parent owner ancestors = {
  Model.scope = { Model.scope_id = id; parent; scope_at = at };
  process_owner = owner; ancestors;
}
let root_scope = lifetime "root" "host" host []
let child_scope = lifetime "child" "root" host ["root"]
let peer_scope = lifetime "peer_root" "peer" peer []

let component id role owner lifetime state lifecycle source verification = {
  Model.component = {
    Model.component_id = id; role; process = owner.Model.process_id;
    scope = lifetime.Model.scope.scope_id; source; state; lifecycle; verification; component_at = at;
  };
  process_owner = owner; lifetime;
}

let service = component "app" Model.Service host root_scope Model.Existing Model.Scoped
  (Some injection) (Model.Test_reference "tests/acceptance\"\nreference|`<tag>")
let manager = component "manager" Model.Manager host child_scope Model.Planned Model.Scoped None Model.Pending
let library = component "library" Model.Library host root_scope Model.Existing Model.Borrowed
  (Some "pkg/library") Model.Pending
let adapter = component "adapter" Model.Adapter host child_scope Model.Planned Model.Scoped None Model.Pending
let gateway = component "gateway" Model.Gateway host root_scope Model.Planned Model.Scoped None Model.Pending
let resource = component "resource" Model.Resource peer peer_scope Model.Existing Model.Borrowed None Model.Pending
let components = [service; manager; library; adapter; gateway; resource]

let connection source target transport boundary connection_state = {
  Model.connection = {
    Model.caller = source.Model.component.component_id;
    callee = target.Model.component.component_id;
    transport; boundary; connection_state; connection_at = at;
  };
  source; target; crossing = Option.map (fun _ -> gateway) boundary;
}
let connections = [
  connection service library Model.Call None Model.Existing;
  connection service manager Model.Channel None Model.Planned;
  connection service resource Model.Subprocess (Some "gateway") Model.Planned;
  connection gateway resource Model.Remote (Some "gateway") Model.Existing;
  connection adapter resource Model.Device (Some "gateway") Model.Planned;
]

let fixture : Model.resolved = {
  architecture = {
    Model.name = "example"; version = 1; at; processes = [host; peer];
    scopes = List.map (fun (value : Model.resolved_scope) -> value.scope) [root_scope; child_scope; peer_scope];
    components = List.map (fun (value : Model.resolved_component) -> value.component) components;
    dependencies = [{ Model.consumer = "app"; provider = "library"; dependency_at = at }];
    connections = List.map (fun (value : Model.resolved_connection) -> value.connection) connections;
    scan_roots = [{ Model.path = "source/\"\\\n"; path_at = at }];
    generated_roots = [{ Model.path = "generated/"; path_at = at }];
  };
  scopes = [root_scope; child_scope; peer_scope]; components; connections;
  start_order = [gateway; service; manager; adapter]; stop_order = [adapter; manager; service; gateway];
  obligations = [
    { Model.subject = "manager"; requirement = "Implement lifecycle"; evidence = None };
    { Model.subject = "app"; requirement = "Check cleanup"; evidence = Some "test|`<tag>\nnext" };
  ];
}

let test_typed_output () =
  let output = Emit.ocaml fixture in
  expect "generated module header missing"
    (String.starts_with ~prefix:(Codegen_header.render Codegen_header.Ocaml) output);
  expect "generated value is not typed" (contains output "let architecture : Model.architecture = {");
  List.iter (fun name -> expect ("missing constructor " ^ name) (contains output ("Model." ^ name)))
    ["Go"; "External"; "Existing"; "Planned"; "Service"; "Manager"; "Library";
     "Adapter"; "Gateway"; "Resource"; "Scoped"; "Borrowed"; "Pending"; "Test_reference";
     "Call"; "Channel"; "Subprocess"; "Remote"; "Device"];
  expect "location omitted" (contains output "line = 12; column = 7");
  expect "source filename escapes are lost" (contains output (Printf.sprintf "%S" at.file));
  expect "declaration source escapes are lost" (contains output (Printf.sprintf "%S" injection));
  expect "missing optional entrypoint lost" (contains output "entrypoint = None");
  expect "missing optional source lost" (contains output "source = None");
  expect "missing optional boundary lost" (contains output "boundary = None");
  expect "present boundary lost" (contains output "boundary = Some (\"gateway\")")

let test_diagram () =
  let output = Emit.mermaid fixture in
  expect "generated diagram header is not first"
    (String.starts_with ~prefix:(Codegen_header.render Codegen_header.Mermaid) output);
  expect "declaration boundary missing" (contains output "Declared architecture, not observed running state");
  expect "process identity used as syntax" (contains output "subgraph p0[");
  expect "scope identity used as syntax" (contains output "subgraph s1[");
  expect "component identity used as syntax" (contains output "c0[\"app<br/>");
  expect "nested scope omitted" (contains output "      subgraph s1[");
  expect "component role state lifecycle omitted" (contains output "service #124; existing #124; scoped");
  expect "dependency edge omitted" (contains output "c0 -.->|\"requires\"| c2");
  expect "call edge omitted" (contains output "c0 -->|\"call #124; existing\"| c2");
  expect "typed boundary edge omitted"
    (contains output "c3 -->|\"device #124; planned<br/>boundary#58; gateway\"| c5");
  expect "quotes are not escaped" (contains output "source#34;#93;<br/>end<br/>click c0 #34;");
  expect "hash entity can be injected" (contains output "#35;35#59;");
  expect "HTML delimiters are not escaped" (contains output "#60;script#62;");
  expect "diagram command can be injected" (not (contains output "\nclick "));
  expect "directive can be injected" (not (contains output "%%{init"));
  expect "label has raw HTML" (not (contains output "<script>"));
  expect "raw source survived escaping" (not (contains output injection))

let test_review () =
  let output = Emit.review fixture in
  expect "generated review header is not first"
    (String.starts_with ~prefix:(Codegen_header.render Codegen_header.Markdown) output);
  expect "pending obligation absent" (contains output "manager | Implement lifecycle | Pending");
  expect "evidence promoted to execution" (contains output "Reference only&#59; not executed&#58;");
  expect "test declaration promoted to result" (contains output "Test reference only&#59; not executed&#58;");
  expect "Markdown pipe not escaped" (contains output "test&#124;&#96;&#60;tag&#62;<br/>next");
  expect "lifecycle declared order missing"
    (contains output "gateway &#45;&#62; app &#45;&#62; manager &#45;&#62; adapter");
  expect "shutdown evidence boundary absent" (contains output "not cleanup or shutdown evidence");
  expect "review includes source commands" (not (contains output "\nclick "))

let test_empty_optionals () =
  let empty : Model.resolved = {
    architecture = { fixture.architecture with processes = []; scopes = []; components = [];
      dependencies = []; connections = []; scan_roots = []; generated_roots = [] };
    scopes = []; components = []; connections = []; start_order = []; stop_order = []; obligations = [];
  } in
  expect "empty declaration cannot be emitted" (contains (Emit.ocaml empty) "components = []");
  expect "empty diagram contains invented nodes" (not (contains (Emit.mermaid empty) "subgraph"));
  expect "empty review invents obligations" (contains (Emit.review empty) "No pending obligations");
  expect "empty lifecycle invents an order" (contains (Emit.review empty) "None declared")

let test_determinism () =
  List.iter (fun project -> expect "projection changes across calls" (project fixture = project fixture))
    [Emit.ocaml; Emit.mermaid; Emit.review]

let () =
  test_typed_output ();
  test_diagram ();
  test_review ();
  test_empty_optionals ();
  test_determinism ();
  print_endline "CSF architecture projection tests passed"
