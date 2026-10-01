module Node = Tree_sitter.Node

external go_language : unit -> nativeint = "candace_tree_sitter_go"

let go_parser = lazy (Tree_sitter.Parser.create (Tree_sitter.Language.of_address (go_language ())))


(* Interpret a literal already identified by the upstream grammar. This owns
   Go's string values, not lexical or syntactic recognition of Go source. *)
let string_value literal =
  let length = String.length literal in
  if length = 0 then invalid_arg "empty dependency path";
  if literal.[0] = '`' then begin
    if length < 2 || literal.[length - 1] <> '`' then invalid_arg "unterminated raw string";
    String.sub literal 1 (length - 2) |> String.to_seq
    |> Seq.filter ((<>) '\r') |> String.of_seq
  end else if literal.[0] <> '"' then literal
  else begin
    if length < 2 || literal.[length - 1] <> '"' then invalid_arg "unterminated quoted string";
    let output = Buffer.create length in
    let digit base char =
      let value = match char with
        | '0' .. '9' -> Char.code char - Char.code '0'
        | 'a' .. 'f' -> Char.code char - Char.code 'a' + 10
        | 'A' .. 'F' -> Char.code char - Char.code 'A' + 10
        | _ -> base in
      if value >= base then invalid_arg "invalid string escape";
      value in
    let number offset count base =
      if offset + count > length - 1 then invalid_arg "incomplete string escape";
      let value = ref 0 in
      for index = offset to offset + count - 1 do
        value := !value * base + digit base literal.[index]
      done;
      !value in
    let rec decode index =
      if index < length - 1 then
        if literal.[index] <> '\\' then begin
          Buffer.add_char output literal.[index]; decode (index + 1)
        end else begin
          if index + 1 >= length - 1 then invalid_arg "incomplete string escape";
          let escaped = literal.[index + 1] in
          match escaped with
          | 'x' | 'u' | 'U' ->
              let count = if escaped = 'x' then 2 else if escaped = 'u' then 4 else 8 in
              let value = number (index + 2) count 16 in
              if escaped = 'x' then Buffer.add_char output (Char.chr value)
              else begin
                if not (Uchar.is_valid value) then invalid_arg "invalid Unicode string escape";
                Buffer.add_utf_8_uchar output (Uchar.of_int value)
              end;
              decode (index + 2 + count)
          | '0' .. '7' ->
              let value = number (index + 1) 3 8 in
              if value > 255 then invalid_arg "octal string escape exceeds one byte";
              Buffer.add_char output (Char.chr value); decode (index + 4)
          | _ ->
              let value = match escaped with
                | 'a' -> '\007' | 'b' -> '\b' | 'f' -> '\012'
                | 'n' -> '\n' | 'r' -> '\r' | 't' -> '\t' | 'v' -> '\011'
                | '\\' -> '\\' | '"' -> '"'
                | _ -> invalid_arg "invalid string escape" in
              Buffer.add_char output value; decode (index + 2)
        end in
    decode 1;
    Buffer.contents output
  end

let node_source source node =
  String.sub source (Node.start_byte node) (Node.end_byte node - Node.start_byte node)

let rec walk visit node =
  visit node;
  for index = 0 to Node.named_child_count node - 1 do
    match Node.named_child node index with Some child -> walk visit child | None -> ()
  done


(* Shared checkout-path boundary; callers choose the accepted final file kind. *)
let contained_path root path =
  let parts = String.split_on_char '/' path in
  if not (Filename.is_relative path) || List.exists (fun part -> List.mem part [""; "."; ".."]) parts then
    invalid_arg "inventory path must be relative and remain inside the checkout";
  List.fold_left (fun parent part ->
    let current = Filename.concat parent part in
    if (Unix.lstat current).Unix.st_kind = Unix.S_LNK then invalid_arg "source or module symlinks are not supported";
    current) root parts
