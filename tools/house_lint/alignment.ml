(* The ontology alignment score: one record per source revision, counting how
   far the tree is from the CSF ontology's placement and vocabulary rules.

   The score is transparent on purpose. Each signal has a stated weight; the
   penalty is the weighted sum of measured counts; the score maps the penalty
   into [0, 1) with a fixed scale so a maximizer has a bounded target. Lower is
   better and 0 means every measured signal is clean. A signal whose checker
   has not landed is reported as not measured, contributes nothing, and marks
   the record incomplete: an absent observation is never a measured zero. *)

type source =
  | House_rule of string  (** count of house-lint native findings with this rule ID *)
  | Csfc_check  (** csfc check diagnostics *)
  | Generated_drift  (** csfc check-generated drift plus language-documentation drift *)
  | Retired_vocabulary  (** retired words in READMEs, from the language generator's lint *)
  | Unlinked_terms  (** unlinked ontology terms in READMEs, from the same lint *)

(* [blocking] signals may not rise in a pull request even when the total
   penalty falls: each one is a broken contract or a capability bypass, which
   other improvements cannot pay for. *)
type signal = { id : string; source : source; weight : int; blocking : bool; meaning : string }

(* Registry in data (CS-6): adding a signal is one row, and the tests assert
   over the whole table. Weights rank how directly a finding breaks the
   ontology: a failing architecture check or stale projection is a broken
   contract (10); an unowned boundary crossing is a capability bypass (5);
   unowned goroutines, environment reads and unnamed directories are placement
   debt (3); vocabulary is documentation debt (2 retired, 1 unlinked). *)
let signals = [
  {id="csfc-check"; source=Csfc_check; weight=10; blocking=true;
   meaning="csfc check diagnostics against csf/architecture/architecture.csf"};
  {id="generated-drift"; source=Generated_drift; weight=10; blocking=true;
   meaning="generated projections that differ from their source (csfc check-generated and the language generator's check)"};
  {id="cs-16"; source=House_rule "CS-16"; weight=5; blocking=true;
   meaning="network, gRPC and PostgreSQL crossings outside ipc/"};
  {id="cs-15"; source=House_rule "CS-15"; weight=3; blocking=false;
   meaning="go statements with no visible owner (no join and no context-driven exit)"};
  {id="cs-17"; source=House_rule "CS-17"; weight=3; blocking=false;
   meaning="process environment reads outside runtime/config"};
  {id="ontology-dirs"; source=House_rule "ONTOLOGY-DIRS"; weight=3; blocking=false;
   meaning="directories whose role segment is not an ontology term"};
  {id="retired-vocabulary"; source=Retired_vocabulary; weight=2; blocking=false;
   meaning="retired ontology words in tracked READMEs"};
  {id="unlinked-terms"; source=Unlinked_terms; weight=1; blocking=false;
   meaning="ontology terms in tracked READMEs without a link to their definition"};
]

let method_version = 1
let schema = "candace.ontology.alignment/v1"

(* The penalty at which the score reads 0.5. Fixed with the method version so
   two records are comparable only when both carry the same method. *)
let scale = 1000

type observation = Measured of int | Not_measured of string

type record = {
  revision : string;
  dirty : bool;
  commit_time : int;
  observations : (signal * observation) list;
}

let contribution (signal, observation) = match observation with
  | Measured count -> signal.weight * count
  | Not_measured _ -> 0

let penalty record = List.fold_left (fun total entry -> total + contribution entry) 0 record.observations

let score_of_penalty penalty =
  if penalty < 0 then invalid_arg "alignment penalty cannot be negative";
  float_of_int penalty /. float_of_int (penalty + scale)

let complete record =
  List.for_all (fun (_, observation) -> match observation with Measured _ -> true | Not_measured _ -> false)
    record.observations

let round4 value = Float.round (value *. 10000.) /. 10000.

let signal_json (signal, observation) =
  let count, status, extra = match observation with
    | Measured count -> `Int count, "measured", []
    | Not_measured reason -> `Null, "not_measured", ["reason", `String reason] in
  `Assoc ([
    "id", `String signal.id;
    "weight", `Int signal.weight;
    "blocking", `Bool signal.blocking;
    "status", `String status;
    "count", count;
    "contribution", `Int (contribution (signal, observation));
    "meaning", `String signal.meaning;
  ] @ extra)

let to_json record =
  let penalty = penalty record in
  `Assoc [
    "schema", `String schema;
    "method_version", `Int method_version;
    "revision", `String record.revision;
    "dirty", `Bool record.dirty;
    "commit_time", `Int record.commit_time;
    "score", `Float (round4 (score_of_penalty penalty));
    "lower_is_better", `Bool true;
    "penalty", `Int penalty;
    "scale", `Int scale;
    "formula", `String "score = penalty / (penalty + scale); penalty = sum(weight * count) over measured signals";
    "complete", `Bool (complete record);
    "signals", `List (List.map signal_json record.observations);
  ]

(* OpenMetrics text for promtool's backfill: one timestamped sample set per
   revision at its commit time. Labels stay bounded (method and signal); the
   revision belongs to the JSON receipt, never a label. A signal that was not
   measured has no count sample, only measured=0, so a panel cannot read it as
   a clean zero. *)
let to_openmetrics record =
  let buffer = Buffer.create 1024 in
  let line format = Printf.ksprintf (fun text -> Buffer.add_string buffer text; Buffer.add_char buffer '\n') format in
  let method_label = Printf.sprintf "method=\"%d\"" method_version in
  let time = record.commit_time in
  let penalty = penalty record in
  line "# TYPE candace_ontology_alignment_score gauge";
  line "# HELP candace_ontology_alignment_score Ontology alignment score in [0,1), lower is better.";
  line "candace_ontology_alignment_score{%s} %.4f %d" method_label (score_of_penalty penalty) time;
  line "# TYPE candace_ontology_alignment_penalty gauge";
  line "# HELP candace_ontology_alignment_penalty Weighted sum of measured signal counts.";
  line "candace_ontology_alignment_penalty{%s} %d %d" method_label penalty time;
  (* PromQL cannot recover a sparse sample's own timestamp through a range
     function, so the commit time is also a value: record age is
     time() - last_over_time(this[window]). *)
  line "# TYPE candace_ontology_alignment_commit_timestamp_seconds gauge";
  line "# UNIT candace_ontology_alignment_commit_timestamp_seconds seconds";
  line "# HELP candace_ontology_alignment_commit_timestamp_seconds Commit time of the measured revision.";
  line "candace_ontology_alignment_commit_timestamp_seconds{%s} %d %d" method_label time time;
  line "# TYPE candace_ontology_alignment_complete gauge";
  line "# HELP candace_ontology_alignment_complete 1 when every signal was measured.";
  line "candace_ontology_alignment_complete{%s} %d %d" method_label (if complete record then 1 else 0) time;
  line "# TYPE candace_ontology_alignment_signal_measured gauge";
  line "# HELP candace_ontology_alignment_signal_measured 1 when the signal's checker ran, 0 when it is not yet implemented.";
  List.iter (fun (signal, observation) ->
    line "candace_ontology_alignment_signal_measured{%s,signal=\"%s\"} %d %d" method_label signal.id
      (match observation with Measured _ -> 1 | Not_measured _ -> 0) time) record.observations;
  line "# TYPE candace_ontology_alignment_signal_count gauge";
  line "# HELP candace_ontology_alignment_signal_count Findings per measured signal.";
  List.iter (fun (signal, observation) -> match observation with
    | Measured count ->
        line "candace_ontology_alignment_signal_count{%s,signal=\"%s\"} %d %d" method_label signal.id count time
    | Not_measured _ -> ()) record.observations;
  line "# EOF";
  Buffer.contents buffer

(* --- Parsing external checker output. Pure functions over captured text. --- *)

let lines text = String.split_on_char '\n' text |> List.filter (fun line -> String.trim line <> "")

let all_digits text = text <> "" && String.for_all (fun c -> c >= '0' && c <= '9') text

(* csfc formats each diagnostic as FILE:LINE:COLUMN: CODE: MESSAGE. *)
let is_diagnostic line = match String.split_on_char ':' line with
  | _ :: row :: column :: code :: _ :: _ -> all_digits row && all_digits column && String.trim code <> ""
  | _ -> false

let diagnostic_code line = match String.split_on_char ':' line with
  | _ :: _ :: _ :: code :: _ -> Some (String.trim code)
  | _ -> None

let csfc_findings text = List.length (List.filter is_diagnostic (lines text))

let csfc_drift text =
  List.length (List.filter (fun line -> is_diagnostic line && diagnostic_code line = Some "CSF_GENERATED_DRIFT")
    (lines text))

(* Index of the first occurrence of [fragment] in [text], if any. *)
let find text fragment =
  let length = String.length fragment in
  let rec search start =
    if start + length > String.length text then None
    else if String.sub text start length = fragment then Some start
    else search (start + 1) in
  search 0

let contains text fragment = Option.is_some (find text fragment)

let documentation_prefix = "generated documentation differs: "

(* The language generator's check names every differing file in one message. *)
let documentation_drift text =
  List.find_map (fun line ->
    Option.map (fun start ->
      let offset = start + String.length documentation_prefix in
      String.sub line offset (String.length line - offset) |> String.split_on_char ','
      |> List.filter (fun path -> String.trim path <> "") |> List.length)
      (find line documentation_prefix)) (lines text)

let retired_marker = ": retired word '"
let unlinked_marker = ": unlinked ontology term '"

(* The vocabulary lint prints PATH:LINE: MESSAGE per finding. *)
let vocabulary_counts text =
  let found = lines text in
  List.length (List.filter (fun line -> contains line retired_marker) found),
  List.length (List.filter (fun line -> contains line unlinked_marker) found)

(* A generator that predates the lint verb answers with its usage line. *)
let lint_unsupported text = contains text "usage: generate" && not (contains text "lint")

(* --- Reading a record back and comparing two of them. --- *)

let field name = function
  | `Assoc fields -> (match List.assoc_opt name fields with
      | Some value -> value
      | None -> failwith ("ontology record lacks " ^ name))
  | _ -> failwith ("ontology record is not an object at " ^ name)

let int_field name json = match field name json with `Int value -> value | _ -> failwith (name ^ " is not an integer")
let string_field name json = match field name json with `String value -> value | _ -> failwith (name ^ " is not a string")

(* Parse a record this module wrote. A record from another schema or method,
   or naming a signal this registry does not define, is refused: comparing it
   would compare different measurements. *)
let of_json json =
  if string_field "schema" json <> schema then failwith ("not a " ^ schema ^ " record");
  if int_field "method_version" json <> method_version then
    failwith (Printf.sprintf "record uses method %d; this checker measures method %d"
      (int_field "method_version" json) method_version);
  let observation entry : observation = match string_field "status" entry with
    | "measured" -> Measured (int_field "count" entry)
    | "not_measured" -> Not_measured (string_field "reason" entry)
    | status -> failwith ("unknown signal status " ^ status) in
  let observations = match field "signals" json with
    | `List entries -> List.map (fun entry ->
        let id = string_field "id" entry in
        match List.find_opt (fun signal -> signal.id = id) signals with
        | Some signal -> signal, observation entry
        | None -> failwith ("record names unknown signal " ^ id)) entries
    | _ -> failwith "signals is not a list" in
  { revision = string_field "revision" json;
    dirty = (match field "dirty" json with `Bool value -> value | _ -> failwith "dirty is not a boolean");
    commit_time = int_field "commit_time" json; observations }

let observation_of record signal = List.assoc_opt signal record.observations
  |> Option.value ~default:(Not_measured "absent from the record")

type row = { signal : signal; before : observation; after : observation }

let rows ~base ~head = List.map (fun signal ->
  { signal; before = observation_of base signal; after = observation_of head signal }) signals

(* Only signals measured on both sides are compared; a signal measured on one
   side only is shown but can neither regress nor improve the comparison. *)
let comparable_penalty rows side =
  List.fold_left (fun total row -> match row.before, row.after with
    | Measured before, Measured after -> total + row.signal.weight * (if side then after else before)
    | _ -> total) 0 rows

type verdict = { penalty_before : int; penalty_after : int; rises : signal list }

let compare_records ~base ~head =
  let rows = rows ~base ~head in
  let rises = List.filter_map (fun row -> match row.before, row.after with
    | Measured before, Measured after when row.signal.blocking && after > before -> Some row.signal
    | _ -> None) rows in
  rows, { penalty_before = comparable_penalty rows false; penalty_after = comparable_penalty rows true; rises }

let regressed verdict = verdict.penalty_after > verdict.penalty_before || verdict.rises <> []

let count_text = function Measured count -> string_of_int count | Not_measured _ -> "not measured"

let delta_text row = match row.before, row.after with
  | Measured before, Measured after -> Printf.sprintf "%+d" (after - before)
  | _ -> "n/a"

let short revision = if String.length revision > 12 then String.sub revision 0 12 else revision

let markdown ~base ~head ~accepted =
  let rows, verdict = compare_records ~base ~head in
  let buffer = Buffer.create 2048 in
  let line format = Printf.ksprintf (fun text -> Buffer.add_string buffer text; Buffer.add_char buffer '\n') format in
  let before_score = score_of_penalty (penalty base) and after_score = score_of_penalty (penalty head) in
  line "## Ontology alignment: `%s` -> `%s`" (short base.revision) (short head.revision);
  line "";
  line "Score %.4f -> %.4f (lower is better). Comparable penalty %d -> %d (%+d), over signals measured on both sides."
    before_score after_score verdict.penalty_before verdict.penalty_after
    (verdict.penalty_after - verdict.penalty_before);
  line "";
  line "| Signal | Weight | Blocking | Before | After | Delta |";
  line "|---|---|---|---|---|---|";
  List.iter (fun row ->
    line "| `%s` | %d | %s | %s | %s | %s |" row.signal.id row.signal.weight
      (if row.signal.blocking then "yes" else "no") (count_text row.before) (count_text row.after) (delta_text row)) rows;
  line "";
  if not (regressed verdict) then line "Result: no regression."
  else begin
    if verdict.penalty_after > verdict.penalty_before then
      line "Result: REGRESSION: the comparable penalty rose by %d." (verdict.penalty_after - verdict.penalty_before);
    List.iter (fun signal -> line "Result: REGRESSION: blocking signal `%s` rose." signal.id) verdict.rises;
    if accepted then line "The `ontology-regression-accepted` label accepts this regression; the table stays on record."
  end;
  Buffer.contents buffer, regressed verdict

let rounded_score record = `Float (round4 (score_of_penalty (penalty record)))
let count_json = function Measured count -> `Int count | Not_measured _ -> `Null

(* The pr-description skill's `ontology_score` field, so a description quotes
   measured numbers instead of hand-typed ones. *)
let pr_spec ~base ~head =
  `Assoc [
    "before", rounded_score base;
    "after", rounded_score head;
    "signals", `List (List.map (fun row ->
      `Assoc ["id", `String row.signal.id; "before", count_json row.before; "after", count_json row.after])
      (rows ~base ~head));
  ]
