(* A generic concrete syntax tree (CST) retains every named production and token.
   It deliberately uses grammar spellings here: callers can supply different
   grammars. Typed_tree performs the architecture-specific vocabulary conversion
   once, before Decode consumes the tree into Model values. *)
type node = {
  rule : string;
  value : string option;
  children : node list;
  at : Model.location;
}

open Frontend_lexer
module Grammar = Frontend_grammar

(* Partial matches keep children reversed, sharing prefixes through repetition.
   A named production reverses them once when constructing its public CST node.
   Multiple matches at the same position are intentional, not parser failure. *)
type matched = { next : int; nodes : node list }
type parser = {
  grammar : Grammar.t;
  tokens : token array;
  memo : ((string * int), matched list) Hashtbl.t;
  mutable steps : int;
  mutable furthest : int;
  mutable expected : string list;
}

let spend parser index =
  parser.steps <- parser.steps + 1;
  if parser.steps > 1000000 then limit parser.tokens.(index).at "parser exceeds 1000000 steps"

let missing parser index expected =
  if index > parser.furthest then (parser.furthest <- index; parser.expected <- []);
  if index = parser.furthest && not (List.mem expected parser.expected) then
    parser.expected <- expected :: parser.expected;
  []

let leaf parser index rule value =
  [{ next = index + 1;
     nodes = [{ rule; value = Some value; children = []; at = parser.tokens.(index).at }] }]

(* All literal word terminals are reserved throughout the supplied grammar.
   Thus an identifier cannot silently absorb a keyword intended for a later
   production, even when that keyword occurs in a different branch. *)
let lexical parser name index =
  match name, parser.tokens.(index).kind with
  | "identifier", Word value when not (List.mem value parser.grammar.terminals) -> leaf parser index name value
  | "integer", Integer value | "string", Quoted value -> leaf parser index name value
  | _ -> missing parser index name

let terminal parser text index =
  if text = "" then [{ next = index; nodes = [] }]
  else match parser.tokens.(index).kind with
    | Word value | Symbol value | Integer value when value = text -> leaf parser index "$terminal" value
    | _ -> missing parser index (Printf.sprintf "%S" text)

(* Cache complete candidate lists by rule and token offset. The grammar has
   already rejected left recursion, so a recursive call cannot depend on an
   unfinished entry at the same offset. Memoization saves repeated work; the
   step/depth limits still bound highly branching inputs. *)
let rec named parser depth name index =
  spend parser index;
  if depth > 256 then limit parser.tokens.(index).at "parser nesting exceeds 256 levels";
  if Grammar.builtin name then lexical parser name index
  else match Hashtbl.find_opt parser.memo (name, index) with
    | Some results -> results
    | None ->
        let rule = Hashtbl.find parser.grammar.rules name in
        let results = evaluate parser (depth + 1) rule.body index |> List.map (fun matched ->
          { matched with nodes = [{ rule = name; value = None; children = List.rev matched.nodes;
                                    at = parser.tokens.(index).at }] }) in
        Hashtbl.add parser.memo (name, index) results;
        results
(* Keep alternatives, optional skips and repetition lengths until their caller
   has checked the remaining sequence. For ["a"], "a" on input a, taking the
   optional branch first fails later, but the skipped branch must still succeed.
   Committing to the first locally successful branch would change the language. *)
and evaluate parser depth expression index =
  spend parser index;
  if depth > 256 then limit parser.tokens.(index).at "parser nesting exceeds 256 levels";
  match expression with
  | Grammar.Terminal text -> terminal parser text index
  | Grammar.Reference name -> named parser depth name index
  | Grammar.Sequence parts -> sequence parser depth parts [{ next = index; nodes = [] }]
  | Grammar.Choice alternatives -> List.concat_map (fun branch -> evaluate parser (depth + 1) branch index) alternatives
  | Grammar.Optional body -> evaluate parser (depth + 1) body index @ [{ next = index; nodes = [] }]
  | Grammar.Repeat body -> repetition parser depth body index
and sequence parser depth parts starts =
  match parts with
  | [] -> starts
  | part :: rest ->
      let results = List.concat_map (fun previous ->
        evaluate parser (depth + 1) part previous.next |> List.map (fun matched ->
          spend parser matched.next;
          { matched with nodes = matched.nodes @ previous.nodes })) starts in
      sequence parser depth rest results
and repetition parser depth body index =
  let starts = [{ next = index; nodes = [] }] in
  let rec expand frontier accepted =
    let next = List.concat_map (fun previous ->
      evaluate parser (depth + 1) body previous.next |> List.map (fun matched ->
        spend parser matched.next;
        if matched.next <= previous.next then
          fail parser.tokens.(matched.next).at "CSF_GRAMMAR" "repetition made no progress";
        { matched with nodes = matched.nodes @ previous.nodes })) frontier in
    match next with [] -> accepted | _ -> expand next (List.rev_append next accepted) in
  expand starts starts

let source_tokens grammar filename source =
  let symbols = List.filter (fun value ->
    value <> "" && not (is_word value) && not (String.for_all digit value)) grammar.Grammar.terminals in
  scan ~filename ~grammar:false ~symbols source

(* Acceptance requires reaching the End sentinel, not merely matching a valid
   prefix. If several candidates consume the whole document, the first is used;
   this parser does not prove that the supplied grammar is unambiguous. Internal
   diagnostic exceptions become an explicit result at the public boundary. *)
let parse ~grammar_filename ~grammar ~source ~filename =
  try
    let grammar = Grammar.parse ~filename:grammar_filename grammar in
    let tokens = source_tokens grammar filename source in
    let parser = { grammar; tokens; memo = Hashtbl.create 128; steps = 0; furthest = 0; expected = [] } in
    let results = named parser 0 grammar.start 0 in
    let finish = Array.length tokens - 1 in
    match List.find_opt (fun result -> result.next = finish) results with
    | Some { nodes = [root]; _ } -> Ok root
    | _ ->
        List.iter (fun result -> ignore (missing parser result.next "end of input")) results;
        let expected = List.sort_uniq String.compare parser.expected |> String.concat ", " in
        syntax tokens.(parser.furthest).at ("expected " ^ expected)
  with Error diagnostic -> Error [diagnostic]

let parse_text ~grammar ~source ~filename =
  parse ~grammar_filename:"<grammar>" ~grammar ~source ~filename

(* Read one byte beyond the allowance to distinguish an exact-size file from
   an oversized one without first loading an unbounded file into memory. *)
let read_file path maximum =
  In_channel.with_open_bin path (fun channel ->
    let bytes = Bytes.create (maximum + 1) in
    let rec fill offset =
      if offset > maximum then
        limit { Model.file = path; line = 1; column = 1 } "input file exceeds parser size limit";
      let count = input channel bytes offset (maximum + 1 - offset) in
      if count = 0 then Bytes.sub_string bytes 0 offset else fill (offset + count) in
    fill 0)

let parse_files ~grammar_path ~source_path =
  let read path maximum =
    try read_file path maximum with Sys_error message ->
      fail { Model.file = path; line = 1; column = 1 } "CSF_IO" message in
  try
    let grammar = read grammar_path 65536 in
    let source = read source_path (2 * 1024 * 1024) in
    parse ~grammar_filename:grammar_path ~grammar ~source ~filename:source_path
  with Error diagnostic -> Error [diagnostic]
