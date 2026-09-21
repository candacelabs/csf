(* Semantic checks operate on declarations only. They resolve ownership and
   ordering without reading source files, starting processes or executing tests.
   A valid graph may still carry implementation and inspection obligations. *)
open Model

(* One invocation owns this mutable working state. Diagnostics are prepended
   and reversed at the result boundary to keep discovery order. The cache stores
   both resolved scopes and known failures; it never survives [resolve]. *)
type context = {
  architecture : architecture;
  mutable diagnostics : diagnostic list;
  mutable scope_cache : (string * resolved_scope option) list;
}

let nonempty value = String.trim value <> ""
let present = function Some value -> nonempty value | None -> false
let lookup key identify values = List.find_opt (fun value -> identify value = key) values
let reject context at code message =
  context.diagnostics <- { at; code; message } :: context.diagnostics
let result_error context = Error (List.rev context.diagnostics)
let find_process context id =
  lookup id (fun (p : process) -> p.process_id) context.architecture.processes
let find_scope context id =
  lookup id (fun (s : scope) -> s.scope_id) context.architecture.scopes

(* Processes, scopes and components share a namespace. Without this check,
   a scope parent could ambiguously name both a process and another scope. *)
let check_identifiers context =
  let identifiers = ref [] in
  let identify at id =
    if not (nonempty id) then reject context at "identifier" "Identifiers cannot be empty."
    else if List.mem id !identifiers then
      reject context at "duplicate_identifier" ("Identifier is declared more than once: " ^ id)
    else identifiers := id :: !identifiers
  in
  List.iter (fun (p : process) -> identify p.process_at p.process_id) context.architecture.processes;
  List.iter (fun (s : scope) -> identify s.scope_at s.scope_id) context.architecture.scopes;
  List.iter (fun (c : component) -> identify c.component_at c.component_id) context.architecture.components

let check_process context (process : process) =
  match process.kind with
  | Go when not (present process.entrypoint) ->
      reject context process.process_at "entrypoint" "The Go host must declare a nonempty application entrypoint."
  | External when process.entrypoint <> None ->
      reject context process.process_at "entrypoint" "An external process cannot own the Go application entrypoint."
  | _ -> ()

(* Establish unambiguous names and the single-host policy before traversing
   relationships. This counts declarations, not live OS processes. *)
let check_declarations context =
  let architecture = context.architecture in
  if architecture.version <> 1 then
    reject context architecture.at "version" "Only architecture version 1 is supported.";
  if not (nonempty architecture.name) then
    reject context architecture.at "identifier" "The architecture name cannot be empty.";
  check_identifiers context;
  let hosts = List.filter (fun (p : process) -> p.kind = Go) architecture.processes in
  if List.length hosts <> 1 then
    reject context architecture.at "go_host" "Exactly one Go application process is required.";
  List.iter (check_process context) architecture.processes

(* Follow parent links until reaching a process, independent of declaration
   order. [trail] is the active recursion path: encountering an ID already on
   it detects a cycle. The cache's nested option distinguishes unvisited [None],
   failed [Some None] and resolved [Some (Some scope)] lookups, so a bad shared
   ancestor is not diagnosed anew for every descendant. *)
let rec resolve_scope context trail (scope : scope) =
  match List.assoc_opt scope.scope_id context.scope_cache with
  | Some cached -> cached
  | None ->
      if List.mem scope.scope_id trail then begin
        reject context scope.scope_at "scope_cycle"
          ("Scope ownership contains a cycle: " ^ String.concat " -> " (List.rev (scope.scope_id :: trail)));
        None
      end else begin
        let resolved =
          match find_process context scope.parent with
          | Some process_owner -> Some { scope; process_owner; ancestors = [scope.scope_id] }
          | None ->
              match find_scope context scope.parent with
              | None ->
                  reject context scope.scope_at "scope_parent"
                    ("Unknown process or scope parent: " ^ scope.parent);
                  None
              | Some parent ->
                  match resolve_scope context (scope.scope_id :: trail) parent with
                  | None -> None
                  | Some parent -> Some {
                      scope; process_owner = parent.process_owner;
                      ancestors = scope.scope_id :: parent.ancestors;
                    }
        in
        context.scope_cache <- (scope.scope_id, resolved) :: context.scope_cache;
        resolved
      end

(* Service is the lifecycle-bearing role. Manager remains conceptual and may
   borrow its lifetime. Requiring Scoped records a contract; it cannot establish
   that the implementation actually registers, cancels or joins goroutines. *)
let check_component_contract context (component : component) =
  let at = component.component_at in
  if component.role = Service && component.lifecycle <> Scoped then
    reject context at "lifecycle" "Services require a scoped lifecycle.";
  match component.state, component.verification with
  | Planned, Test_reference _ ->
      reject context at "planned_evidence" "A planned component cannot advertise a test reference."
  | _, Test_reference reference when not (nonempty reference) ->
      reject context at "verification" "A test reference cannot be empty."
  | _ -> ()

(* External endpoints are resources rather than host-mounted capabilities.
   Requiring a source string for an existing host component is only structural;
   the later source check establishes whether the path can be inspected. *)
let check_component_owner context (component : component) process =
  let at = component.component_at in
  match process with
  | None -> reject context at "component_process" ("Unknown component process: " ^ component.process)
  | Some (owner : process) ->
      if owner.kind = External && component.role <> Resource then
        reject context at "component_role" "External process endpoints must use the resource role.";
      if owner.kind = Go && component.state = Existing && not (present component.source) then
        reject context at "component_source" "An existing in-process component requires a nonempty source path."

(* Resolve both declared owner and lifetime, then ensure they lead to the same
   process. A component cannot borrow a scope owned by a different process. *)
let resolve_component context scopes (component : component) =
  check_component_contract context component;
  let process = find_process context component.process in
  let lifetime = lookup component.scope (fun (s : resolved_scope) -> s.scope.scope_id) scopes in
  check_component_owner context component process;
  (match find_scope context component.scope with
  | None -> reject context component.component_at "component_scope" ("Unknown component scope: " ^ component.scope)
  | Some _ -> ());
  match process, lifetime with
  | Some process_owner, Some lifetime ->
      if process_owner.process_id <> lifetime.process_owner.process_id then
        reject context component.component_at "scope_owner" "The component process and scope owner disagree.";
      Some { component; process_owner; lifetime }
  | _ -> None

let endpoint context components at id =
  match lookup id (fun (c : resolved_component) -> c.component.component_id) components with
  | Some component -> Some component
  | None -> reject context at "component_reference" ("Unknown component: " ^ id); None

let same_process (left : resolved_component) (right : resolved_component) =
  left.process_owner.process_id = right.process_owner.process_id

(* Containment direction matters: application-scoped storage outlives a
   request-scoped worker, because application appears in the worker's ancestry.
   The reverse and sibling scopes fail; equality is permitted. *)
let outlives (provider : resolved_component) (consumer : resolved_component) =
  same_process provider consumer && List.mem provider.lifetime.scope.scope_id consumer.lifetime.ancestors

(* A requires edge lends the provider to its consumer, so it needs compatible
   ownership, a sufficient lifetime and an available implementation state. The
   same edges later constrain startup order; they do not launch anything. *)
let resolve_dependency context components (dependency : dependency) =
  let at = dependency.dependency_at in
  let consumer = endpoint context components at dependency.consumer in
  let provider = endpoint context components at dependency.provider in
  match consumer, provider with
  | Some consumer, Some provider ->
      if not (same_process consumer provider) then
        reject context at "dependency_process" "A requires relationship must remain in one process."
      else if not (outlives provider consumer) then
        reject context at "dependency_lifetime" "A provider must have the same or an enclosing lifetime as its consumer.";
      if consumer.component.state = Existing && provider.component.state = Planned then
        reject context at "planned_dependency" "An existing consumer cannot require a planned provider.";
      Some (consumer, provider)
  | _ -> None

let check_connection_state context (connection : connection) source target crossing =
  if connection.connection_state = Existing then
    List.iter (fun (component : resolved_component) ->
      if component.component.state = Planned then
        reject context connection.connection_at "planned_connection"
          "An existing connection cannot depend on a planned endpoint or boundary.")
      (source :: target :: (match crossing with Some value -> [value] | None -> []))

(* A direct call borrows its target and therefore checks target lifetime.
   Channel only declares an internal transport here: it establishes no channel
   allocation, message ownership, buffering, delivery or cancellation policy. *)
let check_internal_connection context (connection : connection) source target =
  let at = connection.connection_at in
  if not (same_process source target) then
    reject context at "internal_process" "Calls and channels must remain in one process.";
  if connection.boundary <> None then
    reject context at "internal_boundary" "Calls and channels cannot name an external crossing boundary.";
  if connection.transport = Call && same_process source target && not (outlives target source) then
    reject context at "call_lifetime" "A called dependency must have the same or an enclosing lifetime as its caller."

(* This slice models outbound crossings. Their owner lives with, and outlives,
   the caller. The external target keeps a separate process lifetime. A gateway
   owns subprocess crossing policy; remote/device crossings may use an adapter. *)
let check_crossing_boundary context (connection : connection) source crossing =
  let at = connection.connection_at in
  match crossing with
  | None when connection.boundary = None ->
      reject context at "crossing_boundary" "An external crossing must name its owning boundary component."
  | None -> ()
  | Some boundary ->
      if not (same_process boundary source) then
        reject context at "boundary_process" "The crossing boundary must belong to the source process."
      else if not (outlives boundary source) then
        reject context at "boundary_lifetime" "The crossing boundary must outlive its source component.";
      let valid_role = match connection.transport, boundary.component.role with
        | Subprocess, Gateway -> true
        | (Remote | Device), (Adapter | Gateway) -> true
        | _ -> false
      in
      if not valid_role then reject context at "boundary_role"
        "Subprocess crossings require a gateway; remote and device crossings require an adapter or gateway."

let resolve_connection context components (connection : connection) =
  let at = connection.connection_at in
  let source = endpoint context components at connection.caller in
  let target = endpoint context components at connection.callee in
  let crossing = match connection.boundary with
    | None -> None
    | Some id -> endpoint context components at id
  in
  match source, target with
  | Some source, Some target ->
      check_connection_state context connection source target crossing;
      (match connection.transport with
      | Call | Channel -> check_internal_connection context connection source target
      | Subprocess | Remote | Device ->
          if same_process source target then
            reject context at "crossing_process" "An external crossing must connect distinct processes.";
          check_crossing_boundary context connection source crossing);
      Some { connection; source; target; crossing }
  | _ -> None

(* A candidate is ready when all of its providers are already in [completed].
   Components with no requires edges are immediately ready; communication edges
   do not silently introduce additional startup dependencies. *)
let dependencies_ready dependencies completed (candidate : resolved_component) =
  List.for_all (fun ((consumer : resolved_component), (provider : resolved_component)) ->
    consumer.component.component_id <> candidate.component.component_id ||
    List.exists (fun (done_ : resolved_component) ->
      done_.component.component_id = provider.component.component_id) completed) dependencies

(* Stable topological ordering: repeatedly take the first ready component in
   declaration order. A nonempty remainder with no ready node contains a cycle.
   For declarations [worker; storage] and worker requiring storage, the result is
   [storage; worker]. The reverse is a declarative shutdown order, not cleanup. *)
let rec dependency_order context dependencies completed remaining =
  match remaining with
  | [] -> Some (List.rev completed)
  | _ ->
      match List.find_opt (dependencies_ready dependencies completed) remaining with
      | None ->
          reject context context.architecture.at "dependency_cycle" "Requires relationships contain a dependency cycle.";
          None
      | Some next -> dependency_order context dependencies (next :: completed)
          (List.filter (fun (candidate : resolved_component) ->
            candidate.component.component_id <> next.component.component_id) remaining)

(* Evidence is retained for inspection, never promoted to a passing verdict.
   Both Scoped and Borrowed components keep an ownership obligation; a supplied
   Test_reference does not remove it. Planned implementation is a further item. *)
let component_obligations (resolved : resolved_component) =
  let component = resolved.component in
  let subject = component.component_id in
  let planned = if component.state = Planned then [{
    subject; requirement = "Implement and verify this planned component before treating it as existing.";
    evidence = None;
  }] else [] in
  let evidence = match component.verification with Pending -> None | Test_reference path -> Some path in
  let lifecycle = if component.lifecycle = Scoped then [{
    subject; evidence;
    requirement = "Verify automatic scope cleanup, child cancellation and joining, and cleanup error reporting. A test reference is evidence to inspect, not proof that tests pass or execution guarantees hold.";
  }] else [{
    subject; requirement = "Verify the declared borrowed ownership and lifetime contract; verification remains pending.";
    evidence;
  }] in
  planned @ lifecycle

let connection_obligations (resolved : resolved_connection) =
  if resolved.connection.connection_state = Planned then [{
    subject = resolved.source.component.component_id ^ " -> " ^ resolved.target.component.component_id;
    requirement = "Implement and verify this planned connection before treating it as existing.";
    evidence = None;
  }] else []

(* Several existing crossing owners are truthful migration state, not a graph
   error. Collect distinct gateways before reporting one consolidation item;
   multiple edges through the same gateway do not invent additional owners. *)
let gateway_obligations architecture connections =
  let gateways = List.filter_map (fun (resolved : resolved_connection) ->
    if resolved.connection.transport = Subprocess && resolved.connection.connection_state = Existing then
      Option.map (fun (boundary : resolved_component) -> boundary.component.component_id) resolved.crossing
    else None) connections |> List.sort_uniq String.compare in
  if List.length gateways > 1 then [{
    subject = architecture.name;
    requirement = "Consolidate existing subprocess ownership into one audited gateway; current gateways: " ^ String.concat ", " gateways ^ ".";
    evidence = None;
  }] else []

let collect_obligations architecture components connections =
  List.concat_map component_obligations components @
  List.concat_map connection_obligations connections @
  gateway_obligations architecture connections

(* Each phase completes before the next can depend on its resolved values.
   [filter_map] omits failed records, but their diagnostics prevent returning a
   partial graph as success. Only a consistent graph reaches the obligation and
   ordering result; filesystem and execution checks remain outside this pass. *)
let resolve (architecture : architecture) =
  let context = { architecture; diagnostics = []; scope_cache = [] } in
  check_declarations context;
  (* Ambiguous identifiers cannot be resolved without inventing ownership. *)
  if context.diagnostics <> [] then result_error context else
  let scopes = List.filter_map (resolve_scope context []) architecture.scopes in
  let components = List.filter_map (resolve_component context scopes) architecture.components in
  if context.diagnostics <> [] then result_error context else
  let dependencies = List.filter_map (resolve_dependency context components) architecture.dependencies in
  let connections = List.filter_map (resolve_connection context components) architecture.connections in
  let start_order = dependency_order context dependencies [] components in
  if context.diagnostics <> [] then result_error context else
  match start_order with
  | None -> result_error context
  | Some start_order -> Ok {
      architecture; scopes; components; connections; start_order;
      stop_order = List.rev start_order;
      obligations = collect_obligations architecture components connections;
    }
