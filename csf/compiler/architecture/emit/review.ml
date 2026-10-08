module Label = Emit__Label

let row cells = "| " ^ String.concat " | " (List.map Label.markdown_cell cells) ^ " |\n"
let table headings rows =
  row headings ^ "| " ^ String.concat " | " (List.map (fun _ -> "---") headings) ^ " |\n" ^
  String.concat "" (List.map row rows)

(* An evidence path stays a reference in the human view. Rendering it must not
   change pending work into a success claim just because a path was supplied. *)
let evidence = function
  | None -> "Pending; no evidence reference supplied"
  | Some reference -> "Reference only; not executed: " ^ reference

let verification_row (value : Model.component) =
  let status = match value.verification with
    | Model.Pending -> "Pending"
    | Model.Test_reference reference -> "Test reference only; not executed: " ^ reference in
  [value.component_id; status]

let order values = match values with
  | [] -> "None declared"
  | _ -> String.concat " -> "
      (List.map (fun (value : Model.resolved_component) -> value.component.component_id) values)

(* Even the empty-obligation case makes no execution or cleanup claim. *)
let render (resolved : Model.resolved) =
  let obligations = List.map (fun (value : Model.obligation) ->
    [value.subject; value.requirement; evidence value.evidence]) resolved.obligations in
  let obligations = match obligations with
    | [] -> [["None listed"; "No pending obligations in this resolved model"; "No execution claim"]]
    | values -> values in
  Codegen_header.render Codegen_header.Markdown ^ "\n" ^
  table ["Architecture"; "Version"; "Interpretation"] [[resolved.architecture.name;
    string_of_int resolved.architecture.version; "Declared architecture; not observed running state"]] ^ "\n" ^
  table ["Subject"; "Pending obligation"; "Evidence"] obligations ^ "\n" ^
  table ["Component"; "Verification declaration"]
    (List.map verification_row resolved.architecture.components) ^ "\n" ^
  table ["Lifecycle"; "Declarative order"; "Meaning"] [
    ["Start"; order resolved.start_order; "Declared ordering only; not execution"];
    ["Stop"; order resolved.stop_order; "Declared ordering only; not cleanup or shutdown evidence"];
  ]
