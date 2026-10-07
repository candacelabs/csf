type severity = Mandatory | Advisory
type rule = { id : string; severity : severity; description : string }

(* OPERATOR RULING (2026-10-06), verbatim: "WE NEED TO RELAX MERGE GATES THAT
   ARE STOPPING US FROM GETTING TO 100% CONSISTENCY" and "you can relax
   pre-existing invariants if our chief invariants need to be compromised".
   CS-20, CS-21, CS-20-HOST, CS-20-RUNTIME, CS-20-SPAWN, CS-20-LISTEN and
   NO-MAIN-TEST were registered Mandatory before the tree was retrofitted, so
   they blocked every push with a backlog no single change could clear. They
   are Advisory under that ruling: every finding stays in the report and a scan
   error still fails, but a finding no longer blocks. A rule returns to
   Mandatory only in the change that drives its count to zero. *)

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
  {id="CS-16"; severity=Advisory; description="Network listen/dial and gRPC clients outside the io/ crossing mechanisms; fork/exec outside io/ipc/proc"};
  {id="CS-16-DB"; severity=Mandatory; description="PostgreSQL pools and connections outside io/ipc/db/csfpg, tests included"};
  {id="CS-17"; severity=Advisory; description="Process environment reads outside the config capability"};
  {id="ATOMIC-WRITE"; severity=Mandatory; description="Files written and renamed into place outside pkg/atomicfile, tests included"};
  {id="CS-18-MOCKGEN"; severity=Advisory; description="Exported interfaces without a mockgen directive and tracked generated mock"};
  {id="CS-18-CROSSING"; severity=Advisory; description="Tests crossing real sockets, subprocesses, database pools or containers outside a labelled acceptance suite"};
  {id="CS-18-EXTERNAL"; severity=Advisory; description="Packages with exported API but no external _test package holding specs"};
  {id="CS-19"; severity=Advisory; description="Reflection, anonymous-struct decoding, string maps for typed fields and local generic helpers"};
  {id="CS-20"; severity=Advisory; description="Every compound is named"};
  {id="CS-21"; severity=Advisory; description="Code of one type lives together"};
  {id="CS-20-HOST"; severity=Advisory; description="Applications not mounting exactly one runtime.HostRuntime"};
  {id="CS-20-RUNTIME"; severity=Advisory; description="runtime.HostRuntime built outside a package-main file"};
  {id="CS-20-SPAWN"; severity=Advisory; description="Subprocess spawn (os/exec) outside io/ipc/proc"};
  {id="CS-20-LISTEN"; severity=Advisory; description="Socket bind outside io/ and outside a package-main file"};
  {id="ONTOLOGY-DIRS"; severity=Advisory; description="Directories not named by CSF ontology terms"};
  {id="NO-MAIN-TEST"; severity=Advisory; description="Test file cannot have package main or main_test"};
  (* Operator ruling, 2026-10-06: "THE MERGE GATES DON'T MATTER ANYMORE WE NEED TO RELAX MERGE GATES THAT ARE STOPPING US
     FROM GETTING TO 100% CONSISTENCY". The document rules were written for the paper draft and flag 1,545 findings across
     about 240 tracked Markdown files; they report, and no longer block. *)
  {id="DOC-MATH"; severity=Advisory; description="Math symbols outside $...$; unbalanced $ delimiters; unknown LaTeX commands"};
  {id="DOC-CONTRAST"; severity=Advisory; description="Contrast sentences must name both sides and provide examples"};
  {id="DOC-EXAMPLE"; severity=Advisory; description="Abstract claims must have examples within two sentences"};
  {id="DOC-EVIDENCE"; severity=Advisory; description="Numbers outside References need daggers; agent-draft banner required"};
  {id="DOC-PROVENANCE"; severity=Advisory; description="Document provenance: δ (generated), σ (model output), η (signed human)"};
  {id="DOC-GAP"; severity=Advisory; description="Gap markers need next-step markers in the same paragraph"};
  {id="DOC-CONTRADICTION"; severity=Advisory; description="Present-tense claims about incomplete terms/milestones"};
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
