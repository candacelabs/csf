(* router v1 two arms: extract routing decisions and check for invalid arms *)
open Contract

let field name json = match Yojson.Safe.Util.member name json with `String text -> Some text | _ -> None
let field_num name json = match Yojson.Safe.Util.member name json with `Float num -> Some (int_of_float num) | `Int num -> Some num | _ -> None

(* One routing decisions file, <state>/routing/decisions.jsonl, to facts about
   the routing decisions: the instance ID, real session, arm chosen, and match score. *)
let extract path =
  let open Corpus in
  try
    (* Extract instance ID from the path (e.g., fixtures/INSTANCE_ID/events.jsonl) *)
    let instance =
      let parts = String.split_on_char '/' path in
      match List.rev parts with
      | "events.jsonl" :: instance_id :: _ -> instance_id
      | _ -> Filename.basename (Filename.dirname path)
    in
    let events = List.of_seq (jsonl path) in
    let fact relation args (line, _) = { relation; args; span = { source = path; line } } in
    List.concat_map (fun ((line, json) as entry) ->
      Option.bind (field "r" json) (fun r ->
        Option.bind (field "arm" json) (fun arm ->
          Option.bind (field_num "s" json) (fun score ->
            Some [fact "routing_decision" [Text instance; Text r; Text arm; Number score] entry])))
      |> Option.value ~default:[]
    ) events
  with _ -> []

let miner = {
  name = "router_v1_two_arms";
  package = "services/ouroboros/miners/router_v1_two_arms";
  rules = Rules_cgen.source;
  verdicts = [{ name = "invalid_arm"; arity = 3; severity = S1 }];
  scope = Generic;
  extract;
}
