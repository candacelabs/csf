(* CS-20 "Every compound is named" (operator, 2026-10-06: "WE'RE NOT GOING TO
   CREATE A 4TUPLE TYPE WITHOUT NAMING IT"). A compound value that crosses a
   boundary -- several Go results, an anonymous struct, an OCaml tuple, an
   inline CSF field, an undeclared Datalog relation -- carries no name, so no
   reader can reason about what it is. The fix is always the same: declare a
   named type and use it. Two-result Go functions are the sanctioned shapes:
   (value, error) and (value, bool).

   The OCaml scanner is a text reader: no OCaml tree-sitter grammar is pinned
   here, so it strips comments and string/char literals and counts top-level
   commas inside parentheses. A parenthesized group with three or more elements
   that does not begin with a type variable is a tuple -- but only in value
   position. A tuple *pattern* (a lambda or match-arm argument, a let-bound
   argument list) binds names and is never the compound this rule targets, so a
   group in pattern position is left alone too. *)

open Source

(* results_3_or_more_unnamed: a function or method whose result list holds three
   or more types. (value, error) and (value, bool) are two, and never fire. *)
let result_parameters file node =
  match field node "result" with
  | None -> []
  | Some result when kind result = "parameter_list" ->
      children result |> List.filter (fun child ->
        List.mem (kind child) ["parameter_declaration"; "variadic_parameter_declaration"])
      |> List.concat_map (fun parameter ->
          let bound = children parameter
            |> List.filter (fun child -> kind child = "identifier") |> List.length |> max 1 in
          List.init bound (fun _ -> parameter))
  | Some single -> [single]

let functions file =
  descendants file.root |> List.filter (fun node ->
    List.mem (kind node) ["function_declaration"; "method_declaration"])

(* A node's source spelling with every space removed, so an already-parsed type
   compares by token sequence and never by formatting. *)
let token_text file node =
  text file node |> String.to_seq
  |> Seq.filter (fun character -> not (List.mem character [' '; '\t'; '\n'; '\r']))
  |> String.of_seq

(* The result type nodes, one per value a grouped declaration binds. *)
let result_type_nodes file node =
  match field node "result" with
  | None -> []
  | Some result when kind result = "parameter_list" ->
      children result |> List.filter (fun child ->
        List.mem (kind child) ["parameter_declaration"; "variadic_parameter_declaration"])
      |> List.concat_map (fun parameter ->
          let bound = children parameter
            |> List.filter (fun child -> kind child = "identifier") |> List.length |> max 1 in
          match field parameter "type" with
          | Some typ -> List.init bound (fun _ -> typ)
          | None -> [])
  | Some single -> [single]

let result_signature file node =
  String.concat "," (List.map (token_text file) (result_type_nodes file node))

(* A foreign interface fixes a method's result shape; the compound is named by
   that interface, not by this module, so the module cannot rename it. net/http
   is the one such owner here: coder/websocket and gin type-assert Hijack on the
   ResponseWriter they are handed, so sizedHijacker.Hijack -- and the two test
   doubles standing in for net/http -- must keep the standard library's exact
   signature. Keyed by (method name, comma-joined result type signature); every
   other three-or-more result list is this module's own compound to name. *)
let foreign_methods = [("Hijack", "net.Conn,*bufio.ReadWriter,error")]

(* The model context protocol SDK fixes a tool handler's shape too:
   mcp.ToolHandlerFor[In, Out] is a func(context.Context, *mcp.CallToolRequest,
   In) returning *mcp.CallToolResult, the Out view and error. A handler handed
   to WithMCPTool must keep that exact result list, so the compound -- the call
   result and the view the SDK serializes -- is named by the SDK's contract, not
   by this module. The shape is *mcp.CallToolResult first and error last;
   nothing this module owns spells that but an MCP handler. *)
let mcp_tool_handler_shape signature =
  String.starts_with ~prefix:"*mcp.CallToolResult," signature &&
  let suffix = ",error" in
  String.length signature >= String.length suffix &&
  String.sub signature (String.length signature - String.length suffix)
    (String.length suffix) = suffix

let results_findings file =
  functions file |> List.filter_map (fun node ->
    let results = result_parameters file node in
    if List.length results < 3 then None
    else
      let name = Option.value ~default:"" (field_text file node "name") in
      let signature = result_signature file node in
      if List.mem (name, signature) foreign_methods ||
         mcp_tool_handler_shape signature then None
      else Some (issue file node "CS-20" (Printf.sprintf
        "returns %d values; name the compound result with a declared struct type"
        (List.length results))))

(* go_anonymous_struct_type: a struct type that is not the body of a declared
   type. A named type's body is exempt; every other struct literal is a
   compound without a name. The empty struct -- `struct{}`, the set idiom a
   `map[K]struct{}` is built from -- declares no field and so names no
   compound; it carries nothing a reader could look up. *)
let named_struct_ranges file =
  descendants file.root |> List.filter_map (fun node ->
    if not (List.mem (kind node) ["type_spec"; "type_alias"]) then None else
    match field node "type" with
    | Some body when kind body = "struct_type" -> Some (Node.start_byte body, Node.end_byte body)
    | _ -> None)

let empty_struct node =
  descendants node |> List.for_all (fun child -> kind child <> "field_declaration")

let anonymous_struct_findings file =
  let named = named_struct_ranges file in
  descendants file.root |> List.filter_map (fun node ->
    if kind node <> "struct_type" then None
    else if List.mem (Node.start_byte node, Node.end_byte node) named then None
    else if empty_struct node then None
    else Some (issue file node "CS-20"
      "anonymous struct type; declare a named struct type and use it here"))

(* OCaml tuple literals of arity three or more. *)
let blank source first last =
  for position = first to last - 1 do
    if Bytes.get source position <> '\n' then Bytes.set source position ' '
  done

(* Replace comment and string/char literal bodies with spaces so a parenthesis
   inside one never opens a tuple group; offsets are preserved. *)
let strip_ocaml text =
  let bytes = Bytes.of_string text in
  let length = String.length text in
  let index = ref 0 in
  while !index < length do
    let char = text.[!index] in
    if !index + 1 < length && char = '(' && text.[!index + 1] = '*' then begin
      let depth = ref 1 and cursor = ref (!index + 2) in
      while !depth > 0 && !cursor < length do
        if !cursor + 1 < length && text.[!cursor] = '(' && text.[!cursor + 1] = '*' then
          (incr depth; cursor := !cursor + 2)
        else if !cursor + 1 < length && text.[!cursor] = '*' && text.[!cursor + 1] = ')' then
          (decr depth; cursor := !cursor + 2)
        else incr cursor
      done;
      blank bytes !index !cursor; index := !cursor
    end else if char = '{' then begin
      (* A quoted string [{tag| ... |tag}] holds arbitrary text, fixtures
         included; blank it whole so a tuple inside a fixture is never code.
         The tag is an identifier, empty for [{| ... |}]. *)
      let tag_end = ref (!index + 1) in
      while !tag_end < length &&
        (let c = text.[!tag_end] in
         (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
         (c >= '0' && c <= '9') || c = '_') do incr tag_end done;
      if !tag_end < length && text.[!tag_end] = '|' then begin
        let closing = "|" ^ String.sub text (!index + 1) (!tag_end - !index - 1) ^ "}" in
        let width = String.length closing in
        let cursor = ref (!tag_end + 1) and closed = ref false in
        while not !closed && !cursor < length do
          if !cursor + width <= length && String.sub text !cursor width = closing then
            (closed := true; cursor := !cursor + width)
          else incr cursor
        done;
        if not !closed then cursor := length;
        blank bytes !index !cursor; index := !cursor
      end else incr index
    end else if char = '"' then begin
      let cursor = ref (!index + 1) and closed = ref false in
      while not !closed && !cursor < length do
        if text.[!cursor] = '\\' then cursor := !cursor + 2
        else if text.[!cursor] = '"' then (closed := true; incr cursor)
        else incr cursor
      done;
      blank bytes !index !cursor; index := !cursor
    end else if char = '\'' then begin
      (* A type variable (['a], ['a, 'b]) shares the quote; blanking it would
         hide the very tuples this rule targets, and mistaking it for a char
         could blank code. Only blank a char literal: quote, one char, quote. *)
      if !index + 2 < length && text.[!index + 2] = '\'' &&
         text.[!index + 1] <> '\\' then begin
        blank bytes !index (!index + 3); index := !index + 3
      end else incr index
    end else incr index
  done;
  Bytes.to_string bytes

let line_of text offset =
  let line = ref 1 in
  for position = 0 to min offset (String.length text) - 1 do
    if text.[position] = '\n' then incr line
  done;
  !line

(* A value tuple's elements are plain values; a parenthesized group whose body
   holds function or matching syntax -- fun, ->, match, |, if, let, ... -- is a
   call or a control expression, not a tuple literal, and is under-reported on
   purpose rather than misreported. *)
let is_ident c =
  (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c = '_' || c = '\''

let has_word text start stop word =
  let word_length = String.length word in
  let rec scan i =
    if i + word_length > stop then false
    else if String.sub text i word_length = word &&
            (i = start || not (is_ident text.[i - 1])) &&
            (i + word_length = stop || not (is_ident text.[i + word_length]))
    then true
    else scan (i + 1)
  in
  scan start

let group_is_code clean start stop =
  has_word clean start stop "fun" ||
  has_word clean start stop "match" ||
  has_word clean start stop "function" ||
  has_word clean start stop "if" ||
  has_word clean start stop "then" ||
  has_word clean start stop "else" ||
  has_word clean start stop "let" ||
  has_word clean start stop "in" ||
  has_word clean start stop "with" ||
  (let rec scan i =
     if i >= stop then false
     else let c = clean.[i] in
       if c = '|' then true
       else if i + 1 < stop && c = '-' && clean.[i + 1] = '>' then true
       else scan (i + 1) in
   scan start)

let is_space c = c = ' ' || c = '\n' || c = '\t' || c = '\r'

(* The identifier ending just before [stop], past any space between it and the
   opening paren; returns the word and where it starts, or None when a non-word
   byte (a bar, a comma, a `=` ...) sits there. *)
let ident_before_ws clean stop =
  let i = ref (stop - 1) in
  while !i >= 0 && is_space clean.[!i] do decr i done;
  if !i < 0 || not (is_ident clean.[!i]) then None
  else
    let j = ref !i in
    while !j >= 0 && is_ident clean.[!j] do decr j done;
    Some (String.sub clean (!j + 1) (!i - !j), !j + 1)

(* Step back over a possibly-qualified name -- Module.Sub.ctor -- so a check
   sees what opens the whole name. Returns the index of the byte before it. *)
let name_head clean start =
  let i = ref (start - 1) in
  let rec step () =
    while !i >= 0 && is_space clean.[!i] do decr i done;
    if !i >= 0 && clean.[!i] = '.' then begin
      decr i;
      while !i >= 0 && is_space clean.[!i] do decr i done;
      while !i >= 0 && is_ident clean.[!i] do decr i done;
      step ()
    end
  in
  step (); !i

(* A tuple *pattern* binds names -- a lambda or a match-arm parameter, a
   let-bound function's argument list -- and is never fixed by naming a record;
   only a tuple *literal* is the compound this rule targets. A group is in
   pattern position when a `fun`/`function` word opens it, when `->` or `=` is
   the next byte after it, or when a capitalized constructor (qualified or not)
   opens it from a `|`/`with`/`function` arm. *)
let group_is_pattern clean start close =
  let length = String.length clean in
  (match ident_before_ws clean start with
   | Some (word, _) when word = "fun" || word = "function" -> true
   | _ ->
     let after = ref close in
     while !after < length && is_space clean.[!after] do incr after done;
     (!after < length && clean.[!after] = '=') ||
     (!after + 1 < length && clean.[!after] = '-' && clean.[!after + 1] = '>') ||
     (match ident_before_ws clean start with
      | Some (ctor, ctor_start) when ctor.[0] >= 'A' && ctor.[0] <= 'Z' ->
          let head = name_head clean ctor_start in
          if head >= 0 && (clean.[head] = '|' || clean.[head] = ',') then true
          else (match ident_before_ws clean (head + 1) with
            | Some (before, _) -> before = "with" || before = "function"
            | None -> false)
      | _ -> false))

let tuple_offsets clean =
  let length = String.length clean in
  let groups = ref [] in
  for start = 0 to length - 1 do
    if clean.[start] = '(' then begin
      (* Square brackets and braces nest too: a list or record inside the
         group holds its own element commas, which are not the tuple's. *)
      let depth = ref 0 and commas = ref 0 and cursor = ref start and closed = ref false in
      while not !closed && !cursor < length do
        (match clean.[!cursor] with
         | '(' | '[' | '{' -> incr depth
         | ')' | ']' | '}' -> decr depth; if !depth = 0 then closed := true
         | ',' -> if !depth = 1 then incr commas
         | _ -> ());
        incr cursor
      done;
      if !closed && !commas >= 2 then groups := (start, !cursor) :: !groups
    end
  done;
  let groups = List.rev !groups in
  let type_variable (start, _) =
    let probe = ref (start + 1) in
    while !probe < length && is_space clean.[!probe] do incr probe done;
    !probe < length && clean.[!probe] = '\'' in
  (* A group in pattern position, and every group nested inside it -- a pattern
     holds only patterns -- is a pattern, never a literal. *)
  let pattern_spans = groups |> List.filter (fun (start, close) ->
    not (type_variable (start, close)) &&
    not (group_is_code clean start close) && group_is_pattern clean start close) in
  groups |> List.filter (fun (start, close) ->
    not (type_variable (start, close)) &&
    not (group_is_code clean start close) &&
    not (group_is_pattern clean start close) &&
    not (List.exists (fun (ps, pe) -> ps < start && close <= pe) pattern_spans))
    |> List.map fst

let ocaml_findings path text =
  let clean = strip_ocaml text in
  tuple_offsets clean |> List.map (fun offset ->
    {path; line = line_of text offset; rule = "CS-20";
     message = "tuple of three or more values; declare a named record type with one field per element"})

let text_paths suffixes tracked =
  tracked |> List.filter (fun path ->
    List.exists (fun suffix -> Filename.check_suffix path suffix) suffixes &&
    not (String.starts_with ~prefix:"bazel-" path) &&
    not (List.exists (fun segment -> List.mem segment ["third_party"; "_build"; "node_modules"])
      (String.split_on_char '/' path)))

let ocaml_paths tracked =
  text_paths [".ml"; ".mli"] tracked |> List.filter (fun path ->
    not (List.exists (fun suffix -> Filename.check_suffix path suffix) ["_cgen.ml"; "_cgen.mli"]))

(* Every scanner below reads text, so each shares one generated-file marker:
   a generated file's layout and its compounds belong to its generator. The
   marker is the compiler's banner in whichever comment syntax the file uses. *)
let generated_prefixes = [
  "// Code generated "; "(* Code generated "; "/* Code generated ";
  "# Code generated "; "% Code generated ";
]

let text_generated text =
  String.split_on_char '\n' text |> List.exists (fun line ->
    let line = String.trim line in
    List.exists (fun prefix -> String.starts_with ~prefix line) generated_prefixes)

(* csf_inline_compound_field: a kind field whose type is written inline -- a
   bare `record` or a `[...]` list -- carries no name. Declaring the kind and
   referring to it by name is the fix. Strings, `//`/`#` comments and `/* */`
   blocks are blanked first, preserving offsets, so prose never fires. *)
let strip_csf text =
  let bytes = Bytes.of_string text in
  let length = String.length text in
  let index = ref 0 in
  while !index < length do
    let char = text.[!index] in
    if !index + 1 < length && char = '*' && text.[!index + 1] = '/' then
      incr index (* a stray close never opens a comment *)
    else if !index + 1 < length && char = '/' && text.[!index + 1] = '*' then begin
      let cursor = ref (!index + 2) in
      while !cursor + 1 < length &&
            not (text.[!cursor] = '*' && text.[!cursor + 1] = '/') do incr cursor done;
      let stop = if !cursor + 1 < length then !cursor + 2 else length in
      blank bytes !index stop; index := stop
    end else if !index + 1 < length && char = '/' && text.[!index + 1] = '/' then begin
      let cursor = ref !index in
      while !cursor < length && text.[!cursor] <> '\n' do incr cursor done;
      blank bytes !index !cursor; index := !cursor
    end else if char = '#' then begin
      let cursor = ref !index in
      while !cursor < length && text.[!cursor] <> '\n' do incr cursor done;
      blank bytes !index !cursor; index := !cursor
    end else if char = '"' then begin
      let cursor = ref (!index + 1) and closed = ref false in
      while not !closed && !cursor < length do
        if text.[!cursor] = '\\' then cursor := !cursor + 2
        else if text.[!cursor] = '"' then (closed := true; incr cursor)
        else incr cursor
      done;
      blank bytes !index !cursor; index := !cursor
    end else incr index
  done;
  Bytes.to_string bytes

let csf_findings path text =
  let clean = strip_csf text in
  let length = String.length clean in
  let findings = ref [] in
  for position = 0 to length - 1 do
    if clean.[position] = ':' then begin
      let probe = ref (position + 1) in
      while !probe < length && (clean.[!probe] = ' ' || clean.[!probe] = '\t') do incr probe done;
      let list_or_record =
        !probe < length && (clean.[!probe] = '[' || clean.[!probe] = '{') in
      let bare_record =
        !probe + 6 <= length && String.sub clean !probe 6 = "record" &&
        (!probe + 6 = length || not (is_ident clean.[!probe + 6])) in
      if list_or_record || bare_record then
        findings := {path; line = line_of text position; rule = "CS-20";
          message = "inline compound field; declare a named kind and refer to it by name here"} :: !findings
    end
  done;
  List.rev !findings

(* datalog_relation_undeclared: a relation a rule body reads that no
   declaration in the same file introduces. Rules declare relations in
   `/* facts */` blocks (`relation name ⊆ ...` or `relation name is subset
   of ...`) and introduce derived ones with a clause head (`name(...) :-` or a
   fact `name(...).`); the engine's own built-ins are the interpreter's. A
   body term with no such introduction is a relation the reader cannot look
   up. *)
let datalog_builtins = ["lt"; "le"; "gt"; "ge"; "eq"; "neq"; "print"; "eval"; "sum"; "count"]

let is_dl_ident character =
  (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
  (character >= '0' && character <= '9') || character = '_'

(* The subset mark U+2286 opens every `relation name ⊆ ...` declaration. *)
let subset_mark text position =
  position + 2 < String.length text &&
  text.[position] = '\xe2' && text.[position + 1] = '\x8a' && text.[position + 2] = '\x86'

(* The identifier ending just before [stop]. *)
let ident_backward text stop =
  let cursor = ref (stop - 1) in
  while !cursor >= 0 && is_dl_ident text.[!cursor] do decr cursor done;
  let start = !cursor + 1 in
  if start = stop then None else Some (String.sub text start (stop - start))

(* The identifier just before [stop], past any space between it and a mark. *)
let ident_before text stop =
  let cursor = ref (stop - 1) in
  while !cursor >= 0 && (text.[!cursor] = ' ' || text.[!cursor] = '\t') do decr cursor done;
  ident_backward text (!cursor + 1)

let ident_forward text start =
  let cursor = ref start in
  while !cursor < String.length text && is_dl_ident text.[!cursor] do incr cursor done;
  if !cursor = start then None else Some (String.sub text start (!cursor - start))

let skip_blank text start =
  let cursor = ref start in
  while !cursor < String.length text &&
        (text.[!cursor] = ' ' || text.[!cursor] = '\t' || text.[!cursor] = '\r') do incr cursor done;
  !cursor

(* Comment bodies are blanked, offsets preserved, so a relation named in prose
   is never a use; the facts and predicate blocks keep their declarations. *)
let strip_datalog text =
  let bytes = Bytes.of_string text in
  let length = String.length text in
  let index = ref 0 in
  while !index < length do
    let char = text.[!index] in
    if char = '%' then begin
      let cursor = ref !index in
      while !cursor < length && text.[!cursor] <> '\n' do incr cursor done;
      blank bytes !index !cursor; index := !cursor
    end else if !index + 1 < length && char = '/' && text.[!index + 1] = '*' then begin
      let cursor = ref (!index + 2) and depth = ref 1 in
      while !depth > 0 && !cursor < length do
        if !cursor + 1 < length && text.[!cursor] = '*' && text.[!cursor + 1] = '/' then
          (decr depth; cursor := !cursor + 2)
        else if !cursor + 1 < length && text.[!cursor] = '/' && text.[!cursor + 1] = '*' then
          (incr depth; cursor := !cursor + 2)
        else incr cursor
      done;
      blank bytes !index !cursor; index := !cursor
    end else incr index
  done;
  Bytes.to_string bytes

(* Declarations live in comments; clause heads live in code. *)
let datalog_declared text =
  let declared = Hashtbl.create 64 in
  let add = function None -> () | Some name -> Hashtbl.replace declared name () in
  let length = String.length text in
  for position = 0 to length - 1 do
    if subset_mark text position then add (ident_before text position)
  done;
  let keyword = "relation" and width = String.length "relation" in
  for position = 0 to length - width do
    if String.sub text position width = keyword &&
       (position = 0 || not (is_dl_ident text.[position - 1])) &&
       (position + width = length || not (is_dl_ident text.[position + width]))
    then add (ident_forward text (skip_blank text (position + width)))
  done;
  declared

(* Every name a body reads: the identifier before each `(`, past `~` and
   space. Relations are the only parenthesized terms in these files. *)
let datalog_uses body number uses =
  let length = String.length body in
  for position = 0 to length - 1 do
    if body.[position] = '(' then begin
      let cursor = ref (position - 1) in
      while !cursor >= 0 && (body.[!cursor] = ' ' || body.[!cursor] = '\t' || body.[!cursor] = '~') do
        decr cursor
      done;
      (match ident_backward body (!cursor + 1) with
       | Some name when not (Hashtbl.mem uses name) -> Hashtbl.replace uses name number
       | _ -> ())
    end
  done

let datalog_clauses clean =
  let heads = Hashtbl.create 64 and uses = Hashtbl.create 64 in
  String.split_on_char '\n' clean |> List.iteri (fun index line ->
    let line = String.trim line in
    if line <> "" then begin
      (match ident_forward line 0 with
       | Some name when String.length line > String.length name &&
                        line.[String.length name] = '(' -> Hashtbl.replace heads name ()
       | _ -> ());
      (match String.index_opt line ':' with
       | Some colon when colon + 1 < String.length line && line.[colon + 1] = '-' ->
           datalog_uses (String.sub line (colon + 2) (String.length line - colon - 2)) (index + 1) uses
       | _ -> ())
    end);
  heads, uses

let datalog_findings path text =
  let clean = strip_datalog text in
  let declared = datalog_declared text in
  let heads, uses = datalog_clauses clean in
  Hashtbl.fold (fun name line findings ->
    if Hashtbl.mem heads name || Hashtbl.mem declared name ||
       List.mem name datalog_builtins then findings
    else {path; line; rule = "CS-20"; message = Printf.sprintf
      "relation %s is used but never declared; declare it beside the other relations" name} :: findings)
    uses []
  |> List.sort (fun a b -> compare (a.line, a.message) (b.line, b.message))

let check_ocaml root tracked =
  ocaml_paths tracked |> List.concat_map (fun path ->
    try
      let text = Checker.read_file (Checker.checked_path root path) in
      if text_generated text then [] else ocaml_findings path text
    with Sys_error message ->
      [{path; line = 1; rule = "SCAN"; message}])

let check_csf root tracked =
  text_paths [".csf"] tracked |> List.concat_map (fun path ->
    try
      let text = Checker.read_file (Checker.checked_path root path) in
      if text_generated text then [] else csf_findings path text
    with Sys_error message ->
      [{path; line = 1; rule = "SCAN"; message}])

let check_datalog root tracked =
  text_paths [".dl"] tracked |> List.concat_map (fun path ->
    try
      let text = Checker.read_file (Checker.checked_path root path) in
      if text_generated text then [] else datalog_findings path text
    with Sys_error message ->
      [{path; line = 1; rule = "SCAN"; message}])

let collect files =
  files |> List.filter selected |> List.concat_map (fun file ->
    results_findings file @ anonymous_struct_findings file)
