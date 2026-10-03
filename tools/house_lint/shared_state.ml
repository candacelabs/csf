open Source

(* A local review locator, not alias analysis or a proof of happens-before.
   Compare an immediately launched closure with later accesses in its caller.
   A matched, explicitly typed mutex section can discharge that local pair. *)
type guard = Mutex of string | Completion of string * int
type access = { node : Node.t; root : string; location : string; write : bool;
                guards : guard list }
type launch = { node : Node.t; closure : Node.t; completion : string option }
type synchronization = { mutexes : string list; groups : string list }

let significant node = children node |> List.filter (fun child -> kind child <> "comment")
let identifiers file node = significant node |> List.filter_map (fun child ->
  if kind child = "identifier" then Some (text file child) else None)
let rec root_name file node = match kind node with
  | "identifier" when text file node = "_" -> None
  | "identifier" -> Some (text file node)
  | "selector_expression" | "index_expression" -> Option.bind (field node "operand") (root_name file)
  | "unary_expression" | "parenthesized_expression" ->
      (match significant node with [child] -> root_name file child | _ -> None)
  | _ -> None

let declared_names file node = descendants node |> List.concat_map (fun declaration ->
  match kind declaration with
  | "parameter_declaration" | "variadic_parameter_declaration" | "var_spec" -> identifiers file declaration
  | "short_var_declaration" -> Option.fold ~none:[] ~some:(identifiers file) (field declaration "left")
  | _ -> [])

let sync_names file body types =
  let aliases = imports file |> List.filter_map (fun (alias, path) -> if path = "sync" then Some alias else None) in
  let rec is_mutex typ = match kind typ with
    | "pointer_type" -> List.exists is_mutex (significant typ)
    | "qualified_type" -> List.exists (fun alias ->
        List.exists (fun name -> text file typ = alias ^ "." ^ name) types) aliases
    | _ -> false in
  let declarations = declared_names file body in
  descendants body |> List.concat_map (fun node ->
    if not (List.mem (kind node) ["parameter_declaration"; "var_spec"]) then [] else
    match field node "type" with
    | Some typ when is_mutex typ -> identifiers file node
    | _ -> []) |> List.filter (fun name -> List.length (List.filter ((=) name) declarations) = 1)

let lock_call file known node =
  if kind node <> "expression_statement" then None else
  match significant node with
  | [call] when kind call = "call_expression" ->
      (match field call "function", field call "arguments" with
       | Some callee, Some arguments when kind callee = "selector_expression" && significant arguments = [] ->
           (match field_text file callee "operand", field_text file callee "field" with
            | Some name, Some method_name when List.mem name known -> Some (name, method_name)
            | _ -> None)
       | _ -> None)
  | _ -> None

let rec changed_locks file known node =
  if List.mem (kind node) ["go_statement"; "func_literal"; "defer_statement"] then [] else
  match lock_call file known node with
  | Some (name, ("Lock" | "RLock" | "Unlock" | "RUnlock")) -> [name]
  | _ -> significant node |> List.concat_map (changed_locks file known)

let next_locks file known locks node = match lock_call file known.mutexes node with
  | Some (name, "Lock") -> (Mutex name, true) :: List.remove_assoc (Mutex name) locks
  | Some (name, "RLock") -> (Mutex name, false) :: List.remove_assoc (Mutex name) locks
  | Some (name, ("Unlock" | "RUnlock")) -> List.remove_assoc (Mutex name) locks
  | _ -> let changed = changed_locks file known.mutexes node in
      let locks = List.filter (fun (guard, _) -> match guard with
        | Mutex name -> not (List.mem name changed) | Completion _ -> true) locks in
      match lock_call file known.groups node with
      | Some (name, "Wait") -> (Completion (name, Node.start_byte node), true) :: locks
      | _ -> locks

let registration file known node =
  match significant node with
  | [call] when kind node = "expression_statement" && kind call = "call_expression" ->
      (match field call "function", field call "arguments" with
       | Some callee, Some arguments when kind callee = "selector_expression" &&
           field_text file callee "field" = Some "Add" ->
           (match field_text file callee "operand", significant arguments with
            | Some name, [count] when List.mem name known.groups && text file count = "1" -> Some name
            | _ -> None)
       | _ -> None)
  | _ -> None

let statements node = significant node |> List.concat_map (fun child ->
  if kind child = "statement_list" then significant child else [child])

let deferred_completion file closure group =
  let calls = Option.fold ~none:[] ~some:statements (field closure "body") in
  match calls with
  | first :: _ when kind first = "defer_statement" ->
      (match significant first with
       | [call] -> field_text file call "function" = Some (group ^ ".Done") &&
           Option.fold ~none:false ~some:(fun args -> significant args = []) (field call "arguments")
       | _ -> false)
  | _ -> false

let immediate_closure node =
  match significant node with
  | [call] when kind call = "call_expression" ->
      (match field call "function" with Some closure when kind closure = "func_literal" -> Some closure | _ -> None)
  | _ -> None

let accesses file known initial_locks node =
  let found = ref [] and launches = ref [] in
  let record locks write node = match root_name file node with
    | Some root ->
        let guards = locks |> List.filter_map (fun (name, exclusive) ->
          if not write || exclusive then Some name else None) in
        found := {node; root; location=text file node; write; guards} :: !found
    | None -> () in
  let rec visit locks node = match kind node with
    | "func_literal" -> ()
    | "go_statement" -> ()
    | "block" | "statement_list" -> ignore (List.fold_left (fun (held, previous) child ->
        (match immediate_closure child with
         | Some closure when kind child = "go_statement" ->
             let completion = Option.bind (Option.bind previous (registration file known)) (fun group ->
               if deferred_completion file closure group then Some group else None) in
             launches := {node=child; closure; completion} :: !launches
         | _ -> visit held child);
        next_locks file known held child, Some child) (locks, None) (statements node))
    | "assignment_statement" | "short_var_declaration" ->
        Option.iter (fun left -> significant left |> List.iter (fun target ->
          if kind node = "assignment_statement" then record locks true target;
          if kind target <> "identifier" then significant target |> List.iter (visit locks))) (field node "left");
        Option.iter (visit locks) (field node "right")
    | "inc_statement" | "dec_statement" -> significant node |> List.iter (record locks true)
    | "identifier" | "selector_expression" | "index_expression" | "unary_expression" ->
        record locks false node;
        if kind node <> "identifier" then significant node |> List.iter (visit locks)
    | "var_spec" -> Option.iter (visit locks) (field node "value")
    | _ -> significant node |> List.iter (visit locks) in
  visit initial_locks node;
  List.rev !found, List.rev !launches

let closure_accesses file known launch =
  match field launch.closure "body" with
  | None -> []
  | Some body ->
      let locals = declared_names file launch.closure in
      let known = {mutexes=List.filter (fun name -> not (List.mem name locals)) known.mutexes;
                   groups=List.filter (fun name -> not (List.mem name locals)) known.groups} in
      let found, _ = accesses file known [] body in
      found |> List.filter (fun access -> not (List.mem access.root locals)) |> List.map (fun access ->
        match launch.completion with
        | Some group when not (List.mem group locals) -> {access with guards=Completion (group, Node.end_byte launch.node) :: access.guards}
        | _ -> access)

let conflicts (child : access) (parent : access) =
  child.root = parent.root && (child.write || parent.write) &&
  not (List.exists (fun guard -> List.exists (fun parent_guard -> match guard, parent_guard with
    | Mutex name, Mutex parent_name -> name = parent_name
    | Completion (group, launch), Completion (parent_group, wait) -> group = parent_group && wait > launch
    | _ -> false) parent.guards) child.guards)

let inspect_function file node = match field node "body" with
  | None -> []
  | Some body ->
      let known = {mutexes=sync_names file node ["Mutex"; "RWMutex"]; groups=sync_names file node ["WaitGroup"]} in
      let parent_accesses, launches = accesses file known [] body in
      launches |> List.concat_map (fun launch ->
        let later = List.filter (fun (access : access) ->
          Node.start_byte access.node > Node.end_byte launch.node) parent_accesses in
        closure_accesses file known launch |> List.filter_map (fun child ->
          match List.find_opt (conflicts child) later with
          | None -> None
          | Some parent ->
              let write = if child.write then child else parent in
              Some (issue file write.node "GOROUTINE-SHARED-STATE" (Printf.sprintf
                "possible unsynchronized shared-memory access: %s; goroutine launched at line %d captures %s, caller accesses it at line %d. Review ownership/completion or protect both accesses with the same mutex; local analysis has not established synchronization"
                write.location ((Node.start_point launch.node).row + 1) child.root
                ((Node.start_point parent.node).row + 1)))))

let collect files = files |> List.filter selected |> List.concat_map (fun file ->
  descendants file.root |> List.filter (fun node -> List.mem (kind node) ["function_declaration"; "method_declaration"])
  |> List.concat_map (inspect_function file)) |> List.sort_uniq compare
