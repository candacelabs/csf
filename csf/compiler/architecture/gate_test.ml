(* The golden test: the gate generator's two projections must equal the
   checked-in golden files byte for byte. The declaration, its Datalog program
   and its Go check are the same bytes the operator reviewed, so a change to
   the emitter or the declaration that changes the output fails here. *)

let expect message condition = if not condition then failwith message

let read path = In_channel.with_open_bin path In_channel.input_all

let accept = function
  | Ok value -> value
  | Error errors -> failwith (String.concat "; "
      (List.map (fun (error : Model.diagnostic) -> error.message) errors))

(* The gate's logical name, as the generated header records it. The golden
   files are read from the paths the build hands in; the name is part of the
   declaration, not of where the file happens to sit on disk. *)
let logical = "csf/compiler/testdata/golden/github_gate/github_gate.csf"

let () =
  let argument index default =
    if Array.length Sys.argv > index then Sys.argv.(index) else default in
  let grammar_path = argument 1 "csf/compiler/architecture/gate.ebnf" in
  let source_path = argument 2 "csf/compiler/testdata/golden/github_gate/github_gate.csf" in
  let dl_path = argument 3 "csf/compiler/testdata/golden/github_gate/github_shell.dl" in
  let go_path = argument 4 "csf/compiler/testdata/golden/github_gate/github_gate.go" in
  let grammar = read grammar_path in
  let source = read source_path in
  let gate = accept (Gate.parse ~grammar ~source ~filename:logical) in
  expect "the gate declaration is invalid" (Gate.check gate = []);
  expect "the emitted Datalog program differs from the golden"
    (Gate.render_dl ~source:logical gate = read dl_path);
  expect "the emitted Go check differs from the golden"
    (Gate.render_go ~source:logical gate = read go_path);
  print_endline "CSF gate golden tests passed"
