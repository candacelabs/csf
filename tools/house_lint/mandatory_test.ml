let check label expected files =
  let files = List.map (fun (path, source) -> Source.parse path source) files in
  let actual = Mandatory.collect files |> List.map (fun (finding : Source.finding) ->
    finding.rule, finding.path, finding.line) |> List.sort compare in
  let expected = List.sort compare expected in
  if actual <> expected then begin
    let render findings = findings |> List.map (fun (rule, path, line) ->
      Printf.sprintf "%s:%d:%s" path line rule) |> String.concat ", " in
    failwith (Printf.sprintf "%s\nexpected: %s\nactual:   %s" label (render expected) (render actual))
  end

let one label rules source =
  check label (List.map (fun (rule, line) -> rule, "sample.go", line) rules) ["sample.go", source]

let test_go_126_grammar () =
  List.iter (fun (expression, expected_kind) ->
    let file = Source.parse "allocation.go"
      ("package fixture\nfunc Allocate(request Request) { _ = " ^ expression ^ " }\n") in
    if Source.Node.has_error file.root then
      failwith ("Go 1.26 allocation must parse without recovery: " ^ expression);
    let allocation = Source.descendants file.root |> List.find (fun node ->
      Source.kind node = "call_expression" &&
      List.mem (Source.field_text file node "function") [Some "new"; Some "make"]) in
    let arguments = Option.get (Source.field allocation "arguments") |> Source.children in
    match arguments with
    | first :: _ when Source.kind first = expected_kind -> ()
    | _ -> failwith ("allocation argument lost its AST shape: " ^ expression)
  ) ["new(3)", "int_literal"; "new(\"state\")", "interpreted_string_literal";
     "new(int64(1))", "call_expression"; "new(int(request.Limit))", "call_expression";
     "new(int)", "type_identifier"; "new([]int)", "slice_type";
     "new(map[string]int)", "map_type"; "make([]int, 1)", "slice_type";
     "make(chan int, 1)", "channel_type"];
  let generics = Source.parse "generic.go" {|package fixture
type Box[T any] = struct { Value T }
func new[T any](value T) T { return value }
func make[T any](value T) T { return value }
func Calls() { _ = new[int](3); _ = make[[]int]([]int{1}) }
|} in
  if Source.Node.has_error generics.root then
    failwith "generic aliases and shadowed generic new/make calls must remain valid";
  let broken = Source.parse "broken.go" "package fixture\nfunc Broken() { _ = new(3 }\n" in
  if not (Source.Node.has_error broken.root) then
    failwith "allocation grammar must still reject malformed source"

let () =
  test_go_126_grammar ();
  one "interface names, aliases and grouped declarations"
    ["CS-1", 2; "CS-1", 4; "CS-1", 8] {|package fixture
type Store interface { Read(key string) error }
type (
  reader interface { Read(key string) error }
  IStore interface { Read(key string) error }
  iReader interface { Read(key string) error }
)
type Alias[T any] = interface { Read(key T) error }
|};
  one "every parameter position, not results or receivers"
    ["CS-2", 2; "CS-2", 3; "CS-2", 4; "CS-2", 5; "CS-2", 6; "CS-2", 7]
    {|package fixture
func F(string, int) error { return nil }
func (Thing) Method(string) error { return nil }
type Handler func(string) error
type IStore interface { Read(string) error }
var callback = func(string) error { return nil }
func Nested(callback func(string) error) error { return nil }
func Named(left, right string, rest ...int) (string, error) { return "", nil }
func (Thing) Good(value string) error { return nil }
|};
  one "comments and literal text are not declarations" [] {|package fixture
// type Bad interface { Read(string) error }
var source = "func F(string) IStore"
var raw = `type Bad interface { Read(string) error }`
func Good(name string) error { return nil }
|};
  one "interface-return wrappers and explicit exemptions"
    ["CS-8", 6; "CS-8", 7; "CS-8", 8; "CS-8", 9; "CS-8", 10]
    {|package fixture
type IStore interface { Read(key string) error }
type IClosed interface { seal() }
type iPrivate interface { seal() }
type ID [16]byte
func Direct() IStore { return nil }
func Slice() []IStore { return nil }
func Array() [3]IStore { return nil }
func Map() map[string][]IStore { return nil }
func Private() iPrivate { return nil }
func Sealed() IClosed { return nil }
func Pointer() *IStore { return nil }
func Channel() chan IStore { return nil }
func Generic[T any]() IStore[T] { return nil }
func ThirdParty() http.Handler { return nil }
func Identifier() ID { return ID{} }
func MapKey() map[IStore]string { return nil }
func (IStore) Method() IStore { return nil }
func Contract() func(value int) IStore { return nil }
|};
  let pair = Source.parse "sample.go" {|package fixture
type IStore interface { Read(key string) error }
func Pair() (IStore, IStore) { return nil, nil }
|} in
  let pair_findings = Mandatory.collect [pair] in
  if List.length (List.sort_uniq compare pair_findings) <> 2 then
    failwith "each interface result must remain distinct in the deduplicated report";
  one "contract-fixed hooks ignore parameter names and grouped naming" [] {|package fixture
type IStore interface { Read(key string) error }
type Factory func(first, second int) (IStore, error)
func Build(left /* name */ int, right int) (store /* contract */ IStore, err error) { return nil, nil }
|};
  check "interface and hook facts cross files"
    ["CS-8", "app/main.go", 2]
    ["domain/store.go", "package domain\ntype IStore interface { Read(key string) error }\n";
     "app/main.go", "package main\nfunc Build() domain.IStore { return nil }\n"];
  check "generated declarations preserve handwritten interface-return findings"
    ["CS-8", "app/main.go", 2]
    ["domain/store.go", "// Code generated by fixture. DO NOT EDIT.\npackage domain\ntype IStore interface { Read(string) error }\n";
     "app/main.go", "package main\nfunc Build() domain.IStore { return nil }\n"];
  check "ordinary rules retain generated and excluded corpus boundaries" []
    ["research/sample.go", "package fixture\ntype Bad interface { Read(string) error }\n";
     "examples/gotth/sample.go", "package fixture\ntype Bad interface { Read(string) error }\n";
     "go/generated.go", "// Code generated by fixture. DO NOT EDIT.\npackage fixture\ntype Bad interface { Read(string) error }\n"];
  one "else after a return on every path is reported, conditional return is not"
    ["ELSE-AFTER-RETURN", 5; "ELSE-AFTER-RETURN", 21; "ELSE-AFTER-RETURN", 24]
    {|package fixture
func Simple(err error) error {
  if err != nil {
    return err
  } else {
    use(err)
  }
  return nil
}
func Conditional(flag bool) {
  if flag {
    if flag { return }
  } else {
    use(flag)
  }
}
func AllPaths(first bool) {
  if first {
    if first {
      return
    } else {
      return
    }
  } else {
    use(first)
  }
}
|};
  one "nested else-if guards retain their condition and each return boundary is located"
    ["ELSE-AFTER-RETURN", 5; "ELSE-AFTER-RETURN", 7]
    {|package fixture
func RequestGuard(request *Request) {
  if request.Code() == "bad" {
    return
  } else if request.Allowed() {
    return
  } else {
    handle(request)
  }
}
|};
  one "if-initializer return guards reject an else-if branch"
    ["ELSE-AFTER-RETURN", 5]
    {|package fixture
func Validate(ctx Context, adapter *Adapter) error {
  if _, err := adapter.store.GetSessionCreation(ctx, "key"); err == nil {
    return nil
  } else if !errors.Is(err, sql.ErrNoRows) {
    return storeFailure(err)
  }
  return nil
}
|};
  one "flattened guard clauses preserve the error variable scope"
    []
    {|package fixture
func Validate(ctx Context, adapter *Adapter) error {
  _, err := adapter.store.GetSessionCreation(ctx, "key")
  if err == nil {
    return nil
  }
  if !errors.Is(err, sql.ErrNoRows) {
    return storeFailure(err)
  }
  return nil
}
|};
  check "dot imports require exact paths and test filenames"
    ["CS-11", "go/sample_test.go", 3; "CS-11", "go/sample_test.go", 4]
    ["go/sample_test.go", {|package fixture_test
import (
  "github.com/onsi/ginkgo/v2"
  assertions "github.com/onsi/gomega"
  "github.com/onsi/gomega/gstruct"
)
|}; "go/library.go", {|package fixture
import "github.com/onsi/gomega"
|}];
  check "dot imports and subpackage helpers stay clear" []
    ["go/sample_test.go", {|package fixture_test
import (
 . "github.com/onsi/ginkgo/v2"
 . "github.com/onsi/gomega"
 "github.com/onsi/ginkgo/v2/types"
)
|}];
  check "collision evidence crosses files but not external test packages"
    ["CS-11", "go/external_test.go", 2]
    ["go/types.go", "package fixture\ntype Entry struct {}\n";
     "go/evidence_test.go", "package fixture\nimport bdd \"github.com/onsi/ginkgo/v2\"\nvar _ = bdd.Entry\n";
     "go/another_test.go", "package fixture\nimport \"github.com/onsi/ginkgo/v2\"\n";
     "go/external_test.go", "package fixture_test\nimport \"github.com/onsi/ginkgo/v2\"\n"];
  check "blank import never gains collision exemption"
    ["CS-11", "go/blank_test.go", 2]
    ["go/types.go", "package fixture\nvar Entry = 1\n";
     "go/evidence_test.go", "package fixture\nimport bdd \"github.com/onsi/ginkgo/v2\"\nvar _ = bdd.Entry\n";
     "go/blank_test.go", "package fixture\nimport _ \"github.com/onsi/ginkgo/v2\"\n"];
  check "CS-11 retains research tests but exempts generated tests and vendor"
    ["CS-11", "research/sample_test.go", 2]
    ["research/sample_test.go", "package fixture\nimport \"github.com/onsi/gomega\"\n";
     "go/generated_test.go", "// Code generated by fixture. DO NOT EDIT.\npackage fixture\nimport \"github.com/onsi/gomega\"\n";
     "vendor/sample_test.go", "package fixture\nimport \"github.com/onsi/gomega\"\n"];
  check "generated declarations preserve handwritten collision exemptions" []
    ["go/types.go", "// Code generated by fixture. DO NOT EDIT.\npackage fixture\ntype Entry struct {}\n";
     "go/evidence_test.go", "// Code generated by fixture. DO NOT EDIT.\npackage fixture\nimport bdd \"github.com/onsi/ginkgo/v2\"\nvar _ = bdd.Entry\n";
     "go/another_test.go", "package fixture\nimport \"github.com/onsi/ginkgo/v2\"\n"];
  check "bootstrap scope and counts"
    ["TEST-BOOTSTRAP", "pkg/example/behavior_test.go", 2;
     "TEST-BOOTSTRAP", "tools/example/behavior_test.go", 2]
    ["pkg/example/behavior_test.go", "package fixture\nfunc TestBehavior(t *testing.T) {}\n";
     "tools/example/behavior_test.go", "package fixture\nfunc TestBehavior(t *testing.T) {}\n";
     "pkg/example/suite_test.go", "package fixture\nfunc TestSuite(t *testing.T) { RunSpecs(t, \"Suite\") }\n";
     "pkg/example/generated_test.go", "// Code generated by fixture. DO NOT EDIT.\npackage fixture\nfunc TestBehavior(t *testing.T) {}\n";
     "tools/example/generated_test.go", "// Code generated by fixture. DO NOT EDIT.\npackage fixture\nfunc TestBehavior(t *testing.T) {}\n";
     "pkg/gotth/behavior_test.go", "package fixture\nfunc TestBehavior(t *testing.T) {}\n";
     "go/behavior_test.go", "package fixture\nfunc TestBehavior(t *testing.T) {}\n"];
  check "bootstrap ignores comments and strings"
    ["TEST-BOOTSTRAP", "pkg/example/suite_test.go", 2]
    ["pkg/example/suite_test.go", {|package fixture
func TestSuite(t *testing.T) {
 // RunSpecs(t, "Suite")
 _ = "RunSpecs(t, Suite)"
}
|}];
  print_endline "mandatory house rules: native regressions passed"
