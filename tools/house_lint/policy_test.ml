let expect message condition = if not condition then failwith message
let finding rule : Source.finding = {path="fixture.go"; line=1; rule; message="fixture"}
let () =
  List.iter (fun rule ->
    expect (rule ^ " is not mandatory") ((Policy.find rule).severity = Policy.Mandatory);
    expect (rule ^ " does not block") (Policy.status ~report_only:false [finding rule] [] = 1);
    expect (rule ^ " blocks report-only mode") (Policy.status ~report_only:true [finding rule] [] = 0))
    ["HANDLER-DB-IO"; "ELSE-AFTER-RETURN"; "CS-16-DB"];
  expect "the network part of CS-16 became blocking before its test sites moved"
    ((Policy.find "CS-16").severity = Policy.Advisory);
  (* An owner driven to zero blocks on every crossing rule, its tests
     included; the same finding elsewhere stays advisory. *)
  let at path rule : Source.finding = {path; line=1; rule; message="fixture"} in
  List.iter (fun rule ->
    List.iter (fun path ->
      expect (rule ^ " stopped blocking in " ^ path) (Policy.status ~report_only:false [at path rule] [] = 1);
      expect (rule ^ " blocks report-only mode in " ^ path) (Policy.status ~report_only:true [at path rule] [] = 0))
      ["services/copilot-adapter/traces.go"; "services/copilot-adapter/integration/adapter_test.go"];
    expect (rule ^ " became blocking outside its zero owners")
      (rule = "CS-16-DB" || Policy.status ~report_only:false [at "services/deploy/store/store.go" rule] [] = 0);
    expect (rule ^ " matched an owner by a bare name prefix")
      (rule = "CS-16-DB" || Policy.status ~report_only:false [at "services/copilot-adapter-legacy/main.go" rule] [] = 0))
    ["CS-16"; "CS-16-DB"; "CS-18-CROSSING"];
  expect "an owner-scoped rule blocked a different rule in the owner"
    (Policy.status ~report_only:false [at "services/copilot-adapter/traces.go" "CS-13"] [] = 0);
  expect "the summary hides where CS-16 blocks"
    (Policy.describe (Policy.find "CS-16") = "advisory; mandatory in services/copilot-adapter/");
  expect "the summary relabelled a globally mandatory rule"
    (Policy.describe (Policy.find "CS-16-DB") = "mandatory");
  expect "shared-state review became blocking"
    ((Policy.find "GOROUTINE-SHARED-STATE").severity = Policy.Advisory);
  expect "Python magic-string review became blocking"
    ((Policy.find "PY-MAGIC-STRING").severity = Policy.Advisory);
  let advisory = List.filter (fun (rule : Policy.rule) -> rule.severity = Advisory) Policy.rules in
  List.iter (fun (rule : Policy.rule) ->
    expect (rule.id ^ " became blocking") (Policy.status ~report_only:false [finding rule.id] [] = 0)) advisory;
  let mandatory = List.filter (fun (rule : Policy.rule) -> rule.severity = Mandatory) Policy.rules in
  List.iter (fun (rule : Policy.rule) ->
    expect (rule.id ^ " lost enforcement") (Policy.status ~report_only:false [finding rule.id] [] = 1);
    expect "report-only still fails on findings" (Policy.status ~report_only:true [finding rule.id] [] = 0)) mandatory;
  expect "errors lost to optional findings" (Policy.status ~report_only:true [finding "CS-13"] [finding "SCAN"] = 2);
  List.iter (fun severity -> List.iter (fun report_only ->
    expect "specialist failure hidden" (Policy.external_status ~report_only severity 2 = 2);
    expect "process launch failure hidden" (Policy.external_status ~report_only severity 127 = 2)) [true;false])
    [Policy.Mandatory;Policy.Advisory];
  expect "optional specialist findings block" (Policy.external_status ~report_only:false Policy.Advisory 1 = 0);
  expect "mandatory specialist findings pass" (Policy.external_status ~report_only:false Policy.Mandatory 1 = 1);
  expect "rule IDs duplicate" (List.length Policy.rules = List.length (List.sort_uniq String.compare (List.map (fun (r:Policy.rule) -> r.id) Policy.rules)));
  print_endline "House lint policy tests passed"
