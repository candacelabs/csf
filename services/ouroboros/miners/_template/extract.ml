(* DRAFT-PR-LATE (source monorepo issue #376), the extractor. A new miner replaces
   this file and rules.dl; main.ml, the test and BUILD.bazel stay as copied. *)
open Contract

let field name json = match Yojson.Safe.Util.member name json with `String text -> Some text | _ -> None
let event kind (_, json) = field "event_type" json = Some kind

(* One harness event log, <state>/<assignment>/events.jsonl, to facts about
   its run: whether the session gate saw it, when the commit gate opened its
   draft pull request, and the score the knee is fitted on. *)
let extract path =
  let events = List.of_seq (Corpus.jsonl path) in
  let fact relation args (line, _) = { relation; args; span = { source = path; line } } in
  let seconds (_, json) = Corpus.seconds (Option.get (field "time" json)) in
  match List.find_opt (event "harness_run_started") events with
  | None -> []
  | Some started ->
      let run = Text (Option.value (field "assignment_id" (snd started)) ~default:path) in
      let opened = List.find_opt (fun (_, json) -> field "gate" json = Some "commit" && field "decision" json = Some "opened") events in
      let last = List.fold_left (fun last entry -> if field "time" (snd entry) = None then last else entry) started events in
      let until = Option.value opened ~default:last in
      List.concat [
        [fact "run" [run] started];
        (match List.find_opt (event "session_gate_decision") events with Some gate -> [fact "gated" [run] gate] | None -> []);
        (match opened with Some opened -> [fact "opened" [run; Number (seconds opened)] opened] | None -> []);
        [fact "score" [run; Number (seconds until - seconds started)] until];
      ]

let miner = {
  name = "draft-pr-late";
  package = "services/ouroboros/miners/_template";
  rules = Rules_cgen.source;
  verdicts = [{ name = "invisible"; arity = 1; severity = S3 }];
  (* A draft pull request opened late is an offense for any harness user. *)
  scope = Generic;
  extract;
}
