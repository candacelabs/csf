module Label = Emit__Label

type change = Unchanged | Added | Removed | Changed

let class_name = function
  | Unchanged -> "unchanged" | Added -> "added" | Removed -> "removed" | Changed -> "changed"

let marker = function Unchanged -> "" | Added -> "+ " | Removed -> "- " | Changed -> "~ "

(* Colour carries two things at once: the fill says what a declaration is (its
   role, or the kind of frame), the border says what happened to it. No value
   ends in a semicolon: Mermaid reads "#hex;" as a character entity. *)
type paint = { fill : string; stroke : string; text : string }

let role_paint : Model.role -> paint = function
  | Model.Service -> { fill = "#ddf4ff"; stroke = "#0969da"; text = "#0a3069" }
  | Model.Manager -> { fill = "#fbefff"; stroke = "#8250df"; text = "#3e1f79" }
  | Model.Library -> { fill = "#fff1e5"; stroke = "#bc4c00"; text = "#762c00" }
  | Model.Adapter -> { fill = "#ffeff7"; stroke = "#bf3989"; text = "#6d1846" }
  | Model.Gateway -> { fill = "#ccf2ee"; stroke = "#0e7c74"; text = "#0b4f4a" }
  | Model.Resource -> { fill = "#eef1ff"; stroke = "#4f5bd5"; text = "#262f80" }

let process_paint : Model.process_kind -> paint = function
  | Model.Go -> { fill = "#f0f7ff"; stroke = "#54aeff"; text = "#0550ae" }
  | Model.External -> { fill = "#fff8f2"; stroke = "#fb8f44"; text = "#953800" }

let scope_paint = { fill = "#fcfdff"; stroke = "#afb8c1"; text = "#424a53" }

(* GitHub's diff colours for borders and edges that changed. *)
let change_border paint = function
  | Unchanged -> "stroke:" ^ paint.stroke ^ ",stroke-width:1.5px"
  | Added -> "stroke:#1a7f37,stroke-width:4px"
  | Removed -> "stroke:#cf222e,stroke-width:4px,stroke-dasharray:6 4"
  | Changed -> "stroke:#d29922,stroke-width:4px"

let class_style paint change = "fill:" ^ paint.fill ^ ",color:" ^ paint.text ^ "," ^ change_border paint change

let transport_colour : Model.transport -> string = function
  | Model.Call -> "#0969da" | Model.Channel -> "#8250df" | Model.Subprocess -> "#bc4c00"
  | Model.Remote -> "#0e7c74" | Model.Device -> "#bf3989"

let requires_colour = "#6e7781"

let edge_style colour = function
  | Unchanged -> "stroke:" ^ colour ^ ",stroke-width:2px"
  | Added -> "stroke:#1a7f37,stroke-width:3.5px"
  | Removed -> "stroke:#cf222e,stroke-width:3px,stroke-dasharray:6 4"
  | Changed -> "stroke:#d29922,stroke-width:3.5px"


let nowhere : Model.location = { file = ""; line = 0; column = 0 }

(* [key] identifies a declaration across revisions; [strip] drops what may
   move without changing it, its location. *)
let change_of key strip base head id =
  let find items = List.find_opt (fun item -> key item = id) items in
  match find base, find head with
  | Some _, None -> Removed
  | None, Some _ -> Added
  | Some was, Some now -> if strip was = strip now then Unchanged else Changed
  | None, None -> Unchanged

(* Every declaration of head in order, then those only base has: removed ones
   are drawn where they were. *)
let union key head base =
  head @ List.filter (fun item -> not (List.exists (fun kept -> key kept = key item) head)) base

let component_changes (was : Model.component) (now : Model.component) =
  let differs name show a b = if a = b then [] else [name ^ " was " ^ show a] in
  let optional = function None -> "none" | Some value -> value in
  differs "role" (Label.spelling Syntax_cgen.role_terminal) was.role now.role @
  differs "state" (Label.spelling Syntax_cgen.state_terminal) was.state now.state @
  differs "lifecycle" (Label.spelling Syntax_cgen.lifecycle_terminal) was.lifecycle now.lifecycle @
  differs "source" optional was.source now.source @
  differs "process" Fun.id was.process now.process @
  differs "scope" Fun.id was.scope now.scope

let process_key (value : Model.process) = value.process_id
let scope_key (value : Model.scope) = value.scope_id
let component_key (value : Model.component) = value.component_id
let dependency_key (value : Model.dependency) = value.consumer, value.provider
let connection_key (value : Model.connection) = value.caller, value.callee

let render ~(base : Model.resolved) ~(head : Model.resolved) =
  let was = base.architecture and now = head.architecture in
  let processes = union process_key now.processes was.processes in
  let scopes = union scope_key now.scopes was.scopes in
  let components = union component_key now.components was.components in
  let process_change = change_of process_key (fun (v : Model.process) -> { v with process_at = nowhere })
    was.processes now.processes in
  let scope_change = change_of scope_key (fun (v : Model.scope) -> { v with scope_at = nowhere })
    was.scopes now.scopes in
  let component_change = change_of component_key (fun (v : Model.component) -> { v with component_at = nowhere })
    was.components now.components in
  let process_ids = Label.numbered "p" processes process_key in
  let scope_ids = Label.numbered "s" scopes scope_key in
  let component_ids = Label.numbered "c" components component_key in
  let output = Buffer.create 4096 in
  let line depth value = Buffer.add_string output (String.make (2 * depth) ' ' ^ value ^ "\n") in
  (* One class per (kind, change) pair actually drawn, defined in first-use
     order, so equal inputs give equal bytes and unused pairs add no noise. *)
  let defined = ref [] and assigned = ref [] in
  let paint id kind paint change =
    let name = kind ^ "_" ^ class_name change in
    if not (List.mem_assoc name !defined) then defined := (name, class_style paint change) :: !defined;
    assigned := (id, name) :: !assigned in
  let node depth (value : Model.component) =
    let change = component_change value.component_id in
    let earlier = match change, List.find_opt (fun c -> component_key c = value.component_id) was.components with
      | Changed, Some before -> String.concat "" (List.map (fun text -> "\n" ^ text) (component_changes before value))
      | _ -> "" in
    let id = List.assoc value.component_id component_ids in
    line depth (id ^ "[" ^ Label.diagram (marker change ^ Label.component value ^ earlier) ^ "]");
    paint id (Label.spelling Syntax_cgen.role_terminal value.role) (role_paint value.role) change in
  let rec nest depth parent =
    scopes |> List.iter (fun (value : Model.scope) ->
      if value.parent = parent then begin
        let id = List.assoc value.scope_id scope_ids and change = scope_change value.scope_id in
        line depth ("subgraph " ^ id ^ "[" ^ Label.diagram (marker change ^ "scope " ^ value.scope_id) ^ "]");
        paint id "scope" scope_paint change;
        components |> List.iter (fun (component : Model.component) ->
          if component.scope = value.scope_id then node (depth + 1) component);
        nest (depth + 1) value.scope_id;
        line depth "end"
      end) in
  line 0 "flowchart TB";
  line 1 "%% Declared architecture diff, not observed running state.";
  processes |> List.iter (fun (value : Model.process) ->
    let id = List.assoc value.process_id process_ids and change = process_change value.process_id in
    line 1 ("subgraph " ^ id ^ "[" ^ Label.diagram (marker change ^ Label.process value) ^ "]");
    paint id ("process_" ^ Label.spelling Syntax_cgen.process_kind_terminal value.kind) (process_paint value.kind) change;
    nest 2 value.process_id;
    line 1 "end");
  (* Mermaid numbers links in drawing order; linkStyle addresses them so. *)
  let edges = ref [] in
  let edge from_id arrow label target_id style =
    line 1 (from_id ^ " " ^ arrow ^ "|" ^ Label.diagram label ^ "| " ^ target_id);
    edges := style :: !edges in
  union dependency_key now.dependencies was.dependencies |> List.iter (fun (value : Model.dependency) ->
    let change = change_of dependency_key (fun (v : Model.dependency) -> { v with dependency_at = nowhere })
      was.dependencies now.dependencies (dependency_key value) in
    edge (List.assoc value.consumer component_ids) "-.->" (marker change ^ "requires")
      (List.assoc value.provider component_ids) (edge_style requires_colour change));
  union connection_key now.connections was.connections |> List.iter (fun (value : Model.connection) ->
    let strip (v : Model.connection) = { v with connection_at = nowhere } in
    let change = change_of connection_key strip was.connections now.connections (connection_key value) in
    let earlier = match change, List.find_opt (fun c -> connection_key c = connection_key value) was.connections with
      | Changed, Some before -> "\nwas " ^ Label.connection before
      | _ -> "" in
    edge (List.assoc value.caller component_ids) "-->" (marker change ^ Label.connection value ^ earlier)
      (List.assoc value.callee component_ids) (edge_style (transport_colour value.transport) change));
  List.rev !defined |> List.iter (fun (name, style) -> line 1 ("classDef " ^ name ^ " " ^ style));
  List.rev !assigned |> List.iter (fun (id, name) -> line 1 ("class " ^ id ^ " " ^ name));
  let indexed = List.mapi (fun index style -> index, style) (List.rev !edges) in
  List.sort_uniq compare (List.map snd indexed) |> List.iter (fun style ->
    let indices = List.filter_map (fun (index, s) -> if s = style then Some (string_of_int index) else None) indexed in
    line 1 ("linkStyle " ^ String.concat "," indices ^ " " ^ style));
  Buffer.contents output
