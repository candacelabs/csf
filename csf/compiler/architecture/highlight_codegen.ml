let provenance : Codegen_header.provenance = {
  sources = ["csf/compiler/architecture/language.ebnf";
             "csf/compiler/architecture/frontend_lexer.ml"];
  generator = "csf/compiler/architecture/highlight_codegen.ml";
  owner = "//csf/compiler/architecture:highlight_codegen";
  regenerate = "bash csf/editor/generate.sh write";
}

(* Editor projection of the compiler's EBNF AST. Syntax stays owned by EBNF;
   the external identifier scanner preserves the lexer's adjacent-arrow rule
   and globally reserved words, which a context-sensitive regex lexer loses. *)
module Grammar = Frontend_grammar
let obj fields = `Assoc fields
let str value = `String value
let typed kind fields = obj (("type", str kind) :: fields)
let symbol name = typed "SYMBOL" ["name", str name]
let pattern value = typed "PATTERN" ["value", str value]
let rec expression = function
  | Grammar.Terminal "" -> typed "BLANK" []
  | Grammar.Terminal value -> typed "STRING" ["value", str value]
  | Grammar.Reference name -> symbol name
  | Grammar.Sequence parts -> members "SEQ" parts
  | Grammar.Choice parts -> members "CHOICE" parts
  | Grammar.Optional body -> typed "CHOICE" ["members", `List [expression body; typed "BLANK" []]]
  | Grammar.Repeat body -> typed "REPEAT" ["content", expression body]
and members kind parts = typed kind ["members", `List (List.map expression parts)]

let ordered_rules (grammar : Grammar.t) =
  Hashtbl.to_seq_values grammar.Grammar.rules |> List.of_seq
  |> List.sort (fun (a : Grammar.rule) b ->
    if a.name = b.name then 0 else if a.name = grammar.start then -1
    else if b.name = grammar.start then 1 else String.compare a.name b.name)

let ascii_characters predicate = List.init 128 Char.chr |> List.filter predicate
let character_class predicate =
  "[" ^ String.concat "" (List.map (fun c -> Printf.sprintf "\\x%02x" (Char.code c))
    (ascii_characters predicate)) ^ "]"

let grammar_json grammar =
  let rules = List.map (fun (rule : Grammar.rule) -> rule.name, expression rule.body)
    (ordered_rules grammar) in
  let token body = typed "TOKEN" ["content", body] in
  (* These are projections of the fixed lexical builtins, not EBNF productions.
     JSON escape shape is highlighted; the compiler still validates Unicode
     surrogate pairing and source limits. *)
  let builtins = [
    "_word", pattern (character_class Frontend_lexer.first ^ character_class Frontend_lexer.rest ^ "*");
    "integer", token (pattern "[0-9]+");
    "string", token (pattern "\"([^\"\\\\\x00-\x1f]|\\\\([\"\\\\/bfnrt]|u[0-9a-fA-F]{4}))*\"");
    "comment", token (pattern "//[^\n]*");
  ] in
  Yojson.Safe.pretty_to_string (obj [
    "name", str "csf";
    "word", str "_word";
    "rules", obj (rules @ builtins);
    "extras", `List [pattern "[ \t\r\n]"; symbol "comment"];
    "externals", `List [symbol "identifier"];
    "conflicts", `List [];
    "inline", `List [];
    "supertypes", `List [];
  ]) ^ "\n"

let query grammar =
  let literals capture values =
    if values = [] then "" else
      "[\n" ^ String.concat "" (List.map (fun value ->
        "  " ^ Yojson.Safe.to_string (str value) ^ "\n") values) ^ "] @" ^ capture ^ "\n" in
  let words, punctuation = List.partition Frontend_lexer.is_word grammar.Grammar.terminals in
  let brackets, operators = List.partition (fun text -> List.mem text ["{"; "}"; "["; "]"; "("; ")"]) punctuation in
  let delimiters, operators = List.partition (fun text -> List.mem text [";"; ","; ":"]) operators in
  Codegen_header.render ~provenance Codegen_header.Sexp ^
  "(comment) @comment\n(string) @string\n(integer) @number\n(identifier) @variable\n" ^
  literals "keyword" words ^ literals "punctuation.bracket" brackets ^
  literals "punctuation.delimiter" delimiters ^ literals "operator" (List.filter ((<>) "") operators)

let scanner grammar =
  let letters predicate = ascii_characters predicate
    |> List.map (fun c -> Printf.sprintf "c == %d" (Char.code c)) |> String.concat " || " in
  let words = List.filter Frontend_lexer.is_word grammar.Grammar.terminals in
  let longest = List.fold_left (fun n s -> max n (String.length s)) 0 words in
  let keywords = String.concat "" (List.map (fun word ->
    Printf.sprintf "  if (length == %d && memcmp(word, %s, %d) == 0) return false;\n"
      (String.length word) (Yojson.Safe.to_string (str word)) (String.length word)) words) in
  Codegen_header.render ~provenance Codegen_header.C ^
  Printf.sprintf {|#include "tree_sitter/parser.h"
#include <string.h>
static bool first(int32_t c) { return %s; }
static bool rest(int32_t c) { return %s; }
void *tree_sitter_csf_external_scanner_create(void) { return NULL; }
void tree_sitter_csf_external_scanner_destroy(void *payload) { (void)payload; }
unsigned tree_sitter_csf_external_scanner_serialize(void *payload, char *buffer) {
  (void)payload; (void)buffer; return 0;
}
void tree_sitter_csf_external_scanner_deserialize(void *payload, const char *buffer, unsigned length) {
  (void)payload; (void)buffer; (void)length;
}
bool tree_sitter_csf_external_scanner_scan(void *payload, TSLexer *lexer, const bool *valid) {
  (void)payload;
  if (!valid[0]) return false;
  while (lexer->lookahead == ' ' || lexer->lookahead == '\t' ||
         lexer->lookahead == '\r' || lexer->lookahead == '\n') lexer->advance(lexer, true);
  if (!first(lexer->lookahead)) return false;
  char word[%d];
  unsigned length = 0;
  while (rest(lexer->lookahead)) {
    int32_t c = lexer->lookahead;
    lexer->advance(lexer, false);
    if (c == '-' && lexer->lookahead == '>') break;
    if (length < sizeof(word)) word[length] = (char)c;
    ++length;
    lexer->mark_end(lexer);
  }
%s  lexer->result_symbol = 0;
  return true;
}
|} (letters Frontend_lexer.first) (letters Frontend_lexer.rest) (max 1 longest) keywords

let generate ~grammar =
  let grammar = Grammar.parse ~filename:"<grammar>" grammar in
  let reserved = ["comment"; "_word"] in
  List.iter (fun name -> if Hashtbl.mem grammar.rules name then
    Grammar.grammar_error (Hashtbl.find grammar.rules name).at ("editor-reserved rule " ^ name)) reserved;
  ["src/grammar.json", grammar_json grammar;
   "src/scanner.c", scanner grammar;
   "queries/highlights.scm", query grammar]
