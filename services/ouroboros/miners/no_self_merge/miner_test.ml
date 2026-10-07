(* A miner's acceptance: its rules are sound, every labeled positive fires at
   the fitted knee and no labeled negative does, the walk-forward backtest has
   no false negative, and backtest.md is the block the fixtures generate. *)
open Contract

let check condition message = if not condition then failwith message
let miner = Extract.miner
let fixtures = Filename.concat miner.package "fixtures"
let labels_path = Filename.concat fixtures "labels.tsv"
let pattern = Filename.concat fixtures "*/events.jsonl"

let runs = Sys.readdir fixtures |> Array.to_list |> List.sort compare
  |> List.map (fun run -> Filename.concat (Filename.concat fixtures run) "events.jsonl")
  |> List.filter Sys.file_exists

let facts = List.concat_map miner.extract runs
let labels = Contract.labels labels_path
let result = backtest miner facts labels
let instances knee = findings ?knee miner facts |> List.map (fun (f : finding) -> f.subject)
  |> List.filter_map (function Text instance :: _ -> Some instance | _ -> None)

let tests = [
  "the rules are safe and stratified", (fun () -> check_rules miner.rules);
  "the labeled positives fire and the negative does not", (fun () ->
    let fired = instances result.knee in
    List.iter (fun (label : label) ->
      check (List.mem label.instance fired = label.positive)
        (label.instance ^ (if label.positive then " should fire" else " should not fire"))) labels);
  "the walk-forward backtest has no false negative", (fun () ->
    check (result.fn = []) ("false negatives: " ^ String.concat ", " result.fn);
    check (result.tp <> []) "the accepted labels must include a fired positive");
  "backtest.md is the generated block", (fun () ->
    let block = backtest_block ~command:(command miner ["backtest"; labels_path; pattern]) result in
    let committed = In_channel.with_open_text (Filename.concat miner.package "backtest.md") In_channel.input_all in
    check (block = committed) ("backtest.md is stale; it should read:\n" ^ block));
]

let () =
  List.iter (fun (name, test) ->
    try test () with exception_ ->
      Printf.eprintf "FAIL %s: %s\n" name (Printexc.to_string exception_);
      exit 1) tests;
  Printf.printf "%s: %d tests passed\n" miner.name (List.length tests)
