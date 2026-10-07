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

(* ---- Tree census: tracked files, their kinds and directory declarations ----

   The rules cannot compare or split strings, so every prefix relation is
   computed here and emitted as a fact: [covers] holds the directory/file pairs
   a declaration governs and [deeper] orders declared directories by containment.
   The offense logic itself stays in rules.dl. *)

(* The census kind is the file's extension, or its whole name when it has none,
   lowercased and reduced to letters, digits and underscores. This matches how
   the tree census spells a kind, so [go] is a kind and never a grammar token. *)
let kind_of_path path =
  let base = Filename.basename path in
  let extension = match String.rindex_opt base '.' with
    | Some dot when dot + 1 < String.length base -> String.sub base (dot + 1) (String.length base - dot - 1)
    | _ -> base in
  let rec sanitize index reversed =
    if index = String.length extension then String.of_seq (List.to_seq (List.rev reversed))
    else
      let next = match extension.[index] with
        | 'A' .. 'Z' as letter -> Char.lowercase_ascii letter
        | ('a' .. 'z' | '0' .. '9' | '_') as character -> character
        | _ -> '_' in
      sanitize (index + 1) (next :: reversed) in
  sanitize 0 []

(* Every directory that contains the file, deepest first. A repository-root file
   has only the root directory ".". *)
let ancestors path =
  let rec enclosing directory reversed =
    let reversed = directory :: reversed in
    if directory = "." then List.rev reversed else enclosing (Filename.dirname directory) reversed in
  if String.contains path '/' then enclosing (Filename.dirname path) [] else ["."]

(* A declaration at a directory governs its whole subtree; a deeper declaration
   overrides an ancestor's, which the rules realize through [deeper] and
   [shadowed]. The root "." therefore covers every tracked path. *)
let covers directory path =
  directory = "." || String.starts_with ~prefix:(directory ^ "/") path

(* [child] lies strictly below [parent] among directories. *)
let deeper child parent =
  if parent = "." then child <> "." else String.starts_with ~prefix:(parent ^ "/") child

(* A directory whose path names a [testdata] segment holds fixtures, not a menu
   of choices; only the other directories are questions the census measures. *)
let testdata path =
  List.exists (fun segment -> segment = "testdata") (String.split_on_char '/' path)

(* [child] is the immediate-subdirectory relation: [path] sits directly under
   [parent]. The root "." is the parent of every top-level directory. *)
let child parent path =
  path <> "." && Filename.dirname path = parent

(* The tier a declared directory lives under, read from the first path segment
   after [io]. The four io children name the tiers; the "/" segment maps the
   [inproc] directory to the [in_process] tier, because a directory name cannot
   hold the hyphen-free spelling's underscore-free synonym otherwise. A path
   outside [io] carries no tier. *)
let under_io path = match String.split_on_char '/' path with
  | "io" :: "inproc" :: _ -> Some "in_process"
  | "io" :: "kernel" :: _ -> Some "kernel"
  | "io" :: "ipc" :: _ -> Some "ipc"
  | "io" :: "net" :: _ -> Some "net"
  | _ -> None

let tree_facts (architecture : architecture) paths =
  let directories = architecture.directories in
  let directory_paths = List.map (fun (d : directory) -> d.path) directories in
  List.map (fun path -> fact "tracked" [atom path]) paths
  @ List.map (fun path -> fact "kind_of" [atom path; atom (kind_of_path path)]) paths
  @ List.concat_map (fun (d : directory) ->
      fact "directory" [atom d.path] ::
      List.map (fun kind -> fact "allowed" [atom d.path; atom kind]) d.allowed) directories
  @ List.concat_map (fun path ->
      List.filter_map (fun directory -> if covers directory path then
        Some (fact "covers" [atom directory; atom path]) else None) (ancestors path)) paths
  @ List.concat_map (fun child ->
      List.filter_map (fun parent -> if deeper child parent then
        Some (fact "deeper" [atom child; atom parent]) else None) directory_paths) directory_paths
  @ List.filter_map (fun path ->
      if testdata path then Some (fact "directory_role" [atom path; atom "testdata"]) else None) directory_paths
  @ List.concat_map (fun path ->
      List.filter_map (fun parent -> if child parent path then
        Some (fact "child" [atom parent; atom path]) else None) directory_paths) directory_paths
  @ List.concat_map (fun (d : directory) ->
      match under_io d.path with
      | Some tier -> [fact "under_io" [atom d.path; atom tier]]
      | None -> []) directories
  @ List.filter_map (fun (d : directory) ->
      Option.map (fun tier ->
        fact "crosses" [atom d.path; spelling Syntax_cgen.tier_terminal tier]) d.tier) directories

(* The JEV-writability census as facts: one [use_site] per grammar production,
   and [bounded_use_site] for each a single JEV pick can write. The rules decide
   the offense; this only states which side of the bound each site is on. *)
let grammar_facts (sites : Grammar.use_site list) =
  List.concat_map (fun site ->
    let id = atom (Grammar.site_id site) in
    fact "use_site" [id] ::
    (if Grammar.bounded site then [fact "bounded_use_site" [id]] else [])) sites

(* The set of directories the tracked files occupy, counting every ancestor up
   to and including the root. This is the denominator for the panel. *)
module Directory_set = Set.Make (String)

let tracked_directories paths =
  let add directory directories =
    let rec walk directory directories =
      let directories = Directory_set.add directory directories in
      if directory = "." then directories else walk (Filename.dirname directory) directories in
    walk directory directories in
  List.fold_left (fun directories path ->
    let directory = match String.rindex_opt path '/' with
      | Some _ -> Filename.dirname path
      | None -> "." in
    add directory directories) Directory_set.empty paths
  |> Directory_set.cardinal
