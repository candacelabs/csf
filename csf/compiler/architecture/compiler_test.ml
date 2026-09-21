open Model

let fail errors = failwith (String.concat "; " (List.map (fun (error : diagnostic) -> error.code ^ ": " ^ error.message) errors))
let require = function Ok value -> value | Error errors -> fail errors
let expect label condition = if not condition then failwith label
let grammar = In_channel.with_open_bin "csf/compiler/architecture/language.ebnf" In_channel.input_all
let compile source = Frontend.parse_text ~grammar ~source ~filename:"test.csf"
  |> require |> Decode.architecture |> require |> Validate.resolve

let definition = {|architecture example version 1 {
 process host kind go entrypoint "main.go";
 scope application under host;
 library capability in host scope application source "capability.go" state existing lifecycle borrowed verification pending;
 scan "main.go";
 scan "capability.go";
}|}

let policy ?(entrypoint=false) ?(gateway=false) ?(test=false) ?(entrypoint_package=false) source =
  Go_policy.inspect ~entrypoint ~gateway ~test ~entrypoint_package "fixture.go" source

let policy_cases () =
  let denied source = expect "expected process ownership rejection"
    (List.exists (fun (error : diagnostic) -> error.code = "CSF_PROCESS_OWNER") (policy source)) in
  List.iter denied [
    "package main\nfunc main() {}";
    "package service\nimport network \"net\"\nfunc start() { network.Listen(\"tcp\", \"\") }";
    "package service\nimport \"net/http\"\nvar server = &http.Server{}";
    "package service\nimport h \"net/http\"\nvar server = new(h.Server)";
    "package service\nfunc start(s server) { s.ListenAndServe() }";
    "package service\nimport s \"os/signal\"\nvar notify = s.NotifyContext";
    "package service\nimport \"github.com/gin-gonic/gin\"\nvar router = gin.New()";
  ];
  let launch = "package service\nimport commands \"os/exec\"\nfunc launch() { commands.Command(\"git\").Run() }" in
  expect "aliased subprocess denied" (List.exists (fun (error : diagnostic) -> error.code = "CSF_PROCESS_BOUNDARY") (policy launch));
  expect "Go 1.26 value allocation admitted" (policy "package service\nvar pointer = new(int(3))" = []);
  expect "Go 1.26 new does not conceal subprocess API" (List.exists
    (fun (error : diagnostic) -> error.code = "CSF_PROCESS_BOUNDARY")
    (policy "package service\nimport \"os/exec\"\nvar pointer = new(exec.Command(\"git\"))"));
  expect "declared gateway admitted" (policy ~gateway:true launch = []);
  expect "test launches isolated from production rule" (policy ~test:true launch = []);
  expect "test fixture environment allowed" (policy ~test:true "package service\nimport \"os\"\nvar path = os.Getenv(\"PATH\")" = []);
  expect "comments and strings ignored" (policy "package service\n// net.Listen()\nvar text = `exec.Command(\"git\")`" = []);
  expect "entrypoint helper shares package" (policy ~entrypoint_package:true "package main\nfunc helper() {}" = []);
  expect "malformed Go fails closed" (policy "package service\nfunc !!!" <> []);
  expect "restricted dot import fails closed" (policy "package service\nimport . \"os/exec\"\nvar c = Command(\"x\")" <> []);
  expect "entrypoint owns listener" (policy ~entrypoint:true ~entrypoint_package:true "package main\nimport \"net\"\nfunc main() { net.Listen(\"tcp\", \"\") }" = [])

let with_fixture run =
  let root = Filename.temp_file "csf-source-" "" in
  Sys.remove root; Unix.mkdir root 0o700;
  let write name text = Out_channel.with_open_bin (Filename.concat root name) (fun output -> output_string output text) in
  let rec remove path =
    if (Unix.lstat path).st_kind = Unix.S_DIR then begin
      Sys.readdir path |> Array.iter (fun name -> remove (Filename.concat path name)); Unix.rmdir path
    end else Sys.remove path in
  Fun.protect ~finally:(fun () -> remove root)
    (fun () -> write "main.go" "package main\nfunc main() {}\n";
      write "capability.go" "package main\nfunc capability() {}\n";
      run root write)

let source_cases () =
  let resolved = compile definition |> require in
  expect "typed decode role" ((List.hd resolved.components).component.role = Library);
  with_fixture (fun root write ->
    expect "valid source fixture" (Source_check.check ~root resolved = []);
    write "capability.go" "package main\nimport \"os/exec\"\nvar c = exec.Command(\"git\")\n";
    expect "source gate consumes typed gateway policy" (Source_check.check ~root resolved <> []);
    Sys.remove (Filename.concat root "capability.go");
    expect "missing source rejected" (Source_check.check ~root resolved <> []);
    Unix.symlink "/dev/null" (Filename.concat root "capability.go");
    expect "escaping symlink rejected" (Source_check.check ~root resolved <> []));
  let bad = "architecture x version 1 { process h kind go entrypoint \"main.go\"; scope s under h; service c in h scope s source \"main.go\" state existing lifecycle borrowed verification pending; }" in
  expect "grammar acceptance is not semantic acceptance" (Result.is_error (compile bad))

let compiler_cases () = with_fixture (fun root write ->
  write "architecture.csf" definition;
  let config : Compiler.config = {
    grammar_path = "csf/compiler/architecture/language.ebnf";
    source_path = Filename.concat root "architecture.csf"; root;
    output_path = Filename.concat root "generated"; require_closed = false;
  } in
  ignore (Compiler.run Compiler.Check config |> require);
  expect "check has no output side effect" (not (Sys.file_exists config.output_path));
  let closed = {config with require_closed = true} in
  expect "unverified scope cannot emit closed architecture" (Result.is_error (Compiler.run Compiler.Emit closed));
  expect "failed compilation leaves outputs absent" (not (Sys.file_exists config.output_path));
  ignore (Compiler.run Compiler.Emit config |> require);
  ignore (Compiler.run Compiler.Check_generated config |> require);
  write "generated/review_cgen.md" "stale";
  expect "library detects artifact drift" (Result.is_error (Compiler.run Compiler.Check_generated config));
  ignore (Compiler.run Compiler.Emit config |> require);
  ignore (Compiler.run Compiler.Check_generated config |> require))

let inventory_cases () = with_fixture (fun root write ->
  let model = (compile definition |> require).architecture in
  let checked model = Source_check.check ~root (Validate.resolve model |> require) in
  let scan path = {path; path_at = model.at} in
  let host path = {(List.hd model.processes) with entrypoint = Some path} in
  let entrypoint_model path = {model with processes = [host path]; components = []; scan_roots = [scan path]} in
  write "README.md" "not Go source";
  expect "non-Go entrypoint cannot pass a zero-file inspection" (checked (entrypoint_model "README.md") <> []);
  write "main_test.go" "package main\nfunc main() {}\n";
  expect "test-only entrypoint is not an application" (checked (entrypoint_model "main_test.go") <> []);
  List.iter (fun source ->
    write "main.go" source;
    expect "entrypoint must declare main package and function" (checked (entrypoint_model "main.go") <> []))
    ["package library\nfunc main() {}\n"; "package main\nfunc helper() {}\n"];
  write "main.go" "package main\nfunc main() {}\n";
  Unix.mkdir (Filename.concat root "src") 0o700;
  List.iter (fun directory ->
    Unix.mkdir (Filename.concat root ("src/" ^ directory)) 0o700;
    let hidden = "src/" ^ directory ^ "/hidden.go" in
    write hidden "package hidden\nimport \"os/exec\"\nvar c = exec.Command(\"git\")\n";
    let component = {(List.hd model.components) with source = Some hidden} in
    expect "excluded component cannot count as inspected"
      (checked {model with components = [component]; scan_roots = [scan "main.go"; scan "src"]} <> []))
    [".git"; ".cache"; "node_modules"];
  Unix.mkdir (Filename.concat root "real") 0o700;
  write "real/main.go" "package main\nfunc main() {}\n";
  Unix.symlink "real" (Filename.concat root "alias");
  expect "intermediate directory symlink rejected" (checked (entrypoint_model "alias/main.go") <> []);
  let resolved = Validate.resolve model |> require in
  expect "missing repository returns diagnostics instead of throwing"
    (Source_check.check ~root:(Filename.concat root "missing") resolved <> []))

let () =
  policy_cases (); source_cases (); compiler_cases (); inventory_cases ();
  print_endline "CSF decoder, source consumer and compiler library tests passed"
