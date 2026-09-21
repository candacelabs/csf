(** Convert accepted syntax into owned semantic records. The generic Extended
    Backus-Naur Form (EBNF) parser permits grammar changes; this decoder understands only the Model
    contract. Every child must be accounted for, so a new syntactic field cannot
    silently disappear before semantic validation. *)
open Model
module Rule = Syntax_cgen.Rule
module Terminal = Syntax_cgen.Terminal

exception Invalid_node of diagnostic

let invalid (node : Typed_tree.node) message =
  raise (Invalid_node { at = node.at; code = "CSF_MODEL"; message })

(* Decoding consumes the entire typed tree. Syntax accepted by an edited
   grammar must never vanish simply because Model does not recognize it. *)
type reader = { parent : Typed_tree.node; mutable remaining : Typed_tree.node list }

let take rule reader = match reader.remaining with
  | node :: rest when node.Typed_tree.rule = rule -> reader.remaining <- rest; node
  | node :: _ -> invalid node ("expected " ^ Rule.name rule ^ ", found " ^ Rule.name node.Typed_tree.rule)
  | [] -> invalid reader.parent ("expected " ^ Rule.name rule)

(* A reader belongs to one branch. [take] advances only its [remaining] list;
   it does not mutate the tree. Even a successful callback must consume every
   child: adding another known field to the grammar therefore still needs a
   decoder change, rather than being ignored by record construction. *)
let fields rule node decode =
  if node.Typed_tree.rule <> rule || node.value <> Typed_tree.Branch then
    invalid node ("expected " ^ Rule.name rule ^ " rule without a literal value");
  let reader = { parent = node; remaining = node.children } in
  let result = decode reader in
  (match reader.remaining with
   | [] -> ()
   | extra :: _ -> invalid extra ("unconsumed " ^ Rule.name extra.Typed_tree.rule ^ " in " ^ Rule.name rule));
  result

(* Identifiers, integer text and paths are data. Keyword identity has already
   become a generated variant at the Typed_tree boundary. *)
let text rule reader =
  let node = take rule reader in
  match node.Typed_tree.value, node.children with
  | Typed_tree.Lexeme value, [] -> value
  | _ -> invalid node ("expected " ^ Rule.name rule ^ " literal without children")

let keyword reader =
  let node = take Rule.Terminal reader in
  match node.Typed_tree.value, node.children with
  | Typed_tree.Keyword value, [] -> value
  | _ -> invalid node ("expected " ^ Rule.name Rule.Terminal ^ " literal without children")

let terminal expected reader =
  let node = match reader.remaining with first :: _ -> first | [] -> reader.parent in
  if keyword reader <> expected then invalid node ("expected terminal " ^ Terminal.name expected)

(* Look ahead without advancing when the introducer is absent. For example,
   a missing source clause leaves the following state clause for its reader. *)
let optional expected rule reader = match reader.remaining with
  | node :: _ when node.Typed_tree.rule = Rule.Terminal && node.value = Typed_tree.Keyword expected ->
      terminal expected reader; Some (text rule reader)
  | _ -> None

(* The generated mapping owns finite alternatives: a role keyword becomes a
   Model.role here. Rules with payloads, such as a test path, need the explicit
   assembly in [verification] below instead of another spelling table. *)
let choice rule mapping reader =
  let node = take rule reader in
  fields rule node (fun reader ->
    let symbol = keyword reader in
    match mapping symbol with
    | Some value -> value
    | None -> invalid node ("unsupported " ^ Rule.name rule ^ ": " ^ Terminal.name symbol))

(* Partial application fixes the rule and mapping; [state] still takes the
   reader, just as a closure can remember two arguments for a later call. *)
let state = choice Rule.State Syntax_cgen.state

let process node = fields Rule.Process node (fun reader ->
  terminal Terminal.Process reader;
  let process_id = text Rule.Identifier reader in
  terminal Terminal.Kind reader;
  let kind = choice Rule.Process_kind Syntax_cgen.process_kind reader in
  let entrypoint = optional Terminal.Entrypoint Rule.String reader in
  terminal Terminal.Semicolon reader;
  { process_id; kind; entrypoint; process_at = node.Typed_tree.at })

let scope node = fields Rule.Scope node (fun reader ->
  terminal Terminal.Scope reader;
  let scope_id = text Rule.Identifier reader in
  terminal Terminal.Under reader;
  let parent = text Rule.Identifier reader in
  terminal Terminal.Semicolon reader;
  { scope_id; parent; scope_at = node.Typed_tree.at })

(* Test_reference carries the path as data. Constructing this value neither
   reads that file nor runs the test; those are separate inspection concerns. *)
let verification reader =
  let node = take Rule.Verification reader in
  fields Rule.Verification node (fun reader ->
    match keyword reader with
    | Terminal.Pending -> Pending
    | Terminal.Test -> Test_reference (text Rule.String reader)
    | _ -> invalid node ("expected " ^ Terminal.name Terminal.Pending ^ " or " ^
        Terminal.name Terminal.Test ^ " reference"))

let component node = fields Rule.Component node (fun reader ->
  let role = choice Rule.Role Syntax_cgen.role reader in
  let component_id = text Rule.Identifier reader in
  terminal Terminal.In reader;
  let process = text Rule.Identifier reader in
  terminal Terminal.Scope reader;
  let scope = text Rule.Identifier reader in
  let source = optional Terminal.Source Rule.String reader in
  terminal Terminal.State reader;
  let state = state reader in
  terminal Terminal.Lifecycle reader;
  let lifecycle = choice Rule.Lifecycle Syntax_cgen.lifecycle reader in
  terminal Terminal.Verification reader;
  let verification = verification reader in
  terminal Terminal.Semicolon reader;
  { component_id; process; scope; role; source; state; lifecycle; verification;
    component_at = node.Typed_tree.at })

let dependency node = fields Rule.Dependency node (fun reader ->
  terminal Terminal.Requires reader;
  let consumer = text Rule.Identifier reader in
  terminal Terminal.Arrow reader;
  let provider = text Rule.Identifier reader in
  terminal Terminal.Semicolon reader;
  { consumer; provider; dependency_at = node.Typed_tree.at })

let connection node = fields Rule.Connection node (fun reader ->
  terminal Terminal.Connect reader;
  let caller = text Rule.Identifier reader in
  terminal Terminal.Arrow reader;
  let callee = text Rule.Identifier reader in
  terminal Terminal.Via reader;
  let transport = choice Rule.Transport Syntax_cgen.transport reader in
  let boundary = optional Terminal.Boundary Rule.Identifier reader in
  terminal Terminal.State reader;
  let connection_state = state reader in
  terminal Terminal.Semicolon reader;
  { caller; callee; boundary; transport; connection_state; connection_at = node.Typed_tree.at })

let source_root rule symbol node = fields rule node (fun reader ->
  terminal symbol reader;
  let path = text Rule.String reader in
  terminal Terminal.Semicolon reader;
  { path; path_at = node.Typed_tree.at })

(* Require one recognized declaration beneath its grammar wrapper. Filtering
   unknown children later would accept syntax while discarding its meaning. *)
let declaration node = fields Rule.Declaration node (fun reader ->
  match reader.remaining with
  | [item] when List.mem item.Typed_tree.rule
      [Rule.Process; Rule.Scope; Rule.Component; Rule.Dependency; Rule.Connection; Rule.Scan; Rule.Generated] ->
      reader.remaining <- []; item
  | _ -> invalid node "expected one supported declaration")

(* Prepend while reading, then reverse once to retain input order without
   repeatedly copying a growing prefix. A non-declaration is left for the
   enclosing architecture reader, which must consume the closing delimiter. *)
let declarations reader =
  let rec gather reversed = match reader.remaining with
    | node :: _ when node.Typed_tree.rule = Rule.Declaration ->
        let node = declaration (take Rule.Declaration reader) in
        gather (node :: reversed)
    | _ -> List.rev reversed in
  gather []

(* Group declarations by their Model field while preserving order within each
   group. Records retain declaration positions; optional paths remain data. *)
let decode_architecture node = fields Rule.Architecture node (fun reader ->
  terminal Terminal.Architecture reader;
  let name = text Rule.Identifier reader in
  terminal Terminal.Version reader;
  let version = match int_of_string_opt (text Rule.Integer reader) with
    | Some value -> value | None -> invalid node "version exceeds integer range" in
  terminal Terminal.LeftBrace reader;
  let declarations = declarations reader in
  terminal Terminal.RightBrace reader;
  let collect rule convert = declarations
    |> List.filter (fun (item : Typed_tree.node) -> item.rule = rule) |> List.map convert in
  { name; version; at = node.Typed_tree.at;
    processes = collect Rule.Process process; scopes = collect Rule.Scope scope;
    components = collect Rule.Component component; dependencies = collect Rule.Dependency dependency;
    connections = collect Rule.Connection connection; scan_roots = collect Rule.Scan (source_root Rule.Scan Terminal.Scan);
    generated_roots = collect Rule.Generated (source_root Rule.Generated Terminal.Generated) })

(** Public boundary: convert generic parser labels to typed vocabulary once,
    then assemble the model. Expected decoding errors become [Error diagnostics];
    unrelated implementation exceptions are not disguised as invalid input. *)
let architecture (node : Frontend.node) =
  match Typed_tree.of_frontend node with
  | Error diagnostics -> Error diagnostics
  | Ok node ->
      try Ok (decode_architecture node)
      with Invalid_node diagnostic -> Error [diagnostic]
