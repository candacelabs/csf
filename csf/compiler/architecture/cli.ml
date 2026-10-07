(** The command-line interface (CLI): Cmdliner parses argv into Compiler.config and a
    Compiler.mode. Core passes receive those typed values, not flag strings.
    Only this layer formats diagnostics and selects a process exit status. *)
open Cmdliner

let summary (report : Compiler.report) =
  Printf.sprintf "architecture=%s mode=%s declarations=checked source=checked directories=%d/%d obligations=%d"
    report.architecture_name (Compiler.mode_name report.mode)
    report.directories_declared report.directories_tracked report.obligations

let diagnostic (error : Model.diagnostic) =
  Printf.sprintf "%s:%d:%d: %s: %s" error.at.file error.at.line error.at.column
    error.code error.message

let failed errors =
  List.iter (fun error -> Printf.eprintf "%s\n%!" (diagnostic error)) errors;
  1

let report_result = function
  | Ok report -> Printf.printf "%s\n%!" (summary report); 0
  | Error errors -> failed errors

(* The JSON document is the whole of standard output, so a consumer can parse
   it directly; diagnostics still go to standard error with exit status one. *)
let json_result = function
  | Ok document -> print_string document; flush stdout; 0
  | Error errors -> failed errors

(** [Files] writes the projection directory; [Json] prints [Emit.json]. *)
type format = Files | Json

let format =
  Arg.(value & opt (enum ["files", Files; "json", Json]) Files & info ["format"] ~docv:"FORMAT"
    ~doc:"$(b,files) writes the projections to $(b,--output); $(b,json) prints the checked \
          architecture as JSON on standard output and writes nothing.")

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

(* --- the shell verb --- *)

(* One service declaration compiled into a Go shell. The three chief invariants
   are compile errors: a declaration that fans a question past sixteen options,
   leaves a question with no option to project it, or leaves a leaf without one
   template is refused before any file is written. *)
let shell_grammar =
  path "grammar" "csf/compiler/architecture/shell.ebnf" "Executable EBNF grammar for the shell declaration."

let shell_out =
  Arg.(value & opt string "." & info ["out"] ~docv:"DIR"
    ~doc:"Directory the generated shell is written into.")

let shell_declaration =
  Arg.(required & pos 0 (some string) None & info [] ~docv:"DECLARATION"
    ~doc:"A service declaration written in the shell grammar.")

let shell_run grammar_path out source_path =
  let result =
    match Shell.parse_files ~grammar_path ~source_path with
    | Error diagnostics -> Error diagnostics
    | Ok declaration -> begin match Shell.check declaration with
        | [] ->
            (try Shell.write ~source:source_path ~out declaration; Ok ()
             with
             | Sys_error message -> Error [{ at = { file = source_path; line = 1; column = 1 };
                 code = "CSF_IO"; message }]
             | Unix.Unix_error (error, operation, path) -> Error [{ at = { file = path; line = 1; column = 1 };
                 code = "CSF_IO"; message = operation ^ ": " ^ Unix.error_message error }])
        | findings -> Error findings end in
  match result with
  | Ok () -> Printf.printf "shell=%s out=%s\n%!" source_path out; 0
  | Error errors -> failed errors

let shell_command =
  Cmd.v (Cmd.info "shell"
    ~doc:"Compile one service declaration into a Go shell, refusing an invariant violation.")
    Term.(const shell_run $ shell_grammar $ shell_out $ shell_declaration)

(* Inject the runner only at the CLI boundary so argument tests need no source
   tree or writes. The end-to-end example separately exercises Compiler.run
   through the actual executable; injected tests alone do not establish that. *)
let command_with ?(json = Compiler.json) run =
  let term mode = Term.(const (fun config -> report_result (run mode config)) $ config) in
  let command mode doc = Cmd.v (Cmd.info (Compiler.mode_name mode) ~doc) (term mode) in
  let emit = Term.(const (fun config -> function
      | Files -> report_result (run Compiler.Emit config)
      | Json -> json_result (json config)) $ config $ format) in
  Cmd.group ~default:(term Compiler.Check)
    (Cmd.info "csfc" ~doc:"Check and project declared CSF architectures.") [
      command Compiler.Check "Check declarations and repository source (the default command).";
      Cmd.v (Cmd.info (Compiler.mode_name Compiler.Emit)
        ~doc:"Check the architecture and write its projections, or print it as JSON.") emit;
      command Compiler.Check_generated "Check the architecture and reject projection drift.";
      shell_command;
    ]

let command = command_with Compiler.run
