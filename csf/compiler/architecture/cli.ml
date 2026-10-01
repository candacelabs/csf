(** The command-line interface (CLI): Cmdliner parses argv into Compiler.config and a
    Compiler.mode. Core passes receive those typed values, not flag strings.
    Only this layer formats diagnostics and selects a process exit status. *)
open Cmdliner

let summary (report : Compiler.report) =
  Printf.sprintf "architecture=%s mode=%s declarations=checked source=checked obligations=%d"
    report.architecture_name (Compiler.mode_name report.mode) report.obligations

let diagnostic (error : Model.diagnostic) =
  Printf.sprintf "%s:%d:%d: %s: %s" error.at.file error.at.line error.at.column
    error.code error.message

let report_result = function
  | Ok report -> Printf.printf "%s\n%!" (summary report); 0
  | Error errors ->
      List.iter (fun error -> Printf.eprintf "%s\n%!" (diagnostic error)) errors;
      1

let path name default doc =
  Arg.(value & opt string default & info [name] ~docv:"PATH" ~doc)

(* The grammar describes legal syntax; the source is one architecture written
   in that syntax. Keeping defaults together lets the executable example use
   the same inputs without inventing another set of repository paths. *)
let default_config : Compiler.config = {
  grammar_path = "csf/compiler/architecture/language.ebnf";
  source_path = "csf/architecture/architecture.csf";
  root = ".";
  output_path = "csf/architecture/generated";
  require_closed = false;
}

let config =
  let make grammar_path source_path root output_path require_closed : Compiler.config =
    { grammar_path; source_path; root; output_path; require_closed } in
  Term.(const make
    $ path "grammar" default_config.grammar_path "Executable Extended Backus-Naur Form (EBNF) grammar."
    $ path "source" default_config.source_path "Architecture source."
    $ path "root" default_config.root "Repository root for source checks."
    $ path "output" default_config.output_path "Projection directory."
    $ Arg.(value & flag & info ["require-closed"]
        ~doc:"Reject outstanding implementation or verification obligations."))

(* Inject the runner only at the CLI boundary so argument tests need no source
   tree or writes. The end-to-end example separately exercises Compiler.run
   through the actual executable; injected tests alone do not establish that. *)
let command_with run =
  let term mode = Term.(const (fun config -> report_result (run mode config)) $ config) in
  let command mode doc = Cmd.v (Cmd.info (Compiler.mode_name mode) ~doc) (term mode) in
  Cmd.group ~default:(term Compiler.Check)
    (Cmd.info "csfc" ~doc:"Check and project declared CSF architectures.") [
      command Compiler.Check "Check declarations and repository source (the default command).";
      command Compiler.Emit "Check the architecture and write its projections.";
      command Compiler.Check_generated "Check the architecture and reject projection drift.";
    ]

let command = command_with Compiler.run
