(* The rest_client template engine's argv: template <kind.go.tmpl> <instance.go.tmpl> <declaration.csf> <output-directory>. *)
open Template

let read path =
  let channel = open_in_bin path in
  Fun.protect ~finally:(fun () -> close_in channel)
    (fun () -> really_input_string channel (in_channel_length channel))

let write path content =
  let channel = open_out_bin path in
  Fun.protect ~finally:(fun () -> close_out channel) (fun () -> output_string channel content)

let () =
  try
    if Array.length Sys.argv <> 5 then
      failwith "usage: template <kind.go.tmpl> <instance.go.tmpl> <declaration.csf> <output-directory>";
    let kind_template = read Sys.argv.(1) in
    let instance_template = read Sys.argv.(2) in
    let source = read Sys.argv.(3) in
    let root = Sys.argv.(4) in
    let rec make_directory path =
      if path <> "" && not (Sys.file_exists path) then begin
        make_directory (Filename.dirname path);
        Sys.mkdir path 0o755
      end in
    List.iter
      (fun (path, content) ->
        let target = Filename.concat root path in
        make_directory (Filename.dirname target);
        write target content)
      (generate ~kind_template ~instance_template source)
  with
  | Failure message | Sys_error message -> Printf.eprintf "csfc rest_client template: %s\n" message; exit 1
