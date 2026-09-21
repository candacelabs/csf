(* Both the Extended Backus-Naur Form (EBNF) grammar and the .csf document
   pass through this lexer. The
   grammar flag selects their comment and quote conventions; it does not make
   the grammar a scannerless parser. Words, decimal integers and quoted strings
   have fixed lexical rules, while the caller supplies punctuation terminals. *)
type kind = Word of string | Integer of string | Quoted of string | Symbol of string | End
type token = { kind : kind; at : Model.location }
exception Error of Model.diagnostic

let fail at code message = raise (Error { Model.at; code; message })
let syntax at message = fail at "CSF_SYNTAX" message
let limit at message = fail at "CSF_LIMIT" message

let first = function 'a' .. 'z' | 'A' .. 'Z' | '_' -> true | _ -> false
let digit = function '0' .. '9' -> true | _ -> false
let rest c = first c || digit c || c = '-'
let is_word text =
  String.length text > 0 && first text.[0] && String.for_all rest text

(* Locations are one-based; columns count source bytes, including the bytes of
   UTF-8 (Unicode Transformation Format with 8-bit code units) characters.
   Mutability is confined to this per-scan cursor, so parsing
   one input cannot change the position reported for another. *)
type cursor = { source : string; filename : string; mutable offset : int;
                mutable line : int; mutable column : int }

let location cursor =
  { Model.file = cursor.filename; line = cursor.line; column = cursor.column }
let ended cursor = cursor.offset = String.length cursor.source
let starts cursor text =
  let length = String.length text in
  cursor.offset + length <= String.length cursor.source &&
  String.sub cursor.source cursor.offset length = text
let advance cursor =
  let character = cursor.source.[cursor.offset] in
  cursor.offset <- cursor.offset + 1;
  if character = '\n' then (cursor.line <- cursor.line + 1; cursor.column <- 1)
  else cursor.column <- cursor.column + 1;
  character
let consume cursor count = for _ = 1 to count do ignore (advance cursor) done

let block_comment cursor =
  let at = location cursor in
  consume cursor 2;
  let depth = ref 1 in
  while !depth > 0 do
    if ended cursor then syntax at "unterminated EBNF comment";
    if starts cursor "(*" then (consume cursor 2; incr depth)
    else if starts cursor "*)" then (consume cursor 2; decr depth)
    else ignore (advance cursor)
  done

let skip cursor grammar =
  let again = ref true in
  while !again && not (ended cursor) do
    match cursor.source.[cursor.offset] with
    | ' ' | '\t' | '\r' | '\n' -> ignore (advance cursor)
    | _ when grammar && starts cursor "(*" -> block_comment cursor
    | _ when not grammar && starts cursor "//" ->
        while not (ended cursor) && cursor.source.[cursor.offset] <> '\n' do
          ignore (advance cursor)
        done
    | _ -> again := false
  done

let hex = function
  | '0' .. '9' as c -> Char.code c - Char.code '0'
  | 'a' .. 'f' as c -> Char.code c - Char.code 'a' + 10
  | 'A' .. 'F' as c -> Char.code c - Char.code 'A' + 10
  | _ -> -1

let unicode_unit cursor at =
  let value = ref 0 in
  for _ = 1 to 4 do
    if ended cursor then syntax at "incomplete Unicode escape";
    let number = hex (advance cursor) in
    if number < 0 then syntax at "invalid Unicode escape";
    value := !value * 16 + number
  done;
  !value

(* JavaScript Object Notation (JSON) string escapes encode Unicode
   Transformation Format with 16-bit code units (UTF-16). A surrogate pair
   must become one Unicode
   scalar before UTF-8 emission; accepting either half alone would manufacture
   invalid text. Raw string bytes receive the equivalent UTF-8 validity check. *)
let unicode_escape cursor at buffer =
  let first = unicode_unit cursor at in
  let scalar =
    if first >= 0xd800 && first <= 0xdbff then begin
      if not (starts cursor "\\u") then syntax at "unpaired Unicode high surrogate";
      consume cursor 2;
      let second = unicode_unit cursor at in
      if second < 0xdc00 || second > 0xdfff then syntax at "invalid Unicode surrogate pair";
      0x10000 + ((first - 0xd800) lsl 10) + second - 0xdc00
    end else begin
      if first >= 0xdc00 && first <= 0xdfff then syntax at "unpaired Unicode low surrogate";
      first
    end in
  Buffer.add_utf_8_uchar buffer (Uchar.of_int scalar)

let escaped cursor at quote buffer =
  if ended cursor then syntax at "unterminated string escape";
  match advance cursor with
  | '"' -> Buffer.add_char buffer '"'
  | '\'' when quote = '\'' -> Buffer.add_char buffer '\''
  | '\\' -> Buffer.add_char buffer '\\'
  | '/' -> Buffer.add_char buffer '/'
  | 'b' -> Buffer.add_char buffer '\b'
  | 'f' -> Buffer.add_char buffer '\012'
  | 'n' -> Buffer.add_char buffer '\n'
  | 'r' -> Buffer.add_char buffer '\r'
  | 't' -> Buffer.add_char buffer '\t'
  | 'u' -> unicode_escape cursor at buffer
  | _ -> syntax at "invalid JSON string escape"

let raw_character cursor at buffer =
  let decoded = String.get_utf_8_uchar cursor.source cursor.offset in
  if not (Uchar.utf_decode_is_valid decoded) then syntax at "invalid UTF-8 in string";
  let length = Uchar.utf_decode_length decoded in
  Buffer.add_substring buffer cursor.source cursor.offset length;
  consume cursor length

(* Quoted carries decoded content, not the original delimiters or escapes:
   source "a\n" becomes a payload containing a newline. The location remains
   the opening quote in the original input for downstream diagnostics. *)
let quoted cursor =
  let at = location cursor in
  let quote = advance cursor in
  let buffer = Buffer.create 32 in
  while not (ended cursor) && cursor.source.[cursor.offset] <> quote do
    match cursor.source.[cursor.offset] with
    | '\\' -> ignore (advance cursor); escaped cursor at quote buffer
    | c when Char.code c < 0x20 -> syntax at "unescaped control character in string"
    | _ -> raw_character cursor at buffer
  done;
  if ended cursor then syntax at "unterminated quoted string";
  ignore (advance cursor);
  Quoted (Buffer.contents buffer)

(* Hyphens belong to identifiers, but an adjacent arrow ends the word:
   worker->provider must tokenize like worker -> provider. *)
let take cursor predicate =
  let start = cursor.offset in
  while not (ended cursor) && predicate cursor.source.[cursor.offset] &&
        not (starts cursor "->") do
    ignore (advance cursor)
  done;
  String.sub cursor.source start (cursor.offset - start)

let next cursor grammar symbols =
  let character = cursor.source.[cursor.offset] in
  if character = '"' || (grammar && character = '\'') then quoted cursor
  else if first character then Word (take cursor rest)
  else if digit character then Integer (take cursor digit)
  else match List.find_opt (starts cursor) symbols with
    | Some symbol -> consume cursor (String.length symbol); Symbol symbol
    | None -> syntax (location cursor) (Printf.sprintf "unexpected character %C" character)

(* Longest punctuation wins when terminals overlap. The final End sentinel
   gives the parser a location even at end of file; the array supports indexed
   backtracking without rescanning input. Size and token caps apply before
   grammar-driven parsing can multiply the number of candidate matches. *)
let scan ~filename ~grammar ~symbols source =
  let cursor = { source; filename; offset = 0; line = 1; column = 1 } in
  if String.length source > 2 * 1024 * 1024 then limit (location cursor) "source exceeds 2 MiB";
  let symbols = List.sort (fun a b -> compare (String.length b) (String.length a)) symbols in
  let tokens = ref [] and count = ref 0 in
  skip cursor grammar;
  while not (ended cursor) do
    incr count;
    if !count > 100000 then limit (location cursor) "source exceeds 100000 tokens";
    let at = location cursor in
    let kind = next cursor grammar symbols in
    tokens := { kind; at } :: !tokens;
    skip cursor grammar
  done;
  Array.of_list (List.rev ({ kind = End; at = location cursor } :: !tokens))
