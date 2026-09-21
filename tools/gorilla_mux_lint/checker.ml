module Node = Tree_sitter.Node

external go_language : unit -> nativeint = "candace_tree_sitter_go"

let go_parser = lazy (Tree_sitter.Parser.create (Tree_sitter.Language.of_address (go_language ())))

let banned = [
  "github.com/gorilla/mux";
  "github.com/getkin/kin-openapi/routers/gorillamux";
  "github.com/oapi-codegen/gin-middleware";
]

type issue = { path : string; line : int; message : string }
type report = { files : int; findings : issue list; errors : issue list }

let forbidden dependency = List.exists (fun path ->
  dependency = path || String.starts_with ~prefix:(path ^ "/") dependency) banned

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

let import_header_end source root =
  let declarations = ["function_declaration"; "method_declaration";
    "type_declaration"; "const_declaration"; "var_declaration"] in
  let boundary = ref (String.length source) in
  for index = 0 to Node.named_child_count root - 1 do
    match Node.named_child root index with
    | Some node when List.mem (Node.kind node) declarations ->
        boundary := min !boundary (Node.start_byte node)
    | _ -> ()
  done;
  !boundary

let import_dependencies path source =
  let tree = Tree_sitter.Parser.parse_string (Lazy.force go_parser) source in
  let root = Tree_sitter.Tree.root_node tree in
  (* Imports precede declarations in Go. Reparse that prefix so language
     additions inside declaration bodies do not turn an import check into a
     full-language gate. Only recognized top-level declarations end the prefix;
     an ERROR node can never make malformed imports disappear. *)
  let boundary = import_header_end source root in
  let syntax_root = if boundary = String.length source then root else
    Tree_sitter.Parser.parse_string (Lazy.force go_parser) (String.sub source 0 boundary)
    |> Tree_sitter.Tree.root_node in
  let findings = ref [] and errors = ref [] in
  let issue node message = {path; line = (Node.start_point node).row + 1; message} in
  let check node =
    try
      let dependency = string_value (node_source source node) in
      if forbidden dependency then
        findings := issue node ("forbidden dependency " ^ dependency) :: !findings
    with Invalid_argument message -> errors := issue node message :: !errors in
  walk (fun node ->
    if Node.is_error node || Node.is_missing node then
      errors := issue node "upstream grammar could not parse this syntax" :: !errors
  ) syntax_root;
  let inspect_dependency node =
    if Node.kind node = "import_spec" then
      match Node.child_by_field_name node "path" with
      | Some imported -> check imported
      | None -> errors := issue node "import declaration has no path" :: !errors in
  walk inspect_dependency syntax_root;
  (* Keep any imports recovered after the header visible as well. A misplaced
     import should not bypass the dependency policy merely because Go rejects
     its declaration order independently. *)
  if boundary < String.length source then
    walk (fun node -> if Node.start_byte node >= boundary then inspect_dependency node) root;
  if Node.has_error syntax_root && !errors = [] then
    errors := [issue syntax_root "upstream grammar could not parse this syntax"];
  List.rev !findings, List.rev !errors

(* Module/workspace files are a dependency-token policy, not a second grammar
   validator. Go owns directive syntax. Preserve quoted whitespace and comment
   markers, decode quoted values, and never accept malformed quoted tokens. *)
let manifest_dependencies path source =
  let length = String.length source in
  let findings = ref [] and errors = ref [] in
  let space = function ' ' | '\t' | '\r' | '\n' | '\011' | '\012' -> true | _ -> false in
  let comment index = index + 1 < length && source.[index] = '/' && source.[index + 1] = '/' in
  let arrow index = index + 1 < length && source.[index] = '=' && source.[index + 1] = '>' in
  let rec quoted_end index =
    if index >= length || source.[index] = '\n' then invalid_arg "unterminated quoted string"
    else if source.[index] = '"' then index + 1
    else if source.[index] = '\\' then begin
      if index + 1 >= length || source.[index + 1] = '\n' then invalid_arg "incomplete string escape";
      quoted_end (index + 2)
    end else quoted_end (index + 1) in
  let rec token_end index =
    if index >= length || space source.[index] || comment index || arrow index
       || List.mem source.[index] ['('; ')'; '"'] then index
    else token_end (index + 1) in
  let rec scan index line =
    if index < length then
      if source.[index] = '\n' then scan (index + 1) (line + 1)
      else if space source.[index] || List.mem source.[index] ['('; ')'] then scan (index + 1) line
      else if comment index then
        (match String.index_from_opt source index '\n' with Some ending -> scan ending line | None -> ())
      else if arrow index then scan (index + 2) line
      else
        let issue message = {path; line; message} in
        try
          let ending = if source.[index] = '"' then quoted_end (index + 1) else token_end index in
          let token = String.sub source index (ending - index) in
          let dependency = if source.[index] = '"' then string_value token else token in
          if forbidden dependency then findings := issue ("forbidden dependency " ^ dependency) :: !findings;
          scan ending line
        with Invalid_argument message -> errors := issue message :: !errors in
  scan 0 1;
  List.rev !findings, List.rev !errors

let words line =
  String.map (function '\t' | '\r' | '\011' | '\012' -> ' ' | char -> char) line
  |> String.split_on_char ' ' |> List.filter ((<>) "")

let checksum_dependencies path source =
  let findings = ref [] and errors = ref [] in
  source |> String.split_on_char '\n' |> List.iteri (fun index line ->
    let issue message = {path; line = index + 1; message} in
    match words line with
    | [] -> ()
    | [dependency; _version; _checksum] ->
        if forbidden dependency then findings := issue ("forbidden dependency " ^ dependency) :: !findings
    | _ -> errors := issue "expected a module, version and checksum" :: !errors);
  List.rev !findings, List.rev !errors

let selected path = Filename.check_suffix path ".go" ||
  List.mem (Filename.basename path) ["go.mod"; "go.sum"; "go.work"; "go.work.sum"]

let inspect path source =
  (* Upstream grammars spell some line terminators explicitly; Go also accepts
     EOF after the final record. Preserve every original byte and offset. *)
  let terminated = if source = "" || String.ends_with ~suffix:"\n" source then source else source ^ "\n" in
  if Filename.check_suffix path ".go" then import_dependencies path terminated
  else match Filename.basename path with
    | "go.mod" | "go.work" -> manifest_dependencies path source
    | "go.sum" | "go.work.sum" -> checksum_dependencies path source
    | _ -> [], []

let read_file path = In_channel.with_open_bin path In_channel.input_all

let inventory contents =
  if contents = "" then []
  else if contents.[String.length contents - 1] <> '\000' then
    invalid_arg "file inventory must end with a NUL byte"
  else
    String.sub contents 0 (String.length contents - 1) |> String.split_on_char '\000'
    |> List.sort_uniq String.compare

(* Shared checkout-path boundary; callers choose the accepted final file kind. *)
let contained_path root path =
  let parts = String.split_on_char '/' path in
  if not (Filename.is_relative path) || List.exists (fun part -> List.mem part [""; "."; ".."]) parts then
    invalid_arg "inventory path must be relative and remain inside the checkout";
  List.fold_left (fun parent part ->
    let current = Filename.concat parent part in
    if (Unix.lstat current).Unix.st_kind = Unix.S_LNK then invalid_arg "source or module symlinks are not supported";
    current) root parts

let checked_path root path =
  let file = contained_path root path in
  if (Unix.lstat file).Unix.st_kind <> Unix.S_REG then invalid_arg "expected a regular source or module file";
  file

let check root paths =
  let files = List.filter selected paths |> List.sort_uniq String.compare in
  if files = [] then {files = 0; findings = []; errors = [{path = "inventory"; line = 1; message = "no Go source or module files found"}]}
  else
    List.fold_left (fun report path ->
      try
        let source = read_file (checked_path root path) in
        let findings, errors = inspect path source in
        {report with findings = report.findings @ findings; errors = report.errors @ errors}
      with
      | Sys_error message | Invalid_argument message | Failure message ->
          {report with errors = report.errors @ [{path; line = 1; message}]}
      | Unix.Unix_error (error, operation, _) ->
          {report with errors = report.errors @ [{path; line = 1; message = operation ^ ": " ^ Unix.error_message error}]}
    ) {files = List.length files; findings = []; errors = []} files

let status report = if report.errors <> [] then 2 else if report.findings <> [] then 1 else 0

let print_report report =
  let print channel issue = Printf.fprintf channel "%s:%d: %s\n" issue.path issue.line issue.message in
  List.iter (print stdout) report.findings;
  List.iter (print stderr) report.errors;
  Printf.printf "Gorilla mux dependency check: %d findings, %d errors in %d files\n"
    (List.length report.findings) (List.length report.errors) report.files
