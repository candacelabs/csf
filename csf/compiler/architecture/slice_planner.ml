(** The slice planner declaration and its Go projection. A slice_planner is the
    ordered decision a dispatch pass makes about one queued slice, declared once
    in the ontology language; [render_go] emits the Go decision set the pass
    checks. The declaration names the planner's instance and its decisions in
    order — the closed decision set the generated grammar
    (architecture/slice_planner.ebnf) spells as quoted terminals, so nothing
    here is free text. [render_go] is a pure function of the declaration, pinned
    byte for byte by a golden test, and idempotent — rendering twice yields the
    same bytes, so stage 1 and stage 2 agree for the planner. Nothing here reads
    a file or runs a session; [check] reports the declaration's own defects as
    diagnostics. *)

open Model

exception Invalid of diagnostic

let invalid (node : Frontend.node) message =
  raise (Invalid { at = node.at; code = "CSF_SLICE_PLANNER"; message })

(* --- the declaration --- *)

type planner = {
  id : string;
  decisions : string list;
  planner_at : location;
}

(* --- decoding the syntax tree --- *)

type reader = { parent : Frontend.node; mutable rest : Frontend.node list }

let take rule reader = match reader.rest with
  | node :: rest when node.Frontend.rule = rule -> reader.rest <- rest; node
  | node :: _ -> invalid node (Printf.sprintf "expected %s, found %s at line %d col %d"
      rule node.Frontend.rule node.Frontend.at.line node.Frontend.at.column)
  | [] -> invalid reader.parent ("expected " ^ rule)

let terminal expected reader =
  let node = take "$terminal" reader in
  match node.Frontend.value with
  | Some value when value = expected -> ()
  | Some value -> invalid node ("expected " ^ expected ^ ", found " ^ value)
  | None -> invalid node "terminal without a value"

(* A quoted terminal read for its value: the planner instance is a literal in
   the generated grammar, so its node is the terminal itself. *)
let terminal_value reader =
  let node = take "$terminal" reader in
  match node.Frontend.value with
  | Some value -> value
  | None -> invalid node "terminal without a value"

(* A decision name is the generated grammar's decision_name choice, one quoted
   terminal per declared planner kind. The choice node carries the one terminal
   that matched, whose value is the word the declaration used. *)
let decision_value reader =
  let node = take "decision_name" reader in
  match node.Frontend.children with
  | [ leaf ] ->
      (match leaf.Frontend.value with
       | Some value -> value
       | None -> invalid leaf "decision terminal without a value")
  | _ -> invalid node "expected one decision terminal in decision_name"

let decode_decision (node : Frontend.node) =
  let reader = { parent = node; rest = node.Frontend.children } in
  terminal "decision" reader;
  let name = decision_value reader in
  terminal ";" reader;
  name

let decode_planner (node : Frontend.node) =
  let reader = { parent = node; rest = node.Frontend.children } in
  terminal "slice_planner" reader;
  let id = terminal_value reader in
  terminal "{" reader;
  let rec collect acc = match reader.rest with
    | next :: _ when next.Frontend.rule = "decision" ->
        let next = take "decision" reader in
        collect (decode_decision next :: acc)
    | _ -> List.rev acc in
  let decisions = collect [] in
  terminal "}" reader;
  (match reader.rest with
   | [] -> ()
   | extra :: _ -> invalid extra ("unconsumed " ^ extra.Frontend.rule ^ " in slice_planner"));
  { id; decisions; planner_at = node.Frontend.at }

let parse ~grammar ~source ~filename =
  match Frontend.parse_text ~grammar ~source ~filename with
  | Error diagnostics -> Error diagnostics
  | Ok node -> (try Ok (decode_planner node) with Invalid diagnostic -> Error [diagnostic])

let parse_files ~grammar_path ~source_path =
  match Frontend.parse_files ~grammar_path ~source_path with
  | Error diagnostics -> Error diagnostics
  | Ok node -> (try Ok (decode_planner node) with Invalid diagnostic -> Error [diagnostic])

(* --- the check --- *)

let check (p : planner) =
  let findings = ref [] in
  let add code message = findings := { at = p.planner_at; code; message } :: !findings in
  let nonempty what value =
    if String.trim value = "" then add "slice_planner_empty" (what ^ " must not be empty.") in
  let duplicates values =
    let rec walk acc = function
      | a :: (b :: _ as rest) when a = b -> walk (a :: acc) rest
      | _ :: rest -> walk acc rest
      | [] -> List.rev acc in
    walk [] (List.sort String.compare values) in
  nonempty "The slice_planner identifier" p.id;
  List.iter (fun name -> add "slice_planner_duplicate"
    ("A decision is declared more than once: " ^ name)) (duplicates p.decisions);
  List.iter (fun name -> nonempty ("The decision name " ^ name) name) p.decisions;
  if p.decisions = [] then add "slice_planner_empty" "The slice_planner declares no decisions.";
  List.rev !findings

(* --- the projection --- *)

let capitalize value =
  if value = "" then value else
    String.make 1 (Char.uppercase_ascii value.[0]) ^ String.sub value 1 (String.length value - 1)

let go_name = function
  | "depends_on" -> "DependsOn"
  | "held" -> "Held"
  | "contends" -> "Contends"
  | "paused" -> "Paused"
  | "rate" -> "Rate"
  | "capacity" -> "Capacity"
  | "next_pass" -> "NextPass"
  | value -> String.concat "" (List.map capitalize (String.split_on_char '_' value))

(* The DecideWaiting parameter for each decision: the boolean whose truth is
   that decision's condition. next_pass is the default and names no parameter. *)
let parameter_name = function
  | "depends_on" -> "hasPredecessor"
  | "held" -> "held"
  | "contends" -> "hasContender"
  | "paused" -> "paused"
  | "rate" -> "throttled"
  | "capacity" -> "atCapacity"
  | value -> "when" ^ go_name value

let pad value width = value ^ String.make (max 0 (width - String.length value)) ' '
let go_string value = Printf.sprintf "%S" value

let render_go ~source (p : planner) =
  let consts = List.map (fun id -> ("Waiting" ^ go_name id, id)) p.decisions in
  let width = List.fold_left (fun w (name, _) -> max w (String.length name)) 0 consts in
  let buf = Buffer.create 4096 in
  let add = Buffer.add_string buf in
  Printf.bprintf buf "// Code generated by csfc from %s; DO NOT EDIT.\n" source;
  add "//\n";
  Printf.bprintf buf "// %s: the ordered decision a dispatch pass makes about one queued\n" p.id;
  Printf.bprintf buf "// slice: %s.\n\n" (String.concat ", " p.decisions);
  add "package dispatch\n\n";
  add "// WaitingReason is one decision a dispatch pass makes about a queued slice.\n";
  add "type WaitingReason string\n\n";
  add "// The reasons a queued slice does not run, in the order the pass checks them.\n";
  add "const (\n";
  List.iter (fun (name, id) ->
    Printf.bprintf buf "\t%s WaitingReason = %s\n" (pad name width) (go_string id)) consts;
  add ")\n\n";
  add "// WaitingReasons is the closed set, in the order the pass checks them.\n";
  add "var WaitingReasons = []WaitingReason{\n";
  List.iter (fun (name, _) -> Printf.bprintf buf "\t%s,\n" name) consts;
  add "}\n\n";
  add "// DecideWaiting is the ordered decision: the first reason whose condition\n";
  add "// holds, and next_pass when none does. It is the generated shape of the\n";
  add "// dispatch pass's waiting switch, so the order lives here and not in a\n";
  add "// hand-written switch.\n";
  let conditions = List.filter (fun id -> id <> "next_pass") p.decisions in
  let params = List.map parameter_name conditions in
  Printf.bprintf buf "func DecideWaiting(%s bool) WaitingReason {\n" (String.concat ", " params);
  add "\tswitch {\n";
  List.iter (fun id ->
    Printf.bprintf buf "\tcase %s:\n\t\treturn Waiting%s\n" (parameter_name id) (go_name id)) conditions;
  add "\tdefault:\n\t\treturn WaitingNextPass\n\t}\n}\n";
  Buffer.contents buf
