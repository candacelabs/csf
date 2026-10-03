type severity = Mandatory | Advisory
type rule = { id : string; severity : severity; description : string }

let rules = [
  {id="CS-1"; severity=Mandatory; description="I-prefixed interfaces"};
  {id="CS-2"; severity=Mandatory; description="Named input parameters"};
  {id="CS-3"; severity=Mandatory; description="Duplicate primitives (dupl)"};
  {id="CS-4"; severity=Advisory; description="Handwritten SQL, mocks and unmarked generated filenames"};
  {id="CS-5"; severity=Advisory; description="Mutex declarations"};
  {id="CS-6"; severity=Advisory; description="Hardcoded receiver dispatch lists"};
  {id="CS-7"; severity=Advisory; description="Erased public contracts"};
  {id="CS-8"; severity=Mandatory; description="Owned interface returns"};
  {id="CS-9"; severity=Advisory; description="Sleep loops in tests"};
  {id="CS-10"; severity=Advisory; description="Service process ownership"};
  {id="CS-11"; severity=Mandatory; description="Ginkgo/Gomega dot imports"};
  {id="CS-12"; severity=Advisory; description="Generic constructor names"};
  {id="CS-13"; severity=Advisory; description="Go magic strings"};
  {id="PY-MAGIC-STRING"; severity=Advisory; description="Python semantic string literals in function bodies"};
  {id="CS-14"; severity=Advisory; description="Optional-value conversion twins"};
  {id="HANDLER-DB-IO"; severity=Mandatory; description="Database and store I/O stays behind service operations"};
  {id="ELSE-AFTER-RETURN"; severity=Mandatory; description="No else branch after a branch that returns on every path"};
  {id="TEST-BOOTSTRAP"; severity=Mandatory; description="Ginkgo suite bootstraps"};
  {id="DEPENDENCIES"; severity=Mandatory; description="Forbidden Gorilla dependencies"};
  {id="INTERFACE-RETURNS"; severity=Advisory; description="All interface returns (Go type analysis)"};
  {id="FUNCTION-LENGTH"; severity=Advisory; description="Functions exceeding 60 non-comment lines"};
  {id="GOROUTINE-SHARED-STATE"; severity=Advisory; description="Captured shared writes with later caller access; local synchronization review"};
  {id="CS-15"; severity=Advisory; description="Goroutines with no visible join or context-driven exit"};
  {id="CS-16"; severity=Advisory; description="Network listen/dial and gRPC clients outside ipc/; fork/exec outside ipc/proc"};
  {id="CS-16-DB"; severity=Mandatory; description="PostgreSQL pools and connections outside ipc/db/csfpg, tests included"};
  {id="CS-17"; severity=Advisory; description="Process environment reads outside the config capability"};
  {id="CS-18-MOCKGEN"; severity=Advisory; description="Exported interfaces without a mockgen directive and tracked generated mock"};
  {id="CS-18-CROSSING"; severity=Advisory; description="Tests crossing real sockets, subprocesses, database pools or containers outside a labelled acceptance suite"};
  {id="CS-18-EXTERNAL"; severity=Advisory; description="Packages with exported API but no external _test package holding specs"};
  {id="ONTOLOGY-DIRS"; severity=Advisory; description="Directories not named by CSF ontology terms"};
]

let find id = List.find (fun rule -> rule.id = id) rules
let label = function Mandatory -> "mandatory" | Advisory -> "advisory"

(* The crossing rules block inside an owner once its sites reach zero, tests
   included, while the rest of the tree still carries its backlog: an owner
   enters this list in the slice that drives it to zero, and never leaves. *)
let zero_crossing_owners = ["services/copilot-adapter/"]
let crossing_rules = ["CS-16"; "CS-16-DB"; "CS-18-CROSSING"]
let owner_mandatory (finding : Source.finding) =
  List.mem finding.rule crossing_rules &&
  List.exists (fun prefix -> String.starts_with ~prefix finding.path) zero_crossing_owners

(* The policy column of the summary: a rule's own severity, plus the owners it
   already blocks in. *)
let describe rule =
  if rule.severity = Advisory && List.mem rule.id crossing_rules then
    label Advisory ^ "; mandatory in " ^ String.concat ", " zero_crossing_owners
  else label rule.severity

let blocks finding = (find finding.Source.rule).severity = Mandatory || owner_mandatory finding
let status ~report_only findings errors =
  if errors <> [] then 2
  else if not report_only && List.exists blocks findings then 1
  else 0

(* Every lane runs even after findings. A broken advisory scanner is an error,
   never an optional finding and never a clean pass. *)
let external_status ~report_only severity code =
  if code = 0 then 0
  else if code = 1 then if severity = Mandatory && not report_only then 1 else 0
  else 2
