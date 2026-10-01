let expect message condition = if not condition then failwith message

let evaluate run arguments =
  let sink = Format.make_formatter (fun _ _ _ -> ()) (fun () -> ()) in
  Cmdliner.Cmd.eval_value ~catch:false ~help:sink ~err:sink ~env:(fun _ -> None)
    ~argv:(Array.of_list ("csfc" :: arguments)) (Cli.command_with run)

let capture arguments =
  let calls = ref [] in
  let run mode config =
    calls := (mode, config) :: !calls;
    Ok { Compiler.architecture_name = "example"; mode; obligations = 3 } in
  let result = evaluate run arguments in
  expect "successful command did not return zero" (result = Ok (`Ok 0));
  match !calls with [call] -> call | _ -> failwith "compiler must execute exactly once"

let test_defaults () =
  let mode, config = capture [] in
  expect "default mode is not check" (mode = Compiler.Check);
  expect "default configuration changed" (config = {
    Compiler.grammar_path = "csf/compiler/architecture/language.ebnf";
    source_path = "csf/architecture/architecture.csf";
    root = "."; output_path = "csf/architecture/generated";
    require_closed = false;
  })

let test_commands_and_options () =
  List.iter (fun mode ->
    let actual, config = capture [Compiler.mode_name mode;
      "--grammar"; "custom grammar.ebnf"; "--source"; "example.csf";
      "--root"; "checkout"; "--output"; "new projections"; "--require-closed"] in
    expect "subcommand selected wrong mode" (actual = mode);
    expect "explicit configuration lost" (config = {
      Compiler.grammar_path = "custom grammar.ebnf"; source_path = "example.csf";
      root = "checkout"; output_path = "new projections"; require_closed = true;
    })) [Compiler.Check; Compiler.Emit; Compiler.Check_generated];
  let mode, config = capture ["--source"; "default.csf"; "--require-closed"] in
  expect "options without a subcommand lost default mode"
    (mode = Compiler.Check && config.source_path = "default.csf" && config.require_closed)

let test_invalid_arguments () =
  List.iter (fun arguments ->
    let called = ref false in
    let run _ _ = called := true; failwith "invalid arguments reached compiler" in
    expect ("invalid arguments were not rejected by Cmdliner: " ^ String.concat " " arguments)
      (match evaluate run arguments with Error (`Parse | `Term) -> true | _ -> false);
    expect "compiler ran for invalid arguments" (not !called)) [
      ["unknown"]; ["emit"; "check"]; ["check"; "check"];
      ["check-generated"; "extra"]; ["--unknown"]; ["emit"; "--source"];
      ["check"; "--require-closed"; "--require-closed"];
    ]

let test_help () =
  List.iter (fun arguments ->
    let run _ _ = failwith "help reached compiler" in
    expect "help was not handled without execution" (evaluate run arguments = Ok `Help)) [
      ["--help=plain"]; ["check"; "--help=plain"];
      ["emit"; "--help=plain"]; ["check-generated"; "--help=plain"];
    ]

let test_reporting () =
  let error = { Model.at = { file = "example.csf"; line = 4; column = 9 };
    code = "CSF_MODEL"; message = "invalid declaration" } in
  expect "diagnostic format changed"
    (Cli.diagnostic error = "example.csf:4:9: CSF_MODEL: invalid declaration");
  expect "failure did not return one"
    (evaluate (fun _ _ -> Error [error]) ["check"] = Ok (`Ok 1));
  expect "summary format changed"
    (Cli.summary { Compiler.architecture_name = "example"; mode = Compiler.Emit; obligations = 3 }
     = "architecture=example mode=emit declarations=checked source=checked obligations=3")

let () =
  test_defaults ();
  test_commands_and_options ();
  test_invalid_arguments ();
  test_help ();
  test_reporting ();
  print_endline "CSF declarative CLI tests passed"
