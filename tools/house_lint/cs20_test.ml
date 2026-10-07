(* parse_checked rejects a fixture the Go grammar cannot parse, so a fixture
   typo is a test failure and never a silent zero. *)
let parse_checked (path, source) =
  let file = Source.parse path source in
  if Source.Node.has_error file.root then begin
    Printf.eprintf "Invalid fixture %s:\n%s\n" path source;
    failwith (path ^ ": invalid fixture syntax")
  end;
  file

let findings ?(path="fixture/source.go") source =
  CS20.collect [parse_checked (path, source)]
  |> List.filter (fun (finding : Source.finding) -> finding.rule = "CS-20")

let count ?path label expected source =
  let actual = findings ?path source in
  if List.length actual <> expected then begin
    List.iter (fun (finding : Source.finding) ->
      Printf.eprintf "%s:%d %s %s\n" finding.path finding.line finding.rule finding.message) actual;
    failwith (Printf.sprintf "%s: expected %d findings, got %d" label expected (List.length actual))
  end

(* results_3_or_more_unnamed, the ticket's golden fixture *)
let test_results () =
  count "three results is one offense" 1 {|package fixture
import "net/http"
type Resp struct{ Code int }
func perform(name string, header http.Header) (Resp, http.Header, error) {
	return Resp{}, header, nil
}
|};
  count "four results is one offense" 1 {|package fixture
func quad() (int, string, bool, error) { return 0, "", false, nil }
|};
  count "(value, error) is the allowed shape" 0 {|package fixture
func load(name string) (string, error) { return name, nil }
|};
  count "(value, bool) is the allowed shape" 0 {|package fixture
func lookup(key string) (int, bool) { return 0, false }
|};
  count "the net/http.Hijacker contract keeps the standard library's shape" 0 {|package fixture
import (
	"bufio"
	"net"
)
type sized struct{ inner net.Conn }
func (s sized) Hijack() (net.Conn, *bufio.ReadWriter, error) { return nil, nil, nil }
|};
  count "a three-result method that only borrows Hijack's shape still fires" 1 {|package fixture
import (
	"bufio"
	"net"
)
type sized struct{ inner net.Conn }
func (s sized) hijack() (net.Conn, *bufio.ReadWriter, error) { return nil, nil, nil }
|};
  count "the MCP tool handler contract keeps the SDK's shape" 0 {|package fixture
import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)
type view struct{ Name string }
func handler(ctx context.Context, request *mcp.CallToolRequest, input view) (*mcp.CallToolResult, view, error) {
	return nil, view{}, nil
}
|};
  count "a three-result function without the trailing error is not an MCP handler" 1 {|package fixture
import "github.com/modelcontextprotocol/go-sdk/mcp"
func load(name string) (*mcp.CallToolResult, string, bool) { return nil, name, false }
|};
  count "a single named struct result is the fix" 0 {|package fixture
type result struct {
	Code   int
	Header string
}
func perform(name string) result { return result{} }
|}

(* go_anonymous_struct_type *)
let test_anonymous () =
  count "an anonymous struct variable is one offense" 1 {|package fixture
func keep() {
	var counters struct {
		Total int
	}
	counters.Total++
}
|};
  count "an anonymous struct field type is one offense" 1 {|package fixture
type holder struct {
	inner struct {
		Value int
	}
}
|};
  count "a declared struct type is the fix" 0 {|package fixture
type counters struct {
	Total int
}
func keep() {
	var c counters
	c.Total++
}
|};
  count "the empty struct set idiom is not a compound" 0 {|package fixture
func keep(seen map[string]struct{}) {
	seen["x"] = struct{}{}
}
|}

(* ocaml_tuple_arity_3_or_more, the text scanner *)
let ocaml_count label expected source =
  let actual = CS20.ocaml_findings "fixture/source.ml" source in
  if List.length actual <> expected then begin
    List.iter (fun (finding : Source.finding) ->
      Printf.eprintf "%s:%d %s %s\n" finding.path finding.line finding.rule finding.message) actual;
    failwith (Printf.sprintf "%s: expected %d findings, got %d" label expected (List.length actual))
  end

let test_ocaml () =
  ocaml_count "a three-tuple literal is one offense" 1
    {|let triple = (start, stop, prose)|};
  ocaml_count "a four-tuple literal is one offense" 1
    {|let bound a b c d = compare (a.path, b.line, c.rule, d.message)|};
  ocaml_count "a pair is the allowed shape" 0
    {|let pair = (left, right)|};
  ocaml_count "a type variable tuple is not a value tuple" 0
    {|type ('a, 'b, 'c) triple = 'a * 'b * 'c|};
  ocaml_count "a tuple inside a comment does not count" 0
    {|(* (a, b, c) is a comment, not code *) let unit_ = ()|};
  ocaml_count "a tuple inside a string does not count" 0
    {|let text = "(a, b, c)"|};
  ocaml_count "a tuple inside a tagged quoted string does not count" 0
    {|let dl = {dl|replacement(noun, verb, tool)|dl}|};
  ocaml_count "a tuple inside an untagged quoted string does not count" 0
    {q|let dl = {|(a, b, c)|}|q};
  ocaml_count "a fold whose lambda binds a pair is a call, not a tuple" 0
    {|let bound, problems = List.fold_left (fun (bound, problems) literal ->
        match literal with
        | Value v -> bound, v :: problems
        | _ -> bound, problems) ([], []) literals|};
  ocaml_count "a lambda binding a three-tuple is a pattern, not a tuple" 0
    {|let project = List.map (fun (a, b, c) -> a + b + c) triples|};
  ocaml_count "a match-arm constructor tuple is a pattern, not a tuple" 0
    {|let name = match value with Some (typ, values, replacement) -> typ | None -> ""|};
  ocaml_count "a let-bound argument tuple is a pattern, not a tuple" 0
    {|let precision (tp, fp, _) = List.length tp + List.length fp|};
  ocaml_count "a match scrutinee tuple is still a value" 1
    {|let head = match (field "r" json, field "arm" json, field_num "s" json) with a, b, c -> a|};
  ocaml_count "a constructor wrapping a tuple in value position still fires" 1
    {|let kept = Some (node, name, body)|};
  ocaml_count "a qualified constructor arm is a pattern, not a tuple" 0
    {|let gone = match value with None -> () | Some (a, b, c) -> ()|};
  ocaml_count "a qualified constructor pattern is a pattern, not a tuple" 0
    {|match outcome with
      | Unix.Unix_error (error, operation, _) -> raise error
      | Ok kept -> kept|};
  ocaml_count "the left of = is a pattern, the right a value" 1
    {|let (a, b, c) = (1, 2, 3)|};
  ocaml_count "a map whose lambda returns a pair is a call, not a tuple" 0
    {|let project fields = `Assoc (List.map (fun (key, value) -> key, match key, value with _ -> value) fields)|};
  ocaml_count "an if expression is not a tuple" 0
    {|let outcome = (if ready then first, second else third, fourth)|};
  ocaml_count "a let-in expression is not a tuple" 0
    {|let offset = (let base = 4 in base, base + 1, base + 2)|};
  ocaml_count "a list and a record nested in a pair are not tuple commas" 0
    {|let shape = (elements @ [tail], {start; stop; prose})|};
  ocaml_count "a polymorphic variant type is not a value tuple" 0
    {|type token = [ `Name | `Int | `Op ]|}

(* csf_inline_compound_field, the ticket's golden fixture *)
let csf_count label expected source =
  let actual = CS20.csf_findings "fixture/source.csf" source in
  if List.length actual <> expected then begin
    List.iter (fun (finding : Source.finding) ->
      Printf.eprintf "%s:%d %s %s\n" finding.path finding.line finding.rule finding.message) actual;
    failwith (Printf.sprintf "%s: expected %d findings, got %d" label expected (List.length actual))
  end

let test_csf () =
  csf_count "an inline record field and an inline list field fire" 3
    {|op [operation(name, in: record, out: record, outcomes: [outcome])]|};
  csf_count "a named kind field is the fix" 0
    {|op [operation(name, in: fields, out: fields, outcomes: outcomes)]|};
  csf_count "a scalar kind declaration does not fire" 0
    {|term observe "Observe" "Read the run's events." kind concept;|};
  csf_count "a colon inside a string is prose, not a field" 0
    {|term note "Note" "A field like this: record the value." kind concept;|};
  csf_count "a colon inside a comment does not fire" 0
    {|// in: record, out: record is the offending shape
term observe "Observe" "Read." kind concept;|}

(* datalog_relation_undeclared, the ticket's golden fixture *)
let datalog_count label expected source =
  let actual = CS20.datalog_findings "fixture/rules.dl" source in
  if List.length actual <> expected then begin
    List.iter (fun (finding : Source.finding) ->
      Printf.eprintf "%s:%d %s %s\n" finding.path finding.line finding.rule finding.message) actual;
    failwith (Printf.sprintf "%s: expected %d findings, got %d" label expected (List.length actual))
  end

let test_datalog () =
  datalog_count "a body relation with no declaration fires" 1
    {|/* facts
   relation   run ⊆ R; gated ⊆ R
*/
invisible(R) :- gated(R), knee(K), gt(S, K).|};
  datalog_count "every declared relation passes" 0
    {|/* facts
   relation   run ⊆ R; gated ⊆ R; score ⊆ R × S
*/
invisible(R) :- gated(R), score(R, S), gt(S, K).|};
  datalog_count "a fact clause and a rule head introduce relations" 0
    {|/* facts
   relation   routing_decision ⊆ V × R × Arm
*/
valid_arm(attach_warm).
invalid_arm(V) :- routing_decision(V, R, A), ~valid_arm(A).|};
  datalog_count "the is-subset-of spelling declares" 0
    {|/* facts
   relation   routing_decision is subset of V x R x Arm x Score
*/
valid_arm(fresh).
invalid_arm(V, R, Arm) :- routing_decision(V, R, Arm, S), ~valid_arm(Arm).|};
  datalog_count "a rule head is declared by its own clause" 0
    {|scope_reach(S, X) :- scope_up(S, X).
scope_reach(S, X) :- scope_reach(S, Y), scope_up(Y, X).
/* facts
   relation   scope_up ⊆ Sc × Sc
*/|};
  datalog_count "a relation named only in prose is not a use" 0
    {|/* predicate invisible
   relation   score : R → ℕ
   the contract adds knee(K), injected by the engine
*/
invisible(R) :- score(R, S).|};
  datalog_count "the engine's built-ins are not undeclared relations" 0
    {|/* facts
   relation   score ⊆ R × S
*/
invisible_count(N) :- N := count I : score(I, S), gt(N, 0).|}

let () =
  test_results ();
  test_anonymous ();
  test_ocaml ();
  test_csf ();
  test_datalog ();
  print_endline "CS-20 house rule OCaml tests passed"
