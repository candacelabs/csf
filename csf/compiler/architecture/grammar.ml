(* The JEV-writability census of the CSF EBNF grammars. The scan is textual:
   comments are stripped, quoted terminals and ? ... ? special sequences are
   blanked, and a statement runs to the first ';' outside a group. Nothing here
   parses the grammar as the frontends do; a production is one use-site. *)

(* The free-text builtins: a production that names one is not writable by a
   single JEV pick, because the writer would type arbitrary text. [text] is one
   of them -- the operator's one_pick rule counts it with the lexical builtins
   even where no grammar names it yet. *)
let builtins = ["identifier"; "integer"; "string"; "text"]
let max_options = 16

(* The builtins one pick writes, by the role its slot plays.
   [references_to_lookups], [binders_to_spans] and [prose_to_generated] took
   [identifier] and [string]: a reference is a lookup into the declared nouns, a
   binder a name copied from the operator's tokens or derived from the path
   answers, a quote a verbatim span of those tokens, a description rendered from
   the typed fields. [versions_derived] takes [integer]: a version is computed
   and monotonic, never typed, and a quantity is a declared range or read from
   the data. [text] alone stays free -- one pick cannot write it, and no grammar
   names it. *)
let writable_builtin = function "identifier" | "string" | "integer" -> true | _ -> false

type use_site = { file : string; name : string; alternatives : int; free_text : string list }

let bounded site = site.free_text = [] && site.alternatives <= max_options

let is_quote = function '\'' | '"' | '?' -> true | _ -> false
let is_name_start = function 'a' .. 'z' | 'A' .. 'Z' | '_' -> true | _ -> false
let is_name_char = function 'a' .. 'z' | 'A' .. 'Z' | '0' .. '9' | '_' -> true | _ -> false

let is_identifier name =
  name <> "" && is_name_start name.[0] &&
  (let valid = ref true in
   String.iter (fun character -> if not (is_name_char character) then valid := false) name;
   !valid)

(* Drop [(* ... *)] and [// ...] comments, keeping every other byte so that
   statement splitting sees the same text. *)
let strip_comments text =
  let length = String.length text in
  let buffer = Buffer.create length in
  let rec comment index =
    if index + 1 >= length then length
    else if text.[index] = '*' && text.[index + 1] = ')' then index + 2
    else comment (index + 1) in
  let rec line index =
    if index >= length then length
    else if text.[index] = '\n' then index
    else line (index + 1) in
  let rec scan index =
    if index >= length then ()
    else if index + 1 < length && text.[index] = '(' && text.[index + 1] = '*' then scan (comment (index + 2))
    else if index + 1 < length && text.[index] = '/' && text.[index + 1] = '/' then scan (line (index + 2))
    else begin Buffer.add_char buffer text.[index]; scan (index + 1) end in
  scan 0; Buffer.contents buffer

(* The statements of a grammar: its [;] separators split at depth zero and
   outside quotes, so a ';' inside a quoted terminal does not end a statement. *)
let split_statements text =
  let length = String.length text in
  let parts = ref [] and current = Buffer.create 128 in
  let depth = ref 0 and index = ref 0 in
  while !index < length do
    let character = text.[!index] in
    if is_quote character then begin
      match String.index_from_opt text (!index + 1) character with
      | None -> Buffer.add_substring current text !index (length - !index); index := length
      | Some stop -> Buffer.add_substring current text !index (stop - !index + 1); index := stop + 1
    end else begin
      (match character with
       | '(' | '{' | '[' -> incr depth
       | ')' | '}' | ']' -> decr depth
       | _ -> ());
      if character = ';' && !depth = 0 then begin
        parts := Buffer.contents current :: !parts; Buffer.clear current
      end else Buffer.add_char current character;
      incr index
    end
  done;
  parts := Buffer.contents current :: !parts;
  List.rev !parts

(* Blank every quoted terminal and [? ... ?] special sequence, so a ';' or '|'
   or a name inside one is not read as structure. *)
let mask text =
  let length = String.length text in
  let buffer = Buffer.create length in
  let index = ref 0 in
  while !index < length do
    let character = text.[!index] in
    if is_quote character then begin
      match String.index_from_opt text (!index + 1) character with
      | None -> Buffer.add_string buffer (String.make (length - !index) ' '); index := length
      | Some stop -> Buffer.add_string buffer (String.make (stop - !index + 1) ' '); index := stop + 1
    end else begin Buffer.add_char buffer character; incr index end
  done;
  Buffer.contents buffer

(* The alternatives of one choice: the [|]-separated parts at depth zero. *)
let split_top body =
  let body = mask body in
  let length = String.length body in
  let parts = ref [] and current = Buffer.create 32 in
  let depth = ref 0 in
  for index = 0 to length - 1 do
    let character = body.[index] in
    (match character with
     | '(' | '{' | '[' -> incr depth
     | ')' | '}' | ']' -> decr depth
     | _ -> ());
    if character = '|' && !depth = 0 then begin
      parts := Buffer.contents current :: !parts; Buffer.clear current
    end else Buffer.add_char current character
  done;
  parts := Buffer.contents current :: !parts;
  List.rev !parts

(* The contents of each top-level brace, bracket or paren group, so a nested
   choice is measured too. *)
let groups body =
  let body = mask body in
  let length = String.length body in
  let collected = ref [] and depth = ref 0 and start = ref (-1) in
  for index = 0 to length - 1 do
    let character = body.[index] in
    if character = '(' || character = '{' || character = '[' then begin
      if !depth = 0 then start := index + 1;
      incr depth
    end else if character = ')' || character = '}' || character = ']' then begin
      decr depth;
      if !depth = 0 && !start >= 0 then begin
        collected := String.sub body !start (index - !start) :: !collected; start := -1
      end
    end
  done;
  List.rev !collected

(* The names a right-hand side references, in order. *)
let references body =
  let body = mask body in
  let length = String.length body in
  let names = ref [] and index = ref 0 in
  while !index < length do
    if is_name_start body.[!index] then begin
      let start = !index in
      while !index < length && is_name_char body.[!index] do incr index done;
      names := String.sub body start (!index - start) :: !names
    end else incr index
  done;
  List.rev !names

(* The widest choice in a right-hand side, at its top level or any group. *)
let rec widest_choice body =
  let widest = ref (List.length (split_top body)) in
  List.iter (fun group -> widest := max !widest (widest_choice group)) (groups body);
  !widest

(* The [name = body] productions of a grammar, in source order. *)
let productions text =
  split_statements (strip_comments text)
  |> List.filter_map (fun statement ->
    match String.index_opt statement '=' with
    | None -> None
    | Some equal ->
        let name = String.trim (String.sub statement 0 equal) in
        let body = String.trim (String.sub statement (equal + 1) (String.length statement - equal - 1)) in
        if is_identifier name then Some (name, body) else None)

let sites_of_file ~file text =
  List.map (fun (name, body) ->
    let free_text = references body
      |> List.filter (fun reference ->
        List.mem reference builtins && not (writable_builtin reference))
      |> List.sort_uniq String.compare in
    { file; name; alternatives = widest_choice body; free_text }) (productions text)

let read_file path = try Some (In_channel.with_open_bin path In_channel.input_all) with Sys_error _ -> None

let census_files paths =
  List.concat_map (fun path ->
    match read_file path with None -> [] | Some text -> sites_of_file ~file:path text) paths

let site_id site = site.file ^ ":" ^ site.name

let share sites =
  List.fold_left (fun (bounded_count, total) site ->
    ((if bounded site then bounded_count + 1 else bounded_count), total + 1)) (0, 0) sites
