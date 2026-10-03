(* The procedure behind `tools/ontology-score.sh`: run the native house scan
   and the CSF checkers once against one checkout, and turn their output into
   an Alignment.record. Every checker that ran but failed in an unexpected way
   is a scan error, never a zero. *)

type tools = { csfc : string; generator : string }

let capture argv =
  let output = Filename.temp_file "ontology-score-" "" in
  Fun.protect ~finally:(fun () -> Sys.remove output) (fun () ->
    let code = Runner.process argv output in
    code, Checker.read_file output)

let git root arguments =
  let code, text = capture (Array.of_list (["git"; "-C"; root] @ arguments)) in
  if code <> 0 then failwith ("git " ^ String.concat " " arguments ^ " failed: " ^ String.trim text);
  String.trim text

(* The repository root is the CSF compiler's own source root. csfc runs there
   with its default inputs, exactly as CI's `csfc check-generated` does:
   projections embed their input paths, so any other spelling of the same files
   reads as drift. *)
let csf_root = "."

let in_directory directory thunk =
  let previous = Sys.getcwd () in
  Unix.chdir directory;
  Fun.protect ~finally:(fun () -> Unix.chdir previous) thunk

let csfc tools mode = in_directory csf_root (fun () -> capture [|tools.csfc; mode|])

let unexpected name code text =
  failwith (Printf.sprintf "%s exited %d: %s" name code (String.trim text))

let csfc_check tools : Alignment.observation =
  match csfc tools "check" with
  | 0, _ -> Measured 0
  | 1, text when Alignment.csfc_findings text > 0 -> Measured (Alignment.csfc_findings text)
  | code, text -> unexpected "csfc check" code text

let architecture_drift tools : Alignment.observation =
  match csfc tools "check-generated" with
  | 0, _ -> Measured 0
  | 1, text when Alignment.csfc_drift text > 0 -> Measured (Alignment.csfc_drift text)
  | 1, _ -> Not_measured "csfc check failed before comparing projections; fix csfc-check first"
  | code, text -> unexpected "csfc check-generated" code text

let documentation_drift tools : Alignment.observation =
  match capture [|tools.generator; "--root"; csf_root; "check"|] with
  | 0, _ -> Measured 0
  | 1, text -> (match Alignment.documentation_drift text with
      | Some count -> Measured count
      | None -> Not_measured ("language generator check stopped before comparing documents: " ^ String.trim text))
  | code, text -> unexpected "language generator check" code text

let combine_drift (left : Alignment.observation) (right : Alignment.observation) : Alignment.observation =
  match left, right with
  | Measured a, Measured b -> Measured (a + b)
  | Not_measured reason, Measured _ | Measured _, Not_measured reason -> Not_measured reason
  | Not_measured a, Not_measured b -> Not_measured (a ^ "; " ^ b)

let vocabulary_pending =
  "TODO(D1, #298): the language generator has no `lint` verb on this revision; \
   this signal is measured automatically once it does"

let readmes tracked = List.filter (fun path -> Filename.basename path = "README.md") tracked

(* Retired and unlinked vocabulary share one lint run. *)
let vocabulary tools tracked : Alignment.observation * Alignment.observation =
  match capture (Array.of_list ([tools.generator; "--root"; csf_root; "lint"] @ readmes tracked)) with
  | 1, text when Alignment.lint_unsupported text -> Not_measured vocabulary_pending, Not_measured vocabulary_pending
  | (0 | 1), text -> let retired, unlinked = Alignment.vocabulary_counts text in Measured retired, Measured unlinked
  | code, text -> unexpected "language generator lint" code text

let house_rule findings rule : Alignment.observation =
  if List.exists (fun (registered : Policy.rule) -> registered.id = rule) Policy.rules then
    Measured (List.length (List.filter (fun (finding : Source.finding) -> finding.rule = rule) findings))
  else Not_measured (Printf.sprintf
    "TODO: %s is not registered in house lint's policy on this revision; it is measured once its checker lands" rule)

let measure ~root tools =
  Unix.chdir root;
  let tracked = Runner.git_inventory root false in
  let inventory = Runner.git_inventory root true in
  let _, findings, errors = Runner.native root tracked inventory in
  if errors <> [] then begin
    List.iter (Runner.render stderr) errors;
    failwith "the native house scan reported errors; refusing to score an incomplete scan"
  end;
  let drift = lazy (combine_drift (architecture_drift tools) (documentation_drift tools)) in
  let vocabulary = lazy (vocabulary tools tracked) in
  let observe (signal : Alignment.signal) : Alignment.observation = match signal.source with
    | House_rule rule -> house_rule findings rule
    | Csfc_check -> csfc_check tools
    | Generated_drift -> Lazy.force drift
    | Retired_vocabulary -> fst (Lazy.force vocabulary)
    | Unlinked_terms -> snd (Lazy.force vocabulary) in
  let observations = List.map (fun signal -> signal, observe signal) Alignment.signals in
  { Alignment.revision = git root ["rev-parse"; "HEAD"];
    dirty = git root ["status"; "--porcelain"; "--untracked-files=no"] <> "";
    commit_time = int_of_string (git root ["show"; "-s"; "--format=%ct"; "HEAD"]);
    observations }
