let print_diagnostic (diagnostic : Model.diagnostic) =
  Printf.eprintf "%s:%d:%d: %s: %s\n" diagnostic.at.file diagnostic.at.line
    diagnostic.at.column diagnostic.code diagnostic.message

let () =
  if Array.length Sys.argv <> 3 then begin
    prerr_endline "usage: symbol_codegen GRAMMAR_PATH OUTPUT_PATH";
    exit 2
  end;
  let grammar_path = Sys.argv.(1) and output_path = Sys.argv.(2) in
  try
    let grammar = In_channel.with_open_bin grammar_path In_channel.input_all in
    match Symbol_codegen.generate ~grammar with
    | Ok source -> Out_channel.with_open_bin output_path (fun channel -> output_string channel source)
    | Error diagnostics ->
        List.iter (fun (diagnostic : Model.diagnostic) ->
          print_diagnostic { diagnostic with at = { diagnostic.at with file = grammar_path } }) diagnostics;
        exit 1
  with
  | Sys_error message -> prerr_endline message; exit 2
  | Frontend_lexer.Error diagnostic -> print_diagnostic diagnostic; exit 1
