(** Reusable pipeline shared by the command-line interface (CLI) and library consumers.
    Text -> syntax tree -> typed declarations -> resolved graph -> source
    findings. An analysis-stage failure stops the pipeline before any writes;
    callers receive diagnostics rather than console output or process exits. *)
open Model

type mode = Check | Emit | Check_generated
type config = {
  grammar_path : string;
  source_path : string;
  root : string;
  output_path : string;
  require_closed : bool;
}
type report = {
  architecture_name : string;
  mode : mode;
  obligations : int;
  directories_declared : int;
  directories_tracked : int;
}

let mode_name = function Check -> "check" | Emit -> "emit" | Check_generated -> "check-generated"
(* [let*] below is Result.bind: unwrap [Ok] to continue, or return the first
   stage's [Error] unchanged. It does not introduce asynchronous execution. *)
let ( let* ) = Result.bind

let io_result file operation =
  let failure path message = Error [{at = {file = path; line = 1; column = 1}; code = "CSF_IO"; message}] in
  try operation () with
  | Sys_error message -> failure file message
  | Unix.Unix_error (error, operation, path) -> failure path (operation ^ ": " ^ Unix.error_message error)

(* Text to a resolved graph plus its tree census. The census needs the tracked
   inventory, which comes from git, so this is where the checkout is read. *)
let analyze config =
  let* syntax = Frontend.parse_files ~grammar_path:config.grammar_path ~source_path:config.source_path in
  let* model = Decode.architecture syntax in
  let* resolved = Validate.resolve model in
  Ok (resolved, Validate.tree_diagnostics resolved.architecture (Source_check.tracked ~root:config.root))

(* Ordinary compilation retains open obligations as data. Closed mode promotes
   them to errors: valid declarations are not evidence of implemented cleanup.
   Tree offenses are errors in every mode: a tracked file outside its declared
   home is not a warning. *)
let checked config = io_result config.source_path (fun () ->
  let* resolved, tree = analyze config in
  let errors = Source_check.check ~root:config.root resolved @ tree.offenses in
  if errors <> [] then Error errors
  else if config.require_closed && resolved.obligations <> [] then
    Error (List.map (fun (item : obligation) ->
      { at = resolved.architecture.at; code = "CSF_OBLIGATION"; message = item.subject ^ ": " ^ item.requirement }) resolved.obligations)
  else Ok (resolved, tree))

let compile config = Result.map fst (checked config)

(* The panel ratio: declared directories over tracked directories. Reads the
   same git inventory the census does, without resolving the graph. *)
let census ~root (architecture : Model.architecture) =
  (List.length architecture.directories, Facts.tracked_directories (Source_check.tracked ~root))

let projections model = [
  "csf_architecture_cgen.ml", Emit.ocaml model;
  "architecture_cgen.mmd", Emit.mermaid model;
  "review_cgen.md", Emit.review model;
]

(* Rename a completed sibling file into place; readers never see a half-written
   artifact. The whole artifact set is NOT transactional: if a later write
   fails, earlier files may already be replaced. Fun.protect removes leftovers
   whether output_string/rename succeeds or raises an exception. *)
let write_file path contents =
  let temporary = Filename.temp_file ~temp_dir:(Filename.dirname path) ".csfc-" ".tmp" in
  Fun.protect ~finally:(fun () -> if Sys.file_exists temporary then Sys.remove temporary) (fun () ->
    Out_channel.with_open_bin temporary (fun channel -> output_string channel contents);
    Sys.rename temporary path)

let write output model =
  if not (Sys.file_exists output) then Unix.mkdir output 0o755;
  projections model |> List.iter (fun (name, contents) -> write_file (Filename.concat output name) contents);
  Ok ()

(* Recompute expected bytes from the same checked model. A missing output and
   an edited output both mean drift; this check deliberately performs no repair. *)
let check_generated output model =
  let errors = projections model |> List.filter_map (fun (name, expected) ->
    let path = Filename.concat output name in
    if Sys.file_exists path && In_channel.with_open_bin path In_channel.input_all = expected then None
    else Some {at = {file = path; line = 1; column = 1}; code = "CSF_GENERATED_DRIFT";
      message = "run csfc emit with the same grammar, source and output paths"}) in
  if errors = [] then Ok () else Error errors

let run mode config = io_result config.output_path (fun () ->
  let* resolved, tree = checked config in
  let* () = match mode with
    | Check -> Ok ()
    | Emit -> write config.output_path resolved
    | Check_generated -> check_generated config.output_path resolved in
  Ok {
    architecture_name = resolved.architecture.name; mode;
    obligations = List.length resolved.obligations;
    directories_declared = tree.directories_declared;
    directories_tracked = tree.directories_tracked;
  })

let json config = Result.map Emit.json (compile config)
