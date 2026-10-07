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
  verdicts = [{ name = "late"; arity = 1; severity = S3 }]; scope = Generic; extract = (fun _ -> []) }

let facts = [
  fact "observed" [Text "a"] 1; fact "score" [Text "a"; Number 900] 2;
  fact "observed" [Text "b"] 3; fact "score" [Text "b"; Number 100] 4;
  fact "observed" [Text "c"] 5; fact "score" [Text "c"; Number 1200] 6;
  fact "observed" [Text "d"] 7; fact "score" [Text "d"; Number 2000] 8; fact "excused" [Text "d"] 9;
]

let label instance positive at flagged = { instance; positive; at; flagged; source = "fixture" }

(* Λ for the mutation gate, on which the miner above passes every check: the
   excused d is a negative here. κ⋆ = 100 fits on a and b; c is the one true
   positive and d the negative the negation excuses. *)
let accepted = [label "a" true 100 None; label "b" false 200 None; label "c" true 300 (Some 2000); label "d" false 400 None]
let committed = { reproduce = "miner.exe backtest labels.tsv fixtures"; block = backtest_block ~command:"miner.exe backtest labels.tsv fixtures" (backtest miner facts accepted) }
let gate = Mutate.run ~checks:(Contract.checks ~committed) miner facts accepted
let trial description = List.find (fun (trial : Mutate.trial) -> trial.mutant.description = description) gate.trials
let without name miner facts labels = List.filter (fun (check, _) -> check <> name) (Contract.checks ~committed miner facts labels)

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
    check (not (raises (fun () -> check_rules rules))) "the test rules are sound";
    check (not (raises (fun () -> check_rules ""))) "an empty program is sound";
    check (findings ~knee:500 { miner with rules = "" } facts = []) "an empty program derives nothing");
  "the acceptance checks pass on the miner and name what fails", (fun () ->
    List.iter (fun (name, run) -> try run () with exception_ -> failwith (name ^ ": " ^ Printexc.to_string exception_))
      (Contract.checks ~committed miner facts accepted);
    let failing = List.filter_map (fun (name, run) -> match run () with () -> None | exception _ -> Some name)
      (Contract.checks ~committed { miner with rules = "late(I) :- observed(I), score(I, S), gt(S, 100), ~excused(I)." } facts accepted) in
    check (failing = ["the knee binds"]) ("a written threshold fails exactly the knee check, not " ^ String.concat ", " failing));
  "every kind of mutant is generated and prints as source the engine reads back", (fun () ->
    let two = { miner with rules = rules ^ "early(I) :- observed(I), score(I, S), knee(K), S < K, tagged(I, \"x-1\").";
      verdicts = [{ name = "late"; arity = 1; severity = S3 }; { name = "early"; arity = 1; severity = S3 }] } in
    let mutants = Mutate.mutants two facts accepted in
    let kinds = List.sort_uniq compare (List.map (fun (mutant : Mutate.mutant) -> mutant.kind) mutants) in
    check (List.length kinds = 8) (Printf.sprintf "8 kinds, got %d" (List.length kinds));
    check (List.exists (fun (mutant : Mutate.mutant) -> mutant.description = "rules 1 and 2: `early` for `late` and back") mutants) "the verdict heads swap";
    check (List.exists (fun (mutant : Mutate.mutant) -> mutant.description = "rule 2: `S <= K` for `S < K`") mutants) "an infix comparison swaps";
    List.iter (fun (mutant : Mutate.mutant) -> match check_rules mutant.rules with
      | () | exception Invalid_rules _ -> ()
      | exception exception_ -> failwith (mutant.description ^ ": " ^ Printexc.to_string exception_)) mutants;
    let unparsed = List.filter (fun (mutant : Mutate.mutant) -> match check_rules mutant.rules with
      | exception Invalid_rules reason -> String.starts_with ~prefix:"rules do not parse" reason
      | _ -> false) mutants in
    check (unparsed = []) ("every mutant parses, unlike: " ^ String.concat "; " (List.map (fun (m : Mutate.mutant) -> m.rules) unparsed));
    check (String.ends_with ~suffix:"tagged(I, \"x-1\")." (List.hd (List.filter (fun (m : Mutate.mutant) -> m.description = "rule 2: drop `observed(I)`") mutants)).rules)
      "a quoted constant keeps its quotes");
  "the miner kills every mutant; excluded ones carry their reason", (fun () ->
    check (Mutate.rejections gate = []) (String.concat "\n" (Mutate.rejections gate));
    check (gate.killed = 13 && gate.survived = 0 && gate.excluded = 6)
      (Printf.sprintf "13 killed, 0 survived, 6 excluded; got %d, %d, %d" gate.killed gate.survived gate.excluded);
    check (Mutate.score gate = 1.) "the score is killed over counted";
    check ((trial "rule 1: drop `observed(I)`").outcome = Mutate.Excluded "equivalent: the same verdicts under no knee and under every knee candidate")
      "every instance is observed, so dropping the atom changes no verdict";
    check (match (trial "rule 1: drop `knee(K)`").outcome with Mutate.Excluded reason -> String.starts_with ~prefix:"refused: builtin before" reason | _ -> false)
      "an unbound knee is refused, not counted";
    check ((trial "rule 1: write 100 for `knee(K)`").outcome = Mutate.Killed "the knee binds") "only the knee check sees a written κ⋆";
    check ((trial "rule 1: write 100 for `knee(K)`").labeled = ["a"; "c"]) "the written knee changes a and c under other knees";
    check ((trial "rule 1: `ge(S, K)` for `gt(S, K)`").outcome = Mutate.Killed "backtest.md is the generated block") "ge refits κ⋆ to 900 and moves the block";
    check ((trial "move the split: b and c exchange starts").outcome = Mutate.Killed "the walk-forward backtest has no false negative") "the moved split leaves no accepted positive";
    check ((trial "flip b to +").changed = ["b"]) "a label mutant names the label it changed");
  "a weakened test is caught: without the knee check the written knee survives", (fun () ->
    let weak = Mutate.run ~checks:(without "the knee binds") miner facts accepted in
    check ((weak.killed, weak.survived) = (12, 1)) (Printf.sprintf "12 killed and 1 survived, got %d and %d" weak.killed weak.survived);
    let survivor = List.find (fun (trial : Mutate.trial) -> trial.outcome = Mutate.Survived) weak.trials in
    check (survivor.mutant.description = "rule 1: write 100 for `knee(K)`") "the survivor writes κ⋆ into the rules";
    check (List.exists (fun reason -> String.starts_with ~prefix:"a surviving mutant changes the verdict of a, c" reason) (Mutate.rejections weak))
      ("the gate names the survivor and the instances: " ^ String.concat "\n" (Mutate.rejections weak));
    check (List.exists (fun reason -> String.starts_with ~prefix:"mutation score 12/13 = 0.92 is below the floor" reason) (Mutate.rejections weak))
      "the score falls below the floor";
    let parse_only = Mutate.run ~checks:(fun miner facts labels -> List.filter (fun (name, _) -> name = safety_check) (Contract.checks miner facts labels)) miner facts accepted in
    check (parse_only.survived = 13) "a test that only parses the rules kills nothing");
  "the mutation record and its block carry the measurement", (fun () ->
    check (Mutate.source (trial "flip b to +").mutant = "a\t+\t1970-01-01T00:01:40Z\t-\tfixture\nb\t+\t1970-01-01T00:03:20Z\t-\tfixture\nc\t+\t1970-01-01T00:05:00Z\t1970-01-01T00:33:20Z\tfixture\nd\t-\t1970-01-01T00:06:40Z\t-\tfixture")
      ("a label mutant prints as the fixtures' TSV:\n" ^ Mutate.source (trial "flip b to +").mutant);
    let block = Mutate.block ~command:"miner.exe mutate labels.tsv fixtures" gate in
    check (String.starts_with ~prefix:"| What we measured | Result |\n|---|---|\n| Mutation score | 13/13 = 1.00; floor 1.00 |" block) ("the block opens with the score:\n" ^ block);
    check (List.length (String.split_on_char '\n' block) = 7 + 3 + 19 + 1) ("one row per mutant:\n" ^ block));
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
    within "Label" (match backtest_json result with `Assoc fields -> (match List.assoc "labels" fields with `List (l :: _) -> l | _ -> `Null) | _ -> `Null);
    let weak = Mutate.json (Mutate.run ~checks:(without "the knee binds") miner facts accepted) in
    within "Mutation" weak;
    within "Mutant" (match weak with `Assoc fields -> (match List.assoc "surviving" fields with `List (m :: _) -> m | _ -> failwith "no survivor") | _ -> `Null));
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
