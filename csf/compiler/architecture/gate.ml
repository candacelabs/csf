(** The gate declaration and its two projections. A gate is one rule a session
    enforces, declared once in the ontology language; [render_dl] emits the
    Datalog program the evaluator reads and [render_go] emits the Go check the
    harness registers through [WithChecks]. Both are pure functions of the
    declaration, pinned byte for byte by a golden test. Nothing here reads a
    file, runs the check or observes a session; [check] reports the
    declaration's own defects as diagnostics. *)

open Model

exception Invalid of diagnostic

let invalid (node : Frontend.node) message =
  raise (Invalid { at = node.at; code = "CSF_GATE"; message })

(* --- the declaration --- *)

type side = Harness | Harness_environment | Environment | Provider | Model

(** One replacement: the gh noun and verb a session maps, and the CSF tool that
    replaces the command the two name. *)
type replacement = { noun : string; verb : string; tool : string }

(** A fact names its relation and then its arguments: the gh verb a gate maps,
    the gh noun it names, or one replacement (noun, verb, CSF tool). *)
type fact =
  | Verb of string
  | Word of string
  | Replacement of replacement

type gate = {
  gate_id : string;
  title : string;
  definition : string;
  edge_from : string;
  edge_to : string;
  side : side;
  relations : (string * string) list;
  rules : (string * string) list;
  facts : fact list;
  gate_at : location;
}

let side_of = function
  | "harness" -> Harness | "harness_environment" -> Harness_environment
  | "environment" -> Environment | "provider" -> Provider | "model" -> Model
  | value -> failwith ("unsupported gate side: " ^ value)

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

(* Every fact is a sequence of identifier leaves that ends in ';'. The first
   identifies the relation, and the arity then decides it. *)
let decode_fact (node : Frontend.node) =
  let atoms = List.filter_map (fun (child : Frontend.node) ->
    if child.Frontend.rule = "identifier" then child.Frontend.value else None)
      node.Frontend.children in
  match atoms with
  | ["verb"; value] -> Verb value
  | ["word"; noun] -> Word noun
  | ["replacement"; noun; verb; tool] -> Replacement { noun; verb; tool }
  | _ -> invalid node "unsupported fact: expected word W, verb V or replacement W V T"

let declared kind (node : Frontend.node) =
  let reader = { parent = node; rest = node.Frontend.children } in
  terminal kind reader;
  let name = literal "identifier" reader in
  let definition = literal "string" reader in
  (name, definition)

let decode_gate (node : Frontend.node) =
  let reader = { parent = node; rest = node.Frontend.children } in
  terminal "gate" reader;
  let gate_id = literal "identifier" reader in
  let title = literal "string" reader in
  let definition = literal "string" reader in
  let edge = take "edge_clause" reader in
  let edge_reader = { parent = edge; rest = edge.Frontend.children } in
  terminal "edge" edge_reader;
  let edge_from = literal "identifier" edge_reader in
  let edge_to = literal "identifier" edge_reader in
  let side_clause = take "side_clause" reader in
  let side_reader = { parent = side_clause; rest = side_clause.Frontend.children } in
  terminal "side" side_reader;
  let side_node = take "side" side_reader in
  let side = match (take "$terminal"
    { parent = side_node; rest = side_node.Frontend.children }).Frontend.value with
    | Some value -> side_of value | None -> invalid side_node "side without a value" in
  let rec collect rule decode acc = match reader.rest with
    | next :: _ when next.Frontend.rule = rule ->
        let next = take rule reader in
        collect rule decode (decode next :: acc)
    | _ -> List.rev acc in
  let relations = collect "relation" (declared "relation") [] in
  let rules = collect "rule" (declared "rule") [] in
  terminal "{" reader;
  let facts = collect "fact" decode_fact [] in
  terminal "}" reader;
  (match reader.rest with
   | [] -> ()
   | extra :: _ -> invalid extra ("unconsumed " ^ extra.Frontend.rule ^ " in gate"));
  { gate_id; title; definition; edge_from; edge_to; side; relations; rules; facts;
    gate_at = node.Frontend.at }

(** Parse a gate source against the gate grammar. Expected syntax and decoding
    errors become diagnostics; nothing is read from disk. *)
let parse ~grammar ~source ~filename =
  match Frontend.parse_text ~grammar ~source ~filename with
  | Error diagnostics -> Error diagnostics
  | Ok node -> (try Ok (decode_gate node) with Invalid diagnostic -> Error [diagnostic])

let parse_files ~grammar_path ~source_path =
  match Frontend.parse_files ~grammar_path ~source_path with
  | Error diagnostics -> Error diagnostics
  | Ok node -> (try Ok (decode_gate node) with Invalid diagnostic -> Error [diagnostic])

(* --- the check --- *)

let words (g : gate) =
  List.filter_map (function Word noun -> Some noun | _ -> None) g.facts
let replacements (g : gate) =
  List.filter_map (function Replacement replacement -> Some replacement | _ -> None) g.facts

(* The checks a gate must pass before it is projected: names are nonempty and
   unique, and every replacement names a declared noun. A finding is reported
   at the declaration, never at a running session. *)
let check (g : gate) =
  let findings = ref [] in
  let add code message = findings := { at = g.gate_at; code; message } :: !findings in
  let nonempty what value =
    if String.trim value = "" then add "gate_empty" (what ^ " must not be empty.") in
  let duplicates values =
    let rec walk acc = function
      | a :: (b :: _ as rest) when a = b -> walk (a :: acc) rest
      | _ :: rest -> walk acc rest
      | [] -> List.rev acc in
    walk [] (List.sort String.compare values) in
  let unique what values =
    List.iter (fun value -> add "gate_duplicate"
      (what ^ " is declared more than once: " ^ value)) (duplicates values) in
  let relation_names = List.map fst g.relations in
  let rule_names = List.map fst g.rules in
  let noun_names = words g in
  nonempty "The gate identifier" g.gate_id;
  nonempty "The gate title" g.title;
  nonempty "The gate definition" g.definition;
  nonempty "The gate edge" g.edge_from;
  nonempty "The gate edge" g.edge_to;
  unique "A relation" relation_names;
  unique "A rule" rule_names;
  unique "A gh noun" noun_names;
  List.iter (fun (name, _) -> nonempty ("The relation name " ^ name) name) g.relations;
  List.iter (fun (name, _) -> nonempty ("The rule name " ^ name) name) g.rules;
  List.iter (fun noun -> nonempty "A gh noun" noun) noun_names;
  List.iter (fun { noun; verb; tool } ->
    if not (List.mem noun noun_names) then
      add "gate_reference" ("No word fact names the noun " ^ noun ^ ".");
    nonempty "A gh verb" verb;
    nonempty "A CSF tool" tool) (replacements g);
  unique "A replacement" (List.map (fun { noun; verb; _ } -> noun ^ " " ^ verb) (replacements g));
  List.rev !findings

(* --- the projections --- *)

let capitalize value =
  if value = "" then value else
    String.make 1 (Char.uppercase_ascii value.[0]) ^ String.sub value 1 (String.length value - 1)

let lower_first value =
  if value = "" then value else
    String.make 1 (Char.lowercase_ascii value.[0]) ^ String.sub value 1 (String.length value - 1)

(* The github kind spells its Go names "GitHub"; every other token capitalizes
   its first letter. This is the one place the kind's spelling is fixed. *)
let go_word = function "github" -> "GitHub" | value -> capitalize value
let go_pascal value = String.concat "" (List.map go_word (String.split_on_char '_' value))

(* The gh noun's exported Go identifier: gh's own spellings for pr, issue and
   api, and the capitalized noun for anything outside that table. *)
let word_go_name = function
  | "pr" -> "PullRequest" | "issue" -> "Issue" | "api" -> "API"
  | noun -> capitalize noun

(* The gh verbs, in the order a reader meets them in the gh reference. The
   declaration orders its replacement facts, not its verbs, so the template
   owns this canonical order; a verb the table omits is appended after it. *)
let canonical_verbs =
  ["list"; "create"; "ready"; "merge"; "view"; "checks"; "edit"; "close"; "reopen"; "comment"]

let pad value width = value ^ String.make (max 0 (width - String.length value)) ' '
let go_string value = Printf.sprintf "%S" value

(* "gh pr, gh issue or gh api": every noun in declaration order, joined. *)
let gh_phrase joiner nouns =
  match List.rev (List.map (fun noun -> "gh " ^ noun) nouns) with
  | [] -> "gh command"
  | last :: rest -> String.concat ", " (List.rev rest) ^ " " ^ joiner ^ " " ^ last

let verbs_of g =
  let used = List.map (fun { verb; _ } -> verb) (replacements g) in
  let ordered = List.filter (fun verb -> List.mem verb used) canonical_verbs in
  ordered @ List.sort_uniq String.compare
    (List.filter (fun verb -> not (List.mem verb canonical_verbs)) used)

(** The Datalog program the evaluator runs. The rule and the two relations name
    the clause; every other line is the github kind's fixed prose. *)
let render_dl ~source (g : gate) =
  let name index default =
    match List.nth_opt (List.map fst g.relations) index with Some v -> v | None -> default in
  let word_rel = name 0 "word" and replacement_rel = name 1 "replacement" in
  let rule_id = match g.rules with (id, _) :: _ -> id | [] -> "rule" in
  let title = String.uppercase_ascii (String.map (fun c -> if c = '_' then '-' else c) g.gate_id) in
  Printf.sprintf
{dl|%% %s: the Datalog program csfc emits from the %s gate
%% declaration (%s). A session reaches GitHub
%% through CSF's typed GitHub tools, which record every call; the simple
%% commands in one Bash tool call are the claims.

/* facts
   universe   N: gh nouns the gate names; V: gh verbs; T: CSF GitHub tool names
   relation   %s ⊆ N
   relation   %s ⊆ N × V × T
*/

/* predicate %s
   universe   C: the simple commands in one Bash tool call
   relation   gh_noun : C ⇀ N, the gh noun a command names, when it names one
   relation   gh_verb : C ⇀ V, the gh verb a command names, when it names one
   clause     ∀c ∈ C. gh_noun(c) ∈ dom(%s) ⇒ refused(c)
   finding    %s = {c ∈ C | gh_noun(c) ∈ dom(%s)}
   tool       replacement_of = {(c, t) | gh_noun(c, n), gh_verb(c, v), %s(n, v, t)}
*/
%s(C) :- gh_noun(C, N), %s(N).
replacement_of(C, T) :- gh_noun(C, N), gh_verb(C, V), %s(N, V, T).
|dl}
    title g.gate_id source word_rel replacement_rel rule_id word_rel rule_id word_rel
    replacement_rel rule_id word_rel replacement_rel

(** The Go check the harness registers. The declaration supplies the gate and
    rule names, the typed facts and the decoders; every other line is the
    github kind's fixed shape. *)
let render_go ~source (g : gate) =
  let noun_names = words g and replacement_facts = replacements g in
  let verb_names = verbs_of g in
  let rule_id, rule_definition =
    match g.rules with (id, def) :: _ -> id, def | [] -> "rule", "" in
  let gate_const = "Gate" ^ go_pascal g.edge_to and rule_const = "Rule" ^ go_pascal rule_id in
  let word_name noun = "Word" ^ word_go_name noun and verb_name verb = "Verb" ^ capitalize verb in
  let buf = Buffer.create 8192 in
  let add = Buffer.add_string buf in
  Printf.bprintf buf "// Code generated by csfc from %s; DO NOT EDIT.\n" source;
  add "//\n";
  Printf.bprintf buf "// %s: a session reaches GitHub through CSF's typed GitHub tools,\n" g.gate_id;
  Printf.bprintf buf "// which record every call, so a %s command in the\n" (gh_phrase "or" noun_names);
  add "// Bash shell is refused and names the tool that replaces it.\n\n";
  add "package sessiongate\n\n";
  add "import (\n\t\"context\"\n\t\"fmt\"\n\t\"log/slog\"\n\t\"slices\"\n\t\"strings\"\n\n";
  add "\t\"mvdan.cc/sh/v3/syntax\"\n\n";
  add "\t\"github.com/candacelabs/csf/csf/githubtools\"\n";
  add "\t\"github.com/candacelabs/csf/services/harness/session\"\n)\n\n";
  add "// The gate's name and its one rule, as the event log records them.\nconst (\n";
  Printf.bprintf buf "\t// %s is the PreToolUse gate on Bash that refuses a gh command\n" gate_const;
  add "\t// reaching GitHub's issues, pull requests or API.\n";
  Printf.bprintf buf "\t%s = %s\n" gate_const (go_string g.edge_to);
  Printf.bprintf buf "\t// %s is %s\n" rule_const (lower_first rule_definition);
  Printf.bprintf buf "\t%s Rule = %s\n\n" rule_const (go_string rule_id);
  add "\tmcpToolPrefix = \"mcp__\" + session.MCPServerName + \"__\"\n";
  add "\ttoolList      = \"csf github -list\"\n)\n\n";
  add "// GithubWord is a gh noun the gate names: the first word after gh.\n";
  add "type GithubWord string\n\n";
  add "// The gh nouns. WordAPI reaches the API directly and names no verb.\nconst (\n";
  let word_consts = List.map (fun noun -> word_name noun, noun) noun_names in
  let word_width = List.fold_left (fun w (name, _) -> max w (String.length name)) 0 word_consts in
  List.iter (fun (name, noun) ->
    Printf.bprintf buf "\t%s GithubWord = %s\n" (pad name word_width) (go_string noun)) word_consts;
  add ")\n\n";
  add "// GithubVerb is a gh verb the gate maps to one CSF tool.\n";
  add "type GithubVerb string\n\n";
  add "// The gh verbs that name a tool.\nconst (\n";
  let verb_consts = List.map (fun verb -> verb_name verb, verb) verb_names in
  let verb_width = List.fold_left (fun w (name, _) -> max w (String.length name)) 0 verb_consts in
  List.iter (fun (name, verb) ->
    Printf.bprintf buf "\t%s GithubVerb = %s\n" (pad name verb_width) (go_string verb)) verb_consts;
  add ")\n\n";
  add "// GithubReplacement is one typed fact: replacement(Word, Verb, Tool), the CSF\n";
  add "// tool that replaces the gh command \"gh Word Verb\". The relation is a typed\n";
  add "// list, never a map of spellings.\n";
  add "type GithubReplacement struct {\n\tWord GithubWord\n\tVerb GithubVerb\n\tTool string\n}\n\n";
  add "// GithubReplacements is the replacement relation, in declaration order.\n";
  add "var GithubReplacements = []GithubReplacement{\n";
  let previous = ref None in
  List.iter (fun { noun; verb; tool } ->
    (match !previous with Some last when last <> noun -> add "\n" | _ -> ());
    previous := Some noun;
    Printf.bprintf buf "\t{Word: %s, Verb: %s, Tool: githubtools.Tool%s},\n"
      (word_name noun) (verb_name verb) (go_pascal tool)) replacement_facts;
  add "}\n\n";
  add "// wordOf decodes a literal token to the gh noun it names.\n";
  add "func wordOf(token string) (GithubWord, bool) {\n\tswitch GithubWord(token) {\n";
  List.iter (fun noun ->
    Printf.bprintf buf "\tcase %s:\n\t\treturn %s, true\n" (word_name noun) (word_name noun)) noun_names;
  add "\tdefault:\n\t\treturn \"\", false\n\t}\n}\n\n";
  add "// verbOf decodes a literal token to the gh verb it names.\n";
  add "func verbOf(token string) (GithubVerb, bool) {\n\tswitch GithubVerb(token) {\n";
  List.iter (fun verb ->
    Printf.bprintf buf "\tcase %s:\n\t\treturn %s, true\n" (verb_name verb) (verb_name verb)) verb_names;
  add "\tdefault:\n\t\treturn \"\", false\n\t}\n}\n\n";
  add "// replacementTool is the CSF tool that replaces the gh command \"gh word verb\",\n";
  add "// or \"\" when no one tool does.\n";
  add "func replacementTool(word GithubWord, verb GithubVerb) string {\n";
  add "\tat := slices.IndexFunc(GithubReplacements, func(replacement GithubReplacement) bool {\n";
  add "\t\treturn replacement.Word == word && replacement.Verb == verb\n\t})\n";
  add "\tif at < 0 {\n\t\treturn \"\"\n\t}\n\treturn GithubReplacements[at].Tool\n}\n\n";
  add "// GitHubFinding is one gh command a session may not run, and the tool that\n";
  add "// replaces it.\n";
  add "type GitHubFinding struct {\n\tSnippet string\n";
  add "\t// Tool is the replacing tool's name; empty when no one tool replaces\n";
  add "\t// the command, as for gh api.\n\tTool string\n}\n\n";
  add "// Message is the refusal the agent reads.\n";
  add "func (finding GitHubFinding) Message() string {\n";
  add "\treplacement := \"the CSF GitHub tools (\" + toolList + \" names them; each is the MCP tool \" + mcpToolPrefix + \"<name>)\"\n";
  add "\tif finding.Tool != \"\" {\n";
  add "\t\treplacement = fmt.Sprintf(\"the MCP tool %s%s (csf github %s from a script)\", mcpToolPrefix, finding.Tool, finding.Tool)\n";
  add "\t}\n";
  add "\treturn fmt.Sprintf(\"%s: %q is refused in a CSF session; GitHub is reached through CSF's typed GitHub tools, which record every call. Use %s\", ";
  Printf.bprintf buf "%s, finding.Snippet, replacement)\n}\n\n" rule_const;
  add "// FindGitHubCommands parses command as bash and returns every gh pr, gh\n";
  add "// issue and gh api command in it, including inside sh -c, in source order.\n";
  add "func FindGitHubCommands(command string) ([]GitHubFinding, error) {\n";
  add "\tfile, err := parse(command)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n";
  add "\tvar findings []GitHubFinding\n";
  add "\tsyntax.Walk(file, func(node syntax.Node) bool {\n";
  add "\t\tcall, ok := node.(*syntax.CallExpr)\n\t\tif !ok {\n\t\t\treturn true\n\t\t}\n";
  add "\t\tname, arguments := commandWords(call)\n\t\tswitch {\n";
  add "\t\tcase name == commandGh && len(arguments) > 0:\n";
  add "\t\t\tword, known := wordOf(arguments[0])\n\t\t\tif !known {\n\t\t\t\treturn true\n\t\t\t}\n";
  add "\t\t\tfinding := GitHubFinding{Snippet: snippetOf(command, call)}\n";
  add "\t\t\tif len(arguments) > 1 {\n\t\t\t\tif verb, ok := verbOf(arguments[1]); ok {\n";
  add "\t\t\t\t\tfinding.Tool = replacementTool(word, verb)\n\t\t\t\t}\n\t\t\t}\n";
  add "\t\t\tfindings = append(findings, finding)\n";
  add "\t\tcase shells[name]:\n\t\t\tif script, ok := shellScript(arguments); ok {\n";
  add "\t\t\t\tnested, _ := FindGitHubCommands(script)\n\t\t\t\tfindings = append(findings, nested...)\n";
  add "\t\t\t}\n\t\t}\n\t\treturn true\n\t})\n\treturn findings, nil\n}\n\n";
  add "func snippetOf(source string, node syntax.Node) string {\n";
  add "\tstart, end := int(node.Pos().Offset()), int(node.End().Offset())\n";
  add "\tif start < 0 || end > len(source) || start > end {\n\t\treturn \"\"\n\t}\n";
  add "\treturn strings.TrimSpace(source[start:end])\n}\n\n";
  add "// GitHubCheck is the generated PreToolUse check for the github gate: it\n";
  add "// refuses a Bash call whose command reaches GitHub through gh, naming the CSF\n";
  add "// tool that replaces each command. It is a function value, not a method, so\n";
  add "// the gate registers it in data.\n";
  add "func GitHubCheck(ctx context.Context, call *gateCall) *HookOutput {\n";
  add "\tif call.hook.ToolName != session.ToolBash {\n\t\treturn nil\n\t}\n";
  add "\tfindings, err := FindGitHubCommands(call.hook.ToolInput.Command)\n";
  add "\tif err != nil || len(findings) == 0 {\n\t\treturn nil\n\t}\n";
  add "\tmessages := make([]string, 0, len(findings))\n";
  add "\tfor _, finding := range findings {\n\t\tmessages = append(messages, finding.Message())\n\t}\n";
  add "\treason := \"Rejected by the CSF session gate. \" + strings.Join(messages, \"; \")\n";
  Printf.bprintf buf "\tcall.record(ctx, %s, DecisionDeny, slog.Any(keyRules, []string{string(%s)}), slog.String(keyReason, reason))\n" gate_const rule_const;
  add "\treturn &HookOutput{HookSpecificOutput: &HookSpecificOutput{\n";
  add "\t\tHookEventName:            session.HookPreToolUse,\n";
  add "\t\tPermissionDecision:       permissionDeny,\n";
  add "\t\tPermissionDecisionReason: reason,\n\t}}\n}\n";
  Buffer.contents buf
