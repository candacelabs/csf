let require condition message = if not condition then failwith message
let rejects thunk =
  match thunk () with
  | _ -> failwith "expected invalid input to fail"
  | exception Failure _ | exception Invalid_argument _ -> ()

let () =
  let image = "example.invalid/build:1@sha256:" ^ String.make 64 'a' in
  let public = Devcontainer_codegen.public ~image () in
  require (public = Devcontainer_codegen.public ~image:(image ^ "\n") ()) "image file newline";
  require (String.ends_with ~suffix:"LABEL dis.shell=\"/bin/bash\"\n" public) "dis integration";
  require (public = Devcontainer_codegen.public ~image ()) "deterministic output";
  List.iter (fun image -> rejects (fun () -> Devcontainer_codegen.public ~image ())) [
    ""; "image:latest"; "image@sha256:abc"; image ^ "\nRUN false";
    "image name@sha256:" ^ String.make 64 'a'; "image@sha256:" ^ String.make 64 'g';
    "image@sha256:" ^ String.make 64 'a' ^ "@extra"
  ];
  let source = "FROM example.invalid/runtime:1\nLABEL dis.ports=\"1234\"\nRUN printf 'private runtime'\n" in
  require (Devcontainer_codegen.runtime ~image ~source () =
    Devcontainer_codegen.header ^ "FROM " ^ image ^ " AS csf_bazel\n\n" ^ source ^
    "\n# Bazel resolves the build toolchains declared in MODULE.bazel.\n" ^
    "COPY --from=csf_bazel /usr/local/bin/bazel /usr/local/bin/bazel\n") "runtime preserved verbatim";
  rejects (fun () -> Devcontainer_codegen.runtime ~image ~source:"\n" ());
  let source = "# untouched\nFROM @BAZEL_EXECUTION_IMAGE@ AS compiler\nCOPY --from=@BAZEL_EXECUTION_IMAGE@ /tool /tool\n" in
  require (Devcontainer_codegen.template ~image ~source () =
    Devcontainer_codegen.header ^ "# untouched\nFROM " ^ image ^ " AS compiler\nCOPY --from=" ^ image ^ " /tool /tool\n")
    "replace all image tokens and preserve template bytes";
  rejects (fun () -> Devcontainer_codegen.template ~image ~source:"FROM other\n" ());
  let provenance : Codegen_header.provenance = {
    sources = ["//bazel:execution_image.txt"; "//runtime:Dockerfile.in"];
    generator = "//tools/devcontainer:devcontainer_codegen.ml";
    owner = "//tools/devcontainer:dockerfile";
    regenerate = "bash tools/devcontainer/generate.sh write";
  } in
  let declared = Codegen_header.render ~provenance Codegen_header.Hash in
  require (String.starts_with ~prefix:declared
    (Devcontainer_codegen.public ~provenance ~image ())) "shared provenance header is first";
  require (String.starts_with ~prefix:declared
    (Devcontainer_codegen.template ~provenance ~image ~source ())) "template uses shared provenance";
  List.iter (fun syntax ->
    let with_metadata = Codegen_header.render ~provenance syntax in
    require (String.length with_metadata > String.length (Codegen_header.render syntax))
      "provenance accompanies every supported comment syntax")
    [Codegen_header.Ocaml; Mermaid; Markdown; Hash; C; Sexp];
  List.iter (fun bad ->
    rejects (fun () -> Codegen_header.render ~provenance:{provenance with generator = bad} Codegen_header.Hash))
    [""; "line\nnext"; "line\rnext"; "line\000next"; "a*)b"; "a*/b"; "a--b"; "a\"b";
     "/tmp/sandbox/generator.ml"; "bash /tmp/sandbox/generate.sh write"];
  rejects (fun () -> Codegen_header.render ~provenance:{provenance with sources = []} Codegen_header.Hash);
  print_endline "devcontainer generator: all checks passed"
