(* The golden test: the shell generator's projections must equal the checked-in
   golden files byte for byte, and its three chief-invariant refusals must fire
   on a declaration that violates them. The declaration, the emitted tree and
   the same bytes the operator reviewed are pinned here, so a change to the
   emitter or the declaration that changes the output fails. *)

let expect message condition = if not condition then failwith message

let read path = In_channel.with_open_bin path In_channel.input_all

let accept = function
  | Ok value -> value
  | Error errors -> failwith (String.concat "; "
      (List.map (fun (error : Model.diagnostic) -> error.message) errors))

let codes diagnostics =
  List.map (fun (diagnostic : Model.diagnostic) -> diagnostic.code) diagnostics

(* The first line at which two texts differ, with both lines, so a golden
   mismatch names the exact byte the emitter got wrong. *)
let first_difference emitted golden =
  let rec go line left right = match left, right with
    | actual :: left, expected :: right ->
        if actual = expected then go (line + 1) left right
        else Some (line, actual, expected)
    | actual :: _, [] -> Some (line, actual, "<eof>")
    | [], expected :: _ -> Some (line, "<eof>", expected)
    | [], [] -> None in
  go 1 (String.split_on_char '\n' emitted) (String.split_on_char '\n' golden)

(* The declaration's logical name, as the generated header records it. The
   golden files are read from the paths the build hands in; the name is part of
   the declaration, not of where the file happens to sit on disk. *)
let logical = "csf/compiler/testdata/golden/service/blah/blah.csf"
let email_logical = "csf/compiler/testdata/golden/service/email/email.csf"

(* Compare every generated file with its checked-in golden, naming the exact
   line and both sides on the first difference. *)
let compare_projection (relative, contents) golden_root =
  let golden = read (Filename.concat golden_root relative) in
  match first_difference contents golden with
  | None -> ()
  | Some (line, actual, expected) ->
      failwith (Printf.sprintf "%s differs at line %d:\n  emitted: %S\n  golden:  %S"
        relative line actual expected)

let compare_projections golden_root projections =
  List.iter (fun projection -> compare_projection projection golden_root) projections

let options count =
  String.concat "\n" (List.init count (fun index ->
    Printf.sprintf "option o%d { question q; preferred \"a\"; }" index))

let question = "question q { region q; text \"which primitive?\"; }"

let () =
  let argument index default =
    if Array.length Sys.argv > index then Sys.argv.(index) else default in
  let grammar_path = argument 1 "csf/compiler/architecture/shell.ebnf" in
  let source_path = argument 2 "csf/compiler/testdata/golden/service/blah/blah.csf" in
  (* The golden tree sits beside the declaration and mirrors the emitted paths,
     so the declaration's own directory is the root the projections are read
     against. Bazel hands in the runfiles path of the one file, not of a tree. *)
  let golden_root = Filename.dirname source_path in
  let grammar = read grammar_path in
  let source = read source_path in
  let declaration = accept (Shell.parse ~grammar ~source ~filename:logical) in
  expect "the shell declaration is invalid" (Shell.check declaration = []);
  let projections = Shell.projections ~source:logical declaration in
  expect "the fresh-service template emits six generated files"
    (List.length projections = 6);
  expect "a fresh service has no holes" (Shell.holes ~source:logical declaration = None);
  compare_projections golden_root projections;

  (* email's capability files are emitted from the declared facts alone: the
     contract, its one method, the import, the missing-boundary error and the
     option that binds it. Each file is a projection of its `capability` block,
     so the same generator that renders the fresh template renders these. *)
  let email_path = argument 3 "csf/compiler/testdata/golden/service/email/email.csf" in
  let email_root = Filename.dirname email_path in
  let email_source = read email_path in
  let email = accept (Shell.parse ~grammar ~source:email_source ~filename:email_logical) in
  expect "the email declaration is invalid" (Shell.check email = []);
  let capabilities = Shell.capability_projections ~source:email_logical email in
  expect "email declares three capability files"
    (List.length capabilities = 3);
  compare_projections email_root capabilities;

  (* The capability test files are emitted from the same declared facts: the
     suite plus one gomock double per capability whose misuse spec builds the
     service with every other boundary present and expects this one's refusal.
     The method signature is parsed into the double's name, parameters and
     returns, so the double mirrors the interface and never restates it. *)
  let capability_tests = Shell.service_test_projections ~source:email_logical email in
  expect "email declares the suite and three capability test files"
    (List.length capability_tests = 4);
  compare_projections email_root capability_tests;

  let refuses label text expected =
    let declaration = accept (Shell.parse ~grammar ~source:text ~filename:label) in
    let found = codes (Shell.check declaration) in
    expect (Printf.sprintf "the %s declaration should be refused for %s, found [%s]"
              label expected (String.concat ", " found))
      (List.mem expected found) in
  refuses "fanout" (question ^ "\n" ^ options 17) "fanout_over_16";
  refuses "unprojected" question "unprojected_question";
  refuses "leaf" (question ^ "\noption o { question q; preferred \"\"; }")
    "leaf_without_one_template";
  print_endline "CSF shell golden tests passed"
