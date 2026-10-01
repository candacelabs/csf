(* Frontend serves arbitrary grammars; Decode serves this architecture language.
   This is their one vocabulary boundary, using variants generated from
   Extended Backus-Naur Form (EBNF), the grammar notation,
   rather than maintaining another table of accepted rule/terminal spellings. *)
module Rule = Syntax_cgen.Rule
module Terminal = Syntax_cgen.Terminal

(* Free-form identifiers and decoded strings remain text. Only fixed grammar
   tokens become Keyword values: a concrete syntax tree (CST) leaf -- a node
   with no children -- with rule "$terminal" and payload
   "existing" becomes Rule.Terminal with Keyword Terminal.Existing. *)
type value = Branch | Lexeme of string | Keyword of Terminal.t
type node = {
  rule : Rule.t;
  value : value;
  children : node list;
  at : Model.location;
}

exception Invalid_node of Model.diagnostic

let invalid (node : Frontend.node) message =
  raise (Invalid_node { at = node.at; code = "CSF_MODEL"; message })

let resolve_rule (node : Frontend.node) =
  match Rule.of_string node.rule with
  | Some rule -> rule
  | None -> invalid node ("unsupported grammar rule: " ^ node.rule)

let literal rule (node : Frontend.node) =
  match node.value, node.children with
  | Some value, [] -> value
  | _ -> invalid node ("expected " ^ Rule.name rule ^ " literal without children")

let resolve_value rule (node : Frontend.node) =
  match rule with
  | Rule.Identifier | Rule.Integer | Rule.String -> Lexeme (literal rule node)
  | Rule.Terminal ->
      let value = literal rule node in
      (match Terminal.of_string value with
      | Some keyword -> Keyword keyword
      | None -> invalid node ("unsupported grammar terminal: " ^ value))
  | _ ->
      if node.value <> None then
        invalid node ("expected " ^ Rule.name rule ^ " rule without a literal value");
      Branch

(* Rebuild every node, preserving child order and source locations. Unknown
   vocabulary or malformed leaves fail instead of being dropped, so a changed
   grammar cannot silently lose information before semantic decoding. Decode
   still owns which children each architecture production must contain. *)
let rec convert (node : Frontend.node) =
  let rule = resolve_rule node in
  let value = resolve_value rule node in
  { rule; value; children = List.map convert node.children; at = node.at }

(* The local exception short-circuits recursive traversal; callers see a typed
   result and a source diagnostic, not an exception or process exit. *)
let of_frontend node =
  try Ok (convert node) with Invalid_node diagnostic -> Error [diagnostic]
