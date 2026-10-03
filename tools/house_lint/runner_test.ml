let expect message condition = if not condition then failwith message
let write root path contents = Out_channel.with_open_text (Filename.concat root path) (fun channel -> output_string channel contents)
let partition_selection root =
  let available : Runner.lane list = [
    {rule="CS-3"; name="reuse"; argv=[|"false"|]};
    {rule="INTERFACE-RETURNS"; name="interface-returns"; argv=[|"true"|]};
  ] in
  expect "ordinary invocations lost specialist coverage" (Runner.select_lanes None available = available);
  expect "native partition invoked a specialist" (Runner.select_lanes (Some "native") available = []);
  let partitions = List.concat_map (fun (lane : Runner.lane) ->
    Runner.select_lanes (Some lane.name) available) available in
  expect "partition union changed the complete scanner inventory" (partitions = available);
  List.iter (fun lanes ->
    let refused = try ignore (Runner.select_lanes (Some "reuse") lanes); false
      with Invalid_argument _ -> true in
    expect "missing or duplicated lane was accepted" refused) [ []; available @ [List.hd available] ];
  let report = Filename.concat root "partial-summary.md" in
  Out_channel.with_open_text report (fun channel -> Runner.summary ~selected_lane:"reuse" channel
    ~files:0 ~findings:[] ~errors:[] ~external_results:["CS-3", "reuse", 2]
    ~native_only:false ~report_only:false);
  let lines = String.split_on_char '\n' (Checker.read_file report) in
  expect "partial scan was presented as a complete native verdict"
    (List.mem "Native source scan was not selected." lines &&
     List.exists (String.starts_with ~prefix:"| CS-1 | mandatory | not run (different lane) |") lines);
  expect "specialist scan error disappeared from partial report"
    (List.exists (String.starts_with ~prefix:"| CS-3 | mandatory | reuse: SCAN ERROR |") lines)

let python_integration root =
  write root "fixture.py" "def choose():\n    return 'pending'\n";
  write root "fixture.ml" "let value = \"pending\"\n";
  let inventory = ["fixture.py"; "fixture.ml"; "go.mod"] in
  let files, python, errors = Runner.native root inventory inventory in
  expect "Python-only corpus not scanned or OCaml entered corpus" (files = 1 && errors = []);
  expect "Python rule missing or Go magic-string rule crossed languages"
    (List.map (fun (f : Source.finding) -> f.rule) python = ["PY-MAGIC-STRING"]);
  expect "Python advisory blocks" (Policy.status ~report_only:false python errors = 0);
  let report = Filename.concat root "python-summary.md" in
  Out_channel.with_open_text report (fun channel -> Runner.summary channel ~files ~findings:python
    ~errors ~external_results:[] ~native_only:true ~report_only:false);
  expect "Python count absent from report" (String.split_on_char '\n' (Checker.read_file report)
    |> List.exists (String.starts_with ~prefix:"| PY-MAGIC-STRING | advisory | 1 |"));
  write root "fixture.py" "def broken(:\n";
  let _, _, errors = Runner.native root ["fixture.py"; "go.mod"] ["fixture.py"; "go.mod"] in
  expect "Python parse error missing from combined scanner" (List.exists (fun (finding : Source.finding) ->
    finding.path = "fixture.py" && finding.rule = "PARSE") errors);
  expect "Python parse error was an optional finding" (Policy.status ~report_only:true [] errors = 2)

let generated_integration root =
  let header = "/*\n" ^ String.concat "\n" Codegen_header.banner ^ "\n*/\n" in
  write root "generated.gen.go" (header ^ {|package fixture
type Reader interface { Read(string) error }
func New() int { return 1 }
const query = `SELECT id FROM widgets`
func clearShared(pointer **int) {
  go func() { *pointer = nil }()
  _ = *pointer
}
func Guard(flag bool) {
  if flag { return } else { consume(flag) }
}
|});
  write root "generated_test.go" (header ^ "package fixture\nimport \"github.com/onsi/gomega\"\n");
  let inventory = ["fixture.go"; "generated.gen.go"; "generated_test.go"; "go.mod"] in
  let count, findings, errors = Runner.native root inventory inventory in
  expect "generated source changed handwritten count or scanner errors" (count = 1 && errors = []);
  expect "native rules reported on generated files"
    (List.for_all (fun (finding : Source.finding) -> finding.path = "fixture.go") findings);
  expect "generated exemption hid handwritten violations"
    (List.exists (fun (finding : Source.finding) -> finding.rule = "CS-1") findings &&
     List.exists (fun (finding : Source.finding) -> finding.rule = "CS-12") findings);
  let _, _, errors = Runner.native root ["generated.gen.go"] ["generated.gen.go"] in
  expect "generated-only inventory became a vacuous pass" (Policy.status ~report_only:true [] errors = 2);
  write root "generated.gen.go" (header ^ "package fixture\nimport _ \"github.com/gorilla/mux\"\n");
  let _, findings, errors = Runner.native root inventory inventory in
  expect "generated exemption bypassed dependency policy"
    (errors = [] && List.exists (fun (finding : Source.finding) ->
      finding.path = "generated.gen.go" && finding.rule = "DEPENDENCIES") findings);
  write root "generated.gen.go" (header ^ "package fixture\nimport \"\\q\"\n");
  let _, _, errors = Runner.native root inventory inventory in
  expect "generated exemption hid malformed imports" (Policy.status ~report_only:true [] errors = 2)

let () =
  expect "CRLF generated marker entered handwritten corpus"
    (not (Source.selected (Source.parse "generated.go" "// Code generated by fixture. DO NOT EDIT.\r\npackage fixture\r\n")));
  let root = Filename.temp_file "house-lint-native-" "" in
  Sys.remove root;
  Unix.mkdir root 0o700;
  Fun.protect ~finally:(fun () -> Sys.readdir root |> Array.iter (fun name -> Sys.remove (Filename.concat root name)); Unix.rmdir root) (fun () ->
    partition_selection root;
    write root "go.mod" "module example.invalid/fixture\ngo 1.26\n";
    write root "fixture.go" "package fixture\ntype Reader interface { Read(value string) error }\nfunc New() int { return 1 }\n";
    let inventory = ["fixture.go"; "go.mod"] in
    let files, findings, errors = Runner.native root inventory inventory in
    expect "fixture scan failed" (files = 1 && errors = []);
    expect "mandatory result disappeared" (List.exists (fun (f:Source.finding) -> f.rule = "CS-1") findings);
    expect "advisory result lost after mandatory violation" (List.exists (fun (f:Source.finding) -> f.rule = "CS-12") findings);
    expect "combined verdict does not block" (Policy.status ~report_only:false findings errors = 1);
    generated_integration root;
    write root "handlers.go" {|package fixture
import (
  "net/http"
  "database/sql"
)
type apiHandlers struct { store *sql.DB; service *Service }
func (handler *apiHandlers) Serve(request *http.Request) {
  _ = request.URL.Query()
  handler.service.FindSession(request.Context())
  handler.store.Query(request.Context(), "select")
}
|};
    write root "returns.go" {|package fixture
func HandleRequest(request *Request) {
  if request.Code() != "" {
    return
  } else {
    continueRequest(request)
  }
}
|};
    let _, boundary_findings, boundary_errors = Runner.native root
      ["handlers.go"; "returns.go"; "go.mod"] ["handlers.go"; "returns.go"; "go.mod"] in
    expect "handler and else rules are wired into the native runner" (boundary_errors = [] &&
      List.exists (fun (finding : Source.finding) -> finding.rule = "HANDLER-DB-IO") boundary_findings &&
      List.exists (fun (finding : Source.finding) -> finding.rule = "ELSE-AFTER-RETURN") boundary_findings);
    expect "both new mandatory rules affect the CLI policy verdict"
      (Policy.status ~report_only:false boundary_findings boundary_errors = 1 &&
       Policy.status ~report_only:true boundary_findings boundary_errors = 0);
    let boundary_report = Filename.concat root "boundary-summary.md" in
    Out_channel.with_open_text boundary_report (fun channel -> Runner.summary channel ~files:2
      ~findings:boundary_findings ~errors:boundary_errors ~external_results:[] ~native_only:true ~report_only:false);
    let boundary_summary = Checker.read_file boundary_report in
    expect "mandatory handler rule missing from runner report"
      (String.split_on_char '\n' boundary_summary |> List.exists
        (String.starts_with ~prefix:"| HANDLER-DB-IO | mandatory |"));
    expect "mandatory else rule missing from runner report"
      (String.split_on_char '\n' boundary_summary |> List.exists
        (String.starts_with ~prefix:"| ELSE-AFTER-RETURN | mandatory |"));
    write root "shared.go" "package fixture\nfunc clearShared(pointer **int) {\n go func() { *pointer = nil }()\n _ = *pointer\n}\n";
    let _, shared, shared_errors = Runner.native root ["shared.go"; "go.mod"] ["shared.go"; "go.mod"] in
    expect "shared-state rule not wired into the runner"
      (List.exists (fun (f : Source.finding) -> f.rule = "GOROUTINE-SHARED-STATE" && f.line = 3) shared);
    expect "shared-state advisory blocks or fails scanning" (Policy.status ~report_only:false shared shared_errors = 0);
    let report = Filename.concat root "summary.md" in
    Out_channel.with_open_text report (fun channel -> Runner.summary channel ~files:1 ~findings:shared
      ~errors:shared_errors ~external_results:[] ~native_only:true ~report_only:false);
    expect "shared-state count absent from report" (String.split_on_char '\n' (Checker.read_file report)
      |> List.exists (String.starts_with ~prefix:"| GOROUTINE-SHARED-STATE | advisory | 1 |"));
    python_integration root;
    write root "scratch.go" "package fixture\nimport _ \"github.com/gorilla/mux\"\n";
    let _, findings, errors = Runner.native root inventory ("scratch.go" :: inventory) in
    expect "dependency scope lost untracked source" (errors = [] && List.exists (fun (f:Source.finding) -> f.rule = "DEPENDENCIES") findings);
    let _, _, errors = Runner.native root ["missing.go"] ["missing.go"] in
    expect "unreadable source passed" (errors <> []);
    let _, _, errors = Runner.native root [] [] in
    expect "empty inventory passed" (errors <> []);
    write root "fixture.go" "package fixture\nfunc Broken( {\n";
    let _, _, errors = Runner.native root inventory inventory in
    expect "invalid syntax passed" (errors <> []);
    write root "fixture.go" "package fixture\nfunc broken(value int {}\n";
    let _, _, errors = Runner.native root inventory inventory in
    expect "missing unnamed syntax token passed" (errors <> []);
    write root "fixture.go" "package fixture\nfunc broken() { consume(\"\\q\") }\n";
    write root "other.go" "package fixture\ntype Reader interface { Read(value string) error }\n";
    let _, findings, errors = Runner.native root ("other.go" :: inventory) ("other.go" :: inventory) in
    expect "invalid escape aborted other file findings" (errors <> [] &&
      List.exists (fun (f:Source.finding) -> f.rule = "CS-1") findings);
    let output = Filename.concat root "process.log" in
    expect "specialist stdout/status lost" (Runner.process [|"sh"; "-c"; "printf 'fixture output'; exit 2"|] output = 2 && Checker.read_file output = "fixture output"));
  print_endline "House lint integrated runner tests passed"
