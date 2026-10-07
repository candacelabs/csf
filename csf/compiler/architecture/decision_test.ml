(* The golden test: the decision emitter's projections must equal the checked-in
   files byte for byte -- the Lean proof and the four text projections. The
   declaration, its grammar and the emitted files are the same bytes the
   operator reviewed, so a change to the emitter or the declaration that changes
   the output fails here. *)

let expect message condition = if not condition then failwith message

let read path = In_channel.with_open_bin path In_channel.input_all

let accept = function
  | Ok value -> value
  | Error errors -> failwith (String.concat "; "
      (List.map (fun (error : Model.diagnostic) -> error.message) errors))

(* The declaration's logical name, as the generated header records it. The
   golden file is read from the path the build hands in; the name is part of
   the declaration, not of where the file happens to sit on disk. *)
let logical = "csf/compiler/verification/question_tree.csf"

let () =
  let argument index default =
    if Array.length Sys.argv > index then Sys.argv.(index) else default in
  let grammar_path = argument 1 "csf/compiler/architecture/decision.ebnf" in
  let source_path = argument 2 "csf/compiler/verification/question_tree.csf" in
  let lean_path = argument 3 "csf/compiler/verification/CSFCVerifier.lean" in
  let kind_enum_path = argument 4 "csf/compiler/verification/question_kind_enum.txt" in
  let directory_path = argument 5 "csf/compiler/verification/question_directory.txt" in
  let cli_layer_path = argument 6 "csf/compiler/verification/question_cli_layer.txt" in
  let jev_prompt_path = argument 7 "csf/compiler/verification/question_jev_prompt.txt" in
  let grammar = read grammar_path in
  let source = read source_path in
  let tree = accept (Decision.parse ~grammar ~source ~filename:logical) in
  expect "the decision tree is invalid" (Decision.check tree = []);
  expect "the emitted Lean file differs from the golden"
    (Decision.render_lean ~source:logical tree = read lean_path);
  let projections = Decision.projections ~source:logical tree in
  let golden name path =
    expect ("the emitted " ^ name ^ " differs from the golden")
      (List.assoc name projections = read path) in
  golden "question_kind_enum.txt" kind_enum_path;
  golden "question_directory.txt" directory_path;
  golden "question_cli_layer.txt" cli_layer_path;
  golden "question_jev_prompt.txt" jev_prompt_path;
  print_endline "CSF decision golden tests passed"
