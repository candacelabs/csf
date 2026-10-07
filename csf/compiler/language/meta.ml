(* The CSF meta language: the kinds and relations CSF declares about itself
   (slice kinds_declared, ticket #487). meta.csf is one document in this
   language; this module decodes it into typed values through the same generic
   EBNF frontend the architecture language uses. It links no existing decoder,
   so declaring a kind cannot change the output of any existing kind. *)

(** The language a kind belongs to: the documentation grammar
    (grammar.ebnf), the architecture grammar (architecture/language.ebnf),
    this meta language, or the slice planner grammar
    (architecture/slice_planner.ebnf), whose closed decision set is generated
    from the kinds the planner home declares. *)
type home = Documentation | Architecture | Meta | Planner

(** One declared kind: [name] is what the home grammar accepts, [at] is its
    declaration. *)
type kind = {
  name : string;
  home : home;
  at : Model.location;
}

(** One declared relation between two kinds. The trailing underscores on
    [from_] and [to_] leave the OCaml keywords free. *)
type relation = {
  from_ : string;
  to_ : string;
  at : Model.location;
}

(** Kinds and relations keep declaration order; that order is deterministic
    output order, not an observation of any running system. *)
type t = { kinds : kind list; relations : relation list }

exception Malformed of Model.diagnostic

let malformed at message = raise (Malformed { Model.at; code = "CSF_META"; message })

let home_of_string = function
  | "documentation" -> Some Documentation
  | "architecture" -> Some Architecture
  | "meta" -> Some Meta
  | "planner" -> Some Planner
  | _ -> None

let has_rule name (node : Frontend.node) = node.rule = name
let has_value value (node : Frontend.node) = node.value = Some value

let payload (node : Frontend.node) =
  match node.value with
  | Some value -> value
  | None -> malformed node.at "expected a token leaf"

let home_of (node : Frontend.node) =
  match node.children with
  | [ leaf ] ->
      (match home_of_string (payload leaf) with
       | Some home -> home
       | None -> malformed leaf.at ("unknown kind home " ^ payload leaf))
  | _ -> malformed node.at "a kind home is exactly one of documentation, architecture, meta or planner"

let decode_kind (node : Frontend.node) =
  match node.children with
  | [ keyword; name; preposition; home; terminator ]
    when has_value "kind" keyword && has_rule "string" name && has_value "in" preposition &&
         has_rule "home" home && has_value ";" terminator ->
      { name = payload name; home = home_of home; at = node.at }
  | _ -> malformed node.at {|malformed kind: expected 'kind "name" in documentation|architecture|meta|planner;'|}

let decode_relation (node : Frontend.node) =
  match node.children with
  | [ keyword; source; arrow; target; terminator ]
    when has_value "relation" keyword && has_rule "string" source && has_value "->" arrow &&
         has_rule "string" target && has_value ";" terminator ->
      { from_ = payload source; to_ = payload target; at = node.at }
  | _ -> malformed node.at {|malformed relation: expected 'relation "from" -> "to";'|}

let decode_declaration (node : Frontend.node) =
  match node.children with
  | [ inner ] when has_rule "kind" inner -> `Kind (decode_kind inner)
  | [ inner ] when has_rule "relation" inner -> `Relation (decode_relation inner)
  | _ -> malformed node.at "a declaration is a kind or a relation"

let decode (root : Frontend.node) =
  if not (has_rule "meta" root) then malformed root.at "expected a meta document";
  let kinds = ref [] and relations = ref [] in
  List.iter (fun (node : Frontend.node) ->
    if not (has_rule "declaration" node) then malformed node.at "expected a declaration";
    match decode_declaration node with
    | `Kind kind -> kinds := kind :: !kinds
    | `Relation relation -> relations := relation :: !relations) root.children;
  { kinds = List.rev !kinds; relations = List.rev !relations }

let decoded = function
  | Ok root -> (try Ok (decode root) with Malformed diagnostic -> Error [ diagnostic ])
  | Error diagnostics -> Error diagnostics

(** [grammar] contains meta-language EBNF; [source] contains one meta document.
    The result is the decoded kinds and relations, or syntax and [CSF_META]
    diagnostics. *)
let parse_text ~grammar ~source ~filename =
  decoded (Frontend.parse_text ~grammar ~source ~filename)

(** The same decode with bounded file reads and actual paths in diagnostics. *)
let parse_files ~grammar_path ~source_path =
  decoded (Frontend.parse_files ~grammar_path ~source_path)
