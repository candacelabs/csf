let () =
  let root = ref (Sys.getcwd ()) and native_only = ref false and report_only = ref false
  and quiet = ref false and summary_path = ref "" in
  let selected_lane = ref None and ci_lanes = ref false in
  let ontology_score = ref false and csfc = ref "" and generator = ref "" and format = ref "json" in
  let receipt = ref "" in
  let compare_base = ref "" and compare_head = ref "" and accept_regression = ref false in
  let options = [
    "--root", Arg.Set_string root, "PATH scan this checkout";
    "--native-only", Arg.Set native_only, "skip the three specialist tools";
    "--lane", Arg.String (fun value -> selected_lane := Some value), "NAME run one explicitly partial CI lane";
    "--ci-lanes", Arg.Set ci_lanes, "print the complete CI lane inventory as JSON";
    "--report-only", Arg.Set report_only, "findings do not fail; scanner errors still fail";
    "--quiet", Arg.Set quiet, "print counts instead of individual findings";
    "--summary", Arg.Set_string summary_path, "PATH write the Markdown report";
    "--ontology-score", Arg.Set ontology_score, "print one ontology alignment record instead of linting";
    "--csfc", Arg.Set_string csfc, "PATH csfc executable for --ontology-score";
    "--generator", Arg.Set_string generator, "PATH CSF language generator for --ontology-score";
    "--format", Arg.Symbol (["json"; "openmetrics"; "markdown"; "pr-spec"], (fun value -> format := value)),
      " --ontology-score: json or openmetrics; --ontology-compare: markdown (ratchet) or pr-spec";
    "--receipt", Arg.Set_string receipt,
      "DIR also write ontology-alignment.json and ontology-alignment.openmetrics there from the same run";
    "--ontology-compare", Arg.Tuple [Arg.Set_string compare_base; Arg.Set_string compare_head],
      "BASE HEAD compare two ontology alignment records; exits 1 on a regression";
    "--accept-regression", Arg.Set accept_regression,
      " with --ontology-compare: print the regression but exit 0 (the PR carries the acceptance label)";
  ] in
  Arg.parse options (fun argument -> raise (Arg.Bad ("unexpected argument: " ^ argument))) "house-lint [options]";
  try
    let root = Unix.realpath !root in
    let read path = Alignment.of_json (Yojson.Basic.from_file path) in
    if !compare_base <> "" then begin
      let base = read !compare_base and head = read !compare_head in
      if !format = "pr-spec" then print_endline (Yojson.Basic.pretty_to_string (Alignment.pr_spec ~base ~head))
      else begin
        let text, regressed = Alignment.markdown ~base ~head ~accepted:!accept_regression in
        print_string text;
        if regressed && not !accept_regression then exit 1
      end
    end
    else if !ontology_score then begin
      if !csfc = "" || !generator = "" then invalid_arg "--ontology-score requires --csfc and --generator";
      if not (List.mem !format ["json"; "openmetrics"]) then invalid_arg "--ontology-score prints json or openmetrics";
      let tools = { Score.csfc = Unix.realpath !csfc; generator = Unix.realpath !generator } in
      let receipt = if !receipt = "" then None else Some (Unix.realpath !receipt) in
      let record = Score.measure ~root tools in
      let json = Yojson.Basic.pretty_to_string (Alignment.to_json record) ^ "\n" in
      let openmetrics = Alignment.to_openmetrics record in
      Option.iter (fun directory ->
        Out_channel.with_open_text (Filename.concat directory "ontology-alignment.json") (fun channel ->
          output_string channel json);
        Out_channel.with_open_text (Filename.concat directory "ontology-alignment.openmetrics") (fun channel ->
          output_string channel openmetrics)) receipt;
      print_string (if !format = "openmetrics" then openmetrics else json)
    end
    else if !ci_lanes then
      print_endline (Yojson.Basic.to_string (`List (List.map (fun name -> `String name) (Runner.ci_lanes root))))
    else exit (Runner.run_selected ~selected_lane:!selected_lane ~root ~native_only:!native_only
      ~report_only:!report_only ~quiet:!quiet ~summary_path:!summary_path)
  with
  | Sys_error message | Invalid_argument message | Failure message | Yojson.Json_error message ->
      Printf.eprintf "house-lint: scanner error: %s\n" message; exit 2
  | Unix.Unix_error (error, operation, _) ->
      Printf.eprintf "house-lint: scanner error: %s: %s\n" operation (Unix.error_message error); exit 2
