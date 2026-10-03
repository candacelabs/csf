open Contract

let check condition message = if not condition then failwith message
let span line = { source = "fixture"; line }
let fact relation args line = { relation; args; span = span line }

(* An instance is late when it was observed, scores above the knee and was
   not excused: one join, one threshold, one negation. *)
let rules = {|
late(I) :- observed(I), score(I, S), knee(K), gt(S, K), ~excused(I).
|}

let miner = { name = "contract-test"; package = "services/ouroboros/contract"; rules;
  verdicts = [{ name = "late"; arity = 1; severity = S3 }]; extract = (fun _ -> []) }

let facts = [
  fact "observed" [Text "a"] 1; fact "score" [Text "a"; Number 900] 2;
  fact "observed" [Text "b"] 3; fact "score" [Text "b"; Number 100] 4;
  fact "observed" [Text "c"] 5; fact "score" [Text "c"; Number 1200] 6;
  fact "observed" [Text "d"] 7; fact "score" [Text "d"; Number 2000] 8; fact "excused" [Text "d"] 9;
]

let label instance positive at flagged = { instance; positive; at; flagged; source = "fixture" }

let raises f = match f () with exception Invalid_rules _ -> true | _ -> false

(* Field names per message, read from the schema the encoders claim. *)
let schema path =
  let messages = Hashtbl.create 8 and current = ref "" in
  In_channel.with_open_text path In_channel.input_lines |> List.iter (fun line ->
    match String.split_on_char ' ' (String.trim line) |> List.filter (( <> ) "") with
    | "message" :: name :: _ -> current := name
    | ("repeated" | "optional") :: _ :: name :: "=" :: _ | _ :: name :: "=" :: _ when !current <> "" ->
        Hashtbl.add messages !current name
    | _ -> ());
  fun message -> Hashtbl.find_all messages message

let keys = function `Assoc fields -> List.map fst fields | _ -> failwith "not an object"

let tests = [
  "a finding carries the ground premises that derived it", (fun () ->
    match findings ~knee:500 miner facts with
    | [a; c] ->
        check (a.subject = [Text "a"] && c.subject = [Text "c"]) "late should be exactly a and c";
        check (a.proof = [fact "observed" [Text "a"] 1; fact "score" [Text "a"; Number 900] 2;
                          { relation = "knee"; args = [Number 500]; span = { source = "services/ouroboros/contract/rules.dl"; line = 0 } }])
          "the proof of a is observed(a), score(a, 900) and knee(500), in body order"
    | other -> failwith (Printf.sprintf "expected 2 findings, got %d" (List.length other)));
  "unsafe and unstratified rules are refused", (fun () ->
    check (raises (fun () -> check_rules "bad(X) :- ~seen(X).")) "negation before binding must be refused";
    check (raises (fun () -> check_rules "p(X) :- q(X), ~r(X).\nr(X) :- q(X), p(X).")) "negation through recursion must be refused";
    check (not (raises (fun () -> check_rules rules))) "the test rules are sound");
  "the knee is fitted on the earlier labels and judged on the later ones", (fun () ->
    let labels = [label "c" true 300 (Some 2000); label "a" true 100 None; label "b" false 200 None; label "d" true 400 None] in
    let result = backtest miner facts labels in
    check (List.map (fun l -> l.instance) result.labels = ["a"; "b"; "c"; "d"]) "labels are in time order";
    check (result.split = 200) "fit on a and b, accept on c and d";
    check (result.knee = Some 100) "the smallest knee with no fitted FN and full precision";
    check (result.families = 4) "four distinct scores were tried";
    check (result.tp = ["c"] && result.fp = [] && result.fn = ["d"]) "c fires; the excused d is a false negative";
    check (result.lead = Some 1600) "c starts at 300, fires at 400, is flagged at 2000");
  "the JSON encoders emit only fields records.proto declares", (fun () ->
    let field = schema Sys.argv.(1) in
    let within message json = List.iter (fun key ->
      check (List.mem key (field message)) (message ^ " has no field " ^ key)) (keys json) in
    let finding = List.hd (findings ~knee:500 miner facts) in
    within "Finding" (finding_json finding);
    within "Fact" (fact_json (List.hd finding.proof));
    within "Span" (match fact_json (List.hd finding.proof) with `Assoc fields -> List.assoc "span" fields | _ -> `Null);
    within "Value" (match finding_json finding with `Assoc fields -> (match List.assoc "subject" fields with `List (v :: _) -> v | _ -> `Null) | _ -> `Null);
    let result = backtest miner facts [label "a" true 100 (Some 900); label "b" false 200 None] in
    within "Backtest" (backtest_json result);
    within "Label" (match backtest_json result with `Assoc fields -> (match List.assoc "labels" fields with `List (l :: _) -> l | _ -> `Null) | _ -> `Null));
  "the event log reader streams lines with their numbers", (fun () ->
    let path = Filename.temp_file "events" ".jsonl" in
    Out_channel.with_open_text path (fun channel -> output_string channel "{\"a\":1}\n\n{\"b\":2}\n");
    let lines = List.of_seq (Corpus.jsonl path) |> List.map fst in
    Sys.remove path;
    check (lines = [1; 3]) "blank lines are skipped and numbering follows the file");
  "timestamps are Unix seconds", (fun () ->
    check (Corpus.seconds "1970-01-01T00:00:00Z" = 0) "epoch";
    check (Corpus.seconds "2026-10-02T19:03:08.809646873Z" = 1790967788) "fractional seconds are dropped");
]

let () =
  List.iter (fun (name, test) ->
    try test () with exception_ ->
      Printf.eprintf "FAIL %s: %s\n" name (Printexc.to_string exception_);
      exit 1) tests;
  Printf.printf "Ouroboros contract: %d tests passed\n" (List.length tests)
