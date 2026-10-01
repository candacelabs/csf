exception Error of string
let fail format = Printf.ksprintf (fun message -> raise (Error message)) format

type totals = {
  generated_lines : int;
  non_generated_lines : int;
  total_lines : int;
  generated_percent : float option;
  non_generated_percent : float option;
}

type source_file = { path : string; language : string; generated : bool; lines : int }
type documentation_file = { path : string; totals : totals }
type report = {
  code : totals;
  generated_files : int;
  non_generated_files : int;
  excluded_files : int;
  modified_files : string list;
  by_language : (string * totals) list;
  files : source_file list;
  documentation : totals;
  documentation_files : documentation_file list;
}

(* OCaml owns generators in this metric's policy and is excluded whether or not
   a particular OCaml file has a recognized generated header. *)
let languages = [
  "Go", [".go"]; "Python", [".py"; ".pyi"]; "Rust", [".rs"];
  "C", [".c"; ".h"]; "C++", [".cc"; ".cpp"; ".cxx"; ".hh"; ".hpp"; ".hxx"];
  "JavaScript", [".js"; ".jsx"; ".mjs"; ".cjs"];
  "TypeScript", [".ts"; ".tsx"; ".mts"; ".cts"];
  "Lean", [".lean"]; "Shell", [".sh"; ".bash"];
  "SQL", [".sql"]; "Protocol Buffers", [".proto"];
  "HTML", [".html"; ".htm"; ".vue"; ".svelte"];
  "CSS", [".css"; ".scss"; ".sass"; ".less"];
]

let excluded_code_extensions = [".ml"; ".mli"]
let excluded_code_paths = ["candace/pkg/gotth/"]

let language_for path =
  let extension = String.lowercase_ascii (Filename.extension path) in
  if List.mem extension excluded_code_extensions ||
     List.exists (fun prefix -> String.starts_with ~prefix path) excluded_code_paths then None
  else List.find_map (fun (language, extensions) ->
    if List.mem extension extensions then Some language else None) languages

(* Formatting performs the same binary64, nearest-even hundredths rounding as
   Python round(value, 2), rather than rounding a pre-scaled integer ratio. *)
let percentage count total =
  if total = 0 then None
  else Some (float_of_string (Printf.sprintf "%.2f" (100. *. float_of_int count /. float_of_int total)))

let totals generated_lines total_lines =
  let non_generated_lines = total_lines - generated_lines in
  { generated_lines; non_generated_lines; total_lines;
    generated_percent = percentage generated_lines total_lines;
    non_generated_percent = percentage non_generated_lines total_lines }

let assoc label = function
  | `Assoc fields -> fields
  | _ -> fail "%s must be an object" label

(* JSON's duplicate-name policy is library-defined. Match the existing archive
   calculator's json.loads policy, including duplicates in individual records. *)
let member key fields = List.assoc_opt key (List.rev fields)

let required_string label fields key = match member key fields with
  | Some (`String value) -> value
  | _ -> fail "%s %s must be a string" label key

let valid_path name =
  let parts = String.split_on_char '/' name in
  if name = "" || not (Filename.is_relative name) || String.contains name '\\' ||
     String.contains name '\000' || List.exists (fun part -> part = "" || part = "." || part = "..") parts then
    fail "unsafe archive path: %S" name;
  parts

let rec reject_symlinks name path =
  let stat = Unix.lstat path in
  if stat.Unix.st_kind = Unix.S_LNK then fail "symlink record path: %s" name;
  let parent = Filename.dirname path in
  if parent <> path then reject_symlinks name parent

let checked_source root seen record =
  let fields = assoc "manifest file record" record in
  let name = required_string "record" fields "path" in
  let parts = valid_path name in
  if Hashtbl.mem seen name then fail "duplicate record path: %s" name;
  Hashtbl.add seen name ();
  let path = List.fold_left Filename.concat root parts in
  (try
    reject_symlinks name path;
    if (Unix.stat path).Unix.st_kind <> Unix.S_REG then fail "missing or non-regular record path: %s" name
  with Unix.Unix_error (Unix.ENOENT, _, _) | Unix.Unix_error (Unix.ENOTDIR, _, _) ->
    fail "missing or non-regular record path: %s" name);
  let content = In_channel.with_open_bin path In_channel.input_all in
  let expected = required_string "manifest" fields "sha256" in
  if String.length expected <> 64 || not (String.for_all (function
      | '0' .. '9' | 'a' .. 'f' | 'A' .. 'F' -> true | _ -> false) expected) then
    fail "invalid manifest SHA-256 for %s" name;
  name, content, String.lowercase_ascii expected

let selected_source name content expected =
  if not (String.is_valid_utf_8 content) then fail "selected source is not UTF-8: %s" name;
  Sha256.to_hex (Sha256.string content) <> expected

let sum_totals values =
  let generated, total = List.fold_left (fun (generated, total) value ->
    generated + value.generated_lines, total + value.total_lines) (0, 0) values in
  totals generated total

type accumulation = {
  sources : source_file list;
  documents : documentation_file list;
  modified : string list;
  excluded : int;
}

let measure root seen accumulated record =
  let name, content, expected = checked_source root seen record in
  let language = language_for name in
  let markdown = List.mem (String.lowercase_ascii (Filename.extension name)) [".md"; ".markdown"] in
  let accumulated = if language = None then { accumulated with excluded = accumulated.excluded + 1 } else accumulated in
  if language = None && not markdown then accumulated
  else begin
    let changed = selected_source name content expected in
    let modified = if changed then name :: accumulated.modified else accumulated.modified in
    let lines = List.length (Metric_text.physical_lines content) in
    if markdown then
      let generated = Metric_text.documentation_generated_lines ~name content in
      { accumulated with modified; documents = { path = name; totals = totals generated lines } :: accumulated.documents }
    else
      let language = Option.get language in
      let generated = Metric_text.is_generated content in
      { accumulated with modified; sources = { path = name; language; generated; lines } :: accumulated.sources }
  end

let source_totals (files : source_file list) =
  let generated, total = List.fold_left (fun (generated, total) (file : source_file) ->
    generated + (if file.generated then file.lines else 0), total + file.lines) (0, 0) files in
  totals generated total

let calculate ~root records =
  let seen = Hashtbl.create (List.length records) in
  let accumulated = List.fold_left (measure root seen)
    { sources = []; documents = []; modified = []; excluded = 0 } records in
  let files = List.rev accumulated.sources and documentation_files = List.rev accumulated.documents in
  let names = List.sort_uniq String.compare (List.map (fun (file : source_file) -> file.language) files) in
  let by_language = List.map (fun language ->
    language, source_totals (List.filter (fun (file : source_file) -> file.language = language) files)) names in
  let generated_files = List.length (List.filter (fun (file : source_file) -> file.generated) files) in
  { code = source_totals files; generated_files; non_generated_files = List.length files - generated_files;
    excluded_files = accumulated.excluded; modified_files = List.rev accumulated.modified;
    by_language; files; documentation_files;
    documentation = sum_totals (List.map (fun document -> document.totals) documentation_files) }

(* Yojson owns the JSON grammar but also admits comments, bare object keys and
   raw controls in strings. Reject those lexical extensions without replacing
   its parser. Python's accepted NaN/Infinity metadata remains compatible. *)
let reject_json_extensions content =
  let quoted = ref false and escaped = ref false and previous = ref None in
  String.iteri (fun index character ->
    if !quoted then begin
      if Char.code character < 0x20 then fail "raw control in manifest string at byte %d" index;
      if !escaped then escaped := false
      else if character = '\\' then escaped := true
      else if character = '"' then begin quoted := false; previous := Some '"' end
    end else match character with
      | '"' -> quoted := true
      | '/' -> fail "comments are not permitted in manifest JSON at byte %d" index
      | ':' when !previous <> Some '"' -> fail "manifest object keys must be quoted at byte %d" index
      | ' ' | '\t' | '\r' | '\n' -> ()
      | _ -> previous := Some character) content

let read_manifest path =
  let content = In_channel.with_open_bin path In_channel.input_all in
  if not (String.is_valid_utf_8 content) then fail "manifest is not UTF-8: %s" path;
  reject_json_extensions content;
  let fields = assoc "manifest" (Yojson.Safe.from_string content) in
  match member "files" fields with
  | Some (`List records) -> records
  | _ -> fail "manifest files must be an array"

let percent_json = function None -> `Null | Some value -> `Float value

(* handwritten_* preserves the archive receipt schema. It is an alias for
   non-generated according to explicit markers, never an authorship inference. *)
let totals_json value = [
  "generated_lines", `Int value.generated_lines;
  "non_generated_lines", `Int value.non_generated_lines;
  "handwritten_lines", `Int value.non_generated_lines;
  "total_lines", `Int value.total_lines;
  "generated_percent", percent_json value.generated_percent;
  "non_generated_percent", percent_json value.non_generated_percent;
]

let source_json (file : source_file) = `Assoc [
  "path", `String file.path; "language", `String file.language;
  "generated", `Bool file.generated; "lines", `Int file.lines;
]

let documentation_json report = `Assoc ([
  "metric", `String "docgen_percentage";
  "scope", `String "selected_archive_markdown";
  "files", `List (List.map (fun (file : documentation_file) ->
    `Assoc (("path", `String file.path) :: totals_json file.totals)) report.documentation_files);
  "definition", `String ("physical Markdown lines including comments and blank lines; selected " ^
    "archive files only; explicit generated headers mark whole documents, " ^
    "otherwise only csf:diagram interiors are generated and marker lines " ^
    "are handwritten; not a proof or derivability score");
] @ totals_json report.documentation)

let to_json report = `Assoc ([
  "metric", `String "codegen_percentage";
  "scope", `String "selected_archive_code";
  "excluded_code_extensions", `List (List.map (fun extension -> `String extension) excluded_code_extensions);
  "excluded_code_paths", `List (List.map (fun path -> `String path) excluded_code_paths);
  "generated_files", `Int report.generated_files;
  "non_generated_files", `Int report.non_generated_files;
  "handwritten_files", `Int report.non_generated_files;
  "excluded_files", `Int report.excluded_files;
  "modified_files", `List (List.map (fun name -> `String name) report.modified_files);
  "by_language", `Assoc (List.map (fun (name, totals) -> name, `Assoc (totals_json totals)) report.by_language);
  "files", `List (List.map source_json report.files);
  "documentation", documentation_json report;
  "definition", `String "physical lines including comments and blank lines; selected archive code after declared extension and path exclusions; not all shipped code, a proof or derivability score";
  "authorship", `String "Generated and non-generated classify explicit markers; neither identifies human or agent authorship.";
] @ totals_json report.code)
