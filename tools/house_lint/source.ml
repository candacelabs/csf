module Node = Tree_sitter.Node

type file = { path : string; source : string; tree : Tree_sitter.Tree.t; root : Node.t }
type finding = { path : string; line : int; rule : string; message : string }

let children node = List.init (Node.named_child_count node) (Node.named_child node)
  |> List.filter_map Fun.id
let field = Node.child_by_field_name
let kind = Node.kind
let text file node = Checker.node_source file.source node
let field_text file node name = Option.map (text file) (field node name)
let walk = Checker.walk
let descendants node =
  let result = ref [] in walk (fun child -> result := child :: !result) node; List.rev !result
let issue (file : file) node rule message =
  {path = file.path; line = (Node.start_point node).row + 1; rule; message}
let parse path source =
  let source = if String.ends_with ~suffix:"\n" source then source else source ^ "\n" in
  let tree = Tree_sitter.Parser.parse_string (Lazy.force Checker.go_parser) source in
  {path; source; tree; root = Tree_sitter.Tree.root_node tree}
let exported name = String.length name > 0 && name.[0] >= 'A' && name.[0] <= 'Z'
let package file = children file.root |> List.find_opt (fun node -> kind node = "package_clause")
  |> Option.map (fun node -> children node |> List.map (text file) |> String.concat "")
let is_test (file : file) = Filename.check_suffix file.path "_test.go"
let excluded path =
  List.exists (fun prefix -> String.starts_with ~prefix path)
    ["research/"; "vendor/"; "pkg/gotth/bench/"; "examples/gotth/";
     "pkg/gotth/docs/guide/_samples/"] ||
  List.mem "vendor" (String.split_on_char '/' path)
let standard_generated source = String.split_on_char '\n' source |> List.exists (fun line ->
  let line = if String.ends_with ~suffix:"\r" line then String.sub line 0 (String.length line - 1) else line in
  String.starts_with ~prefix:"// Code generated " line && String.ends_with ~suffix:" DO NOT EDIT." line)
let generated file =
  if standard_generated file.source then true else
  (* Decorative banners share the compiler's configured ownership markers.
     Only leading comments establish ownership, never strings or body comments
     that happen to discuss the generator. *)
  let header = children file.root |> List.take_while (fun node -> kind node = "comment")
    |> List.map (text file) |> String.concat "\n" in
  Generated_policy.contains header Codegen_header.disclaimer &&
  List.exists (Generated_policy.contains ~whole_words:true header) Codegen_header.ownership_markers
let selected (file : file) = not (excluded file.path) && not (generated file)

(* The name an unaliased import binds: its last path element, skipping a
   module major-version suffix (github.com/jackc/pgx/v5 binds pgx). *)
let default_import_name path =
  let is_major_version segment =
    String.length segment > 1 && segment.[0] = 'v' &&
    String.for_all (fun character -> character >= '0' && character <= '9')
      (String.sub segment 1 (String.length segment - 1)) in
  match List.rev (String.split_on_char '/' path) with
  | version :: name :: _ when is_major_version version -> name
  | name :: _ -> name
  | [] -> path

let imports file = descendants file.root |> List.filter_map (fun node ->
  if kind node <> "import_spec" then None else
  match field_text file node "path" with
  | None -> None
  | Some literal ->
      let path = Checker.string_value literal in
      let alias = Option.value (field_text file node "name") ~default:(default_import_name path) in
      Some (alias, path))
