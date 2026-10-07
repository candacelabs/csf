(* Emit pkg/collections/collections.go. The single output is fixed: the ask
   exports exactly two primitives, so there is no instance data to vary. *)
let write path content =
  let channel = open_out_bin path in
  Fun.protect ~finally:(fun () -> close_out channel) (fun () -> output_string channel content)

let () =
  try
    if Array.length Sys.argv <> 2 then
      failwith "usage: generate <go-output>";
    write Sys.argv.(1) (Collections_codegen.render ())
  with
  | Failure message | Sys_error message ->
      Printf.eprintf "CSF collections generation: %s\n" message; exit 1
