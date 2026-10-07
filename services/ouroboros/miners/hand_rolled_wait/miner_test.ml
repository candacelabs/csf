(* A miner's acceptance: the contract's checks (its rules are sound, every
   labeled positive fires at the fitted knee and no labeled negative does, the
   walk-forward backtest has no false negative, the knee binds, backtest.md is
   the block the fixtures generate), then the mutation gate: every mutant of
   its rules and labels is killed, mutation.md is current, and a weakened copy
   of this test is caught; and the executable the loop launches reports the
   scope the miner declares. *)
open Contract

let check condition message = if not condition then failwith message
let miner = Extract.miner
let fixtures = Filename.concat miner.package "fixtures"
let labels_path = Filename.concat fixtures "labels.tsv"
let pattern = Filename.concat fixtures "*/events.jsonl"
let committed name = In_channel.with_open_text (Filename.concat miner.package name) In_channel.input_all

let facts = Contract.facts miner [pattern]
let labels = Contract.labels labels_path
let checks = Contract.checks ~committed:{ reproduce = command miner ["backtest"; labels_path; pattern]; block = committed "backtest.md" }
let report = Mutate.run ~checks miner facts labels

let tests = checks miner facts labels @ [
  "every mutant is killed and the score reaches the floor", (fun () ->
    check (Mutate.rejections report = []) (String.concat "\n" (Mutate.rejections report)));
  "mutation.md is the generated block", (fun () ->
    let block = Mutate.block ~command:(command miner ["mutate"; labels_path; pattern]) report in
    check (block = committed "mutation.md") ("mutation.md is stale; it should read:\n" ^ block));
  "a weakened copy of this test is caught by the gate", (fun () ->
    let weakened miner facts labels = List.filter (fun (name, _) -> name = safety_check) (checks miner facts labels) in
    let weak = Mutate.run ~checks:weakened miner facts labels in
    check (Mutate.rejections weak <> []) "a test that only checks the rules parse must be rejected";
    check (List.exists (fun (trial : Mutate.trial) -> trial.outcome = Mutate.Survived && trial.labeled <> []) weak.trials)
      "the weakened test must let a mutant change a labeled verdict");
  "the executable reports the miner's scope", (fun () ->
    let channel = Unix.open_process_in (Filename.quote (Filename.concat miner.package "miner.exe") ^ " scope") in
    let reported = In_channel.input_all channel in
    check (Unix.close_process_in channel = Unix.WEXITED 0) "miner.exe scope must exit 0";
    check (reported = scope_name miner.scope ^ "\n") ("miner.exe scope printed " ^ String.escaped reported));
]

let () =
  List.iter (fun (name, test) ->
    try test () with exception_ ->
      Printf.eprintf "FAIL %s: %s\n" name (Printexc.to_string exception_);
      exit 1) tests;
  Printf.printf "%s: %d tests passed; %d mutants (%d killed, %d survived, %d excluded) in %.0f ms\n" miner.name
    (List.length tests) (List.length report.trials) report.killed report.survived report.excluded (report.seconds *. 1000.)
