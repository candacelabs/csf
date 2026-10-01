let read path = In_channel.with_open_bin path In_channel.input_all

let () =
  try
    let rendered, destination = match Array.to_list Sys.argv with
      | _ :: mode :: image :: source :: destination :: generator :: owner :: regenerate :: sources ->
          let provenance : Codegen_header.provenance = {sources; generator; owner; regenerate} in
          let image = read image in
          let rendered = match mode with
            | "public" -> Devcontainer_codegen.public ~provenance ~image ()
            | "runtime" -> Devcontainer_codegen.runtime ~provenance ~image ~source:(read source) ()
            | "template" -> Devcontainer_codegen.template ~provenance ~image ~source:(read source) ()
            | _ -> failwith "unknown Dockerfile projection mode" in
          rendered, destination
      | _ ->
          prerr_endline "usage: devcontainer_codegen MODE IMAGE SOURCE OUTPUT GENERATOR OWNER REGENERATE SOURCES...";
          exit 2 in
    Out_channel.with_open_bin destination (fun channel -> output_string channel rendered)
  with Failure message | Invalid_argument message | Sys_error message -> prerr_endline message; exit 1
