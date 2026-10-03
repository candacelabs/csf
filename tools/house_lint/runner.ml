open Source

let go_image = "golang:1.26.5-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599"
let dupl_version = "github.com/golangci/dupl@v0.0.0-20260401084720-c99c5cf5c202"
let funlen_image = "golangci/golangci-lint:v2.13.2"

let process argv output =
  let descriptor = Unix.openfile output [Unix.O_WRONLY; Unix.O_CREAT; Unix.O_TRUNC] 0o600 in
  Fun.protect ~finally:(fun () -> Unix.close descriptor) (fun () ->
    let pid = Unix.create_process argv.(0) argv Unix.stdin descriptor descriptor in
    match snd (Unix.waitpid [] pid) with Unix.WEXITED code -> code | _ -> 2)

let git_inventory root other =
  let output = Filename.temp_file "house-lint-inventory-" "" in
  Fun.protect ~finally:(fun () -> Sys.remove output) (fun () ->
    let arguments = ["git"; "-C"; root; "ls-files"; "-z"; "--cached"] @
      (if other then ["--others"; "--exclude-standard"] else []) in
    if process (Array.of_list arguments) output <> 0 then failwith "git inventory failed";
    Checker.inventory (Checker.read_file output))

let git_path root option =
  let output = Filename.temp_file "house-lint-git-" "" in
  Fun.protect ~finally:(fun () -> Sys.remove output) (fun () ->
    if process [|"git"; "-C"; root; "rev-parse"; "--path-format=absolute"; option|] output <> 0 then
      failwith "could not resolve Git metadata for the containerized reuse check";
    String.trim (Checker.read_file output))

let read_sources root paths =
  let files = ref [] and errors = ref [] in
  paths |> List.filter (fun path -> Filename.check_suffix path ".go" && not (List.mem "vendor" (String.split_on_char '/' path)))
  |> List.iter (fun path ->
    try
      let prior_errors = List.length !errors in
      let file = parse path (Checker.read_file (Checker.checked_path root path)) in
      (* Frozen research is only read by the import-specific dependency checker
         and the repo-wide test import convention. Generated function bodies
         do not belong to the handwritten style corpus. *)
      if selected file && Node.has_error file.root then begin
        let prior = List.length !errors in
        walk (fun node -> if Node.is_error node || Node.is_missing node then
          errors := issue file node "PARSE" "Go grammar could not parse this syntax; scan is incomplete" :: !errors) file.root;
        if List.length !errors = prior then
          errors := issue file file.root "PARSE" "Go grammar found missing syntax; scan is incomplete" :: !errors
      end;
      (* The grammar recognizes escape syntax, not Go's decoded string values.
         Reject malformed values at the file boundary before any rule decodes
         one, so other files and specialist lanes can still be reported. *)
      walk (fun node ->
        if List.mem (kind node) ["interpreted_string_literal"; "raw_string_literal"] then
          ignore (Checker.string_value (text file node))) file.root;
      if List.length !errors = prior_errors then files := file :: !files
    with
    | Sys_error message | Invalid_argument message | Failure message ->
        errors := {path; line=1; rule="SCAN"; message} :: !errors
    | Unix.Unix_error (error, operation, _) ->
        errors := {path; line=1; rule="SCAN"; message=operation ^ ": " ^ Unix.error_message error} :: !errors);
  List.rev !files, List.rev !errors

let native root tracked inventory =
  let files, errors = read_sources root tracked in
  let corpus = List.filter selected files in
  let python_count, python_findings, python_errors = Python_magic.check root tracked in
  let count = List.length corpus + python_count in
  let errors = errors @ python_errors in
  let errors = if count = 0 then
    {path="inventory"; line=1; rule="SCAN"; message="empty handwritten source corpus; refusing a vacuous pass"} :: errors else errors in
  let dependency = Checker.check root inventory in
  let convert rule (finding : Checker.issue) =
    {path=finding.path; line=finding.line; rule; message=finding.message} in
  let findings = Mandatory.collect files @ Advisory.collect files @ Structure.collect files @ Shared_state.collect files @
    Placement.collect files @ Placement.check_directories root tracked @
    Test_layout.collect ~modules:(Test_layout.modules root tracked) files @
    python_findings @ Structure.artifacts files tracked @ List.map (convert "DEPENDENCIES") dependency.findings in
  let errors = errors @ List.map (convert "SCAN") dependency.errors in
  let findings = List.sort_uniq (fun a b -> compare (a.path,a.line,a.rule,a.message) (b.path,b.line,b.rule,b.message)) findings in
  count, findings, errors

type lane = { rule : string; name : string; argv : string array }

let docker_go root cache script = Array.of_list [
  "docker"; "run"; "--rm"; "--user"; Printf.sprintf "%d:%d" (Unix.getuid ()) (Unix.getgid ());
  "--env"; "HOME=/cache"; "--env"; "GOCACHE=/cache/gocache";
  "--env"; "GOMODCACHE=/cache/gomod"; "--env"; "GOBIN=/cache/bin";
  "--env"; "GOTOOLCHAIN=local"; "--env"; "GOFLAGS=-mod=readonly";
  "--env"; "GIT_WORK_TREE=/src"; "--env"; "GIT_DIR=" ^ git_path root "--absolute-git-dir";
  "--volume"; (let common = git_path root "--git-common-dir" in common ^ ":" ^ common ^ ":ro");
  "--volume"; root ^ ":/src:ro"; "--volume"; cache ^ ":/cache";
  "--workdir"; "/src"; go_image; "bash"; "-euc"; script]

let lanes root cache = [
  {rule="CS-3"; name="reuse"; argv=docker_go root cache
    ("go install " ^ dupl_version ^ " || exit 2; export PATH=/cache/bin:$PATH; bash tools/check-go-reuse.sh")};
  {rule="INTERFACE-RETURNS"; name="interface-returns";
   argv=[|"bash"; Filename.concat root "tools/check-ifacereturn.sh"; "--strict"|]};
  {rule="FUNCTION-LENGTH"; name="function-length";
   argv=[|"docker"; "run"; "--rm"; "-v"; root ^ ":/workspace:ro"; "-w"; "/workspace";
     funlen_image; "golangci-lint"; "run"; "--config"; "/workspace/.golangci-funlen.yml";
     "--issues-exit-code=1"; "./..."|]};
]

let ci_lanes root = "native" :: List.map (fun lane -> lane.name) (lanes root "/unused")

let select_lanes selected available = match selected with
  | None -> available
  | Some "native" -> []
  | Some name ->
      match List.filter (fun lane -> lane.name = name) available with
      | [lane] -> [lane]
      | _ -> invalid_arg ("unknown or ambiguous house lint lane: " ^ name)

let render channel finding = Printf.fprintf channel "%s:%d: %s: %s\n"
  finding.path finding.line finding.rule finding.message

let summary ?selected_lane channel ~files ~findings ~errors ~external_results ~native_only ~report_only =
  output_string channel "# House lint\n\n";
  (match selected_lane with None -> () | Some lane ->
    Printf.fprintf channel "Partial check: `%s` lane. Other lanes require separate results.\n\n" lane);
  let native_ran = selected_lane = None || selected_lane = Some "native" in
  if native_ran then
    Printf.fprintf channel "%d handwritten source files (Go and Python); %d native findings; %d scan errors.\n\n" files (List.length findings) (List.length errors)
  else output_string channel "Native source scan was not selected.\n\n";
  if report_only then output_string channel "Findings are report-only for this invocation; scan errors still fail.\n\n";
  output_string channel "| Rule | Policy | Native findings / specialist result | Coverage |\n|---|---|---|---|\n";
  List.iter (fun (rule : Policy.rule) ->
    let native_count = List.length (List.filter (fun (finding : finding) -> finding.rule = rule.id) findings) in
    let external_codes = List.filter (fun (id, _, _) -> id = rule.id) external_results in
    let result = if List.mem rule.id ["CS-3"; "INTERFACE-RETURNS"; "FUNCTION-LENGTH"] then
      if native_only then "not run (--native-only)" else
      if external_codes = [] then "not run (different lane)" else
      String.concat "; " (List.map (fun (_, name, code) -> name ^ ": " ^
        (if code = 0 then "clean" else if code = 1 then "findings" else "SCAN ERROR")) external_codes)
      else if native_ran then string_of_int native_count else "not run (different lane)" in
    Printf.fprintf channel "| %s | %s | %s | %s |\n" rule.id (Policy.describe rule) result rule.description
  ) Policy.rules;
  output_string channel "\nCS-4 and CS-10 report syntax-based review candidates. Generator reproducibility remains checked by the owning component; architectural intent still requires review.\n";
  if errors <> [] then begin output_string channel "\n## Scan errors\n\n```text\n";
    List.iter (render channel) errors; output_string channel "```\n" end

let run_selected ~selected_lane ~root ~native_only ~report_only ~quiet ~summary_path =
  Unix.chdir root;
  if native_only && selected_lane <> None then invalid_arg "--native-only and --lane cannot be combined";
  let cache = Filename.concat (Filename.get_temp_dir_name ()) "candace-house-lint-tools" in
  let selected = if native_only then [] else select_lanes selected_lane (lanes root cache) in
  let files, findings, errors = if selected_lane = None || selected_lane = Some "native" then
    let tracked = git_inventory root false in
    let inventory = git_inventory root true in
    native root tracked inventory
    else 0, [], [] in
  let output_directory = Filename.concat root "house-lint-output" in
  if not (Sys.file_exists output_directory) then Unix.mkdir output_directory 0o700;
  Out_channel.with_open_text (Filename.concat output_directory "native.log") (fun channel ->
    List.iter (render channel) findings; List.iter (render channel) errors);
  if not quiet then List.iter (render stdout) findings;
  List.iter (render stderr) errors;
  let status = ref (Policy.status ~report_only findings errors) in
  let external_results = if selected = [] then [] else begin
    if not (Sys.file_exists cache) then Unix.mkdir cache 0o700;
    selected |> List.map (fun lane ->
      let log = Filename.concat output_directory (lane.name ^ ".log") in
      Printf.printf "house-lint: running %s (log: %s)\n%!" lane.name log;
      let code = try process lane.argv log with Unix.Unix_error (error, _, _) ->
        Printf.eprintf "%s: %s\n" lane.name (Unix.error_message error); 2 in
      if not quiet || code > 1 then print_string (Checker.read_file log);
      status := max !status (Policy.external_status ~report_only (Policy.find lane.rule).severity code);
      lane.rule, lane.name, code)
  end in
  summary ?selected_lane stdout ~files ~findings ~errors ~external_results ~native_only ~report_only;
  if summary_path <> "" then Out_channel.with_open_text summary_path (fun channel ->
    summary ?selected_lane channel ~files ~findings ~errors ~external_results ~native_only ~report_only);
  !status

let run ~root ~native_only ~report_only ~quiet ~summary_path =
  run_selected ~selected_lane:None ~root ~native_only ~report_only ~quiet ~summary_path
