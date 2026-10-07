let usage = "usage: generate --root ROOT write|check|metrics [--manifest PATH] | lint FILE... | link [FILE...]"

(* The READMEs the ontology score's vocabulary signals measure: every tracked
   file named README.md. A tree that is not a git checkout, such as an
   unpacked source archive, tracks none, and says so. *)
let tracked_readmes root =
  let channel = Unix.open_process_args_in "git" [|"git"; "-C"; root; "ls-files"; "-z"; "--"; "README.md"; "*/README.md"|] in
  let listing = In_channel.input_all channel in
  match Unix.close_process_in channel with
  | Unix.WEXITED 0 -> String.split_on_char '\000' listing |> List.filter (fun path -> path <> "")
  | _ -> prerr_endline "CSF generator: not a git checkout, so no tracked README is linked"; []

let documentation root command =
  let mode, readmes = match command with
    | "write" -> Compiler.Write, tracked_readmes root
    | "check" -> Compiler.Check, []
    | _ -> raise (Compiler.Error usage) in
  let changed = Compiler.run ~readmes root mode in
  Printf.printf "CSF documentation: %s (%d changed files)\n"
    (if mode = Compiler.Check then "current" else "generated") (List.length changed)

(* Read-only vocabulary report for any Markdown files; exits 1 on a finding. *)
let lint root paths =
  let findings = Compiler.lint root paths in
  List.iter print_endline findings;
  if findings <> [] then exit 1

(* Links the unlinked terms the lint reports, in place, and names each file it changed. *)
let link root paths =
  let changed = Compiler.link root paths in
  List.iter print_endline changed;
  Printf.printf "CSF documentation: linked (%d changed files)\n" (List.length changed)

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
    | _ :: "--root" :: root :: "lint" :: (_ :: _ as paths) -> lint root paths
    | _ :: "--root" :: root :: "link" :: (_ :: _ as paths) -> link root paths
    | [_; "--root"; root; "link"] -> link root (List.map (Filename.concat root) (tracked_readmes root))
    | _ -> raise (Compiler.Error usage))
  with
  | Compiler.Error message | Ebnf_codegen.Error message | Generation_metrics.Error message | Metric_text.Error message
  | Yojson.Json_error message | Sys_error message ->
      Printf.eprintf "CSF generator: %s\n" message; exit 1
  | Unix.Unix_error (error, operation, path) ->
      Printf.eprintf "CSF generator: %s: %s: %s\n" path operation
        (Unix.error_message error); exit 1
