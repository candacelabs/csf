open Cmdliner

let required_path name doc =
  Arg.(required & opt (some string) None & info [name] ~docv:"PATH" ~doc)

let command =
  let run root compiler output = Example.run ~root ~compiler ~output in
  Cmd.v (Cmd.info "csfc-example" ~doc:"Run the real CSF architecture compiler example and retain its evidence.")
    Term.(const run
      $ required_path "root" "Repository checkout to inspect."
      $ required_path "compiler" "Built csfc executable."
      $ required_path "output" "Fresh output directory; its parent must already exist.")

let () = exit (Cmd.eval' command)
