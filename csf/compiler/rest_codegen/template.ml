(* golden_first step 4 (issue #475): the rest_client template engine.

   csf/compiler/testdata/golden/rest_client/rest_client.csf is the source. This
   module reads the values a declaration names, fills them into the one instance
   template beside it, and returns the Go projections. csf/tools/rest_codegen/generate.sh runs it
   and proves its output is the handwritten golden in testdata/golden byte for
   byte. The kind template -- the rest_client template itself -- is fully
   generic, so it is emitted verbatim.

   The csf grammar a real csfc parser would own is not in the tree yet; the
   reader below is the fixed record this slice is authored from, as the pull
   request body states. It reads only what it consumes: a declaration's
   base_url, auth, rate and retry, and the kind's template path. *)

(* A parsed `keyword name { field value; ... }` block. *)
type block = { keyword : string; name : string; fields : (string * string) list }

(* The values one rest_client instance contributes to its generated Go. *)
type declared = {
  package : string;
  base_url : string;
  hub : string;
  credential : string;
  rate : string;
  retry_max : string;
}

let fail format = Printf.ksprintf failwith format

let drop_prefix ~prefix value =
  if String.starts_with ~prefix value then
    String.sub value (String.length prefix) (String.length value - String.length prefix)
  else value

let drop_suffix ~suffix value =
  if String.ends_with ~suffix value then
    String.sub value 0 (String.length value - String.length suffix)
  else value

let words line =
  String.map (function '\t' -> ' ' | character -> character) line
  |> String.split_on_char ' ' |> List.filter (fun word -> word <> "")

let unquote value =
  if String.length value >= 2 && value.[0] = '"' && value.[String.length value - 1] = '"' then
    String.sub value 1 (String.length value - 2)
  else fail "expected a quoted string, got %S" value

(* A field's value ends at its first semicolon; an inline comment follows it. *)
let before_semicolon value =
  match String.index_opt value ';' with
  | Some stop -> String.sub value 0 stop
  | None -> value

let credential_of = function
  | value when String.starts_with ~prefix:"secret(" value -> "rest.Bearer{}"
  | "none" -> "rest.NoCredential{}"
  | value -> fail "unsupported auth: %S" value

let rate_of = function
  | "from_headers" -> "rest.FromHeaders()"
  | "none" -> "rest.NoRate()"
  | value when String.starts_with ~prefix:"fixed(" value ->
      Printf.sprintf "rest.Fixed(%s)" (String.trim (drop_suffix ~suffix:")" (drop_prefix ~prefix:"fixed(" value)))
  | value -> fail "unsupported rate: %S" value

let retry_max_of = function
  | value when String.starts_with ~prefix:"backoff(" value ->
      String.trim (drop_suffix ~suffix:")" (drop_prefix ~prefix:"backoff(" value))
  | value -> fail "unsupported retry: %S" value

(* Every block in the declaration: full-line // comments and blank lines are
   dropped, a `keyword name {` header opens one, `}` closes it, and a line
   inside it is one `key value;` field. *)
let blocks source =
  let parsed = ref [] and open_block = ref None in
  List.iter
    (fun line ->
      let trimmed = String.trim line in
      if trimmed = "" || String.starts_with ~prefix:"//" trimmed then ()
      else if trimmed = "}" then (
        match !open_block with
        | Some block ->
            parsed := { block with fields = List.rev block.fields } :: !parsed;
            open_block := None
        | None -> fail "unmatched } in the declaration")
      else
        match words trimmed with
        | [ keyword; name; "{" ] -> open_block := Some { keyword; name; fields = [] }
        | keyword :: value -> (
            match !open_block with
            | None -> fail "field outside a block: %S" trimmed
            | Some block ->
                let value = String.concat " " value |> before_semicolon |> String.trim in
                open_block := Some { block with fields = (keyword, value) :: block.fields })
        | [] -> ())
    (String.split_on_char '\n' source);
  (match !open_block with Some _ -> fail "unclosed block in the declaration" | None -> ());
  List.rev !parsed

let field key block =
  match List.assoc_opt key block.fields with
  | Some value -> value
  | None -> fail "%S is missing %s" block.name key

let kind_of blocks =
  match List.find_opt (fun block -> block.keyword = "kind" && block.name = "rest_client") blocks with
  | Some block -> block
  | None -> fail "the declaration has no kind rest_client"

let template_path blocks = unquote (field "template" (kind_of blocks))

let declared_of block =
  let base_url = unquote (field "base_url" block) in
  {
    package = block.name;
    base_url;
    hub = drop_suffix ~suffix:"/api" base_url;
    credential = credential_of (field "auth" block);
    rate = rate_of (field "rate" block);
    retry_max = retry_max_of (field "retry" block);
  }

let bindings (declaration : declared) =
  [
    "package", declaration.package;
    "base_url", declaration.base_url;
    "hub", declaration.hub;
    "credential", declaration.credential;
    "rate", declaration.rate;
    "retry_max", declaration.retry_max;
  ]

let replace_all ~pairs text =
  let length = String.length text in
  let result = Buffer.create (length + 64) in
  let rec close_from index =
    if index >= length then fail "unclosed placeholder in the instance template"
    else if text.[index] = '}' then index
    else close_from (index + 1) in
  let rec scan index =
    if index >= length then ()
    else if index + 1 < length && text.[index] = '{' && text.[index + 1] = '{' then begin
      let close = close_from (index + 2) in
      if close + 1 >= length || text.[close + 1] <> '}' then
        fail "unclosed placeholder in the instance template";
      let name = String.sub text (index + 2) (close - index - 2) in
      (match List.assoc_opt name pairs with
      | Some value -> Buffer.add_string result value
      | None -> fail "the instance template names %S, which the declaration does not fill" name);
      scan (close + 2)
    end
    else begin
      Buffer.add_char result text.[index];
      scan (index + 1)
    end in
  scan 0;
  Buffer.contents result

(* The projections, as (path, content): the kind template once, and one file
   per declared instance. *)
let generate ~kind_template ~instance_template source =
  let parsed = blocks source in
  let template = template_path parsed in
  let kind_path = template ^ "/" ^ Filename.basename template ^ ".go" in
  (kind_path, kind_template)
  :: List.map
       (fun declaration ->
         (* An instance sits in its kind's tier directory, beside the template:
            template "io/net/rest" puts hfjobs at io/net/hfjobs. *)
         ( Printf.sprintf "%s/%s/%s.go" (Filename.dirname template)
             declaration.package declaration.package,
           replace_all ~pairs:(bindings declaration) instance_template ))
       (List.map declared_of (List.filter (fun block -> block.keyword = "rest_client") parsed))
