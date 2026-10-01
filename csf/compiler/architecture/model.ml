(** The declaration model and the resolved graph share one vocabulary.
    Declaration records retain names and source locations; [Validate.resolve]
    replaces relationships between those names with references to typed records.
    Neither representation observes a running application or establishes cleanup. *)

(** One-based positions in the architecture input, retained through decoding
    and emission so a relationship error can point back to its declaration. *)
type location = { file : string; line : int; column : int }
type diagnostic = { at : location; code : string; message : string }

(** [Go] identifies the one shared application host; [External] identifies
    an endpoint outside it. These constructors do not start either process. *)
type process_kind = Go | External

(** [Existing] declares implementation presence, not deployment or verification. *)
type state = Existing | Planned

(** Only [Service] requires [Scoped] by role. [Manager] names coordination
    policy and may borrow a lifetime; its name does not introduce a goroutine. *)
type role = Service | Manager | Library | Adapter | Gateway | Resource

(** [Scoped] declares cleanup, cancellation and joining obligations. [Borrowed]
    leaves ownership with the caller. Validation checks the declaration only. *)
type lifecycle = Scoped | Borrowed

(** An OCaml variant can carry data: [Test_reference "worker_test.go"] stores
    a path while [Pending] carries none. Neither alternative says a test passed. *)
type verification = Pending | Test_reference of string

(** [Call] and [Channel] stay in one process. The other alternatives require
    a named crossing owner; a channel declaration supplies no queue policy. *)
type transport = Call | Channel | Subprocess | Remote | Device

(** The host owns the application entrypoint. External endpoints have no
    entrypoint in this model; their implementation is supplied separately. *)
type process = {
  process_id : string;
  kind : process_kind;
  entrypoint : string option;
  process_at : location;
}

(** [parent] names a process or another scope. This is lifetime containment,
    not a directory tree or a hierarchy of component owners. A child service
    remains a Service in a nested scope; explicit parent-service ownership
    and cancellation/joining behavior are not represented by this record. *)
type scope = {
  scope_id : string;
  parent : string;
  scope_at : location;
}

(** Unresolved [process] and [scope] strings are user-chosen identifiers.
    [source] identifies implementation to inspect, not code to execute. *)
type component = {
  component_id : string;
  role : role;
  process : string;
  scope : string;
  source : string option;
  state : state;
  lifecycle : lifecycle;
  verification : verification;
  component_at : location;
}

(** The edge points from consumer to provider. For [worker -> storage],
    storage must outlive worker and precedes it in declarative startup order. *)
type dependency = { consumer : string; provider : string; dependency_at : location }

(** A communication relationship is distinct from a startup dependency.
    [boundary] names the component owning an external crossing on the caller
    side; it does not transfer lifetime ownership to the remote target. *)
type connection = {
  caller : string;
  callee : string;
  transport : transport;
  boundary : string option;
  connection_state : state;
  connection_at : location;
}

(** A declared source-inspection root and the location that requested it.
    Filesystem existence and containment belong to the separate source check. *)
type source_root = { path : string; path_at : location }

(** Lists preserve declaration order. This order supplies deterministic
    tie breaking and output order; it is not an observation of execution. *)
type architecture = {
  name : string;
  version : int;
  at : location;
  processes : process list;
  scopes : scope list;
  components : component list;
  dependencies : dependency list;
  connections : connection list;
  scan_roots : source_root list;
  generated_roots : source_root list;
}

(* A test reference is evidence to inspect, never a proof that a test passed. *)
type obligation = { subject : string; requirement : string; evidence : string option }

(** [ancestors] includes self, then enclosing scopes: for example
    [["turn"; "request"; "application"]]. The process is stored separately. *)
type resolved_scope = { scope : scope; process_owner : process; ancestors : string list }

(** These links retain the actual resolved records, rather than asking each
    consumer to look up the component's process and scope names again. *)
type resolved_component = { component : component; process_owner : process; lifetime : resolved_scope }

(** [source]/[target] are components, while [crossing] is the optional
    adapter or gateway. Their declaration locations remain available. *)
type resolved_connection = {
  connection : connection;
  source : resolved_component;
  target : resolved_component;
  crossing : resolved_component option;
}

(** Successful declaration resolution. [start_order] uses explicit dependency
    edges; [stop_order] reverses it. These are suggested orders, not executed
    hooks. [obligations] remain work to inspect even when evidence is named. *)
type resolved = {
  architecture : architecture;
  scopes : resolved_scope list;
  components : resolved_component list;
  connections : resolved_connection list;
  start_order : resolved_component list;
  stop_order : resolved_component list;
  obligations : obligation list;
}
