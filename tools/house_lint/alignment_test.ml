let expect message condition = if not condition then failwith message
let raises message thunk =
  match thunk () with
  | exception Invalid_argument _ -> ()
  | _ -> failwith message

let signal id = List.find (fun (signal : Alignment.signal) -> signal.id = id) Alignment.signals
let member key = function `Assoc fields -> List.assoc key fields | _ -> failwith ("not an object at " ^ key)
let contains = Alignment.contains

let record observations : Alignment.record =
  {revision = "0123456789abcdef"; dirty = false; commit_time = 1_790_000_000; observations}

let all_measured count = record (List.map (fun signal -> signal, Alignment.Measured count) Alignment.signals)

let test_registry () =
  let ids = List.map (fun (signal : Alignment.signal) -> signal.id) Alignment.signals in
  expect "signal IDs are unique" (List.length ids = List.length (List.sort_uniq String.compare ids));
  expect "every weight is positive" (List.for_all (fun (signal : Alignment.signal) -> signal.weight > 0) Alignment.signals);
  expect "every signal states its meaning" (List.for_all (fun (signal : Alignment.signal) -> signal.meaning <> "") Alignment.signals);
  (* The acceptance surface names these eight signals; losing one silently
     changes what the score means without a method-version bump. *)
  List.iter (fun id -> ignore (signal id))
    ["csfc-check"; "generated-drift"; "cs-16"; "cs-15"; "cs-17"; "ontology-dirs"; "retired-vocabulary"; "unlinked-terms"];
  expect "eight signals" (List.length Alignment.signals = 8)

let test_score () =
  let clean = all_measured 0 in
  expect "a clean tree has zero penalty" (Alignment.penalty clean = 0);
  expect "a clean tree scores zero" (Alignment.score_of_penalty 0 = 0.);
  expect "a clean, fully measured tree is complete" (Alignment.complete clean);
  let total_weight = List.fold_left (fun total (signal : Alignment.signal) -> total + signal.weight) 0 Alignment.signals in
  expect "penalty is the weighted sum" (Alignment.penalty (all_measured 2) = 2 * total_weight);
  expect "the scale scores one half" (Alignment.score_of_penalty Alignment.scale = 0.5);
  expect "score is monotone in penalty" (Alignment.score_of_penalty 10 < Alignment.score_of_penalty 11);
  expect "score stays below one" (Alignment.score_of_penalty 1_000_000_000 < 1.);
  raises "a negative penalty is accepted" (fun () -> Alignment.score_of_penalty (-1));
  let cs15 = signal "cs-15" in
  let only = record [cs15, Measured 4; signal "cs-17", Not_measured "TODO: pending checker"] in
  expect "measured signal contributes weight times count" (Alignment.penalty only = 4 * cs15.weight);
  expect "an unmeasured signal makes the record incomplete" (not (Alignment.complete only))

let test_json () =
  let pending = record [signal "cs-16", Measured 3; signal "cs-17", Not_measured "TODO: pending checker"] in
  let json = Alignment.to_json pending in
  expect "schema is versioned" (member "schema" json = `String Alignment.schema);
  expect "method version is recorded" (member "method_version" json = `Int Alignment.method_version);
  expect "direction is explicit" (member "lower_is_better" json = `Bool true);
  expect "penalty is reported" (member "penalty" json = `Int (3 * (signal "cs-16").weight));
  expect "incomplete is reported" (member "complete" json = `Bool false);
  expect "the formula is stated" (match member "formula" json with `String text -> contains text "penalty + scale" | _ -> false);
  let signals = match member "signals" json with `List signals -> signals | _ -> failwith "signals is not a list" in
  let unmeasured = List.nth signals 1 in
  expect "an unmeasured count is null, not zero" (member "count" unmeasured = `Null);
  expect "an unmeasured signal says why" (member "reason" unmeasured = `String "TODO: pending checker");
  expect "an unmeasured signal contributes nothing" (member "contribution" unmeasured = `Int 0);
  expect "a measured count is reported" (member "count" (List.hd signals) = `Int 3);
  (* The record is one parseable JSON document. *)
  expect "JSON round-trips" (Yojson.Basic.from_string (Yojson.Basic.to_string json) = json)

let test_openmetrics () =
  let text = Alignment.to_openmetrics
    (record [signal "cs-15", Measured 0; signal "cs-17", Not_measured "TODO: pending checker"]) in
  expect "OpenMetrics ends with EOF" (String.ends_with ~suffix:"# EOF\n" text);
  expect "score sample carries the commit timestamp"
    (contains text "candace_ontology_alignment_score{method=\"1\"} 0.0000 1790000000");
  expect "a measured zero is a sample"
    (contains text "candace_ontology_alignment_signal_count{method=\"1\",signal=\"cs-15\"} 0 1790000000");
  expect "an unmeasured signal has no count sample"
    (not (contains text "signal_count{method=\"1\",signal=\"cs-17\"}"));
  expect "an unmeasured signal is marked unmeasured"
    (contains text "candace_ontology_alignment_signal_measured{method=\"1\",signal=\"cs-17\"} 0 1790000000");
  expect "the commit time is a value for age queries"
    (contains text "candace_ontology_alignment_commit_timestamp_seconds{method=\"1\"} 1790000000 1790000000");
  expect "completeness is a sample" (contains text "candace_ontology_alignment_complete{method=\"1\"} 0 1790000000");
  expect "the revision never becomes a label" (not (contains text "0123456789abcdef"))

let test_parsers () =
  let csfc = String.concat "\n" [
    "csf/architecture/architecture.csf:12:3: lifecycle: Services require a scoped lifecycle.";
    "go/x/manager.go:122:13: CSF_PROCESS_BOUNDARY: os/exec.Command requires a declared gateway source";
    "architecture=csf mode=check declarations=checked source=checked obligations=3";
    "";
  ] in
  expect "csfc diagnostics are counted, summaries are not" (Alignment.csfc_findings csfc = 2);
  expect "no output is no finding" (Alignment.csfc_findings "" = 0);
  expect "a line without a column is not a csfc diagnostic" (Alignment.csfc_findings "file.go:12: CODE: text" = 0);
  let drift = String.concat "\n" [
    "csf/architecture/generated/review_cgen.md:1:1: CSF_GENERATED_DRIFT: run csfc emit";
    "csf/architecture/generated/ocaml_cgen.ml:1:1: CSF_GENERATED_DRIFT: run csfc emit";
    "go/x/manager.go:122:13: CSF_PROCESS_BOUNDARY: not drift";
  ] in
  expect "only drift diagnostics count as drift" (Alignment.csfc_drift drift = 2);
  expect "documentation drift counts every named file"
    (Alignment.documentation_drift "CSF generator: generated documentation differs: a.md, b/c.md\n" = Some 2);
  expect "other generator failures are not drift"
    (Alignment.documentation_drift "CSF generator: vocabulary findings:\nREADME.md:3: retired word 'x': y" = None);
  let lint = String.concat "\n" [
    "README.md:3: retired word 'CandaceOS': name each part by its function";
    "README.md:7: unlinked ontology term 'service' (term service)";
    "pkg/README.md:2: unlinked ontology term 'widgets' (term widget)";
    "pkg/README.md:9: unlinked literature term 'RRSI' (literature rrsi)";
  ] in
  expect "retired and unlinked vocabulary are separated" (Alignment.vocabulary_counts lint = (1, 2));
  expect "a generator without lint is detected"
    (Alignment.lint_unsupported "CSF generator: usage: generate --root ROOT write|check|metrics [--manifest PATH]\n");
  expect "a generator with lint is not mistaken for an old one"
    (not (Alignment.lint_unsupported "CSF generator: usage: generate --root ROOT write|check|metrics [--manifest PATH] | lint FILE...\n"))

let refuses message thunk =
  match thunk () with
  | exception Failure _ -> ()
  | _ -> failwith message

let with_field key value = function
  | `Assoc fields -> `Assoc (List.map (fun (name, old) -> name, if name = key then value else old) fields)
  | json -> json

let test_round_trip () =
  let original = record [signal "cs-16", Measured 3; signal "cs-17", Not_measured "TODO: pending checker"] in
  let json = Alignment.to_json original in
  expect "a record reads back as itself" (Alignment.of_json json = original);
  refuses "another schema was accepted" (fun () -> Alignment.of_json (with_field "schema" (`String "other/v9") json));
  refuses "another method was accepted" (fun () ->
    Alignment.of_json (with_field "method_version" (`Int (Alignment.method_version + 1)) json));
  refuses "an unknown signal was accepted" (fun () ->
    Alignment.of_json (with_field "signals" (`List [`Assoc ["id", `String "invented"; "status", `String "measured";
      "count", `Int 1]]) json));
  refuses "a non-object was accepted" (fun () -> Alignment.of_json (`List []))

(* Records over the same eight signals with chosen counts for two of them. *)
let counts overrides = record (List.map (fun (signal : Alignment.signal) ->
  signal, (match List.assoc_opt signal.id overrides with Some observation -> observation | None -> Alignment.Measured 0))
  Alignment.signals)

let regressed ~base ~head = snd (Alignment.compare_records ~base ~head) |> Alignment.regressed

let test_ratchet () =
  let base = counts ["cs-15", Measured 10; "cs-16", Measured 2] in
  expect "an unchanged tree does not regress" (not (regressed ~base ~head:base));
  expect "fewer findings do not regress" (not (regressed ~base ~head:(counts ["cs-15", Measured 9; "cs-16", Measured 2])));
  expect "a higher penalty regresses" (regressed ~base ~head:(counts ["cs-15", Measured 11; "cs-16", Measured 2]));
  (* cs-16 is blocking: one more crossing fails even though ten goroutines left. *)
  expect "a blocking rise regresses while the penalty falls"
    (regressed ~base ~head:(counts ["cs-15", Measured 0; "cs-16", Measured 3]));
  (* cs-15 is not blocking: a trade that lowers the penalty passes. *)
  expect "a non-blocking rise that lowers the penalty passes"
    (not (regressed ~base ~head:(counts ["cs-15", Measured 11; "cs-16", Measured 1])));
  (* A signal measured on one side only can neither regress nor improve. *)
  let unmeasured_base = counts ["cs-15", Measured 10; "cs-17", Not_measured "TODO: pending checker"] in
  expect "a newly measured signal is not a regression"
    (not (regressed ~base:unmeasured_base ~head:(counts ["cs-15", Measured 10; "cs-17", Measured 40])));
  let _, verdict = Alignment.compare_records ~base:unmeasured_base ~head:(counts ["cs-15", Measured 10; "cs-17", Measured 40]) in
  expect "one-sided signals stay out of the comparable penalty"
    (verdict.penalty_before = verdict.penalty_after);
  let text, failed = Alignment.markdown ~base ~head:(counts ["cs-15", Measured 0; "cs-16", Measured 3]) ~accepted:false in
  expect "the markdown verdict matches the comparison" failed;
  expect "the table has its header" (contains text "| Signal | Weight | Blocking | Before | After | Delta |");
  expect "a row shows before, after and delta" (contains text "| `cs-16` | 5 | yes | 2 | 3 | +1 |");
  expect "the blocking rise is named" (contains text "blocking signal `cs-16` rose");
  expect "unaccepted regressions do not mention the label" (not (contains text "ontology-regression-accepted"));
  let accepted, _ = Alignment.markdown ~base ~head:(counts ["cs-15", Measured 0; "cs-16", Measured 3]) ~accepted:true in
  expect "an accepted regression keeps the table and names the label"
    (contains accepted "| `cs-16` |" && contains accepted "ontology-regression-accepted");
  let clean, failed = Alignment.markdown ~base ~head:base ~accepted:false in
  expect "a clean comparison says so" (not failed && contains clean "Result: no regression.");
  let one_sided, _ = Alignment.markdown ~base:unmeasured_base ~head:base ~accepted:false in
  expect "a one-sided signal shows as not measured" (contains one_sided "| `cs-17` | 3 | no | not measured | 0 | n/a |")

let test_pr_spec () =
  let base = counts ["cs-15", Measured 10; "cs-17", Not_measured "TODO: pending checker"] in
  let head = counts ["cs-15", Measured 4] in
  let spec = Alignment.pr_spec ~base ~head in
  expect "before is the base score" (member "before" spec = `Float (Alignment.round4 (Alignment.score_of_penalty 30)));
  expect "after is the head score" (member "after" spec = `Float (Alignment.round4 (Alignment.score_of_penalty 12)));
  let signals = match member "signals" spec with `List signals -> signals | _ -> failwith "signals is not a list" in
  expect "every registry signal is listed" (List.length signals = List.length Alignment.signals);
  let row id = List.find (fun entry -> member "id" entry = `String id) signals in
  expect "a measured signal carries both counts" (member "before" (row "cs-15") = `Int 10 && member "after" (row "cs-15") = `Int 4);
  expect "an unmeasured side is null" (member "before" (row "cs-17") = `Null && member "after" (row "cs-17") = `Int 0);
  expect "the spec has exactly the skill's keys" (match spec with
    | `Assoc fields -> List.map fst fields = ["before"; "after"; "signals"] | _ -> false)

let () =
  test_round_trip ();
  test_ratchet ();
  test_pr_spec ();
  test_registry ();
  test_score ();
  test_json ();
  test_openmetrics ();
  test_parsers ();
  print_endline "Ontology alignment tests passed"
