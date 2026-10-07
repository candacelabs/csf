(* The golden test: the slice planner generator's Go projection must equal the
   checked-in golden file byte for byte, and rendering it twice must yield the
   same bytes — the stage 1 / stage 2 comparison a self-hosting compiler makes.
   The declaration and its Go projection are the same bytes the operator
   reviewed, so a change to the emitter or the declaration that changes the
   output fails here. *)

let expect message condition = if not condition then failwith message

let read path = In_channel.with_open_bin path In_channel.input_all

let accept = function
  | Ok value -> value
  | Error errors -> failwith (String.concat "; "
      (List.map (fun (error : Model.diagnostic) -> error.message) errors))

(* The planner's logical name, as the generated header records it. The golden
   files are read from the paths the build hands in; the name is part of the
   declaration, not of where the file happens to sit on disk. *)
let logical = "csf/compiler/testdata/golden/slice_planner/slice_planner.csf"

let () =
  let argument index default =
    if Array.length Sys.argv > index then Sys.argv.(index) else default in
  let grammar_path = argument 1 "csf/compiler/architecture/slice_planner.ebnf" in
  let source_path = argument 2 "csf/compiler/testdata/golden/slice_planner/slice_planner.csf" in
  let go_path = argument 3 "csf/compiler/testdata/golden/slice_planner/slice_planner.go" in
  let grammar = read grammar_path in
  let source = read source_path in
  let planner = accept (Slice_planner.parse ~grammar ~source ~filename:logical) in
  expect "the slice planner declaration is invalid" (Slice_planner.check planner = []);
  let stage1 = Slice_planner.render_go ~source:logical planner in
  expect "the emitted Go differs from the golden" (stage1 = read go_path);
  expect "stage 1 and stage 2 differ for the planner"
    (Slice_planner.render_go ~source:logical planner = stage1);
  print_endline "CSF slice planner golden tests passed"
