# Golden: the blah service

`blah` is the second of two fixtures under `golden/service`, the minimal
[service](../../../../../docs/generated/ontology_cgen.md#term-service): one operation, no
[capability](../../../../../docs/generated/ontology_cgen.md#term-capability), no storage and no config.
Where `email.csf` reverse-ports code that already exists, this one is the other
direction — "I want a blah" is one operation — and the tree under
`services/blah/` is exactly what the template emits from that declaration, zero
hand-written lines.

## The declaration

`blah.csf` declares one [service](../../../../../docs/generated/ontology_cgen.md#term-service)
with the `greet` operation, two bounded outcomes (`empty_name`, `ok`), no
capability, and no stores, calls or config. The operation declares its body as
one `logic` rule — `output.text = format("Hello, {}", input.name)` — and its two
outcomes as `when` clauses (`empty_name when input.name = ""`, `ok otherwise`).
There is nothing to configure and nothing to keep, so the whole generated tree
is the one operation, its metrics and its tests.

## What is generated

Everything under `services/blah/` is generated from `blah.csf`, laid out by
declared thing: `blah.go` (the [service](../../../../../docs/generated/ontology_cgen.md#term-service) and its constructor), `greet.go` (the
operation, its records, its generated classifier and its metrics),
`greet_test.go` (the operation's outcome specs), then `blah_suite_test.go` and
`doc.go`. The golden is `generated(every_file)` with `handwritten_lines(0)`;
there is no seam and no hand-written line.

## Method and scope

The brief's `golden_first` runs in five steps. This slice delivers steps 1
(declare) and 2 (handwrite), then the held review that opens step 3. Steps 4
(generate) and 5 (replace) do not start until the operator approves this golden.
