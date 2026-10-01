module Node = Tree_sitter.Node

(* Bounded grammar adaptation for Go 1.26 new(expression), not Go validation.
   The compiler owns Go validity. Only erroneous syntax tree call functions named new
   are replaced in the parser's copy; policy must inspect the original source.
   Equal byte lengths preserve every source span, including nested function calls. *)
let compatibility_passes = 32
let expression_function = "cgn"

let parse_tree source =
  Tree_sitter.Parser.parse_string (Lazy.force Checker.go_parser) (source ^ "\n")
  |> Tree_sitter.Tree.root_node

let adapt_calls original adapted root =
  let changed = ref false in
  Checker.walk (fun node ->
    if Node.kind node = "call_expression" && Node.has_error node then
      match Node.child_by_field_name node "function" with
      | Some fn when Checker.node_source original fn = "new" ->
          let offset = Node.start_byte fn in
          if Bytes.sub_string adapted offset 3 = "new" then begin
            Bytes.blit_string expression_function 0 adapted offset 3;
            changed := true
          end
      | _ -> ()) root;
  !changed

(* For example, new(int64(0)) is reparsed as cgn(int64(0)) only in the
   parser's buffer. Both names occupy three bytes, preserving positions.
   Nested errors can require another pass. The finite budget prevents endless
   adaptation; any remaining error nodes still reach Go_policy as failures. *)
let parse source =
  let adapted = Bytes.of_string source in
  let rec retry remaining root =
    if remaining = 0 || not (Node.has_error root) || not (adapt_calls source adapted root) then root
    else retry (remaining - 1) (parse_tree (Bytes.to_string adapted)) in
  retry compatibility_passes (parse_tree source)
