(* Semantic checks operate on declarations only. They resolve ownership and
   ordering without reading source files, starting processes or executing tests.
   A valid graph may still carry implementation and inspection obligations.

   Checks on a single record stay here as OCaml predicates. Every check that
   is a join, closure or count is a finding relation in rules.dl, evaluated
   over the facts Facts extracts; this module only asks for each relation and
   turns its answers into diagnostics. *)
open Model
module Datalog = Datalog_top_down.Default

let program = lazy (match Datalog.parse_string Rules_cgen.source with
  | `Ok clauses -> clauses
  | `Error message -> invalid_arg ("rules.dl: " ^ message))

(* The engine's aggregates apply a builtin to the set of grouped values. *)
let count = function
  | Datalog.T.Apply (_, values) -> Some (Datalog.T.mk_const (Datalog_top_down.Int (Array.length values)))
  | Datalog.T.Var _ -> None

let database architecture =
  let db = Datalog.DB.create () in
  Datalog.setup_default db;
  Datalog.DB.add_builtin db (Datalog_top_down.String "count") count;
  Datalog.DB.add_clauses db (Lazy.force program);
  Datalog.DB.add_facts db (Facts.of_architecture architecture);
  db

(* Every answer is ground, so each argument is a constant. *)
let ask db relation arity =
  Datalog.ask db (Datalog.T.mk_apply (Datalog_top_down.String relation) (Array.init arity Datalog.T.mk_var))
  |> List.map (function
    | Datalog.T.Apply (_, arguments) -> Array.to_list (Array.map (function
        | Datalog.T.Apply (value, [||]) -> value
        | term -> invalid_arg ("non-constant answer argument: " ^ Datalog.T.to_string term)) arguments)
    | term -> invalid_arg ("non-ground answer: " ^ Datalog.T.to_string term))

let text = function Datalog_top_down.String value -> value | Datalog_top_down.Int value -> string_of_int value
let number = function Datalog_top_down.Int value -> value | value -> invalid_arg ("not an index: " ^ text value)

(* A finding relation's first argument is the index of the record it is
   reported at, unless the finding belongs to the whole architecture. *)
type place = Architecture | Declaration | Dependency | Connection
type finding = { relation : string; code : string; arity : int; place : place; message : string list -> string }

let finding ?code relation arity place message =
  { relation; code = Option.value code ~default:relation; arity; place; message }
let fixed message _ = message
let naming prefix details = prefix ^ List.nth details (List.length details - 1)

let declaration_findings = [
  finding "duplicate_identifier" 2 Declaration (naming "Identifier is declared more than once: ");
  finding "go_host" 0 Architecture (fixed "Exactly one Go application process is required.");
]

let ownership_findings = [
  finding "scope_parent" 2 Declaration (naming "Unknown process or scope parent: ");
  finding "scope_cycle" 2 Declaration (naming "Scope ownership contains a cycle through: ");
  finding "component_process" 2 Declaration (naming "Unknown component process: ");
  finding "component_scope" 2 Declaration (naming "Unknown component scope: ");
  finding "component_role" 1 Declaration (fixed "External process endpoints must use the resource role.");
  finding "component_source" 1 Declaration (fixed "An existing in-process component requires a nonempty source path.");
  finding "scope_owner" 1 Declaration (fixed "The component process and scope owner disagree.");
]

let relationship_findings = [
  finding "requires_reference" ~code:"component_reference" 3 Dependency (naming "Unknown component: ");
  finding "connects_reference" ~code:"component_reference" 3 Connection (naming "Unknown component: ");
  finding "dependency_process" 1 Dependency (fixed "A requires relationship must remain in one process.");
  finding "dependency_lifetime" 1 Dependency (fixed "A provider must have the same or an enclosing lifetime as its consumer.");
  finding "planned_dependency" 1 Dependency (fixed "An existing consumer cannot require a planned provider.");
  finding "dependency_cycle" 0 Architecture (fixed "Requires relationships contain a dependency cycle.");
  finding "planned_connection" 2 Connection (fixed "An existing connection cannot depend on a planned endpoint or boundary.");
  finding "internal_process" 1 Connection (fixed "Calls and channels must remain in one process.");
  finding "internal_boundary" 1 Connection (fixed "Calls and channels cannot name an external crossing boundary.");
  finding "call_lifetime" 1 Connection (fixed "A called dependency must have the same or an enclosing lifetime as its caller.");
  finding "crossing_process" 1 Connection (fixed "An external crossing must connect distinct processes.");
  finding "crossing_boundary" 1 Connection (fixed "An external crossing must name its owning boundary component.");
  finding "boundary_process" 1 Connection (fixed "The crossing boundary must belong to the source process.");
  finding "boundary_lifetime" 1 Connection (fixed "The crossing boundary must outlive its source component.");
  finding "boundary_role" 1 Connection
    (fixed "Subprocess crossings require a gateway; remote and device crossings require an adapter or gateway.");
]

let finding_codes =
  List.sort_uniq String.compare (List.map (fun f -> f.code) (declaration_findings @ ownership_findings @ relationship_findings))

let diagnostics db (architecture : architecture) findings =
  let declarations = Facts.declarations architecture in
  let dependencies = Array.of_list architecture.dependencies in
  let connections = Array.of_list architecture.connections in
  let locate place values = match place, values with
    | Architecture, details -> architecture.at, details
    | Declaration, at :: details -> snd declarations.(number at), details
    | Dependency, at :: details -> dependencies.(number at).dependency_at, details
    | Connection, at :: details -> connections.(number at).connection_at, details
    | _, [] -> invalid_arg "finding without a record index" in
  List.concat_map (fun f -> ask db f.relation f.arity |> List.map (fun values ->
    let at, details = locate f.place values in
    { at; code = f.code; message = f.message (List.map text details) })) findings

(* The tree census answers a question outside declaration resolution: is every
   tracked file in a directory whose declaration allows its kind? Facts computes
   the prefix relations the rules cannot; the rules are shared with the
   declaration checks. An empty [paths] (an unreadable checkout) yields no
   offense rather than a false one. *)
type tree_report = {
  offenses : diagnostic list;
  directories_declared : int;
  directories_tracked : int;
  fanout : (string * int) list;
}

let tree_diagnostics (architecture : architecture) paths =
  let db = database architecture in
  Datalog.DB.add_facts db (Facts.tree_facts architecture paths);
  let at = { file = architecture.at.file; line = 1; column = 1 } in
  let kind_of = ask db "offense_kind" 2
    |> List.map (function [file; kind] -> text file, text kind | _ -> assert false) in
  let offenses = ask db "offense" 3 |> List.map (function
    | [_tree; file; cause] -> let path = text file in match text cause with
        | "undeclared" ->
            { at; code = "tree_undeclared"; message = path ^ " is in no declared directory." }
        | "kind_not_allowed" ->
            { at; code = "tree_kind_not_allowed";
              message = path ^ " has kind " ^ List.assoc path kind_of ^ ", which its directory does not allow." }
        | "csf_outside_csf" ->
            { at; code = "tree_csf_outside_csf"; message = path ^ " is a CSF source outside csf/." }
        | cause -> invalid_arg ("unknown tree offense: " ^ cause)
    | _ -> assert false) in
  let fanout = ask db "fanout" 2
    |> List.map (function [directory; count] -> text directory, number count | _ -> assert false)
    |> List.sort compare in
  let tier_mismatch = ask db "tier_mismatch" 3 |> List.map (function
    | [directory; expected; crossed] ->
        let directory = text directory and expected = text expected and crossed = text crossed in
        { at; code = "tree_tier_mismatch";
          message = directory ^ " lives under the " ^ expected ^ " tier but its declaration names " ^ crossed ^ "." }
    | _ -> assert false) in
  let io_children_stray = ask db "io_children_stray" 1 |> List.map (function
    | [directory] ->
        { at; code = "tree_io_children_stray";
          message = text directory ^ " sits under io/ but is not one of the four tier directories." }
    | _ -> assert false) in
  let io_children_missing = ask db "io_children_missing" 1 |> List.map (function
    | [tier] ->
        { at; code = "tree_io_children_missing";
          message = "io/ has no " ^ text tier ^ " tier directory." }
    | _ -> assert false) in
  { offenses = offenses @ tier_mismatch @ io_children_stray @ io_children_missing;
    directories_declared = List.length architecture.directories;
    directories_tracked = Facts.tracked_directories paths; fanout }

(* The JEV-writability census answers a second question outside declaration
   resolution: can one JEV pick write each production of the CSF grammars? The
   same [offense] relation answers it, tagged with the [grammar] tree, so the
   grammar sites go into a fresh database where no tracked file or census fact
   exists and a grammar site cannot appear among the tree offenses. An empty
   [sites] yields no offense rather than a false one; the report always carries
   the census, which is the meter the gate watches. *)
type grammar_report = {
  offenses : diagnostic list;
  use_sites : int;
  bounded_use_sites : int;
}

let grammar_diagnostics (sites : Grammar.use_site list) =
  let db = Datalog.DB.create () in
  Datalog.setup_default db;
  Datalog.DB.add_builtin db (Datalog_top_down.String "count") count;
  Datalog.DB.add_clauses db (Lazy.force program);
  Datalog.DB.add_facts db (Facts.grammar_facts sites);
  let located = List.map (fun site -> Grammar.site_id site, site) sites in
  let offenses = ask db "offense" 3 |> List.filter_map (function
    | [tree; site; cause] when text tree = "grammar" ->
        let id = text site in
        let site = List.assoc id located in
        (match text cause with
         | "unbounded" ->
             Some { at = { file = site.Grammar.file; line = 1; column = 1 };
               code = "grammar_unbounded";
               message = id ^ " is not writable by one JEV pick: it offers free text or more than sixteen alternatives." }
         | cause -> invalid_arg ("unknown grammar offense: " ^ cause))
    | _ -> None) |> List.sort compare in
  let bounded, total = Grammar.share sites in
  { offenses; use_sites = total; bounded_use_sites = bounded }

(* Checks on one record. *)
let process_diagnostics (process : process) = match process.kind with
  | Go when not (Facts.present process.entrypoint) ->
      [{ at = process.process_at; code = "entrypoint"; message = "The Go host must declare a nonempty application entrypoint." }]
  | External when process.entrypoint <> None ->
      [{ at = process.process_at; code = "entrypoint"; message = "An external process cannot own the Go application entrypoint." }]
  | _ -> []

let declaration_diagnostics (architecture : architecture) =
  (if architecture.version <> 1 then
    [{ at = architecture.at; code = "version"; message = "Only architecture version 1 is supported." }] else []) @
  (if Facts.nonempty architecture.name then [] else
    [{ at = architecture.at; code = "identifier"; message = "The architecture name cannot be empty." }]) @
  (Array.to_list (Facts.declarations architecture) |> List.filter_map (fun (id, at) ->
    if Facts.nonempty id then None else Some { at; code = "identifier"; message = "Identifiers cannot be empty." })) @
  List.concat_map process_diagnostics architecture.processes

(* Service is the lifecycle-bearing role. Manager remains conceptual and may
   borrow its lifetime. Requiring Scoped or Lazy records a contract; it cannot
   establish that the implementation actually registers, cancels or joins
   goroutines. *)
let component_diagnostics (component : component) =
  let at = component.component_at in
  (if component.role = Service && component.lifecycle = Borrowed then
    [{ at; code = "lifecycle"; message = "Services require a scoped or lazy lifecycle." }] else []) @
  match component.state, component.verification with
  | Planned, Test_reference _ ->
      [{ at; code = "planned_evidence"; message = "A planned component cannot advertise a test reference." }]
  | _, Test_reference reference when not (Facts.nonempty reference) ->
      [{ at; code = "verification"; message = "A test reference cannot be empty." }]
  | _ -> []

(* Lifetime containment from the closure in rules.dl. [ancestors] lists the
   scope itself first and then each enclosing scope: a deeper scope has more
   ancestors. *)
let resolve_scopes db (architecture : architecture) =
  let owner = ask db "scope_process" 2 |> List.map (function [s; p] -> text s, text p | _ -> assert false) in
  let depth = ask db "scope_depth" 2 |> List.map (function [s; n] -> text s, number n | _ -> assert false) in
  let pairs = ask db "scope_ancestor" 2 |> List.map (function [s; a] -> text s, text a | _ -> assert false) in
  List.map (fun (scope : scope) ->
    let process_owner = List.find (fun (p : process) -> p.process_id = List.assoc scope.scope_id owner) architecture.processes in
    let ancestors = List.filter_map (fun (s, a) -> if s = scope.scope_id then Some a else None) pairs
      |> List.sort (fun left right -> compare (List.assoc right depth) (List.assoc left depth)) in
    { scope; process_owner; ancestors }) architecture.scopes

(* A candidate is ready when all of its providers are already in [completed].
   Components with no requires edges are immediately ready; communication edges
   do not silently introduce additional startup dependencies. *)
let dependencies_ready dependencies completed (candidate : resolved_component) =
  List.for_all (fun ((consumer : resolved_component), (provider : resolved_component)) ->
    consumer.component.component_id <> candidate.component.component_id ||
    List.exists (fun (done_ : resolved_component) ->
      done_.component.component_id = provider.component.component_id) completed) dependencies

(* Stable topological ordering: repeatedly take the first ready component in
   declaration order. dependency_cycle has already rejected every cycle, so a
   ready component always exists. For declarations [worker; storage] and worker
   requiring storage, the result is [storage; worker]. The reverse is a
   declarative shutdown order, not cleanup. *)
let rec dependency_order dependencies completed = function
  | [] -> List.rev completed
  | remaining ->
      let next = List.find (dependencies_ready dependencies completed) remaining in
      dependency_order dependencies (next :: completed)
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
  let lifecycle = match component.lifecycle with
    | Scoped -> [{
        subject; evidence;
        requirement = "Verify automatic scope cleanup, child cancellation and joining, and cleanup error reporting. A test reference is evidence to inspect, not proof that tests pass or execution guarantees hold.";
      }]
    | Lazy -> [{
        subject; evidence;
        requirement = "Verify start on first use, idle retirement that cancels and joins the run, restart on the next use, and that no work in hand is lost to retirement. A test reference is evidence to inspect, not proof.";
      }]
    | Borrowed -> [{
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
   error. gateway_consolidation counts distinct gateways, so multiple edges
   through the same gateway do not invent additional owners. *)
let gateway_obligations db architecture =
  if ask db "gateway_consolidation" 1 = [] then [] else
  let gateways = ask db "current_gateway" 1 |> List.concat_map (List.map text) |> List.sort String.compare in
  [{
    subject = architecture.name;
    requirement = "Consolidate existing subprocess ownership into one audited gateway; current gateways: " ^ String.concat ", " gateways ^ ".";
    evidence = None;
  }]

(* Each phase completes before the next can depend on its results: ambiguous
   identifiers cannot be resolved without inventing ownership, and an
   unresolved owner or lifetime leaves nothing to relate. Diagnostics within a
   phase are reported in source order. Only a consistent graph reaches the
   obligation and ordering result; filesystem and execution checks remain
   outside this pass. *)
let resolve (architecture : architecture) =
  let db = database architecture in
  let phases = [
    (fun () -> declaration_diagnostics architecture @ diagnostics db architecture declaration_findings);
    (fun () -> List.concat_map component_diagnostics architecture.components @ diagnostics db architecture ownership_findings);
    (fun () -> diagnostics db architecture relationship_findings);
  ] in
  let failed = List.find_map (fun phase -> match phase () with
    | [] -> None
    | errors -> Some (List.stable_sort compare errors)) phases in
  match failed with
  | Some errors -> Error errors
  | None ->
      let scopes = resolve_scopes db architecture in
      let components = List.map (fun (component : component) -> {
          component;
          process_owner = List.find (fun (p : process) -> p.process_id = component.process) architecture.processes;
          lifetime = List.find (fun (s : resolved_scope) -> s.scope.scope_id = component.scope) scopes;
        }) architecture.components in
      let named id = List.find (fun (c : resolved_component) -> c.component.component_id = id) components in
      let connections = List.map (fun (connection : connection) -> {
          connection; source = named connection.caller; target = named connection.callee;
          crossing = Option.map named connection.boundary;
        }) architecture.connections in
      let dependencies = List.map (fun (d : dependency) -> named d.consumer, named d.provider) architecture.dependencies in
      let start_order = dependency_order dependencies [] components in
      Ok {
        architecture; scopes; components; connections; start_order;
        stop_order = List.rev start_order;
        obligations = List.concat_map component_obligations components @
          List.concat_map connection_obligations connections @ gateway_obligations db architecture;
      }
