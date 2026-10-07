# rest_client template engine

The `csfc` template that emits the `rest_client` kind's Go, `golden_first`
step 4 of [#475](../../../issues/475) — a byte-for-byte proof that the generator's
output is the handwritten golden approved in step 2.

```sh
bash tools/bazel.sh test //csf/compiler/rest_codegen:tests
bash csf/tools/rest_codegen/generate.sh check
```

The declaration
(`csf/compiler/testdata/golden/rest_client/rest_client.csf`) is the one record.
The kind's template (`rest.go.tmpl`) is fully generic and emitted verbatim; the
instance template (`hfjobs.go.tmpl`) is the golden with the values a
declaration names spelled as `{{package}}`, `{{base_url}}`, `{{hub}}`,
`{{credential}}`, `{{rate}}` and `{{retry_max}}`, which `Template` fills from
the declaration. `generate.sh check` emits both into a scratch directory and
diffs them against `csf/compiler/testdata/golden/rest_client/`, so drift
between the declaration, a template and the committed golden is a failure.

This is a template engine, not a parser of the `csf` grammar: the
`kind`/instance declaration language is not yet in `csf/compiler/architecture`,
so this reader holds the fixed record the slice is authored from. Wiring the
grammar and the `csfc` `check` for step 1 is a separate slice, outside example
1's acceptance surface.
