module Label = Emit__Label

(* Yojson owns JSON escaping; spellings come from the same generated
   vocabulary as the other projections, so JSON matches the source keywords. *)
let render (resolved : Model.resolved) =
  let str value = `String value in
  let opt encode = function None -> `Null | Some value -> encode value in
  let at (value : Model.location) =
    `Assoc ["file", str value.file; "line", `Int value.line; "column", `Int value.column] in
  let word encode value = str (Label.spelling encode value) in
  let paths values = `List (List.map (fun (value : Model.source_root) ->
    `Assoc ["path", str value.path; "at", at value.path_at]) values) in
  let architecture = resolved.architecture in
  let owner = Hashtbl.create 16 in
  resolved.scopes |> List.iter (fun (value : Model.resolved_scope) ->
    Hashtbl.replace owner value.scope.scope_id value.process_owner.process_id);
  Yojson.Safe.pretty_to_string (`Assoc [
    "format", str "csf-architecture";
    "format_version", `Int 1;
    "architecture", `Assoc ["name", str architecture.name; "version", `Int architecture.version;
      "at", at architecture.at];
    "processes", `List (List.map (fun (value : Model.process) -> `Assoc [
      "name", str value.process_id; "kind", word Syntax_cgen.process_kind_terminal value.kind;
      "entrypoint", opt str value.entrypoint; "at", at value.process_at]) architecture.processes);
    "scopes", `List (List.map (fun (value : Model.scope) -> `Assoc [
      "name", str value.scope_id; "parent", str value.parent;
      "process", opt str (Hashtbl.find_opt owner value.scope_id);
      "at", at value.scope_at]) architecture.scopes);
    "components", `List (List.map (fun (value : Model.component) -> `Assoc [
      "name", str value.component_id; "kind", word Syntax_cgen.role_terminal value.role;
      "process", str value.process; "scope", str value.scope; "source", opt str value.source;
      "state", word Syntax_cgen.state_terminal value.state;
      "lifecycle", word Syntax_cgen.lifecycle_terminal value.lifecycle;
      "verification", (match value.verification with
        | Model.Pending -> `Assoc ["status", str "pending"; "test", `Null]
        | Model.Test_reference path -> `Assoc ["status", str "test_reference"; "test", str path]);
      "at", at value.component_at]) architecture.components);
    "dependencies", `List (List.map (fun (value : Model.dependency) -> `Assoc [
      "consumer", str value.consumer; "provider", str value.provider;
      "at", at value.dependency_at]) architecture.dependencies);
    "connections", `List (List.map (fun (value : Model.connection) -> `Assoc [
      "caller", str value.caller; "callee", str value.callee;
      "transport", word Syntax_cgen.transport_terminal value.transport;
      "boundary", opt str value.boundary;
      "state", word Syntax_cgen.state_terminal value.connection_state;
      "at", at value.connection_at]) architecture.connections);
    "scan_roots", paths architecture.scan_roots;
    "generated_roots", paths architecture.generated_roots;
    "directories", `List (List.map (fun (value : Model.directory) -> `Assoc [
      "path", str value.path; "allowed", `List (List.map str value.allowed);
      "tier", opt (word Syntax_cgen.tier_terminal) value.tier;
      "at", at value.directory_at]) architecture.directories);
    "obligations", `List (List.map (fun (value : Model.obligation) -> `Assoc [
      "subject", str value.subject; "requirement", str value.requirement;
      "evidence", opt str value.evidence]) resolved.obligations);
  ]) ^ "\n"
