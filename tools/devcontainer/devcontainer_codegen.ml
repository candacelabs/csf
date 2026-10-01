(* Dockerfiles project the Bazel execution image; runtime choices stay explicit. *)
let pinned_image text =
  let image = String.trim text in
  let invalid () = failwith "Bazel execution image must be one digest-pinned image reference" in
  match String.split_on_char '@' image with
  | [name; digest] when name <> "" && String.length digest = 71 &&
      String.sub digest 0 7 = "sha256:" ->
      if not (String.for_all (function
        | 'a' .. 'z' | 'A' .. 'Z' | '0' .. '9' | '/' | '.' | '_' | '-' | ':' -> true
        | _ -> false) name) ||
         not (String.for_all (function '0' .. '9' | 'a' .. 'f' -> true | _ -> false)
           (String.sub digest 7 64)) then invalid ();
      image
  | _ -> invalid ()

let header = Codegen_header.render Codegen_header.Hash

let public ?provenance ~image () =
  Codegen_header.render ?provenance Codegen_header.Hash ^ "FROM " ^ pinned_image image ^ "\n" ^
  "USER root\nENTRYPOINT []\nCMD [\"/bin/bash\"]\nWORKDIR /workspace\n" ^
  "LABEL dis.shell=\"/bin/bash\"\n"

let runtime ?provenance ~image ~source () =
  if String.trim source = "" then failwith "runtime Dockerfile must not be empty";
  Codegen_header.render ?provenance Codegen_header.Hash ^ "FROM " ^ pinned_image image ^ " AS csf_bazel\n\n" ^ source ^
  "\n# Bazel resolves the build toolchains declared in MODULE.bazel.\n" ^
  "COPY --from=csf_bazel /usr/local/bin/bazel /usr/local/bin/bazel\n"

let template ?provenance ~image ~source () =
  let image = pinned_image image in
  let token = "@BAZEL_EXECUTION_IMAGE@" in
  let width = String.length token in
  let length = String.length source in
  let output = Buffer.create length in
  let rec copy position count =
    if position = length then count
    else if position + width <= length && String.sub source position width = token then begin
      Buffer.add_string output image;
      copy (position + width) (count + 1)
    end else begin
      Buffer.add_char output source.[position];
      copy (position + 1) count
    end in
  if copy 0 0 = 0 then failwith "Dockerfile template must contain @BAZEL_EXECUTION_IMAGE@";
  Codegen_header.render ?provenance Codegen_header.Hash ^ Buffer.contents output
