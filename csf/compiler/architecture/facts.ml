open Model
module Term = Datalog_top_down.Default.T

let nonempty value = String.trim value <> ""
let present = function Some value -> nonempty value | None -> false

let atom value = Term.mk_const (Datalog_top_down.String value)
let index value = Term.mk_const (Datalog_top_down.Int value)
let fact relation arguments = Term.mk_apply_l (Datalog_top_down.String relation) arguments
let spelling encode value = atom (Syntax_cgen.Terminal.name (encode value))

let declarations (architecture : architecture) = Array.of_list (
  List.map (fun (p : process) -> p.process_id, p.process_at) architecture.processes @
  List.map (fun (s : scope) -> s.scope_id, s.scope_at) architecture.scopes @
  List.map (fun (c : component) -> c.component_id, c.component_at) architecture.components)

(* An empty identifier is reported by its own check and names nothing, so it
   joins no relation by identifier; its process still counts towards go_host. *)
let declaration_facts architecture =
  Array.to_list (Array.mapi (fun at (id, _) ->
    if nonempty id then [fact "declaration" [atom id; index at]] else []) (declarations architecture))
  |> List.concat

let process_fact at (p : process) =
  fact "process" [atom p.process_id; spelling Syntax_cgen.process_kind_terminal p.kind; index at]

let scope_fact at (s : scope) = fact "scope" [atom s.scope_id; atom s.parent; index at]

let component_facts at (c : component) =
  let id = atom c.component_id in
  [
    fact "component" [id; atom c.process; atom c.scope; index at];
    fact "role" [id; spelling Syntax_cgen.role_terminal c.role];
    fact "state" [id; spelling Syntax_cgen.state_terminal c.state];
    fact "lifecycle" [id; spelling Syntax_cgen.lifecycle_terminal c.lifecycle];
  ] @ (if present c.source then [fact "sourced" [id]] else [])

let dependency_fact at (d : dependency) = fact "requires" [index at; atom d.consumer; atom d.provider]

let connection_facts at (k : connection) =
  fact "connects" [index at; atom k.caller; atom k.callee;
    spelling Syntax_cgen.transport_terminal k.transport; spelling Syntax_cgen.state_terminal k.connection_state]
  :: (match k.boundary with Some id -> [fact "boundary" [index at; atom id]] | None -> [])

let of_architecture (architecture : architecture) =
  let scopes_at = List.length architecture.processes in
  let components_at = scopes_at + List.length architecture.scopes in
  declaration_facts architecture
  @ List.mapi process_fact architecture.processes
  @ List.mapi (fun at scope -> scope_fact (scopes_at + at) scope) architecture.scopes
  @ List.concat (List.mapi (fun at component -> component_facts (components_at + at) component) architecture.components)
  @ List.mapi dependency_fact architecture.dependencies
  @ List.concat (List.mapi connection_facts architecture.connections)
