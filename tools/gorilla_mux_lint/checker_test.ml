let expect message condition = if not condition then failwith message

let forbidden path source =
  let findings, errors = Checker.inspect path source in
  expect (path ^ ": unexpected parse error") (errors = []);
  expect (path ^ ": missing finding") (findings <> [])

let clean path source =
  let findings, errors = Checker.inspect path source in
  expect (path ^ ": false finding or parse error") (findings = [] && errors = [])

let parse_error path source =
  let _, errors = Checker.inspect path source in
  expect (path ^ ": invalid or unsupported syntax silently passed") (errors <> [])

let test_imports () =
  List.iter (forbidden "generated_test.go") [
    {|package p; import "github.com/gorilla/mux"|};
    {|package p; import renamed "github.com/gorilla/mux"|};
    {|package p; import (. "github.com/gorilla/mux")|};
    {|package p; import _ `github.com/gorilla/mux`|};
    {|package p; import "github.com/gorilla/\x6dux"|};
    {|package p; import "github.com/gorilla/\155ux"|};
    {|package p; import "github.com/gorilla/\u006dux"|};
    {|package p; import "github.com/gorilla/\U0000006dux"|};
    "package p; import `github.com/gorilla/\rmux`";
    {|package p; import "github.com/gorilla/mux/subpackage"|};
    {|package p; import "github.com/getkin/kin-openapi/routers/gorillamux"|};
    {|package p; import "github.com/oapi-codegen/gin-middleware"|};
  ];
  List.iter (clean "source.go") [
    {|package p; import "github.com/gorilla/websocket"|};
    {|package p; import "github.com/gorilla/muxhelper"|};
    {|package p; // import "github.com/gorilla/mux"|};
    {|package p; var example = `import "github.com/gorilla/mux"`|};
    {|/* import "github.com/gorilla/mux" */ package p|};
    {|package p; import /* "github.com/gorilla/mux" */ "fmt"|};
    "package p\nimport \"fmt\"\nfunc f() { _ = new(1) }\n";
    "package p\ntype Value int\nfunc f() { _ = new(1) }";
    "package p\ntype Value struct { Name string }";
  ];
  let findings, errors = Checker.inspect "nested/routes.go"
    "package p\nimport (\n  alias \"github.com/gorilla/mux\"\n)\n" in
  expect "source location is the imported path" (errors = [] &&
    List.map (fun (finding : Checker.issue) -> finding.path, finding.line) findings = [("nested/routes.go", 3)]);
  parse_error "broken.go" "package p; import (\"github.com/gorilla/mux\"";
  parse_error "broken.go" {|package p; import "github.com/gorilla/\q"|};
  parse_error "broken.go" "package p\nimport (\"fmt\"\nfunc f() { _ = new(1) }\n";
  parse_error "broken.go" "package p\nimport (\"fmt\"\nvar value = 1\n";
  forbidden "future.go" "package p\nimport \"github.com/gorilla/mux\"\nfunc f() { _ = new(1) }\n";
  forbidden "misplaced.go" "package p\nfunc f() {}\nimport \"github.com/gorilla/mux\"\n"

let test_manifests () =
  List.iter (fun (path, source) -> forbidden path (source ^ "\n")) [
    "go.mod", "require github.com/gorilla/mux v1.8.1 // indirect";
    "go.mod", {|require "github.com/gorilla/mux" v1.8.1|};
    "go.mod", {|require "github.com/gorilla/\x6dux" v1.8.1|};
    "go.mod", "tool github.com/gorilla/mux";
    "go.mod", {|tool "github.com/gorilla/\x6dux"|};
    "go.mod", "require (\n github.com/gorilla/mux v1.8.1\n)";
    "go.mod", "tool (\n github.com/gorilla/mux // required tool\n)";
    "go.mod", {|replace example.invalid/router => "github.com/gorilla/\u006dux" v1.8.1|};
    "go.mod", "replace example.invalid/router => \"./local//router\"\nrequire github.com/gorilla/mux v1.8.1";
    "go.mod", {|tool "github.com/gorilla/mux"// comment after a quoted path|};
    "go.work", {|replace example.invalid/router => "github.com/gorilla/\155ux" v1.8.1|};
    "go.work", "replace example.invalid/router => github.com/gorilla/mux v1.8.1";
    "go.sum", "github.com/gorilla/mux v1.8.1/go.mod h1:fixture=";
    "go.work.sum", "github.com/gorilla/mux v1.8.1 h1:fixture=";
  ];
  clean "go.mod" "module example.invalid/fixture\ngo 1.26\n// require github.com/gorilla/mux v1.8.1\nrequire github.com/gorilla/muxhelper v1.0.0\n";
  clean "go.mod" "module example.invalid/fixture\ngo 1.26\ntoolchain go1.26.5\n";
  clean "go.mod" "module example.invalid/fixture\ngo 1.26\ngodebug default=go1.26\n";
  clean "go.mod" "module example.invalid/fixture\nreplace example.invalid/other => \"./has space\"\n";
  clean "go.mod" "module example.invalid/fixture\nreplace example.invalid/other => \"./literal\\\"//github.com/gorilla/mux\" // \"broken quote github.com/gorilla/mux\n";
  forbidden "go.mod" {|require "github.com/gorilla/\x6dux" v1.8.1|};
  clean "go.work" "go 1.26\nuse (\n ./nested\n)\n";
  clean "go.work" "go 1.26\ntoolchain go1.26.5\ngodebug default=go1.26\nuse \"./has space\"\n";
  clean "go.sum" "";
  clean "go.work.sum" "github.com/gorilla/websocket\tv1.5.3\th1:fixture=\r\n";
  parse_error "go.sum" "github.com/gorilla/mux v1.8.1\n";
  List.iter (fun source -> parse_error "go.mod" source) [
    "require \"github.com/gorilla/mux";
    "require \"github.com/gorilla/mux\nnext line\"";
    "require \"github.com/gorilla/\\";
    {|require "github.com/gorilla/\q" v1.8.1|};
  ];
  let findings, errors = Checker.inspect "go.work"
    "go 1.26\n// github.com/gorilla/mux\nreplace (\n example.invalid/router => \"github.com/gorilla/\\x6dux\" v1.8.1\n)\n" in
  expect "manifest token location survives comment and block lines"
    (errors = [] && List.map (fun (issue : Checker.issue) -> issue.line) findings = [4])

let test_literals () =
  expect "all string escapes follow Go semantics"
    (Checker.string_value {|"\a\b\f\n\r\t\v\\\"\377\xFF\u00E9\U0001F600"|}
     = "\007\b\012\n\r\t\011\\\"\255\255\195\169\240\159\152\128");
  expect "raw strings discard carriage returns"
    (Checker.string_value "`raw\rstring`" = "rawstring");
  List.iter (fun literal ->
    let rejected = try ignore (Checker.string_value literal); false with Invalid_argument _ -> true in
    expect "invalid string escape accepted" rejected)
    [{|"\q"|}; {|"\400"|}; {|"\x1"|}; {|"\uD800"|}; {|"\U00110000"|}]

let write path contents = Out_channel.with_open_bin path (fun channel -> output_string channel contents)

let test_inventory () =
  let root = Filename.temp_file "gorilla-mux-lint-" "" in
  Sys.remove root;
  Unix.mkdir root 0o700;
  let remove_tree root =
    let rec remove path =
      if (Unix.lstat path).Unix.st_kind = Unix.S_DIR then begin
        Sys.readdir path |> Array.iter (fun name -> remove (Filename.concat path name));
        Unix.rmdir path
      end else Sys.remove path in
    remove root in
  Fun.protect ~finally:(fun () -> remove_tree root) (fun () ->
    write (Filename.concat root "go.mod") "module example.invalid/fixture\ngo 1.26\n";
    Unix.mkdir (Filename.concat root "research") 0o700;
    write (Filename.concat root "research/generated_test.go")
      "// Code generated by fixture. DO NOT EDIT.\npackage p\nimport `github.com/gorilla/\rmux`\n";
    let paths = Checker.inventory "go.mod\000research/generated_test.go\000README.md\000go.mod\000" in
    let report = Checker.check root paths in
    expect "nested generated source is scanned, CR is preserved and files are deduplicated"
      (report.files = 2 && Checker.status report = 1 && List.length report.findings = 1);
    let report = Checker.check root ["go.mod"] in
    expect "clean manifest passes" (Checker.status report = 0 && report.files = 1);
    expect "empty scan cannot pass" (Checker.status (Checker.check root []) = 2);
    expect "no selected files cannot pass" (Checker.status (Checker.check root ["README.md"]) = 2);
    expect "missing source cannot pass" (Checker.status (Checker.check root ["missing.go"]) = 2);
    Unix.mkdir (Filename.concat root "directory.go") 0o700;
    expect "directory is not a source file" (Checker.status (Checker.check root ["directory.go"]) = 2);
    expect "inventory cannot escape checkout" (Checker.status (Checker.check root ["../outside.go"]) = 2);
    Unix.symlink "go.mod" (Filename.concat root "alias.go");
    expect "source symlink rejected" (Checker.status (Checker.check root ["alias.go"]) = 2);
    Unix.symlink "research" (Filename.concat root "alias");
    expect "parent symlink rejected" (Checker.status (Checker.check root ["alias/generated_test.go"]) = 2);
    let rejected = try ignore (Checker.inventory "go.mod"); false with Invalid_argument _ -> true in
    expect "unterminated inventory rejected" rejected)

let () =
  test_imports ();
  test_manifests ();
  test_literals ();
  test_inventory ();
  print_endline "Gorilla mux OCaml checker tests passed"
