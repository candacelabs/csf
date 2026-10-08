module Label = Emit__Label

(* Subgraphs mean declared lifetimes, not package layout, core affinity or live
   processes. Dashed arrows show consumer-to-provider dependencies; solid
   arrows show connections whose declared transport and state stay in labels. *)
let render (resolved : Model.resolved) =
  let architecture = resolved.architecture in
  let process_ids = Label.numbered "p" architecture.processes (fun (p : Model.process) -> p.process_id) in
  let scope_ids = Label.numbered "s" resolved.scopes (fun (s : Model.resolved_scope) -> s.scope.scope_id) in
  let component_ids = Label.numbered "c" resolved.components
    (fun (c : Model.resolved_component) -> c.component.component_id) in
  let output = Buffer.create 2048 in
  let line depth value = Buffer.add_string output (String.make (2 * depth) ' ' ^ value ^ "\n") in
  let node depth (value : Model.resolved_component) =
    line depth (List.assoc value.component.component_id component_ids ^
      "[" ^ Label.diagram (Label.component value.component) ^ "]") in
  let rec scopes depth parent =
    resolved.scopes |> List.iter (fun (value : Model.resolved_scope) ->
      if value.scope.parent = parent then begin
        line depth ("subgraph " ^ List.assoc value.scope.scope_id scope_ids ^
          "[" ^ Label.diagram ("scope " ^ value.scope.scope_id) ^ "]");
        resolved.components |> List.iter (fun (component : Model.resolved_component) ->
          if component.lifetime.scope.scope_id = value.scope.scope_id then node (depth + 1) component);
        scopes (depth + 1) value.scope.scope_id;
        line depth "end"
      end) in
  Buffer.add_string output (Codegen_header.render Codegen_header.Mermaid);
  line 0 "flowchart TB";
  line 1 "%% Declared architecture, not observed running state.";
  architecture.processes |> List.iter (fun (value : Model.process) ->
    line 1 ("subgraph " ^ List.assoc value.process_id process_ids ^
      "[" ^ Label.diagram (Label.process value) ^ "]");
    scopes 2 value.process_id;
    line 1 "end");
  architecture.dependencies |> List.iter (fun (value : Model.dependency) ->
    line 1 (List.assoc value.consumer component_ids ^ " -.->|" ^ Label.diagram "requires" ^ "| " ^
      List.assoc value.provider component_ids));
  resolved.connections |> List.iter (fun (value : Model.resolved_connection) ->
    line 1 (List.assoc value.source.component.component_id component_ids ^ " -->|" ^
      Label.diagram (Label.connection value.connection) ^ "| " ^
      List.assoc value.target.component.component_id component_ids));
  Buffer.contents output
