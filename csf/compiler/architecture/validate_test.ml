open Model

let at = { file = "fixture.csf"; line = 1; column = 1 }
let process process_id kind entrypoint = { process_id; kind; entrypoint; process_at = at }
let scope scope_id parent = { scope_id; parent; scope_at = at }
let component component_id role process scope lifecycle = {
  component_id; role; process; scope; lifecycle; state = Existing;
  source = (if process = "host" then Some ("src/" ^ component_id ^ ".go") else None);
  verification = Pending; component_at = at;
}
let consumer = component "consumer" Service "host" "request" Scoped
let provider = { (component "provider" Library "host" "root" Borrowed) with
  verification = Test_reference "tests/provider.ml" }
let gateway = component "gateway" Gateway "host" "root" Scoped
let external_target = component "target" Resource "vendor" "vendor_root" Borrowed
let dependency consumer provider = { consumer; provider; dependency_at = at }
let connection ?(transport = Subprocess) ?(boundary = Some "gateway")
    ?(state = Existing) caller callee = {
  caller; callee; transport; boundary; connection_state = state; connection_at = at;
}
let directory path allowed : directory = { path; allowed; tier = None; directory_at = at }
let directory_tiered path allowed tier : directory = { path; allowed; tier = Some tier; directory_at = at }
let valid = {
  name = "fixture"; version = 1; at;
  processes = [process "host" Go (Some "app/main.go"); process "vendor" External None];
  scopes = [scope "root" "host"; scope "request" "root"; scope "vendor_root" "vendor"];
  components = [consumer; provider; gateway; external_target];
  dependencies = [dependency "consumer" "provider"];
  connections = [connection "consumer" "target"];
  scan_roots = []; generated_roots = []; directories = [];
}
let edit_component id change (architecture : architecture) = {
  architecture with components = List.map (fun (c : component) ->
    if c.component_id = id then change c else c) architecture.components;
}
let edit_scope id change (architecture : architecture) = {
  architecture with scopes = List.map (fun (s : scope) ->
    if s.scope_id = id then change s else s) architecture.scopes;
}
let edit_host change (architecture : architecture) = {
  architecture with processes = List.map (fun (p : process) ->
    if p.process_id = "host" then change p else p) architecture.processes;
}
let accepted architecture = match Validate.resolve architecture with
  | Ok value -> value
  | Error errors -> failwith (String.concat "; " (List.map (fun d -> d.code ^ ": " ^ d.message) errors))
let reject code architecture = match Validate.resolve architecture with
  | Ok _ -> failwith ("Expected rejection: " ^ code)
  | Error errors ->
      if not (List.exists (fun d -> d.code = code) errors) then
        failwith ("Missing " ^ code ^ "; got " ^ String.concat ", " (List.map (fun d -> d.code) errors))
let check condition message = if not condition then failwith message
let component_ids values = List.map (fun (c : resolved_component) -> c.component.component_id) values
let contains text fragment =
  let rec loop offset =
    offset + String.length fragment <= String.length text &&
      (String.sub text offset (String.length fragment) = fragment || loop (offset + 1))
  in loop 0
let has_obligation predicate (resolved : resolved) = List.exists predicate resolved.obligations

let invalid_cases = [
  "version", "version", { valid with version = 2 };
  "empty architecture name", "identifier", { valid with name = " " };
  "empty process id", "identifier", edit_host (fun p -> { p with process_id = "" }) valid;
  "empty scope id", "identifier", edit_scope "root" (fun s -> { s with scope_id = " " }) valid;
  "empty component id", "identifier", edit_component "consumer" (fun c -> { c with component_id = "" }) valid;
  "duplicate process", "duplicate_identifier", { valid with processes = List.hd valid.processes :: valid.processes };
  "duplicate scope", "duplicate_identifier", { valid with scopes = List.hd valid.scopes :: valid.scopes };
  "duplicate component", "duplicate_identifier", { valid with components = consumer :: valid.components };
  "cross namespace duplicate", "duplicate_identifier", { valid with scopes = scope "host" "host" :: valid.scopes };
  "no Go process", "go_host", { valid with processes = [process "vendor" External None] };
  "two Go processes", "go_host", { valid with processes = process "another" Go (Some "app/other.go") :: valid.processes };
  "missing entrypoint", "entrypoint", edit_host (fun p -> { p with entrypoint = None }) valid;
  "blank entrypoint", "entrypoint", edit_host (fun p -> { p with entrypoint = Some " " }) valid;
  "external entrypoint", "entrypoint", { valid with processes = [List.hd valid.processes; process "vendor" External (Some "other")] };
  "unknown scope parent", "scope_parent", edit_scope "root" (fun s -> { s with parent = "absent" }) valid;
  "scope self cycle", "scope_cycle", edit_scope "root" (fun s -> { s with parent = "root" }) valid;
  "scope mutual cycle", "scope_cycle", edit_scope "root" (fun s -> { s with parent = "request" }) valid;
  "component unknown process", "component_process", edit_component "consumer" (fun c -> { c with process = "absent" }) valid;
  "component unknown scope", "component_scope", edit_component "consumer" (fun c -> { c with scope = "absent" }) valid;
  "component wrong scope owner", "scope_owner", edit_component "consumer" (fun c -> { c with scope = "vendor_root" }) valid;
  "borrowed service", "lifecycle", edit_component "consumer" (fun c -> { c with lifecycle = Borrowed }) valid;
  "external library", "component_role", edit_component "target" (fun c -> { c with role = Library }) valid;
  "source absent", "component_source", edit_component "consumer" (fun c -> { c with source = None }) valid;
  "source blank", "component_source", edit_component "consumer" (fun c -> { c with source = Some "" }) valid;
  "planned evidence", "planned_evidence", edit_component "provider" (fun c -> { c with state = Planned }) valid;
  "blank evidence", "verification", edit_component "provider" (fun c -> { c with verification = Test_reference " " }) valid;
  "missing dependency consumer", "component_reference", { valid with dependencies = [dependency "absent" "provider"] };
  "missing dependency provider", "component_reference", { valid with dependencies = [dependency "consumer" "absent"] };
  "cross process dependency", "dependency_process", { valid with dependencies = [dependency "consumer" "target"] };
  "short dependency", "dependency_lifetime", { valid with dependencies = [dependency "provider" "consumer"] };
  "sibling dependency", "dependency_lifetime", edit_component "provider" (fun c -> { c with scope = "sibling" })
    { valid with scopes = valid.scopes @ [scope "sibling" "root"] };
  "planned provider", "planned_dependency", edit_component "provider" (fun c -> { c with state = Planned; verification = Pending }) valid;
  "dependency self cycle", "dependency_cycle", { valid with dependencies = [dependency "provider" "provider"] };
  "dependency mutual cycle", "dependency_cycle", edit_component "consumer" (fun c -> { c with scope = "root" })
    { valid with dependencies = [dependency "consumer" "provider"; dependency "provider" "consumer"] };
  "missing call source", "component_reference", { valid with connections = [connection "absent" "target"] };
  "missing call target", "component_reference", { valid with connections = [connection "consumer" "absent"] };
  "missing boundary reference", "component_reference", { valid with connections = [connection ~boundary:(Some "absent") "consumer" "target"] };
  "cross process call", "internal_process", { valid with connections = [connection ~transport:Call ~boundary:None "consumer" "target"] };
  "cross process channel", "internal_process", { valid with connections = [connection ~transport:Channel ~boundary:None "consumer" "target"] };
  "call names boundary", "internal_boundary", { valid with connections = [connection ~transport:Call "consumer" "provider"] };
  "channel names boundary", "internal_boundary", { valid with connections = [connection ~transport:Channel "consumer" "provider"] };
  "short call dependency", "call_lifetime", { valid with connections = [connection ~transport:Call ~boundary:None "provider" "consumer"] };
  "short call into a scoped service", "call_lifetime", { valid with connections = [connection ~transport:Call ~boundary:None "gateway" "consumer"] };
  "short call into a borrowed component", "call_lifetime", edit_component "provider" (fun c -> { c with scope = "request" })
    { valid with dependencies = []; connections = [connection ~transport:Call ~boundary:None "gateway" "provider"] };
  "lazy call crosses processes", "internal_process", edit_component "target" (fun c -> { c with lifecycle = Lazy })
    { valid with connections = [connection ~transport:Call ~boundary:None "consumer" "target"] };
  "same process crossing", "crossing_process", { valid with connections = [connection "consumer" "provider"] };
  "crossing without owner", "crossing_boundary", { valid with connections = [connection ~boundary:None "consumer" "target"] };
  "foreign crossing owner", "boundary_process", { valid with connections = [connection ~boundary:(Some "target") "consumer" "target"] };
  "short crossing owner", "boundary_lifetime", edit_component "gateway" (fun c -> { c with scope = "request" })
    { valid with connections = [connection "provider" "target"] };
  "wrong subprocess owner role", "boundary_role", { valid with connections = [connection ~boundary:(Some "provider") "consumer" "target"] };
  "wrong remote owner role", "boundary_role", { valid with connections = [connection ~transport:Remote ~boundary:(Some "provider") "consumer" "target"] };
  "wrong device owner role", "boundary_role", { valid with connections = [connection ~transport:Device ~boundary:(Some "provider") "consumer" "target"] };
  "existing connection planned source", "planned_connection", edit_component "consumer" (fun c -> { c with state = Planned }) valid;
  "existing connection planned target", "planned_connection", edit_component "target" (fun c -> { c with state = Planned }) valid;
  "existing connection planned boundary", "planned_connection", edit_component "gateway" (fun c -> { c with state = Planned }) valid;
]

let tests = [
  "resolved values retain typed identity", (fun () ->
    let resolved = accepted valid in
    let caller = List.hd resolved.components in
    check (caller.component == consumer) "Component declaration was not retained";
    check (caller.process_owner == List.hd valid.processes) "Process declaration was not retained";
    check (caller.lifetime.ancestors = ["request"; "root"]) "Incorrect scope ancestry";
    let edge = List.hd resolved.connections in
    check (edge.source == caller) "Connection source is not its resolved component";
    check (edge.target.component == external_target) "External target was not retained");
  "stable dependency ordering and reverse shutdown", (fun () ->
    let free = component "free" Library "host" "root" Borrowed in
    let resolved = accepted { valid with components = [consumer; free; provider; gateway; external_target] } in
    check (component_ids resolved.start_order = ["free"; "provider"; "consumer"; "gateway"; "target"]) "Unexpected startup order";
    check (component_ids resolved.stop_order = List.rev (component_ids resolved.start_order)) "Shutdown is not reverse startup";
    check (Validate.resolve valid = Validate.resolve valid) "Resolution is nondeterministic");
  "declaration order does not constrain scope resolution", (fun () ->
    ignore (accepted { valid with scopes = List.rev valid.scopes }));
  "transitive scope ancestry preserves ownership", (fun () ->
    let architecture = edit_component "consumer" (fun c -> { c with scope = "turn" })
      { valid with scopes = scope "turn" "request" :: valid.scopes } in
    let resolved = accepted architecture in
    let caller = List.hd resolved.components in
    check (caller.lifetime.ancestors = ["turn"; "request"; "root"]) "Transitive ancestors lost";
    check (caller.process_owner.process_id = "host") "Transitive owner lost");
  "equal lifetime dependency is valid", (fun () ->
    ignore (accepted (edit_component "consumer" (fun c -> { c with scope = "root" }) valid)));
  "planned consumer may require planned provider", (fun () ->
    let architecture = edit_component "consumer" (fun c -> { c with state = Planned; source = None })
      (edit_component "provider" (fun c -> { c with state = Planned; source = None; verification = Pending })
        { valid with connections = [connection ~state:Planned "consumer" "target"] }) in
    ignore (accepted architecture));
  "call into a shorter-lived lazy service restarts it", (fun () ->
    let resolved = accepted (edit_component "consumer" (fun c -> { c with lifecycle = Lazy })
      { valid with connections = [connection ~transport:Call ~boundary:None "gateway" "consumer"] }) in
    check (has_obligation (fun o -> o.subject = "consumer" && contains o.requirement "idle retirement") resolved)
      "Missing lazy lifecycle obligation");
  "lazy does not relax a requires edge", (fun () ->
    reject "dependency_lifetime" (edit_component "consumer" (fun c -> { c with lifecycle = Lazy })
      { valid with dependencies = [dependency "provider" "consumer"] }));
  "call borrows enclosing lifetime", (fun () ->
    ignore (accepted { valid with connections = [connection ~transport:Call ~boundary:None "consumer" "provider"] }));
  "channel does not imply borrowed dependency", (fun () ->
    ignore (accepted { valid with connections = [connection ~transport:Channel ~boundary:None "provider" "consumer"] }));
  "borrowed policy and adapters are valid", (fun () ->
    List.iter (fun role ->
      ignore (accepted (edit_component "provider" (fun c -> { c with role }) valid))) [Manager; Library; Adapter; Resource]);
  "synchronous gateway can borrow caller ownership", (fun () ->
    ignore (accepted (edit_component "gateway" (fun c -> { c with lifecycle = Borrowed }) valid)));
  "remote and device accept named adapter", (fun () ->
    let architecture = edit_component "gateway" (fun c -> { c with role = Adapter; lifecycle = Borrowed }) valid in
    List.iter (fun transport -> ignore (accepted { architecture with connections = [connection ~transport "consumer" "target"] })) [Remote; Device]);
  "planned nodes can omit source", (fun () ->
    let architecture = edit_component "consumer" (fun c -> { c with state = Planned; source = None })
      { valid with connections = [connection ~state:Planned "consumer" "target"] } in
    let resolved = accepted architecture in
    check (has_obligation (fun o -> o.subject = "consumer" && contains o.requirement "planned component") resolved) "Missing planned component obligation";
    check (has_obligation (fun o -> o.subject = "consumer -> target" && contains o.requirement "planned connection") resolved) "Missing planned connection obligation");
  "scoped test reference remains an inspection obligation", (fun () ->
    let resolved = accepted (edit_component "consumer" (fun c -> { c with verification = Test_reference "tests/lifecycle.ml" }) valid) in
    check (has_obligation (fun o -> o.subject = "consumer" && o.evidence = Some "tests/lifecycle.ml" && contains o.requirement "not proof") resolved)
      "Test reference incorrectly discharged lifecycle verification");
  "borrowed test reference remains an inspection obligation", (fun () ->
    let resolved = accepted valid in
    check (has_obligation (fun o -> o.subject = "provider" && o.evidence = Some "tests/provider.ml") resolved)
      "Borrowed test reference was incorrectly accepted as verification");
  "pending borrowed ownership remains an obligation", (fun () ->
    let resolved = accepted valid in
    check (has_obligation (fun o -> o.subject = "target" && contains o.requirement "verification remains pending") resolved)
      "Missing borrowed ownership obligation");
  "multiple current gateways are explicit migration debt", (fun () ->
    let second = { gateway with component_id = "second_gateway" } in
    let resolved = accepted { valid with components = valid.components @ [second];
      connections = valid.connections @ [connection ~boundary:(Some "second_gateway") "provider" "target"] } in
    check (has_obligation (fun o -> contains o.requirement "one audited gateway" && contains o.requirement "second_gateway") resolved)
      "Missing gateway consolidation obligation");
  "reused gateway does not create consolidation debt", (fun () ->
    let resolved = accepted { valid with connections = valid.connections @ [connection "provider" "target"] } in
    check (not (has_obligation (fun o -> contains o.requirement "one audited gateway") resolved)) "Duplicate references counted as gateways");
  "tree census passes declared files and empty paths", (fun () ->
    let architecture = { valid with directories = [directory "." ["go"]] } in
    let report = Validate.tree_diagnostics architecture ["src/consumer.go"; "src/provider.go"] in
    check (report.offenses = []) "Declared files drew offenses";
    check (report.directories_declared = 1 && report.directories_tracked = 2)
      "Directory counts do not match declarations and tracked dirs";
    let empty = Validate.tree_diagnostics architecture [] in
    check (empty.offenses = [] && empty.directories_tracked = 0) "Empty paths invented offenses or dirs");
  "tree census reports undeclared files", (fun () ->
    let report = Validate.tree_diagnostics valid ["services/harness/sessiongate/github_gate.csf"] in
    check (List.exists (fun d -> d.code = "tree_undeclared") report.offenses)
      "Undeclared file drew no offense");
  "tree census reports csf files outside csf", (fun () ->
    let architecture = { valid with directories = [directory "." ["csf"]] } in
    let report = Validate.tree_diagnostics architecture ["services/harness/sessiongate/github_gate.csf"] in
    check (List.exists (fun d -> d.code = "tree_csf_outside_csf") report.offenses)
      "CSF file outside csf/ drew no offense");
  "tree census reports disallowed kinds", (fun () ->
    let architecture = { valid with directories = [directory "." ["go"]] } in
    let report = Validate.tree_diagnostics architecture ["csf/architecture/architecture.csf"] in
    check (List.exists (fun d -> d.code = "tree_kind_not_allowed" && contains d.message "csf") report.offenses)
      "Disallowed kind drew no offense");
  "nested directory declarations shadow the root", (fun () ->
    let architecture = { valid with directories = [directory "." ["go"; "csf"]; directory "csf" ["csf"]] } in
    let report = Validate.tree_diagnostics architecture ["csf/architecture/architecture.csf"; "go/main.go"] in
    check (report.offenses = []) "Nested declaration did not shadow the root");
  "a question with seventeen options reports fanout", (fun () ->
    let directories = directory "." ["go"] :: List.init 17 (fun i -> directory ("a" ^ string_of_int i) ["go"]) in
    let report = Validate.tree_diagnostics { valid with directories } [] in
    check (List.mem (".", 17) report.fanout) "Seventeen options did not report fanout";
    check (List.length report.fanout = 1) "Unexpected fanout answers");
  "sixteen options stay under the fanout bound", (fun () ->
    let directories = directory "." ["go"] :: List.init 16 (fun i -> directory ("b" ^ string_of_int i) ["go"]) in
    let report = Validate.tree_diagnostics { valid with directories } [] in
    check (report.fanout = []) "Sixteen options reported fanout");
  "a testdata directory is data, not a choice", (fun () ->
    let directories = [directory "." ["go"]; directory "testdata" ["go"]] @
      List.init 17 (fun i -> directory ("testdata/c" ^ string_of_int i) ["go"]) in
    let report = Validate.tree_diagnostics { valid with directories } [] in
    check (report.fanout = []) "Testdata directory drew a fanout");
  "io with its four tier directories draws no shape offense", (fun () ->
    let directories = [
      directory "." ["go"];
      directory "io" ["go"];
      directory_tiered "io/inproc" ["go"] In_process;
      directory_tiered "io/kernel" ["go"] Kernel;
      directory_tiered "io/ipc" ["go"] Ipc;
      directory_tiered "io/net" ["go"] Net;
    ] in
    let report = Validate.tree_diagnostics { valid with directories } [] in
    check (not (List.exists (fun d -> d.code = "tree_io_children_stray" || d.code = "tree_io_children_missing" || d.code = "tree_tier_mismatch") report.offenses))
      "True-to-hardware io drew a shape offense");
  "a stray child under io draws io_children_stray", (fun () ->
    let directories = [
      directory "." ["go"]; directory "io" ["go"];
      directory_tiered "io/inproc" ["go"] In_process; directory_tiered "io/kernel" ["go"] Kernel;
      directory_tiered "io/ipc" ["go"] Ipc; directory_tiered "io/net" ["go"] Net;
      directory "io/db" ["go"];
    ] in
    let report = Validate.tree_diagnostics { valid with directories } [] in
    check (List.exists (fun d -> d.code = "tree_io_children_stray" && contains d.message "io/db") report.offenses)
      "Stray io child drew no offense");
  "a missing tier directory draws io_children_missing", (fun () ->
    let directories = [
      directory "." ["go"]; directory "io" ["go"];
      directory_tiered "io/inproc" ["go"] In_process; directory_tiered "io/kernel" ["go"] Kernel;
      directory_tiered "io/net" ["go"] Net;
    ] in
    let report = Validate.tree_diagnostics { valid with directories } [] in
    check (List.exists (fun d -> d.code = "tree_io_children_missing" && contains d.message "ipc") report.offenses)
      "Missing io/ipc drew no offense");
  "a directory crossing off its io tier draws tier_mismatch", (fun () ->
    let directories = [
      directory "." ["go"]; directory "io" ["go"];
      directory_tiered "io/ipc" ["go"] Net;
    ] in
    let report = Validate.tree_diagnostics { valid with directories } [] in
    check (List.exists (fun d -> d.code = "tree_tier_mismatch" && contains d.message "io/ipc") report.offenses)
      "A directory crossing off its tier drew no mismatch");
] @ List.map (fun (name, code, architecture) -> name, (fun () -> reject code architecture)) invalid_cases

let () =
  List.iter (fun (name, test) ->
    try test () with exception_ ->
      Printf.eprintf "FAIL %s: %s\n" name (Printexc.to_string exception_);
      exit 1) tests;
  Printf.printf "Architecture validation: %d tests passed\n" (List.length tests)
