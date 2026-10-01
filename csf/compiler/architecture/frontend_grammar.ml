(* This module reads the language definition in Extended Backus-Naur Form
   (EBNF, the .ebnf file), not an architecture
   document (.csf). Its expression tree is interpreted by Frontend and also
   supplies Symbol_codegen with the vocabulary used by the typed decoder. *)
open Frontend_lexer

type expression = Terminal of string | Reference of string | Sequence of expression list
                | Choice of expression list | Optional of expression | Repeat of expression
type rule = { name : string; body : expression; at : Model.location }
type t = { rules : (string, rule) Hashtbl.t; start : string; terminals : string list }
type reader = { tokens : token array; mutable index : int }

(* Builtins are lexer-owned token classes, not user-defined productions. Keeping
   their inventory here also lets generated Rule constructors cover concrete
   syntax tree leaves: token nodes with no children. *)
let builtin_names = ["identifier"; "integer"; "string"]
let builtin name = List.mem name builtin_names
let current reader = reader.tokens.(reader.index)
let grammar_error at message = fail at "CSF_GRAMMAR" message
let accept reader symbol =
  if (current reader).kind = Symbol symbol then (reader.index <- reader.index + 1; true)
  else false
let expect reader symbol =
  if not (accept reader symbol) then
    grammar_error (current reader).at ("expected " ^ symbol ^ " in EBNF grammar")

(* The mutually recursive [let rec ... and ...] functions encode precedence:
   comma binds within each alternative, then | joins alternatives. For example,
   ["entrypoint", string] becomes Optional (Sequence [Terminal ...; Reference
   ...]); brackets are grammar notation, not tokens expected in the .csf file. *)
let rec alternative reader depth =
  let first = sequence reader depth in
  let branches = ref [first] in
  while accept reader "|" do branches := sequence reader depth :: !branches done;
  match List.rev !branches with [body] -> body | branches -> Choice branches
and sequence reader depth =
  let first = atom reader depth in
  let parts = ref [first] in
  while accept reader "," do parts := atom reader depth :: !parts done;
  match List.rev !parts with [body] -> body | parts -> Sequence parts
and atom reader depth =
  if depth > 128 then limit (current reader).at "EBNF nesting exceeds 128 levels";
  let token = current reader in
  reader.index <- reader.index + 1;
  match token.kind with
  | Word name -> Reference name
  | Quoted value -> Terminal value
  | Symbol ("(" | "[" | "{" as symbol) ->
      let body = alternative reader (depth + 1) in
      let closing = match symbol with "(" -> ")" | "[" -> "]" | _ -> "}" in
      expect reader closing;
      (match symbol with "[" -> Optional body | "{" -> Repeat body | _ -> body)
  | _ -> grammar_error token.at "expected a rule, terminal or group in EBNF grammar"

(* Declaration order determines only the start rule: the first production is
   the document entry point. The table permits forward references; duplicate
   names and attempts to redefine lexical builtins are rejected immediately. *)
let definitions tokens =
  let reader = { tokens; index = 0 } in
  let rules = Hashtbl.create 32 and order = ref [] in
  while (current reader).kind <> End do
    let token = current reader in
    let name = match token.kind with
      | Word name -> name
      | _ -> grammar_error token.at "expected an EBNF rule name" in
    if builtin name then grammar_error token.at ("cannot redefine lexical builtin " ^ name);
    if Hashtbl.mem rules name then grammar_error token.at ("duplicate grammar rule " ^ name);
    if Hashtbl.length rules >= 256 then limit token.at "grammar exceeds 256 rules";
    reader.index <- reader.index + 1;
    expect reader "=";
    let body = alternative reader 0 in
    expect reader ";";
    Hashtbl.add rules name { name; body; at = token.at };
    order := name :: !order
  done;
  match List.rev !order with
  | [] -> grammar_error tokens.(0).at "grammar has no rules"
  | start :: _ -> rules, start

let rec visit action expression =
  action expression;
  match expression with
  | Sequence parts | Choice parts -> List.iter (visit action) parts
  | Optional body | Repeat body -> visit action body
  | Terminal _ | Reference _ -> ()

let rec nullable names = function
  | Terminal text -> text = ""
  | Reference name -> Hashtbl.mem names name
  | Sequence parts -> List.for_all (nullable names) parts
  | Choice parts -> List.exists (nullable names) parts
  | Optional _ | Repeat _ -> true

(* A rule is nullable if it can accept no tokens. Iterate to a fixed point
   because that fact may travel through forward references over several passes;
   the set only grows, so at most one addition per rule is possible. *)
let nullable_rules rules =
  let names = Hashtbl.create 16 and changed = ref true in
  while !changed do
    changed := false;
    Hashtbl.iter (fun name rule ->
      if not (Hashtbl.mem names name) && nullable names rule.body then
        (Hashtbl.add names name (); changed := true)) rules
  done;
  names

(* Follow only references reachable before consuming a token. A nullable prefix
   exposes the rest of a sequence too: a = ["x"], a is left recursive despite
   starting with an optional terminal. Such cycles cannot use ordinary recursive
   descent or this parser's completed-result memoization safely. *)
let rec left_references names = function
  | Reference name when not (builtin name) -> [name]
  | Reference _ | Terminal _ -> []
  | Optional body | Repeat body -> left_references names body
  | Choice parts -> List.concat_map (left_references names) parts
  | Sequence [] -> []
  | Sequence (first :: rest) ->
      left_references names first @
      if nullable names first then left_references names (Sequence rest) else []

let check_left_recursion rules names =
  let finished = Hashtbl.create 32 in
  let rec walk path name =
    let rule = Hashtbl.find rules name in
    if List.mem name path then grammar_error rule.at ("unsupported left recursion through " ^ name);
    if not (Hashtbl.mem finished name) then begin
      List.iter (walk (name :: path)) (left_references names rule.body);
      Hashtbl.add finished name ()
    end in
  Hashtbl.iter (fun name _ -> walk [] name) rules

(* A terminal is empty, or exactly one unchanged word, decimal integer or
   symbol token under the source lexer. Quoted strings use the string builtin.
   Comment/whitespace-leading literals and mixed word-prefix literals such as
   "hello!" are unsupported: write separate terminals for separate tokens. *)
let validate_terminal at text =
  if text <> "" then begin
    let supported =
      try match scan ~filename:at.Model.file ~grammar:false ~symbols:[text] text with
        | [| { kind = (Word value | Integer value | Symbol value); _ }; { kind = End; _ } |] ->
            value = text
        | _ -> false
      with Error _ -> false in
    if not supported then grammar_error at
      (Printf.sprintf "unsupported terminal %S: expected one word, integer or symbol token" text)
  end

(* These are conservative execution limits on the supported EBNF subset, not
   architecture checks. In particular, { ["x"] } is forbidden: its body can
   succeed without advancing, so repetition could produce candidates forever. *)
let validate rules =
  Hashtbl.iter (fun _ rule -> visit (function
    | Terminal text -> validate_terminal rule.at text
    | Reference name when not (builtin name || Hashtbl.mem rules name) ->
        grammar_error rule.at ("undefined grammar rule " ^ name)
    | _ -> ()) rule.body) rules;
  let names = nullable_rules rules in
  Hashtbl.iter (fun _ rule -> visit (function
    | Repeat body when nullable names body -> grammar_error rule.at "repetition body accepts empty input"
    | _ -> ()) rule.body) rules;
  check_left_recursion rules names

let terminals rules =
  let values = Hashtbl.create 64 in
  Hashtbl.iter (fun _ rule -> visit (function
    | Terminal value -> Hashtbl.replace values value ()
    | _ -> ()) rule.body) rules;
  Hashtbl.to_seq_keys values |> List.of_seq |> List.sort String.compare

let parse ~filename source =
  let at = { Model.file = filename; line = 1; column = 1 } in
  if String.length source > 65536 then limit at "grammar exceeds 64 KiB";
  let tokens =
    try scan ~filename ~grammar:true ~symbols:["="; ";"; ","; "|"; "{"; "}"; "["; "]"; "("; ")"] source
    with Error diagnostic when diagnostic.code = "CSF_SYNTAX" ->
      raise (Error { diagnostic with code = "CSF_GRAMMAR" }) in
  let rules, start = definitions tokens in
  validate rules;
  { rules; start; terminals = terminals rules }
