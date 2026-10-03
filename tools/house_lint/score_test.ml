let expect message condition = if not condition then failwith message
let finding rule : Source.finding = {path="x/x.go"; line=1; rule; message="fixture"}
let measured = function Alignment.Measured count -> Some count | Alignment.Not_measured _ -> None

let () =
  let findings = [finding "CS-15"; finding "CS-15"; finding "CS-16"; finding "CS-13"] in
  expect "a registered rule counts only its own findings" (measured (Score.house_rule findings "CS-15") = Some 2);
  expect "a registered rule with no findings is a measured zero"
    (measured (Score.house_rule findings "ONTOLOGY-DIRS") = Some 0);
  (* A rule house lint does not register yet is pending, never zero. *)
  (match Score.house_rule findings "CS-UNREGISTERED" with
   | Alignment.Not_measured reason -> expect "pending rule names a TODO" (String.starts_with ~prefix:"TODO" reason)
   | Alignment.Measured _ -> failwith "an unregistered rule was reported as measured");
  expect "measured drift adds" (measured (Score.combine_drift (Measured 1) (Measured 2)) = Some 3);
  expect "unknown drift is not a measured zero" (measured (Score.combine_drift (Measured 0) (Not_measured "x")) = None);
  expect "both unknown keeps both reasons"
    (Score.combine_drift (Not_measured "a") (Not_measured "b") = Not_measured "a; b");
  expect "READMEs are the tracked README.md files"
    (Score.readmes ["README.md"; "pkg/x/README.md"; "pkg/x/notes.md"; "README.md.bak"]
     = ["README.md"; "pkg/x/README.md"]);
  print_endline "Ontology score procedure tests passed"
