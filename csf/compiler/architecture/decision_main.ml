(* The decision emitter command: write every projection -- the Lean proof and
   the four text projections -- into the output directory, or check that the
   files already there match the projections byte for byte. The grammar and
   source are read from disk; nothing here runs Lean. *)

let print_diagnostic (diagnostic : Model.diagnostic) =
  Printf.eprintf "%s:%d:%d: %s: %s\n" diagnostic.Model.at.Model.file
    diagnostic.Model.at.Model.line diagnostic.Model.at.Model.column diagnostic.Model.code
    diagnostic.Model.message

let () =
  if Array.length Sys.argv <> 5 || not (List.mem Sys.argv.(1) ["write"; "check"]) then begin
    prerr_endline "usage: decision_codegen (write|check) GRAMMAR_PATH SOURCE_PATH OUTPUT_DIR";
    exit 2
  end;
  let grammar_path = Sys.argv.(2)
  and source_path = Sys.argv.(3)
  and output_dir = Sys.argv.(4) in
  try
    let grammar = In_channel.with_open_bin grammar_path In_channel.input_all in
    let source = In_channel.with_open_bin source_path In_channel.input_all in
    let report (findings : Model.diagnostic list) =
      List.iter print_diagnostic findings;
      exit 1 in
    match Decision.parse ~grammar ~source ~filename:source_path with
    | Error findings -> report findings
    | Ok tree ->
        (match Decision.check tree with
         | _ :: _ as findings -> report findings
         | [] ->
             if Sys.argv.(1) = "write" then
               Decision.write ~source:source_path ~out:output_dir tree
             else Decision.verify ~source:source_path ~out:output_dir tree)
  with
  | Sys_error message -> prerr_endline message; exit 2
