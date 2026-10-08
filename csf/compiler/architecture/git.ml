(* Output goes to a mode-600 temporary file rather than a pipe, so a large
   output cannot block the child while this process waits for it. *)
let output ~root arguments =
  try
    let temporary = Filename.temp_file "csfc-git" ".out" in
    Fun.protect ~finally:(fun () -> if Sys.file_exists temporary then Sys.remove temporary) (fun () ->
      let sink = Unix.openfile temporary [Unix.O_WRONLY; Unix.O_TRUNC] 0o600 in
      let status = Fun.protect ~finally:(fun () -> Unix.close sink) (fun () ->
        let argv = Array.of_list ("git" :: "-C" :: root :: arguments) in
        snd (Unix.waitpid [] (Unix.create_process "git" argv Unix.stdin sink Unix.stderr))) in
      match status with
      | Unix.WEXITED 0 -> Some (In_channel.with_open_bin temporary In_channel.input_all)
      | _ -> None)
  with Unix.Unix_error _ | Sys_error _ -> None
