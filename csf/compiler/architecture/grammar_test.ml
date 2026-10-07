(* The JEV-writability test, the gate's proof for #524: three claims, in the
   order the gate reads them.

   1. The census over the six CSF grammars is the live reading behind the meter
      the scoreboard publishes (whose committed seed is main at 369d12d). A
      use-site is one EBNF production; one JEV pick can write it when its
      right-hand side names no free-text builtin one pick cannot write -- an
      identifier is a reference or a span, a string a quote or a rendered
      description, an integer a computed version or a read quantity, while text
      stays free -- and no choice in it offers more than sixteen options.
   2. The offense relation reports every site one pick cannot write, tagged
      [grammar_unbounded], and reports nothing else.
   3. A JEV writer picks a `service blah` declaration into existence -- every
      pick a question of two to sixteen options, every option a declared name --
      and csfc parses and checks the result clean. *)

let expect message condition = if not condition then failwith message

let read path = In_channel.with_open_bin path In_channel.input_all

let accept = function
  | Ok value -> value
  | Error errors -> failwith (String.concat "; "
      (List.map (fun (error : Model.diagnostic) -> error.message) errors))

(* --- the census --- *)

let grammar_files = List.init (max 0 (Array.length Sys.argv - 1)) (fun index -> Sys.argv.(index + 1))

let census () =
  expect "the build hands in the six CSF grammars" (List.length grammar_files = 6);
  (* The operator's one_pick rule: a production is writable only when it offers
     at most sixteen alternatives and names no free-text builtin, and the
     free-text set is exactly [identifier; integer; string; text]. A site that
     names [text] is therefore not writable, wherever one appears. *)
  expect "the free-text builtins are [identifier; integer; string; text]"
    (Grammar.builtins = ["identifier"; "integer"; "string"; "text"]);
  begin match Grammar.sites_of_file ~file:"synthetic.ebnf" "clause = 'clause', text, ';' ;" with
  | [site] ->
      expect "a site that names text is not writable"
        (site.Grammar.free_text = ["text"] && not (Grammar.bounded site))
  | _ -> expect "the synthetic clause is one use-site" false
  end;
  let sites = Grammar.census_files grammar_files in
  (* The definition, on every site: writable means no free text and no choice
     wider than the bound. *)
  List.iter (fun (site : Grammar.use_site) ->
    expect (Printf.sprintf "%s: writable must mean no free text and no choice over %d"
              (Grammar.site_id site) Grammar.max_options)
      (Grammar.bounded site = (site.free_text = [] && site.alternatives <= Grammar.max_options))) sites;
  let bounded, total = Grammar.share sites in
  let unbounded = List.filter (fun site -> not (Grammar.bounded site)) sites in
  (* Nothing is unbounded for a third reason the definition does not name. *)
  List.iter (fun (site : Grammar.use_site) ->
    expect (Printf.sprintf "%s is unbounded for a reason the bound names" (Grammar.site_id site))
      (Grammar.bounded site || site.free_text <> [] || site.alternatives > Grammar.max_options)) sites;
  let report = Validate.grammar_diagnostics sites in
  expect "the report counts every use-site" (report.Validate.use_sites = total);
  expect "the report counts every writable use-site" (report.Validate.bounded_use_sites = bounded);
  expect "every offense is grammar_unbounded"
    (List.for_all (fun (offense : Model.diagnostic) -> offense.Model.code = "grammar_unbounded")
       report.Validate.offenses);
  let named id (offense : Model.diagnostic) =
    String.length offense.Model.message > String.length id &&
    String.sub offense.Model.message 0 (String.length id) = id in
  let sorted values = List.sort String.compare values in
  let expected = sorted (List.map Grammar.site_id unbounded) in
  let reported = sorted (List.map (fun (offense : Model.diagnostic) ->
    match String.index_opt offense.Model.message ' ' with
    | Some stop -> String.sub offense.Model.message 0 stop
    | None -> offense.Model.message) report.Validate.offenses) in
  expect (Printf.sprintf "the offense relation must name exactly [%s]"
            (String.concat ", " expected))
    (expected = reported);
  expect "each offense is tagged at its site"
    (List.for_all2 named expected report.Validate.offenses);
  (bounded, total, unbounded)

(* --- the proof: jev_writes(declaration("service blah"), pick_by_pick) --- *)

(* A JEV pick is a typed choice: a question with two to sixteen named options.
   Fewer than two is not a choice, and more than sixteen does not fit one
   screen -- the same bound the census counts. *)
type question = { name : string; options : string list }

let question name options =
  let count = List.length options in
  expect (Printf.sprintf "the %s question offers %d options; JEV writes 2..%d"
            name count Grammar.max_options)
    (count >= 2 && count <= Grammar.max_options);
  { name; options }

(* A pick selects a declared option; an option the table does not hold cannot be
   written, so a writer can name only what the program declares. *)
let pick (q : question) option =
  expect (Printf.sprintf "%S is not in the %s table: [%s]"
            option q.name (String.concat "; " q.options))
    (List.mem option q.options);
  option

(* The tables the writer draws from are the program's declared kinds: the block
   heads the CSF language declares, the service the tree names, and the fields
   and value alternatives `kind service` declares in
   csf/compiler/testdata/golden/service/blah/blah.csf. Each holds two to
   sixteen names, so one pick writes any one of them. *)
let head = question "block head"
    ["kind"; "record"; "service"; "operation"; "architecture"; "directory"]
let service = question "service name" ["blah"; "email"]
let field = question "service field"
    ["purpose"; "lifecycle"; "host"; "op"; "stores"; "calls"; "config"; "template"]
let purpose = question "purpose"
    ["Answer one request with one bounded response."; "Record one decision."]
let lifecycle = question "lifecycle" ["scoped"; "lazy"; "borrowed"]
let host = question "host" ["deploy"; "nodeexec"; "warden"]
let op = question "operation" ["greet"; "send"]
let stores = question "stores" ["none"; "table"]
let calls = question "calls" ["none"; "rest_client"]
let config = question "config" ["none"; "record"]
let template = question "template" ["services"; "packages"]

let quoted text = Printf.sprintf "%S" text
let named name value = Printf.sprintf "%s %s;" (pick field name) value

(* The writer picks one token at a time and lays the fields out in the order
   `kind service` declares them. Nothing is typed: every head, name, field and
   value is one pick from a table above. *)
let picked_source = String.concat "\n" [
  Printf.sprintf "%s %s {" (pick head "service") (pick service "blah");
  named "purpose" (quoted (pick purpose "Answer one request with one bounded response."));
  named "lifecycle" (pick lifecycle "scoped");
  named "host" (pick host "deploy");
  named "op" (Printf.sprintf "[%s]" (pick op "greet"));
  named "stores" (pick stores "none");
  named "calls" (pick calls "none");
  named "config" (pick config "none");
  named "template" (quoted (pick template "services"));
  "}";
]

let proof () =
  let shell_grammar =
    match List.find_opt (fun path -> Filename.basename path = "shell.ebnf") grammar_files with
    | Some path -> path
    | None -> failwith "the build must hand in shell.ebnf" in
  let declaration =
    accept (Shell.parse ~grammar:(read shell_grammar) ~source:picked_source
              ~filename:"csf/compiler/testdata/golden/service/blah/blah.csf") in
  expect "csfc checks the picked declaration clean" (Shell.check declaration = []);
  match declaration.Shell.services with
  | [blah] ->
      expect "the writer picked the service blah" (blah.Shell.sname = "blah");
      expect "the writer picked the purpose it declared"
        (blah.Shell.purpose = "Answer one request with one bounded response.");
      expect "the writer picked the greet operation" (blah.Shell.sops = ["greet"]);
      expect "the writer picked the services template" (blah.Shell.stemplate = "services")
  | _ -> failwith "the picked declaration must hold exactly one service"

let () =
  let bounded, total, unbounded = census () in
  (* The census tracks the six grammar files as they stand at HEAD, so it moves
     when a slice edits a grammar: the bounded [tier] production one_tree added
     to language.ebnf took it from 33 of 80 to 34 of 81, and splitting
     [lowercase] into two sub-productions took it to 37 of 83 with the
     unbounded count down 47 -> 46. Treating an identifier that names a declared
     noun as a reference (a lookup, not typed text) cleared the five
     identifier-only productions [dependency], [connection], [block],
     [edge_clause] and [fact], to 42 of 83 with the unbounded count down
     46 -> 41. Making every identifier a span -- a reference is a lookup and a
     binder a copy -- cleared the six productions whose only builtin is
     [identifier]: [scope], [question], [diagram], [node], [group] and
     [policy], to 48 of 83 with the unbounded count down 41 -> 35. Taking
     [string] -- a quote is a verbatim span of the operator's tokens, a
     description is rendered from the typed fields -- cleared the thirty-three
     productions that named it but no [integer], to 81 of 83 with the unbounded
     count down 35 -> 2: only [architecture] and [token] remained, each naming
     [integer]. Taking [integer] -- a version is computed by csfc and monotonic,
     a quantity a declared range or a value read from the data -- cleared those
     two, to 83 of 83 with the unbounded count at 0. Every grammar use-site now
     fits one JEV pick. The committed seed the scoreboard publishes (main at
     369d12d) stays 33 of 80 until a re-measure. *)
  expect (Printf.sprintf "the census is 83 of 83 writable; found %d of %d, unbounded [%s]"
            bounded total (String.concat ", " (List.map Grammar.site_id unbounded)))
    (bounded = 83 && total = 83);
  let by_name suffix site =
    Filename.basename site.Grammar.file ^ ":" ^ site.Grammar.name = suffix in
  let site suffix sites = List.find (by_name suffix) sites in
  expect "a declared-noun identifier is a reference, so dependency is writable"
    (Grammar.bounded (site "language.ebnf:dependency" (Grammar.census_files grammar_files)));
  expect "a binder identifier is a span, so scope is writable"
    (Grammar.bounded (site "language.ebnf:scope" (Grammar.census_files grammar_files)));
  expect "a string is a quote or a rendered description, so scan is writable"
    (Grammar.bounded (site "language.ebnf:scan" (Grammar.census_files grammar_files)));
  expect "an integer is a computed version, so architecture is writable"
    (Grammar.bounded (site "language.ebnf:architecture" (Grammar.census_files grammar_files)));
  expect "a token names integer, still writable now"
    (Grammar.bounded (site "shell.ebnf:token" (Grammar.census_files grammar_files)));
  proof ();
  Printf.printf "CSF JEV writability: %d of %d use-sites writable, %d unbounded\n"
    bounded total (List.length unbounded)
