(* parse_checked rejects a fixture the Go grammar cannot parse, so a fixture
   typo is a test failure and never a silent zero. *)
let parse_checked (path, source) =
  let file = Source.parse path source in
  if Source.Node.has_error file.root then begin
    Printf.eprintf "Invalid fixture %s:\n%s\n" path source;
    Source.descendants file.root |> List.iter (fun node ->
      if Source.Node.is_error node || Source.Node.is_missing node then
        Printf.eprintf "invalid fixture node %s: %s\n" (Source.kind node) (Source.text file node));
    failwith (path ^ ": invalid fixture syntax")
  end;
  file

let findings ?(path="fixture/source.go") source =
  CS19.collect [parse_checked (path, source)]
  |> List.filter (fun (finding : Source.finding) -> finding.rule = "CS-19")

let count ?path label expected source =
  let actual = findings ?path source in
  if List.length actual <> expected then begin
    List.iter (fun (finding : Source.finding) ->
      Printf.eprintf "%s:%d %s %s\n" finding.path finding.line finding.rule finding.message) actual;
    failwith (Printf.sprintf "%s: expected %d findings, got %d" label expected (List.length actual))
  end

(* reflect_import *)
let test_reflect () =
  count "a reflect import is one offense" 1 {|package fixture
import "reflect"
var kind = reflect.TypeOf(0)
|};
  count "an aliased reflect import is still the reflect package" 1 {|package fixture
import r "reflect"
var kind = r.TypeOf(0)
|};
  count "generics and declared types are not reflect" 0 {|package fixture
func Toggle[T comparable](values []T, want T) []T { return values }
|}

(* decode_into_anonymous_struct *)
let test_decode () =
  count "a decoder filling an anonymous struct is one offense" 1 {|package fixture
import (
	"encoding/json"
	"time"
)
func settle(records [][]byte) {
	for _, record := range records {
		var stamped struct {
			Time time.Time `json:"time"`
		}
		if json.Unmarshal(record, &stamped) == nil {
			_ = stamped.Time
		}
	}
}
|};
  count "a named record type is the fix" 0 {|package fixture
import (
	"encoding/json"
	"time"
)
type stamp struct {
	Time time.Time `json:"time"`
}
func settle(record []byte) {
	var stamped stamp
	_ = json.Unmarshal(record, &stamped)
}
|};
  count "a non-decoder call into an anonymous struct is not this offense" 0 {|package fixture
func keep() {
	var counters struct {
		Total int
	}
	counters.Total++
}
|}

(* string_map_for_typed_fields *)
let test_string_map () =
  count "a stringified typed field parsed back in the same file is one offense" 1
    {|package fixture
import "strconv"
func bind(seq int) map[string]string {
	return map[string]string{"row": strconv.Itoa(seq)}
}
func read(key string) int {
	value, err := strconv.Atoi(key)
	if err != nil {
		return 0
	}
	return value
}
|};
  count "a plain string map with no strconv round trip is not this offense" 0
    {|package fixture
func headers() map[string]string {
	return map[string]string{"accept": "application/json"}
}
|};
  count "a stringified typed field with no parse anywhere is not this offense" 0
    {|package fixture
import "strconv"
func bind(seq int) map[string]string {
	return map[string]string{"row": strconv.Itoa(seq)}
}
|};
  (* The parse can live in a sibling file of the same package, which is how the
     site that named this rule is laid out. *)
  let render = parse_checked ("fixture/render.go", {|package fixture
import "strconv"
func bind(seq int) map[string]string {
	return map[string]string{"row": strconv.Itoa(seq)}
}
|}) in
  let chat = parse_checked ("fixture/chat.go", {|package fixture
import "strconv"
func read(key string) int {
	value, err := strconv.Atoi(key)
	if err != nil {
		return 0
	}
	return value
}
|}) in
  let cross_file = CS19.collect [render; chat]
    |> List.filter (fun (finding : Source.finding) -> finding.rule = "CS-19") in
  if List.length cross_file <> 1 then
    failwith (Printf.sprintf "cross-file string map: expected 1 finding, got %d" (List.length cross_file))

(* local_generic_helper *)
let test_local_helper () =
  count "a local set toggle is one offense" 1 {|package fixture
import "slices"
func toggled(open []int, seq int) []int {
	if index := slices.Index(open, seq); index >= 0 {
		return slices.Delete(slices.Clone(open), index, index+1)
	}
	return append(slices.Clip(open), seq)
}
|};
  count "a local lookup by key is one offense" 1 {|package fixture
import "slices"
type row struct{ Seq int }
type transcript struct{ Rows []row }
func (shown transcript) rowOf(seq int) (row, bool) {
	index := slices.IndexFunc(shown.Rows, func(each row) bool { return each.Seq == seq })
	if index < 0 {
		return row{}, false
	}
	return shown.Rows[index], true
}
|};
  count "a single-result IndexFunc search is not the lookup-by-key shape" 0 {|package fixture
import "slices"
func first(values []int, want int) int {
	return slices.IndexFunc(values, func(each int) bool { return each == want })
}
|};
  count "calling the exported primitives is the fix" 0 {|package fixture
import "collections"
func toggled(open []int, seq int) []int { return collections.Toggle(open, seq) }
|};
  (* The primitive's own home implements the refused shape; defining it there is
     the fix, so only reimplementations elsewhere fire. *)
  count ~path:"pkg/collections/collections.go" "the exported primitive's own home is exempt" 0 {|package collections
import "slices"
type Set[T comparable] []T
func (set Set[T]) Toggle(value T) Set[T] {
	if index := slices.Index(set, value); index >= 0 {
		return slices.Delete(slices.Clone(set), index, index+1)
	}
	return append(slices.Clip(set), value)
}
|}

(* The four shapes do not cross-fire, and a clean package reports nothing. *)
let test_clean () =
  count "a package using declared records, typed fields and exported helpers is clean" 0
    {|package fixture
import (
	"encoding/json"
	"collections"
)
type row struct {
	Seq  int    `json:"seq"`
	Name string `json:"name"`
}
func decode(record []byte) (row, error) {
	var shown row
	if err := json.Unmarshal(record, &shown); err != nil {
		return row{}, err
	}
	return shown, nil
}
func toggle(open []int, seq int) []int { return collections.Toggle(open, seq) }
|}

let () =
  test_reflect ();
  test_decode ();
  test_string_map ();
  test_local_helper ();
  test_clean ();
  print_endline "CS-19 house rule OCaml tests passed"
