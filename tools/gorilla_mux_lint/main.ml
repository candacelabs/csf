let () =
  if Array.length Sys.argv <> 3 then begin
    prerr_endline "usage: check ROOT FILE_LIST (NUL-delimited repository-relative paths)";
    exit 2
  end;
  try
    let root = Unix.realpath Sys.argv.(1) in
    let paths = Checker.read_file Sys.argv.(2) |> Checker.inventory in
    let report = Checker.check root paths in
    Checker.print_report report;
    exit (Checker.status report)
  with
  | Sys_error message | Invalid_argument message | Failure message ->
      Printf.eprintf "inventory:1: %s\n" message; exit 2
  | Unix.Unix_error (error, operation, _) ->
      Printf.eprintf "inventory:1: %s: %s\n" operation (Unix.error_message error); exit 2
