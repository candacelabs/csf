(** The shell generator: one service declaration projected into the Go shell a
    host builds -- the package doc, the service and its constructor, one file
    per operation with its records, its generated classifier and its metrics,
    the operation's outcome suite, the package suite, its build file and its
    dashboard. The generator refuses a declaration that fans a question past
    sixteen options, leaves a question with no option to project it, or leaves a
    leaf without exactly one template: a violation is a compile error, not a
    warning. Holes are marked [// csf:hole <service>.<name>] in
    [<service>_holes.go], written once and never regenerated. Parsing and the
    projections are pure functions of the declaration, pinned byte for byte by a
    golden test. *)

open Model

exception Invalid of diagnostic

let invalid (node : Frontend.node) message =
  raise (Invalid { at = node.at; code = "CSF_SHELL"; message })

(* --- the declaration --- *)

type field = { fname : string; ftype : string }
type record = { rname : string; rfields : field list }
type outcome = { oname : string; owhen : string list option }
type operation = {
  oname : string;
  oinput : string;
  ooutput : string;
  ooutcomes : outcome list;
  ologic : (string * string list) list;
  op_at : location;
}
type option' = { oquestion : string; opreferred : string; o_at : location }
type question = { qregion : string; qtext : string; qoptions : option' list; q_at : location }

(* One declared capability: the boundary the host binds. Everything the
   generator emits for its file -- the interface, the option that sets it and
   the error a missing boundary returns -- is declared here, so the output is a
   projection of the declaration and never an invention. The noun is the human
   name the misconfiguration spec calls this boundary by. *)
type capability = {
  cname : string;
  cnoun : string;
  ccontract : string;
  cmethod : string;
  cimport_alias : string;
  cimport_path : string;
  cmissing : string;
  cmissing_text : string;
  cmissing_doc : string;
  cinterface_doc : string list;
  coption : string;
  coption_param : string;
  coption_doc : string;
  cdoc : string list;
  c_at : location;
}
type service = {
  sname : string;
  sgotype : string;
  purpose : string;
  sops : string list;
  scapabilities : string list;
  stemplate : string;
  s_at : location;
}
type directory = { dpath : string; d_at : location }
type declaration = {
  services : service list;
  operations : operation list;
  records : record list;
  capabilities : capability list;
  questions : question list;
  directories : directory list;
  decl_at : location;
}

(* --- decoding the syntax tree --- *)

type reader = { parent : Frontend.node; mutable rest : Frontend.node list }

let take rule reader = match reader.rest with
  | node :: rest when node.Frontend.rule = rule -> reader.rest <- rest; node
  | node :: _ -> invalid node (Printf.sprintf "expected %s, found %s at line %d col %d"
      rule node.Frontend.rule node.Frontend.at.line node.Frontend.at.column)
  | [] -> invalid reader.parent ("expected " ^ rule)

let literal rule reader =
  let node = take rule reader in
  match node.Frontend.value with
  | Some value -> value
  | None -> invalid node (rule ^ " without a literal value")

let terminal expected reader =
  let node = take "$terminal" reader in
  match node.Frontend.value with
  | Some value when value = expected -> ()
  | Some value -> invalid node ("expected " ^ expected ^ ", found " ^ value)
  | None -> invalid node "terminal without a value"

(* A [token] node wraps one child: an [identifier], [integer] or [string] leaf
   whose value is the decoded text, or a [symbol] node whose one child is a
   [$terminal] carrying the literal punctuation. *)
let token_text (token : Frontend.node) =
  match token.Frontend.children with
  | [leaf] -> begin match leaf.Frontend.rule with
    | "symbol" -> begin match leaf.Frontend.children with
      | [terminal] -> (match terminal.Frontend.value with
        | Some value -> value
        | None -> invalid terminal "symbol terminal without a value")
      | _ -> invalid leaf "symbol node with the wrong shape" end
    | _ -> (match leaf.Frontend.value with
      | Some value -> value
      | None -> invalid leaf "token leaf without a value") end
  | _ -> invalid token "token node with the wrong shape"

(* The value tokens of one item, in order, without the closing semicolon. *)
let item_tokens (item : Frontend.node) =
  List.filter_map (fun (child : Frontend.node) ->
    if child.Frontend.rule = "token" then Some (token_text child) else None)
    item.Frontend.children

(* A block's head word, its name and its items. The name rule is an identifier
   or a quoted string; the block braces close the item list. *)
let decode_block (block : Frontend.node) =
  let reader = { parent = block; rest = block.Frontend.children } in
  let head = literal "identifier" reader in
  let name = (match reader.rest with
    | node :: rest ->
        reader.rest <- rest;
        let leaf = match node.Frontend.rule with
          | "name" -> (match node.Frontend.children with
            | [child] -> child
            | _ -> invalid node "name node with the wrong shape")
          | _ -> node in
        (match leaf.Frontend.value with
         | Some value -> value
         | None -> invalid leaf "name without a value")
    | [] -> invalid reader.parent "expected a name") in
  terminal "{" reader;
  let rec collect acc = match reader.rest with
    | next :: _ when next.Frontend.rule = "item" ->
        let item = take "item" reader in
        collect (item_tokens item :: acc)
    | _ -> List.rev acc in
  let items = collect [] in
  terminal "}" reader;
  (match reader.rest with
   | [] -> ()
   | extra :: _ -> invalid extra ("unconsumed " ^ extra.Frontend.rule ^ " after block"));
  head, name, items

(* An item's first token is its field name; the rest are its value tokens. *)
let field_item field items =
  List.find_opt (fun tokens -> match tokens with head :: _ -> head = field | [] -> false) items

let field_value field items =
  match field_item field items with
  | Some (_ :: value :: _) -> value
  | _ -> ""

(* A list value is the tokens between the surrounding brackets: [a, b, c]. *)
let list_value field items =
  match field_item field items with
  | Some (_ :: "[" :: rest) -> begin match List.rev rest with
      | "]" :: inner -> List.rev inner
      | _ -> [] end
  | _ -> []

let split_commas tokens =
  let rec go acc = function
    | [] -> List.rev acc
    | [last] -> List.rev (last :: acc)
    | first :: "," :: rest -> go (first :: acc) rest
    | first :: rest -> go (first :: acc) rest in
  go [] tokens

let decode_service node name items =
  { sname = name;
    sgotype = field_value "type" items;
    purpose = field_value "purpose" items;
    sops = split_commas (list_value "op" items);
    scapabilities = split_commas (list_value "capability" items);
    stemplate = field_value "template" items;
    s_at = node.Frontend.at }

let decode_outcome node oname = function
  | "when" :: condition -> { oname; owhen = Some condition }
  | ["otherwise"] -> { oname; owhen = None }
  | _ -> invalid node ("outcome " ^ oname ^ " must be `when <rule>` or `otherwise`")

(* A logic rule assigns one `output.<field>`. A rule whose target is anything
   else (email's receipts and transport logic) is not projected by this
   generator yet: it is skipped rather than rejected, so the declaration still
   decodes and the service's capability files can be emitted. *)
let decode_logic node tokens =
  let rec split left = function
    | "=" :: right -> Some (List.rev left, right)
    | first :: rest -> split (first :: left) rest
    | [] -> None in
  match split [] tokens with
  | Some (["output"; "."; field], value) -> Some (field, value)
  | Some _ -> None
  | None -> invalid node "logic rule without `=`"

let decode_operation node name items =
  let outcomes = List.filter_map (fun tokens -> match tokens with
    | "outcome" :: oname :: rest -> Some (decode_outcome node oname rest)
    | _ -> None) items in
  let logic = List.filter_map (fun tokens -> match tokens with
    | "logic" :: rest -> decode_logic node rest
    | _ -> None) items in
  { oname = name;
    oinput = field_value "input" items;
    ooutput = field_value "output" items;
    ooutcomes = outcomes;
    ologic = logic;
    op_at = node.Frontend.at }

let decode_record node name items =
  { rname = name;
    rfields = List.map (fun tokens -> match tokens with
      | [fname; ftype] -> { fname; ftype }
      | _ -> invalid node ("record " ^ name ^ ": each field is `name type`")) items }

let decode_question node name items =
  { qregion = field_value "region" items;
    qtext = field_value "text" items;
    qoptions = [];
    q_at = node.Frontend.at }

let decode_option node _name items =
  { oquestion = field_value "question" items;
    opreferred = field_value "preferred" items;
    o_at = node.Frontend.at }

(* A capability's file is a projection of declared facts only: the contract
   type, its one method, the import it needs, the option that sets it and the
   error a missing boundary returns. Nothing is inferred from the service. *)
let decode_capability node name items =
  let tokens field = match field_item field items with
    | Some (_ :: rest) -> rest
    | Some [] | None -> [] in
  match tokens "import", tokens "missing", tokens "option" with
  | alias :: path :: [], missing :: missing_text :: missing_doc :: [], option :: param :: option_doc :: [] ->
      { cname = name;
        cnoun = field_value "noun" items;
        ccontract = field_value "contract" items;
        cmethod = field_value "method" items;
        cimport_alias = alias;
        cimport_path = path;
        cmissing = missing;
        cmissing_text = missing_text;
        cmissing_doc = missing_doc;
        cinterface_doc = split_commas (list_value "interface_doc" items);
        coption = option;
        coption_param = param;
        coption_doc = option_doc;
        cdoc = split_commas (list_value "doc" items);
        c_at = node.Frontend.at }
  | _ ->
      invalid node ("capability " ^ name ^
        " must declare `import alias \"path\"`, `missing Err \"text\" \"doc\"` and `option With param \"doc\"`")

let attach_options (questions : question list) (options : option' list) =
  List.map (fun (q : question) ->
    { q with qoptions = List.filter (fun (o : option') -> o.oquestion = q.qregion) options })
    questions

(* Every block in the shell is read the same way; the head word decides what it
   declares. Unknown heads (the kind declarations) carry no instance and are
   ignored. *)
let decode_declaration (root : Frontend.node) =
  let blocks = List.filter_map (fun (child : Frontend.node) ->
    if child.Frontend.rule = "block" then Some child else None) root.Frontend.children in
  let services = ref [] and operations = ref [] and records = ref [] in
  let capabilities = ref [] in
  let questions = ref [] and options = ref [] and directories = ref [] in
  List.iter (fun (block : Frontend.node) ->
    let head, name, items = decode_block block in
    match head with
    | "service" -> services := decode_service block name items :: !services
    | "operation" -> operations := decode_operation block name items :: !operations
    | "record" -> records := decode_record block name items :: !records
    | "capability" -> capabilities := decode_capability block name items :: !capabilities
    | "question" -> questions := decode_question block name items :: !questions
    | "option" -> options := decode_option block name items :: !options
    | "directory" -> directories := { dpath = name; d_at = block.Frontend.at } :: !directories
    | _ -> ()) blocks;
  { services = List.rev !services;
    operations = List.rev !operations;
    records = List.rev !records;
    capabilities = List.rev !capabilities;
    questions = attach_options (List.rev !questions) (List.rev !options);
    directories = List.rev !directories;
    decl_at = root.Frontend.at }

(** Parse a shell source against the shell grammar. Expected syntax and decoding
    errors become diagnostics; nothing is read from disk. *)
let parse ~grammar ~source ~filename =
  match Frontend.parse_text ~grammar ~source ~filename with
  | Error diagnostics -> Error diagnostics
  | Ok node -> (try Ok (decode_declaration node) with Invalid diagnostic -> Error [diagnostic])

let parse_files ~grammar_path ~source_path =
  match Frontend.parse_files ~grammar_path ~source_path with
  | Error diagnostics -> Error diagnostics
  | Ok node -> (try Ok (decode_declaration node) with Invalid diagnostic -> Error [diagnostic])

(* --- the check ---
   Three invariants of the chief block, each a compile error rather than a
   warning: a question fans past sixteen options (at_most_16), a question has no
   option to project it (one_source), a leaf has no template (one_way). The
   check reports each finding at the declaration it names. *)

let max_options = 16

let check (d : declaration) =
  let findings = ref [] in
  let add at code message = findings := { at; code; message } :: !findings in
  List.iter (fun (q : question) ->
    let count = List.length q.qoptions in
    if count > max_options then
      add q.q_at "fanout_over_16"
        (Printf.sprintf "question %s declares %d options; at most %d are allowed."
           q.qregion count max_options);
    if count = 0 then
      add q.q_at "unprojected_question"
        (Printf.sprintf "question %s declares no option; a question must project to one option."
           q.qregion);
    List.iter (fun (o : option') ->
      if String.trim o.opreferred = "" then
        add o.o_at "leaf_without_one_template"
          (Printf.sprintf "the option of question %s has no preferred template." o.oquestion))
      q.qoptions)
    d.questions;
  List.rev !findings

(* --- the projections --- *)

let capitalize value =
  if value = "" then value else
    String.make 1 (Char.uppercase_ascii value.[0]) ^ String.sub value 1 (String.length value - 1)

let lower_first value =
  if value = "" then value else
    String.make 1 (Char.lowercase_ascii value.[0]) ^ String.sub value 1 (String.length value - 1)

(* A purpose is written in the imperative; a doc sentence reads it in the third
   person, so its leading verb takes an `s` -- "Answer ..." becomes "answers ...". *)
let third_person value =
  let lowered = lower_first value in
  match String.index_opt lowered ' ' with
  | Some at -> String.sub lowered 0 at ^ "s" ^ String.sub lowered at (String.length lowered - at)
  | None -> lowered ^ "s"

let go_word = function "github" -> "GitHub" | value -> capitalize value
let go_pascal value = String.concat "" (List.map go_word (String.split_on_char '_' value))
let pad value width = value ^ String.make (max 0 (width - String.length value)) ' '
let go_string value = Printf.sprintf "%S" value
let go_type = function "text" -> "string" | value -> go_pascal value

(* An outcome's Go constant: the ordinary `ok` spells OK, every other name takes
   the Pascal spelling of its kind. *)
let outcome_const name = if name = "ok" then "outcomeOK" else "outcome" ^ go_pascal name

let max_width names = List.fold_left (fun w name -> max w (String.length name)) 0 names

(* `format("Hello, {}", input.name)` renders to `fmt.Sprintf("Hello, %s", request.Name)`:
   the `{}` hole becomes the `%s` verb, and each `input.<f>` becomes `request.<F>`. *)
let replace_braces value =
  let buf = Buffer.create (String.length value) in
  let n = String.length value in
  let i = ref 0 in
  while !i < n do
    if !i + 1 < n && value.[!i] = '{' && value.[!i + 1] = '}' then
      (Buffer.add_string buf "%s"; i := !i + 2)
    else (Buffer.add_char buf value.[!i]; incr i)
  done;
  Buffer.contents buf

let translate_logic_value tokens =
  match tokens with
  | ["format"; "("; format; ","; "input"; "."; field; ")"] ->
      Printf.sprintf "fmt.Sprintf(%s, request.%s)" (go_string (replace_braces format)) (go_pascal field)
  | _ -> failwith "the fresh-service template renders one `format(...)` logic rule"

(* A condition reads `input.<f> = <literal>`; it renders to `<f> == <literal>`.
   An empty token is the empty string literal the grammar strips to "", so it is
   re-quoted before it is emitted. *)
let translate_condition tokens =
  let rec go acc = function
    | "input" :: "." :: field :: rest -> go (field :: acc) rest
    | "output" :: "." :: field :: rest -> go (go_pascal field :: acc) rest
    | "=" :: rest -> go ("==" :: acc) rest
    | token :: rest -> go ((if token = "" then "\"\"" else token) :: acc) rest
    | [] -> String.concat " " (List.rev acc) in
  go [] tokens

let record_named (d : declaration) name =
  match List.find_opt (fun (r : record) -> r.rname = name) d.records with
  | Some record -> record
  | None -> failwith ("the declaration names no record " ^ name)

let first_when (o : operation) =
  match List.find_opt (fun (x : outcome) -> x.owhen <> None) o.ooutcomes with
  | Some outcome -> outcome
  | None -> failwith ("operation " ^ o.oname ^ " declares no `when` outcome")

let otherwise (o : operation) =
  match List.find_opt (fun (x : outcome) -> x.owhen = None) o.ooutcomes with
  | Some outcome -> outcome
  | None -> failwith ("operation " ^ o.oname ^ " declares no `otherwise` outcome")

let fresh_template (d : declaration) =
  match d.services, d.operations with
  | [service], [operation] -> service, operation
  | _ -> failwith "the fresh-service template emits one service and one operation"

(* --- the rendered files --- *)

let header buf source =
  Printf.bprintf buf "// Code generated by csfc from %s; DO NOT EDIT.\n" source

let render_doc ~source service input_field output_lower first_outcome =
  let buf = Buffer.create 512 in
  header buf source;
  Buffer.add_string buf "//\n";
  Printf.bprintf buf "// Package %s %s A caller supplies\n" service.sname (third_person service.purpose);
  Printf.bprintf buf "// a %s and gets one rendered %s or the %s outcome. The service\n"
    input_field output_lower first_outcome;
  Buffer.add_string buf "// starts no goroutines and keeps no state between calls.\n\n";
  Printf.bprintf buf "package %s\n" service.sname;
  Buffer.contents buf

let render_service ~source service operation =
  let buf = Buffer.create 2048 in
  header buf source;
  Buffer.add_string buf "//\n";
  Printf.bprintf buf "// The %s service: the %s the host builds from its options. %s declares\n"
    service.sname (go_pascal service.sname) service.sname;
  Buffer.add_string buf "// no capability, no storage and no config, so there is nothing to bind and\n";
  Buffer.add_string buf "// nothing to validate; the only option is metrics.\n\n";
  Printf.bprintf buf "package %s\n\n" service.sname;
  Buffer.add_string buf "import (\n\t\"errors\"\n\n";
  Buffer.add_string buf "\t\"github.com/prometheus/client_golang/prometheus\"\n)\n\n";
  Printf.bprintf buf "// %s %s It owns no goroutine and\n"
    (go_pascal service.sname) (third_person service.purpose);
  Buffer.add_string buf "// keeps no state between calls.\n";
  Printf.bprintf buf "type %s struct {\n\tmetrics *%sMetrics\n}\n\n"
    (go_pascal service.sname) operation.oname;
  Printf.bprintf buf "// New%s builds a %s from the host-configured options. There is no boundary\n"
    (go_pascal service.sname) (go_pascal service.sname);
  Buffer.add_string buf "// to bind, so construction fails only on an invalid option.\n";
  Printf.bprintf buf "func New%s(options ...Option) (*%s, error) {\n"
    (go_pascal service.sname) (go_pascal service.sname);
  Printf.bprintf buf "\tconfig := &%sConfig{}\n" (lower_first service.sname);
  Buffer.add_string buf "\tfor _, option := range options {\n\t\tif err := option(config); err != nil {\n\t\t\treturn nil, err\n\t\t}\n\t}\n";
  Printf.bprintf buf "\tmetrics, err := new%sMetrics(config.registerer)\n" (go_pascal operation.oname);
  Buffer.add_string buf "\tif err != nil {\n\t\treturn nil, err\n\t}\n";
  Printf.bprintf buf "\treturn &%s{metrics: metrics}, nil\n}\n\n" (go_pascal service.sname);
  Printf.bprintf buf "// %sConfig holds the host-configured options before the service is built.\n"
    (lower_first service.sname);
  Buffer.add_string buf "// It is not exported; every field is set through one option.\n";
  Printf.bprintf buf "type %sConfig struct {\n\tregisterer prometheus.Registerer\n}\n\n"
    (lower_first service.sname);
  Buffer.add_string buf "// Option configures the service before construction.\n";
  Printf.bprintf buf "type Option func(config *%sConfig) error\n\n" (lower_first service.sname);
  Printf.bprintf buf "// WithMetrics registers bounded %s metrics with registerer.\n" operation.oname;
  Buffer.add_string buf "func WithMetrics(registerer prometheus.Registerer) Option {\n";
  Printf.bprintf buf "\treturn func(config *%sConfig) error {\n" (lower_first service.sname);
  Buffer.add_string buf "\t\tif registerer == nil {\n";
  Printf.bprintf buf "\t\t\treturn errors.New(\"%s: metrics registerer is nil\")\n" service.sname;
  Buffer.add_string buf "\t\t}\n\t\tconfig.registerer = registerer\n\t\treturn nil\n\t}\n}\n";
  Buffer.contents buf

let render_operation ~source service operation input output input_field =
  let buf = Buffer.create 4096 in
  let input_go = go_pascal input.rname and output_go = go_pascal output.rname in
  let op_go = go_pascal operation.oname in
  let output_lower = lower_first output.rname in
  let input_param = lower_first input_field in
  header buf source;
  Buffer.add_string buf "//\n";
  Printf.bprintf buf "// The one declared operation, %s, with its input and output records, its\n" operation.oname;
  Printf.bprintf buf "// declared logic, its generated classifier and its metrics. %s times one\n" op_go;
  Printf.bprintf buf "// attempt, renders the declared %s, classifies the declared outcome and\n" output_lower;
  Buffer.add_string buf "// records both on the declared metrics. There is no seam: the body and the\n";
  Buffer.add_string buf "// classifier are emitted from the declaration's logic and when/otherwise\n";
  Buffer.add_string buf "// clauses.\n\n";
  Printf.bprintf buf "package %s\n\n" service.sname;
  Buffer.add_string buf "import (\n\t\"fmt\"\n\t\"time\"\n\n";
  Buffer.add_string buf "\t\"github.com/prometheus/client_golang/prometheus\"\n\n";
  Buffer.add_string buf "\t\"github.com/candacelabs/csf/pkg/telemetry\"\n)\n\n";
  Printf.bprintf buf "// %s names the subject to %s.\n" input_go operation.oname;
  Printf.bprintf buf "type %s struct {\n" input_go;
  let input_width = max_width (List.map (fun (f : field) -> go_pascal f.fname) input.rfields) in
  List.iter (fun (f : field) ->
    Printf.bprintf buf "\t%s %s\n" (pad (go_pascal f.fname) input_width) (go_type f.ftype))
    input.rfields;
  Buffer.add_string buf "}\n\n";
  Printf.bprintf buf "// %s carries the rendered %s and its outcome as typed data.\n" output_go output_lower;
  Printf.bprintf buf "type %s struct {\n" output_go;
  let output_width = max_width (List.map (fun (f : field) -> go_pascal f.fname) output.rfields) in
  List.iter (fun (f : field) ->
    Printf.bprintf buf "\t%s %s\n" (pad (go_pascal f.fname) output_width) (go_type f.ftype))
    output.rfields;
  Buffer.add_string buf "}\n\n";
  Buffer.add_string buf "const (\n";
  let consts = List.map (fun (o : outcome) -> outcome_const o.oname, o.oname) operation.ooutcomes in
  let const_width = max_width (List.map fst consts) in
  List.iter (fun (constant, value) ->
    Printf.bprintf buf "\t%s = %s\n" (pad constant const_width) (go_string value)) consts;
  Buffer.add_string buf ")\n\n";
  Printf.bprintf buf "// %s renders one %s for the named subject and returns the declared\n" op_go output_lower;
  Printf.bprintf buf "// outcome. %s is safe for concurrent use.\n" op_go;
  Printf.bprintf buf "func (%s *%s) %s(request *%s) *%s {\n"
    (lower_first service.sname) (go_pascal service.sname) op_go input_go output_go;
  Buffer.add_string buf "\tstarted := time.Now()\n";
  Printf.bprintf buf "\tresponse := &%s{\n" output_go;
  let labels = List.map (fun (f : field) -> go_pascal f.fname ^ ":") output.rfields in
  let label_width = max_width labels in
  List.iter (fun (f : field) ->
    let label = go_pascal f.fname ^ ":" in
    let value =
      if f.fname = "outcome" then
        Printf.sprintf "classify%s(request.%s)" op_go (go_pascal input_field)
      else
        match List.assoc_opt f.fname operation.ologic with
        | Some tokens -> translate_logic_value tokens
        | None -> failwith ("no logic rule assigns output." ^ f.fname) in
    Printf.bprintf buf "\t\t%s %s,\n" (pad label label_width) value) output.rfields;
  Buffer.add_string buf "\t}\n";
  Printf.bprintf buf "\t%s.metrics.observe(response.Outcome, time.Since(started).Seconds())\n"
    (lower_first service.sname);
  Buffer.add_string buf "\treturn response\n}\n\n";
  Printf.bprintf buf "// classify%s is the generated classifier for the declared outcomes: the\n" op_go;
  Buffer.add_string buf "// first `when` clause that matches wins, and `otherwise` is the default.\n";
  Printf.bprintf buf "func classify%s(%s string) string {\n" op_go input_param;
  List.iter (fun (o : outcome) -> match o.owhen with
    | Some condition ->
        Printf.bprintf buf "\tif %s {\n\t\treturn %s\n\t}\n"
          (translate_condition condition) (outcome_const o.oname)
    | None -> ()) operation.ooutcomes;
  Printf.bprintf buf "\treturn %s\n}\n\n" (outcome_const (otherwise operation).oname);
  Printf.bprintf buf "// %sMetrics holds the two metric families of the %s operation.\n"
    operation.oname operation.oname;
  Printf.bprintf buf "type %sMetrics struct {\n\tgreets   *prometheus.CounterVec\n\tduration *prometheus.HistogramVec\n}\n\n"
    operation.oname;
  Printf.bprintf buf "// new%sMetrics registers the %s metrics. A nil registerer records\n" op_go operation.oname;
  Buffer.add_string buf "// nothing, so instrumentation is optional and off by default.\n";
  Printf.bprintf buf "func new%sMetrics(registerer prometheus.Registerer) (*%sMetrics, error) {\n"
    op_go operation.oname;
  Buffer.add_string buf "\tif registerer == nil {\n\t\treturn nil, nil\n\t}\n";
  Buffer.add_string buf "\tgreets, err := telemetry.RegisterOnce(registerer, prometheus.NewCounterVec(prometheus.CounterOpts{\n";
  Printf.bprintf buf "\t\tName: \"csf_%s_%s_total\",\n" service.sname operation.oname;
  Printf.bprintf buf "\t\tHelp: \"Total bounded %s %s outcomes; %s means one %s rendered for a non-empty %s.\",\n"
    service.sname operation.oname (otherwise operation).oname output_lower input_field;
  Buffer.add_string buf "\t}, []string{\"outcome\"}))\n";
  Buffer.add_string buf "\tif err != nil {\n\t\treturn nil, err\n\t}\n";
  Buffer.add_string buf "\tduration, err := telemetry.RegisterOnce(registerer, prometheus.NewHistogramVec(prometheus.HistogramOpts{\n";
  Printf.bprintf buf "\t\tName:    \"csf_%s_%s_duration_seconds\",\n" service.sname operation.oname;
  Printf.bprintf buf "\t\tHelp:    \"Duration of %s %s attempts by bounded outcome.\",\n" service.sname operation.oname;
  Buffer.add_string buf "\t\tBuckets: prometheus.DefBuckets,\n";
  Buffer.add_string buf "\t}, []string{\"outcome\"}))\n";
  Buffer.add_string buf "\tif err != nil {\n\t\treturn nil, err\n\t}\n";
  Printf.bprintf buf "\treturn &%sMetrics{greets: greets, duration: duration}, nil\n}\n\n" operation.oname;
  Buffer.add_string buf "// observe records one attempt's bounded outcome and its duration.\n";
  Printf.bprintf buf "func (metrics *%sMetrics) observe(outcome string, seconds float64) {\n" operation.oname;
  Buffer.add_string buf "\tif metrics == nil {\n\t\treturn\n\t}\n";
  Buffer.add_string buf "\tmetrics.greets.WithLabelValues(outcome).Inc()\n";
  Buffer.add_string buf "\tmetrics.duration.WithLabelValues(outcome).Observe(seconds)\n}\n";
  Buffer.contents buf

let render_operation_test ~source service operation input output input_field =
  let buf = Buffer.create 2048 in
  let input_go = go_pascal input.rname in
  let op_go = go_pascal operation.oname in
  let output_lower = lower_first output.rname in
  let first = first_when operation and default = otherwise operation in
  let prefix = match List.assoc_opt "text" operation.ologic with
    | Some ["format"; "("; format; ","; "input"; "."; _; ")"] ->
        (match String.index_opt format '{' with
         | Some at -> String.sub format 0 at
         | None -> format)
    | _ -> "" in
  header buf source;
  Buffer.add_string buf "//\n";
  Printf.bprintf buf "// One spec per declared operation outcome. %s is a fresh service, so the body\n" service.sname;
  Buffer.add_string buf "// and the classifier are generated from the declaration; the specs below assert\n";
  Printf.bprintf buf "// the declared behaviour \xe2\x80\x94 the rendered %s and the bounded outcome \xe2\x80\x94 for\n" output_lower;
  Buffer.add_string buf "// each `when` clause.\n\n";
  Printf.bprintf buf "package %s\n\n" service.sname;
  Buffer.add_string buf "import (\n\t. \"github.com/onsi/ginkgo/v2\"\n\t. \"github.com/onsi/gomega\"\n)\n\n";
  Printf.bprintf buf "var _ = Describe(\"%s %s\", func() {\n" (go_pascal service.sname) operation.oname;
  Printf.bprintf buf "\tIt(\"renders one %s and classifies %s for a non-empty %s\", func() {\n"
    output_lower default.oname input_field;
  Printf.bprintf buf "\t\t%s, err := New%s()\n" service.sname (go_pascal service.sname);
  Buffer.add_string buf "\t\tExpect(err).NotTo(HaveOccurred())\n";
  Printf.bprintf buf "\t\tresponse := %s.%s(&%s{%s: \"world\"})\n"
    service.sname op_go input_go (go_pascal input_field);
  Printf.bprintf buf "\t\tExpect(response.Text).To(Equal(\"%sworld\"))\n" prefix;
  Printf.bprintf buf "\t\tExpect(response.Outcome).To(Equal(%s))\n" (outcome_const default.oname);
  Buffer.add_string buf "\t})\n\n";
  Printf.bprintf buf "\tIt(\"classifies an empty %s as %s\", func() {\n" input_field first.oname;
  Printf.bprintf buf "\t\t%s, err := New%s()\n" service.sname (go_pascal service.sname);
  Buffer.add_string buf "\t\tExpect(err).NotTo(HaveOccurred())\n";
  Printf.bprintf buf "\t\tresponse := %s.%s(&%s{})\n" service.sname op_go input_go;
  Printf.bprintf buf "\t\tExpect(response.Text).To(Equal(\"%s\"))\n" prefix;
  Printf.bprintf buf "\t\tExpect(response.Outcome).To(Equal(%s))\n" (outcome_const first.oname);
  Buffer.add_string buf "\t})\n\n";
  Buffer.add_string buf "\tDescribeTable(\"declares one bounded label per outcome\",\n";
  Buffer.add_string buf "\t\tfunc(outcome string) {\n";
  Printf.bprintf buf "\t\t\tExpect(outcome).To(BeElementOf([]string{%s, %s}))\n"
    (outcome_const first.oname) (outcome_const default.oname);
  Buffer.add_string buf "\t\t},\n";
  List.iter (fun (o : outcome) ->
    Printf.bprintf buf "\t\tEntry(\"%s\", %s),\n" o.oname (outcome_const o.oname)) operation.ooutcomes;
  Buffer.add_string buf "\t)\n})\n";
  Buffer.contents buf

let render_suite ~source service =
  let buf = Buffer.create 512 in
  header buf source;
  Buffer.add_string buf "\n";
  Printf.bprintf buf "package %s\n\n" service.sname;
  Buffer.add_string buf "import (\n\t\"testing\"\n\n";
  Buffer.add_string buf "\t. \"github.com/onsi/ginkgo/v2\"\n\t. \"github.com/onsi/gomega\"\n)\n\n";
  Printf.bprintf buf "func Test%s(t *testing.T) {\n" (go_pascal service.sname);
  Buffer.add_string buf "\tRegisterFailHandler(Fail)\n";
  Printf.bprintf buf "\tRunSpecs(t, \"%s service suite\")\n}\n" service.sname;
  Buffer.contents buf

let render_dashboard ~source service operation input output input_field =
  let buf = Buffer.create 4096 in
  let op_go = go_pascal operation.oname in
  let output_lower = lower_first output.rname in
  let default = otherwise operation and first = first_when operation in
  let counter = Printf.sprintf "csf_%s_%s_total" service.sname operation.oname in
  let histogram = Printf.sprintf "csf_%s_%s_duration_seconds" service.sname operation.oname in
  ignore source; ignore input;
  Printf.bprintf buf "{\n  \"uid\": \"csf-golden-%s\",\n" service.sname;
  Printf.bprintf buf "  \"title\": \"CSF %s %ss\",\n" service.sname output_lower;
  Buffer.add_string buf "  \"schemaVersion\": 41,\n  \"version\": 1,\n";
  Printf.bprintf buf "  \"tags\": [\"csf\", \"%s\", \"golden\"],\n" service.sname;
  Buffer.add_string buf "  \"timezone\": \"browser\",\n  \"refresh\": \"30s\",\n";
  Buffer.add_string buf "  \"time\": {\"from\": \"now-6h\", \"to\": \"now\"},\n";
  Buffer.add_string buf "  \"templating\": {\"list\": [\n";
  Buffer.add_string buf "    {\"name\": \"datasource\", \"type\": \"datasource\", \"query\": \"prometheus\", \"label\": \"Prometheus\"},\n";
  Printf.bprintf buf "    {\"name\": \"instance\", \"type\": \"query\", \"datasource\": {\"type\": \"prometheus\", \"uid\": \"${datasource}\"}, \"query\": \"label_values(%s, instance)\", \"refresh\": 1}\n" counter;
  Buffer.add_string buf "  ]},\n  \"panels\": [\n    {\n      \"id\": 1,\n      \"type\": \"timeseries\",\n";
  Printf.bprintf buf "      \"title\": \"%s %s outcomes\",\n" (go_pascal service.sname) operation.oname;
  Printf.bprintf buf "      \"description\": \"Procedure: %s.%s v1. Rate per second over the selected window, scoped to one scrape instance. %s means one %s rendered for a non-empty %s; %s means the request carried no %s. Missing or stale targets show no data, not zero. Counters reset at restart and are not a ledger. No failure-rate baseline is calibrated.\",\n"
    (go_pascal service.sname) op_go default.oname output_lower input_field first.oname input_field;
  Buffer.add_string buf "      \"datasource\": {\"type\": \"prometheus\", \"uid\": \"${datasource}\"},\n";
  Buffer.add_string buf "      \"gridPos\": {\"x\": 0, \"y\": 0, \"w\": 12, \"h\": 8},\n";
  Printf.bprintf buf "      \"targets\": [{\"refId\": \"A\", \"expr\": \"sum by (outcome) (rate(%s{instance=\\\"$instance\\\"}[$__rate_interval])) and on() (max(up{instance=\\\"$instance\\\"}) == 1)\", \"legendFormat\": \"{{outcome}}\"}],\n" counter;
  Buffer.add_string buf "      \"fieldConfig\": {\"defaults\": {\"unit\": \"ops\", \"noValue\": \"Unknown\"}, \"overrides\": []}\n    },\n";
  Buffer.add_string buf "    {\n      \"id\": 2,\n      \"type\": \"timeseries\",\n";
  Printf.bprintf buf "      \"title\": \"%s %s duration p50/p95\",\n" (go_pascal service.sname) operation.oname;
  Printf.bprintf buf "      \"description\": \"Procedure: %s.%s v1. Histogram estimates in seconds by outcome, scoped to one scrape instance and rate window; each attempt spans one render of the declared %s. No samples, or an unavailable exporter, is Unknown, never a measured zero. No latency objective is calibrated.\",\n"
    (go_pascal service.sname) op_go output_lower;
  Buffer.add_string buf "      \"datasource\": {\"type\": \"prometheus\", \"uid\": \"${datasource}\"},\n";
  Buffer.add_string buf "      \"gridPos\": {\"x\": 12, \"y\": 0, \"w\": 12, \"h\": 8},\n";
  Buffer.add_string buf "      \"targets\": [\n";
  Printf.bprintf buf "        {\"refId\": \"A\", \"expr\": \"histogram_quantile(0.50, sum by (le, outcome) (rate(%s_bucket{instance=\\\"$instance\\\"}[$__rate_interval]))) and on() (max(up{instance=\\\"$instance\\\"}) == 1)\", \"legendFormat\": \"p50 {{outcome}}\"},\n" histogram;
  Printf.bprintf buf "        {\"refId\": \"B\", \"expr\": \"histogram_quantile(0.95, sum by (le, outcome) (rate(%s_bucket{instance=\\\"$instance\\\"}[$__rate_interval]))) and on() (max(up{instance=\\\"$instance\\\"}) == 1)\", \"legendFormat\": \"p95 {{outcome}}\"}\n" histogram;
  Buffer.add_string buf "      ],\n";
  Buffer.add_string buf "      \"fieldConfig\": {\"defaults\": {\"unit\": \"s\", \"noValue\": \"Unknown\"}, \"overrides\": []}\n    }\n  ]\n}\n";
  Buffer.contents buf

(* One capability's file: the contract interface, the option that binds it, and
   the error a missing boundary returns -- every line a projection of a declared
   fact, so the file is generated and never handwritten. The config type the
   option assigns through is the service's declared Go type. *)
let render_capability ~source (service : service) (cap : capability) =
  let buf = Buffer.create 1024 in
  let config_type = lower_first service.sgotype ^ "Config" in
  header buf source;
  Buffer.add_string buf "//\n";
  Printf.bprintf buf "// The %s capability: the interface the host binds, the option that sets\n" cap.cname;
  Buffer.add_string buf "// it, and the error a missing boundary returns.";
  (match cap.cdoc with
   | [] -> Buffer.add_char buf '\n'
   | first :: rest ->
       Printf.bprintf buf " %s\n" first;
       List.iter (fun line -> Printf.bprintf buf "// %s\n" line) rest);
  Buffer.add_char buf '\n';
  Printf.bprintf buf "package %s\n\n" service.sname;
  Buffer.add_string buf "import (\n\t\"context\"\n\t\"errors\"\n\n";
  Printf.bprintf buf "\t%s %s\n)\n\n" cap.cimport_alias (go_string cap.cimport_path);
  Printf.bprintf buf "// %s means %s.\n" cap.cmissing cap.cmissing_doc;
  Printf.bprintf buf "var %s = errors.New(%s)\n\n" cap.cmissing (go_string cap.cmissing_text);
  (match cap.cinterface_doc with
   | [] -> Printf.bprintf buf "// %s\n" cap.ccontract
   | first :: rest ->
       Printf.bprintf buf "// %s %s\n" cap.ccontract first;
       List.iter (fun line -> Printf.bprintf buf "// %s\n" line) rest);
  Printf.bprintf buf "type %s interface {\n\t%s\n}\n\n" cap.ccontract cap.cmethod;
  Printf.bprintf buf "// %s %s.\n" cap.coption cap.coption_doc;
  Printf.bprintf buf "func %s(%s %s) Option {\n" cap.coption cap.coption_param cap.ccontract;
  Printf.bprintf buf "\treturn func(config *%s) error {\n" config_type;
  Printf.bprintf buf "\t\tif %s == nil {\n" cap.coption_param;
  Printf.bprintf buf "\t\t\treturn %s\n" cap.cmissing;
  Buffer.add_string buf "\t\t}\n";
  Printf.bprintf buf "\t\tconfig.%s = %s\n" cap.cname cap.coption_param;
  Buffer.add_string buf "\t\treturn nil\n\t}\n}\n";
  Buffer.contents buf

(* Parse one declared method signature `Name(param type, ...) return` into its
   name, its labelled parameters and its returns. The signature is the only
   source for all three, so the generated double can never drift from the
   interface it mocks. A return is one type, or a parenthesised list. *)
let parse_method signature =
  let invalid () =
    failwith (Printf.sprintf "capability method %S is not `Name(param type, ...) return`" signature) in
  let length = String.length signature in
  let open_at = match String.index_opt signature '(' with
    | Some at -> at
    | None -> invalid () in
  let name = String.trim (String.sub signature 0 open_at) in
  if name = "" then invalid ();
  let depth = ref 0 in
  let close_at = ref length in
  let index = ref open_at in
  while !index < length && !close_at = length do
    (match signature.[!index] with
     | '(' -> incr depth
     | ')' ->
         decr depth;
         if !depth = 0 then close_at := !index
     | _ -> ());
    incr index
  done;
  if !close_at = length then invalid ();
  let params_text = String.sub signature (open_at + 1) (!close_at - open_at - 1) in
  let returns_text = String.sub signature (!close_at + 1) (length - !close_at - 1) in
  let split_words text =
    List.filter (fun word -> word <> "") (String.split_on_char ' ' text) in
  let params =
    if String.trim params_text = "" then []
    else
      List.map (fun pair ->
        match split_words (String.trim pair) with
        | [pname; ptype] -> (pname, ptype)
        | _ -> invalid ())
        (String.split_on_char ',' params_text) in
  let returns =
    let trimmed = String.trim returns_text in
    if trimmed = "" then []
    else if String.length trimmed > 1
         && trimmed.[0] = '(' && trimmed.[String.length trimmed - 1] = ')'
    then List.map String.trim
        (String.split_on_char ',' (String.sub trimmed 1 (String.length trimmed - 2)))
    else [trimmed] in
  (name, params, returns)

(* One capability's test file: the gomock double for its contract, so a spec can
   hold the boundary without a live dependency, and the misuse spec that builds
   the service with every other boundary present and expects the constructor's
   refusal of this one. Every line is a projection of the capability block and
   the service's declared Go type. *)
let render_capability_test ~source (service : service) (cap : capability) (others : capability list) =
  let buf = Buffer.create 2048 in
  let service_go = go_pascal service.sgotype in
  let mock = "Mock" ^ cap.ccontract in
  let recorder = mock ^ "MockRecorder" in
  let name, params, returns = parse_method cap.cmethod in
  let param_names = List.map fst params in
  let param_decls =
    String.concat ", " (List.map (fun (pname, ptype) -> pname ^ " " ^ ptype) params) in
  let returns_decl = match returns with
    | [] -> ""
    | [single] -> single
    | many -> "(" ^ String.concat ", " many ^ ")" in
  header buf source;
  Buffer.add_string buf "//\n";
  Printf.bprintf buf "// The %s capability's test file: its gomock double, so a spec can hold\n" cap.cname;
  Printf.bprintf buf "// the %s boundary without a live %s, and the misuse spec that\n" cap.cname cap.cnoun;
  Buffer.add_string buf "// protects the constructor's refusal of a missing boundary.\n\n";
  Printf.bprintf buf "package %s\n\n" service.sname;
  Buffer.add_string buf "import (\n\t\"context\"\n\t\"reflect\"\n\n";
  Buffer.add_string buf "\t. \"github.com/onsi/ginkgo/v2\"\n\t. \"github.com/onsi/gomega\"\n\t\"go.uber.org/mock/gomock\"\n\n";
  Printf.bprintf buf "\t%s %s\n)\n\n" cap.cimport_alias (go_string cap.cimport_path);
  Printf.bprintf buf "// %s is a mock of %s.\n" mock cap.ccontract;
  Printf.bprintf buf "type %s struct {\n\tctrl     *gomock.Controller\n\trecorder *%s\n}\n\n" mock recorder;
  Printf.bprintf buf "// %s records calls to %s.\n" recorder mock;
  Printf.bprintf buf "type %s struct {\n\tmock *%s\n}\n\n" recorder mock;
  Printf.bprintf buf "// New%s returns a new mock of %s.\n" mock cap.ccontract;
  Printf.bprintf buf "func New%s(ctrl *gomock.Controller) *%s {\n" mock mock;
  Printf.bprintf buf "\tmock := &%s{ctrl: ctrl}\n" mock;
  Printf.bprintf buf "\tmock.recorder = &%s{mock}\n" recorder;
  Buffer.add_string buf "\treturn mock\n}\n\n";
  Buffer.add_string buf "// EXPECT returns the recorder for setting expectations.\n";
  Printf.bprintf buf "func (mock *%s) EXPECT() *%s {\n\treturn mock.recorder\n}\n\n" mock recorder;
  Printf.bprintf buf "// %s mocks %s.%s.\n" name cap.ccontract name;
  Printf.bprintf buf "func (mock *%s) %s(%s) %s {\n" mock name param_decls returns_decl;
  Buffer.add_string buf "\tmock.ctrl.T.Helper()\n";
  Printf.bprintf buf "\tret := mock.ctrl.Call(mock, %s, %s)\n"
    (go_string name) (String.concat ", " param_names);
  List.iteri (fun at rtype -> Printf.bprintf buf "\tret%d, _ := ret[%d].(%s)\n" at at rtype) returns;
  (match returns with
   | [] -> Buffer.add_string buf "\t_ = ret\n"
   | [_] -> Buffer.add_string buf "\treturn ret0\n"
   | many ->
       Printf.bprintf buf "\treturn %s\n"
         (String.concat ", " (List.mapi (fun at _ -> "ret" ^ string_of_int at) many)));
  Buffer.add_string buf "}\n\n";
  Printf.bprintf buf "// %s indicates an expected call of %s.\n" name name;
  Printf.bprintf buf "func (mr *%s) %s(%s) *gomock.Call {\n" recorder name
    (match param_names with
     | [] -> ""
     | names -> String.concat ", " names ^ " any");
  Buffer.add_string buf "\tmr.mock.ctrl.T.Helper()\n";
  Printf.bprintf buf "\treturn mr.mock.ctrl.RecordCallWithMethodType(mr.mock, %s, reflect.TypeOf((*%s)(nil).%s)%s)\n"
    (go_string name) mock name
    (if param_names = [] then "" else ", " ^ String.concat ", " param_names);
  Buffer.add_string buf "}\n\n";
  Printf.bprintf buf "var _ = Describe(%s, func() {\n"
    (go_string (service_go ^ " " ^ cap.cname ^ " boundary"));
  Printf.bprintf buf "\tIt(%s, func() {\n"
    (go_string ("refuses to build without a " ^ cap.cnoun));
  Printf.bprintf buf "\t\t_, err := New%s(\n" service_go;
  List.iter (fun (other : capability) ->
    Printf.bprintf buf "\t\t\t%s(New%s(gomock.NewController(GinkgoT()))),\n"
      other.coption ("Mock" ^ other.ccontract)) others;
  Buffer.add_string buf "\t\t)\n";
  Printf.bprintf buf "\t\tExpect(err).To(MatchError(%s))\n" cap.cmissing;
  Buffer.add_string buf "\t})\n})\n";
  Buffer.contents buf

(* The service's own directory: the `directory` block the declaration writes, or
   the template's default when it writes none. *)
let service_dir (d : declaration) =
  match d.directories with
  | (x : directory) :: _ -> x.dpath
  | [] ->
      (match d.services with (s : service) :: _ -> "services/" ^ s.sname | [] -> "services/service")

(* The capability files the service declares, in declaration order. A service
   that declares no capability -- the fresh template -- emits none. *)
let capability_projections ~source (d : declaration) =
  match d.services with
  | [service] ->
      let dir = service_dir d in
      List.filter_map (fun name ->
        match List.find_opt (fun (c : capability) -> c.cname = name) d.capabilities with
        | Some cap -> Some (Printf.sprintf "%s/%s.go" dir cap.cname, render_capability ~source service cap)
        | None -> None) service.scapabilities
  | _ -> []

(* The capability test projections: the service suite plus one test file per
   capability in declaration order. Each test file builds the service with every
   other boundary present and expects this one's refusal, so it is a projection
   of the capability list, never a per-file invention. *)
let service_test_projections ~source (d : declaration) =
  match d.services with
  | [service] ->
      let dir = service_dir d in
      let capabilities = List.filter_map (fun name ->
        List.find_opt (fun (c : capability) -> c.cname = name) d.capabilities)
        service.scapabilities in
      (Printf.sprintf "%s/%s_suite_test.go" dir service.sname, render_suite ~source service)
      :: List.map (fun (cap : capability) ->
           let others = List.filter (fun (c : capability) -> c.cname <> cap.cname) capabilities in
           (Printf.sprintf "%s/%s_test.go" dir cap.cname,
            render_capability_test ~source service cap others))
          capabilities
  | _ -> []

(* The generated projections: the emitted files of the fresh-service template,
   keyed by their path inside the output directory. Every file is generated, so
   a fresh service's generated share is 1.0. The BUILD file is not emitted: the
   tree's BUILD files are gazelle's projection, and a hand-written one here
   would be erased by the next regeneration. *)
let projections ~source (d : declaration) =
  let service, operation = fresh_template d in
  let input = record_named d operation.oinput in
  let output = record_named d operation.ooutput in
  let input_field = match input.rfields with
    | (f : field) :: _ -> f.fname
    | [] -> failwith ("the input record " ^ input.rname ^ " declares no field") in
  let first = first_when operation in
  let dir = service_dir d in
  [
    Printf.sprintf "%s/doc.go" dir, render_doc ~source service input_field (lower_first output.rname) first.oname;
    Printf.sprintf "%s/%s.go" dir service.sname, render_service ~source service operation;
    Printf.sprintf "%s/%s.go" dir operation.oname, render_operation ~source service operation input output input_field;
    Printf.sprintf "%s/%s_test.go" dir operation.oname, render_operation_test ~source service operation input output input_field;
    Printf.sprintf "%s/%s_suite_test.go" dir service.sname, render_suite ~source service;
    Printf.sprintf "observability/%s-dashboard.json" service.sname, render_dashboard ~source service operation input output input_field;
  ] @ capability_projections ~source d

(* The holes file: one marker per retained region the fresh-service template does
   not project as code. It is written once and never regenerated, so an agent's
   work inside a hole survives the next emit. A declaration with no questions --
   a fresh service -- has no holes at all. *)
let holes ~source (d : declaration) =
  let service = match d.services with (s : service) :: _ -> s.sname | [] -> "service" in
  let questions = List.filter (fun (q : question) -> q.qoptions <> []) d.questions in
  if questions = [] then None
  else
    let buf = Buffer.create 256 in
    header buf source;
    Buffer.add_string buf "//\n";
    Printf.bprintf buf "// The %s service's holes: the retained regions the declaration asked as\n" service;
    Buffer.add_string buf "// questions, each left for an agent to fill. This file is written once and\n";
    Buffer.add_string buf "// never regenerated.\n\n";
    Printf.bprintf buf "package %s\n\n" service;
    List.iter (fun (q : question) ->
      Printf.bprintf buf "// csf:hole %s.%s\n" service q.qregion) questions;
    Some (Printf.sprintf "%s/%s_holes.go" (service_dir d) service, Buffer.contents buf)

(* Write the emitted tree under [out]: every projection, then the holes file when
   the declaration has questions. An existing holes file is left untouched, so
   the one written-once file is never regenerated. *)
let write ~source ~out (d : declaration) =
  let rec mkdir_p dir =
    if dir = "" || dir = "." || dir = "/" || dir = Filename.dirname dir then ()
    else if Sys.file_exists dir then ()
    else (mkdir_p (Filename.dirname dir); Unix.mkdir dir 0o755) in
  let write_file path contents =
    mkdir_p (Filename.dirname path);
    let temporary = Filename.temp_file ~temp_dir:(Filename.dirname path) ".csfc-" ".tmp" in
    Fun.protect
      ~finally:(fun () -> if Sys.file_exists temporary then Sys.remove temporary)
      (fun () ->
        Out_channel.with_open_bin temporary (fun channel -> output_string channel contents);
        Sys.rename temporary path) in
  List.iter (fun (relative, contents) ->
    write_file (Filename.concat out relative) contents) (projections ~source d);
  match holes ~source d with
  | Some (relative, contents) ->
      let path = Filename.concat out relative in
      if not (Sys.file_exists path) then write_file path contents
  | None -> ()
