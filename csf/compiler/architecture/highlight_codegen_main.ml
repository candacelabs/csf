let () =
  if Array.length Sys.argv <> 4 || not (List.mem Sys.argv.(1) ["write"; "check"]) then begin
    prerr_endline "usage: highlight_codegen (write|check) GRAMMAR_PATH OUTPUT_DIRECTORY";
    exit 2
  end;
  try
    let grammar = In_channel.with_open_bin Sys.argv.(2) In_channel.input_all in
    let outputs = Highlight_codegen.generate ~grammar in
    List.iter (fun (path, content) ->
      let path = Filename.concat Sys.argv.(3) path in
      if Sys.argv.(1) = "write" then
        Out_channel.with_open_bin path (fun channel -> output_string channel content)
      else if not (Sys.file_exists path) || In_channel.with_open_bin path In_channel.input_all <> content then
        failwith ("generated highlighting drift: " ^ path)) outputs
  with
  | Frontend_lexer.Error d ->
      Printf.eprintf "%s:%d:%d: %s: %s\n" Sys.argv.(2) d.at.line d.at.column d.code d.message; exit 1
  | Sys_error message | Failure message -> prerr_endline message; exit 1
