(* Regressions for the rest_client template engine. The byte-for-byte proof
   against the golden lives in csf/tools/rest_codegen/generate.sh check; these
   pin the reader and the substitution the proof depends on. *)
open Template

let expect message condition = if not condition then failwith message

let rejects action =
  match action () with
  | _ -> failwith "expected a rejection"
  | exception Failure _ -> ()

(* The kind and one instance, shaped as csf/compiler/testdata/golden/rest_client/rest_client.csf. *)
let declaration = {csf|
// a kind and the instance migrated onto it
kind rest_client {
  base_url  url;
  auth      secret_ref | none;                      // a reference, never the value
  spec      openapi | none;
  rate      from_headers | fixed(per_second: int) | none;
  retry     backoff(max: int);
  template  "io/net/rest";
}

rest_client hfjobs {
  base_url "https://huggingface.co/api";
  auth     secret("hf_token");
  spec     none;
  rate     from_headers;
  retry    backoff(5);
}
|csf}

let test_blocks () =
  let parsed = blocks declaration in
  expect "two blocks" (List.length parsed = 2);
  let kind = List.find (fun block -> block.keyword = "kind") parsed in
  let instance = List.find (fun block -> block.keyword = "rest_client") parsed in
  expect "kind name" (kind.name = "rest_client");
  expect "instance name" (instance.name = "hfjobs");
  expect "kind template" (template_path parsed = "io/net/rest");
  expect "kind fields read" (List.assoc_opt "auth" kind.fields = Some "secret_ref | none");
  rejects (fun () -> blocks "kind rest_client {\ntemplate \"io/net/rest\";\n")

let test_declared () =
  let parsed = blocks declaration in
  let instance = List.find (fun block -> block.keyword = "rest_client") parsed in
  let declared = declared_of instance in
  expect "package" (declared.package = "hfjobs");
  expect "base url" (declared.base_url = "https://huggingface.co/api");
  expect "hub drops the api suffix" (declared.hub = "https://huggingface.co");
  expect "credential" (declared.credential = "rest.Bearer{}");
  expect "rate" (declared.rate = "rest.FromHeaders()");
  expect "retry max" (declared.retry_max = "5")

let test_values () =
  expect "secret" (credential_of "secret(\"hf_token\")" = "rest.Bearer{}");
  expect "no credential" (credential_of "none" = "rest.NoCredential{}");
  expect "from headers" (rate_of "from_headers" = "rest.FromHeaders()");
  expect "fixed" (rate_of "fixed(3)" = "rest.Fixed(3)");
  expect "no rate" (rate_of "none" = "rest.NoRate()");
  expect "backoff" (retry_max_of "backoff(7)" = "7");
  expect "unquote" (unquote "\"io/net/rest\"" = "io/net/rest");
  rejects (fun () -> credential_of "token");
  rejects (fun () -> rate_of "sometimes");
  rejects (fun () -> retry_max_of "once");
  rejects (fun () -> unquote "io/net/rest")

let test_replace () =
  expect "every occurrence" (replace_all ~pairs:[ "x", "1" ] "x={{x}} y={{x}}" = "x=1 y=1");
  expect "adjacent placeholders" (replace_all ~pairs:[ "a", "A"; "b", "B" ] "{{a}}{{b}}" = "AB");
  expect "untouched text" (replace_all ~pairs:[ "x", "1" ] "no placeholders" = "no placeholders");
  rejects (fun () -> replace_all ~pairs:[] "{{x}}");
  rejects (fun () -> replace_all ~pairs:[ "x", "1" ] "{{ x }}")

let test_generate () =
  let outputs =
    generate ~kind_template:"kind body\n"
      ~instance_template:"package {{package}}\nendpoint {{base_url}}\n" declaration in
  expect "kind first, then the instance"
    (List.map fst outputs = [ "io/net/rest/rest.go"; "io/net/hfjobs/hfjobs.go" ]);
  expect "the kind template is emitted verbatim"
    (List.assoc "io/net/rest/rest.go" outputs = "kind body\n");
  expect "the instance template is filled"
    (List.assoc "io/net/hfjobs/hfjobs.go" outputs = "package hfjobs\nendpoint https://huggingface.co/api\n");
  rejects (fun () ->
      generate ~kind_template:"kind" ~instance_template:"{{missing}}" declaration)

let () =
  test_blocks ();
  test_declared ();
  test_values ();
  test_replace ();
  test_generate ();
  print_endline "OCaml rest_client template regressions passed"
