let usage = "usage: generate --root ROOT write|check|metrics [--manifest PATH]"

let documentation root command =
  let mode = match command with
    | "write" -> Compiler.Write
    | "check" -> Compiler.Check
    | _ -> raise (Compiler.Error usage) in
  let changed = Compiler.run root mode in
  Printf.printf "CSF documentation: %s (%d changed files)\n"
    (if mode = Compiler.Check then "current" else "generated") (List.length changed)

let metrics root manifest =
  let records = Generation_metrics.read_manifest manifest in
  let report = Generation_metrics.calculate ~root records in
  print_endline (Yojson.Safe.to_string (Generation_metrics.to_json report))

let () =
  try
    (match Array.to_list Sys.argv with
    | [_; "--root"; root; "metrics"] -> metrics root (Filename.concat root "SOURCE_RECEIPT.json")
    | [_; "--root"; root; "metrics"; "--manifest"; manifest] -> metrics root manifest
    | [_; "--root"; root; ("write" | "check" as command)] -> documentation root command
    | _ -> raise (Compiler.Error usage))
  with
  | Compiler.Error message | Generation_metrics.Error message | Metric_text.Error message
  | Yojson.Json_error message | Sys_error message ->
      Printf.eprintf "CSF generator: %s\n" message; exit 1
  | Unix.Unix_error (error, operation, path) ->
      Printf.eprintf "CSF generator: %s: %s: %s\n" path operation
        (Unix.error_message error); exit 1
