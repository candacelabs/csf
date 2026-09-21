(* This example exercises the real command-line interface (CLI) boundary and the compiled projection.
   It preserves its inputs, subprocess output, artifacts and verdicts. *)
type stage = {
  name : string;
  argv : string list;
  expected : string;
  observed : string;
  diagnostics : string;
  stdout : string option;
  stderr : string option;
  passed : bool;
}

type context = {
  mutable root : string;
  compiler : string;
  output : string;
  mutable stages : stage list;
  mutable artifacts : string list;
  mutable outcome : string;
}

let read path = In_channel.with_open_bin path In_channel.input_all
let write path text = Out_channel.with_open_bin path (fun channel -> output_string channel text)
let absolute path = if Filename.is_relative path then Filename.concat (Sys.getcwd ()) path else path
let portable context path =
  let prefix = context.root ^ "/" in
  if path = context.root then "."
  else if String.starts_with ~prefix path then String.sub path (String.length prefix) (String.length path - String.length prefix)
  else path

let contains text fragment =
  let rec search index =
    index + String.length fragment <= String.length text &&
    (String.sub text index (String.length fragment) = fragment || search (index + 1)) in
  search 0

let markdown text =
  let output = Buffer.create (String.length text) in
  String.iter (function
    | '&' -> Buffer.add_string output "&amp;"
    | '<' -> Buffer.add_string output "&lt;"
    | '>' -> Buffer.add_string output "&gt;"
    | '|' -> Buffer.add_string output "&#124;"
    | '`' -> Buffer.add_string output "&#96;"
    | '\n' -> Buffer.add_string output "<br/>"
    | character -> Buffer.add_char output character) text;
  Buffer.contents output

let link = function None -> "—" | Some path -> "[" ^ path ^ "](" ^ path ^ ")"
let argv_text argv = "[" ^ String.concat ", " (List.map (Printf.sprintf "%S") argv) ^ "]"
let receipt_path context = Filename.concat context.output "receipt_cgen.md"

let receipt context =
  let output = Buffer.create 4096 in
  Buffer.add_string output (Codegen_header.render Codegen_header.Markdown ^ "\n");
  Printf.bprintf output "| Result | %s |\n|---|---|\n| Repository | %s |\n| Compiler | %s |\n\n"
    (markdown context.outcome) (markdown (portable context context.root)) (markdown (portable context context.compiler));
  Buffer.add_string output "| Input | Retained copy |\n|---|---|\n";
  List.iter (fun path -> if Sys.file_exists (Filename.concat context.output path) then
    Printf.bprintf output "| %s | %s |\n" (markdown (Filename.basename path)) (link (Some path)))
    ["inputs/architecture.csf"; "inputs/language.ebnf"; "inputs/lifecycle.csf"; "inputs/gateway.csf";
     "inputs/corrupted-review.md"];
  Buffer.add_string output "\n| Stage | Expected | Observed | Verdict | stdout | stderr |\n|---|---|---|---|---|---|\n";
  List.rev context.stages |> List.iter (fun stage ->
    Printf.bprintf output "| %s | %s | %s | %s | %s | %s |\n"
      (markdown stage.name) (markdown stage.expected) (markdown stage.observed)
      (if stage.passed then "PASS" else "FAIL") (link stage.stdout) (link stage.stderr));
  Buffer.add_string output "\n| Stage | Actual argv or consumer | Observed diagnostics |\n|---|---|---|\n";
  List.rev context.stages |> List.iter (fun stage ->
    let invocation = if stage.argv = [] then
        "Typed_projection.architecture -> Validate.resolve -> Compiler.projections; byte comparison"
      else argv_text stage.argv in
    Printf.bprintf output "| %s | %s | %s |\n" (markdown stage.name) (markdown invocation)
      (markdown (if stage.diagnostics = "" then "None" else stage.diagnostics)));
  Buffer.add_string output "\n| Artifact | Retained file |\n|---|---|\n";
  List.iter (fun name -> Printf.bprintf output "| %s | %s |\n" (markdown name)
    (link (Some ("artifacts/" ^ name)))) context.artifacts;
  Buffer.add_string output "\nThe OCaml consumer links the compiled architecture projection. Fresh command-line interface (CLI) artifacts must match its projections byte for byte. The declaration retains unresolved obligations; this example does not start the Go application or demonstrate service cleanup. Repository sources are read from the named checkout, not copied into this receipt.\n";
  Buffer.contents output

let save_receipt context =
  let temporary = Filename.concat context.output ".receipt.tmp" in
  write temporary (receipt context);
  Sys.rename temporary (receipt_path context)

let record context stage =
  context.stages <- stage :: context.stages;
  save_receipt context;
  if not stage.passed then failwith ("unexpected result in " ^ stage.name)

let with_descriptor path flags action =
  let descriptor = Unix.openfile path flags 0o600 in
  Fun.protect ~finally:(fun () -> Unix.close descriptor) (fun () -> action descriptor)

let rec wait child =
  try snd (Unix.waitpid [] child)
  with Unix.Unix_error (Unix.EINTR, _, _) -> wait child

let execute context argv stdout stderr =
  with_descriptor "/dev/null" [Unix.O_RDONLY; Unix.O_CLOEXEC] (fun input ->
    with_descriptor stdout [Unix.O_WRONLY; Unix.O_CREAT; Unix.O_EXCL; Unix.O_CLOEXEC] (fun output ->
      with_descriptor stderr [Unix.O_WRONLY; Unix.O_CREAT; Unix.O_EXCL; Unix.O_CLOEXEC] (fun errors ->
        let previous = Sys.getcwd () in
        Unix.chdir context.root;
        Fun.protect ~finally:(fun () -> Unix.chdir previous) (fun () ->
          wait (Unix.create_process context.compiler (Array.of_list argv) input output errors)))))

let status = function
  | Unix.WEXITED code -> "exit " ^ string_of_int code
  | Unix.WSIGNALED signal -> "signal " ^ string_of_int signal
  | Unix.WSTOPPED signal -> "stopped by signal " ^ string_of_int signal

(* A negative test must fail for its intended reason: an unreadable source or
   missing executable must not masquerade as a lifecycle rejection. Successful
   runs also check the CLI summary and require empty stderr. *)
let command context resolved ~name ~mode ~source ?(closed = false) ?code ?count expected_exit =
  let argv = [portable context context.compiler; Compiler.mode_name mode;
    "--root"; "."; "--grammar"; Cli.default_config.grammar_path;
    "--source"; portable context source; "--output"; portable context (Filename.concat context.output "artifacts")] @
    if closed then ["--require-closed"] else [] in
  let prefix = Printf.sprintf "logs/%02d-%s" (List.length context.stages + 1) name in
  let stdout = prefix ^ ".stdout.log" and stderr = prefix ^ ".stderr.log" in
  let expected = "exit " ^ string_of_int expected_exit ^ "; " ^
    (match code with None -> "no diagnostics" | Some code -> code) ^
    (match count with None -> "" | Some count -> Printf.sprintf " (%d diagnostics)" count) in
  let stage = try
    let result = execute context argv (Filename.concat context.output stdout) (Filename.concat context.output stderr) in
    let output = String.trim (read (Filename.concat context.output stdout)) in
    let errors = String.trim (read (Filename.concat context.output stderr)) in
    let lines = String.split_on_char '\n' errors |> List.filter (fun line -> String.trim line <> "") in
    let diagnostics_match = match code with
      | None -> lines = []
      | Some code -> lines <> [] && List.for_all (fun line -> contains line (": " ^ code ^ ": ")) lines in
    let expected_output = if expected_exit <> 0 then "" else Cli.summary {
      Compiler.architecture_name = resolved.Model.architecture.name; mode;
      obligations = List.length resolved.obligations } in
    { name; argv; expected; observed = status result; diagnostics = errors;
      stdout = Some stdout; stderr = Some stderr;
      passed = result = Unix.WEXITED expected_exit && output = expected_output && diagnostics_match &&
        (match count with None -> true | Some count -> List.length lines = count) }
  with error ->
    let existing path = if Sys.file_exists (Filename.concat context.output path) then Some path else None in
    { name; argv; expected; observed = "execution failed: " ^ Printexc.to_string error; diagnostics = "";
      stdout = existing stdout; stderr = existing stderr; passed = false } in
  record context stage

(* The required formats are an independent consumer contract. Deriving that
   set solely from the producer would let deleted outputs pass unnoticed.
   Contents still use the real emitter: this checks CLI/model agreement, not
   an independent proof of the emitter's correctness. *)
let compare_artifacts context resolved name =
  let artifacts = Compiler.projections resolved in
  context.artifacts <- List.map fst artifacts;
  let formats = List.map (fun (name, _) -> Filename.extension name) artifacts |> List.sort String.compare in
  let required_formats = [".md"; ".ml"; ".mmd"] in
  let matches = List.map (fun (name, expected) ->
    let path = Filename.concat (Filename.concat context.output "artifacts") name in
    name, Sys.file_exists path && read path = expected) artifacts in
  record context { name; argv = []; expected = "OCaml, Mermaid and Markdown artifacts equal compiled-model projections";
    observed = "formats: " ^ String.concat ", " formats ^ "; " ^ String.concat "; " (List.map (fun (name, equal) ->
      name ^ (if equal then ": equal" else ": DIFFERENT")) matches);
    diagnostics = ""; stdout = None; stderr = None;
    passed = formats = required_formats && List.for_all snd matches }

let replace_once text before after =
  let size = String.length before in
  let rec positions index found =
    if index + size > String.length text then List.rev found
    else if String.sub text index size = before then positions (index + size) (index :: found)
    else positions (index + 1) found in
  match positions 0 [] with
  | [index] -> String.sub text 0 index ^ after ^
      String.sub text (index + size) (String.length text - index - size)
  | _ -> failwith ("negative example requires exactly one occurrence of " ^ before)

(* Negative inputs are edits of the real declaration, using the compiled
   model's source location to target exactly one component. If that source
   stops fitting the edit, fail instead of silently testing unchanged input. *)
let change_component_line source (architecture : Model.architecture) id change =
  let component = match List.find_opt (fun (component : Model.component) -> component.component_id = id)
      architecture.components with
    | Some component -> component | None -> failwith ("example component not found: " ^ id) in
  let lines = String.split_on_char '\n' source in
  let index = component.component_at.line - 1 in
  if index < 0 || index >= List.length lines then failwith ("invalid example source location: " ^ id);
  List.mapi (fun current line -> if current = index then change component line else line) lines
  |> String.concat "\n"

let negative_inputs context source architecture =
  let lifecycle = change_component_line source architecture "csf_services" (fun _ line ->
    replace_once line "lifecycle scoped" "lifecycle borrowed") in
  let host = List.find (fun (process : Model.process) -> process.kind = Model.Go) architecture.Model.processes in
  let gateway = change_component_line source architecture "terminals" (fun component line ->
    match component.Model.source, host.entrypoint with
    | Some previous, Some replacement -> replace_once line
        ("source " ^ Printf.sprintf "%S" previous) ("source " ^ Printf.sprintf "%S" replacement)
    | _ -> failwith "gateway example requires existing source and host entrypoint") in
  write (Filename.concat context.output "inputs/lifecycle.csf") lifecycle;
  write (Filename.concat context.output "inputs/gateway.csf") gateway

let scenario context =
  let defaults = Cli.default_config in
  let source = read (Filename.concat context.root defaults.source_path) in
  write (Filename.concat context.output "inputs/architecture.csf") source;
  write (Filename.concat context.output "inputs/language.ebnf")
    (read (Filename.concat context.root defaults.grammar_path));
  let resolved = match Validate.resolve Typed_projection.architecture with
    | Ok resolved -> resolved
    | Error errors -> failwith (String.concat "; " (List.map Cli.diagnostic errors)) in
  let source = defaults.source_path in
  command context resolved ~name:"check" ~mode:Compiler.Check ~source 0;
  command context resolved ~name:"emit" ~mode:Compiler.Emit ~source 0;
  command context resolved ~name:"check-generated" ~mode:Compiler.Check_generated ~source 0;
  compare_artifacts context resolved "compiled-projection";
  command context resolved ~name:"strict-obligations" ~mode:Compiler.Check ~source ~closed:true
    ~code:"CSF_OBLIGATION" ~count:(List.length resolved.obligations) 1;
  negative_inputs context (read (Filename.concat context.output "inputs/architecture.csf")) resolved.architecture;
  command context resolved ~name:"lifecycle-rejection" ~mode:Compiler.Check
    ~source:(Filename.concat context.output "inputs/lifecycle.csf") ~code:"lifecycle" 1;
  command context resolved ~name:"gateway-rejection" ~mode:Compiler.Check
    ~source:(Filename.concat context.output "inputs/gateway.csf") ~code:"CSF_PROCESS_BOUNDARY" 1;
  let review = match List.filter (fun name -> Filename.extension name = ".md") context.artifacts with
    | [name] -> Filename.concat (Filename.concat context.output "artifacts") name
    | _ -> failwith "expected exactly one Markdown review projection" in
  write review (read review ^ "\nDeliberate acceptance-example drift.\n");
  write (Filename.concat context.output "inputs/corrupted-review.md") (read review);
  command context resolved ~name:"drift-rejection" ~mode:Compiler.Check_generated ~source ~code:"CSF_GENERATED_DRIFT" 1;
  command context resolved ~name:"repair" ~mode:Compiler.Emit ~source 0;
  command context resolved ~name:"check-repaired" ~mode:Compiler.Check_generated ~source 0;
  compare_artifacts context resolved "compiled-projection-repaired"

let run ~root ~compiler ~output =
  try
    let context = { root = absolute root; compiler = absolute compiler; output = absolute output;
      stages = []; artifacts = []; outcome = "RUNNING" } in
    Unix.mkdir context.output 0o700;
    let result = try
      save_receipt context;
      context.root <- Unix.realpath context.root;
      Unix.mkdir (Filename.concat context.output "logs") 0o700;
      Unix.mkdir (Filename.concat context.output "inputs") 0o700;
      scenario context;
      context.outcome <- "PASS"; save_receipt context; 0
    with error ->
      context.outcome <- "FAIL: " ^ Printexc.to_string error;
      (try save_receipt context with receipt_error ->
        Printf.eprintf "Could not update receipt: %s\n%!" (Printexc.to_string receipt_error));
      1 in
    Printf.printf "%s\nReceipt: %s\n%!" context.outcome (receipt_path context);
    result
  with error ->
    Printf.eprintf "Could not create fresh example output: %s\n%!" (Printexc.to_string error);
    1
