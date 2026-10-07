# Golden fixtures: one service kind, two instances

This directory pins, by hand, the tree a `service` declaration should emit,
before the generator exists. Two instances share the one `service.rules.dl`:
`email` reverse-ports `services/email` (which already exists), and `blah` is a
fresh [service](../../../../docs/generated/ontology_cgen.md#term-service) authored from the declaration alone. Together they
are the `describe_system` program's golden-first output for the `service` kind
(issue #488).

The source is three files, written before the generator: `service.rules.dl` (the
program), `email/email.csf` and `blah/blah.csf` (the declarations). The tables
below are the emitted artifacts.

## Model

There are no holes and no hand-written logic. An operation declares its body as
`logic` rules, an outcome declares its `when` rule or is `otherwise`, and the
generator emits every file. `blah` is the fresh case, so its golden is
`generated(every_file)` with `handwritten_lines(0)`. `email` reverse-ports a
[service](../../../../docs/generated/ontology_cgen.md#term-service) that already ships, so each retained region is either `declared` by a
rule or asked as a typed `question` whose preferred `option` names the kind it
needs — never left in a seam.

## Artifacts

One row per emitted file, one file per declared thing. `emits` is the predicate
in `service.rules.dl`; a file that several rules emit is one row naming all of
them. `—` marks a file whose own line count is this document.

### email

| Artifact | Rule | File | Lines |
|---|---|---|---|
| package doc | `emits(S, package_doc)` | `email/services/email/doc.go` | 8 |
| readme | `emits(S, readme)` | `email/README.md` | — |
| email [service](../../../../docs/generated/ontology_cgen.md#term-service) | `emits(S, constructor)` | `email/services/email/email.go` | 181 |
| send operation | `emits(S, op(O))`, `emits(S, metric(O,total))`, `emits(S, metric(O,duration_seconds))` | `email/services/email/send.go` | 257 |
| transport capability | `emits(S, contract(C))`, `emits(S, options(C))` | `email/services/email/transport.go` | 34 |
| provenance capability | `emits(S, contract(C))`, `emits(S, options(C))` | `email/services/email/provenance.go` | 34 |
| receipts capability | `emits(S, contract(C))`, `emits(S, options(C))` | `email/services/email/receipts.go` | 33 |
| send tests | `emits(S, test(O,outcome(X)))` | `email/services/email/send_test.go` | 136 |
| transport tests | `emits(S, mock(C))`, `emits(S, test(misuse(C)))` | `email/services/email/transport_test.go` | 67 |
| provenance tests | `emits(S, mock(C))`, `emits(S, test(misuse(C)))` | `email/services/email/provenance_test.go` | 67 |
| receipts tests | `emits(S, mock(C))`, `emits(S, test(misuse(C)))` | `email/services/email/receipts_test.go` | 66 |
| panels | `emits(S, panel(O,rate_by_outcome))`, `emits(S, panel(O,latency_p50_p95))` | `email/observability/email-dashboard.json` | 39 |
| suite | `emits(S, suite)` | `email/services/email/email_suite_test.go` | 15 |
| build file | `emits(S, build_file)` | `email/services/email/BUILD.bazel` | 20 |

### blah

| Artifact | Rule | File | Lines |
|---|---|---|---|
| package doc | `emits(S, package_doc)` | `blah/services/blah/doc.go` | 7 |
| readme | `emits(S, readme)` | `blah/README.md` | — |
| blah [service](../../../../docs/generated/ontology_cgen.md#term-service) | `emits(S, constructor)` | `blah/services/blah/blah.go` | 55 |
| greet operation | `emits(S, op(O))`, `emits(S, metric(O,total))`, `emits(S, metric(O,duration_seconds))` | `blah/services/blah/greet.go` | 95 |
| greet tests | `emits(S, test(O,outcome(X)))` | `blah/services/blah/greet_test.go` | 39 |
| panels | `emits(S, panel(O,rate_by_outcome))`, `emits(S, panel(O,latency_p50_p95))` | `blah/observability/blah-dashboard.json` | 39 |
| suite | `emits(S, suite)` | `blah/services/blah/blah_suite_test.go` | 15 |
| build file | `emits(S, build_file)` | `blah/services/blah/BUILD.bazel` | 14 |

## What is generated vs. declared

The tables above are what the generator emits, once the declaration is parsed.
Nothing is hand-written: `blah`'s whole tree is generated from `blah.csf`, and
`email`'s retained reverse-ported behaviour is accounted for in `email.csf` as
either a declared rule or a typed `question`. `grep -n '^question '
email/email.csf` lists the four asks; neither tree carries a hand-written seam.

## Method and scope

The brief's `golden_first` runs in five steps: declare, handwrite, review,
generate, replace. This slice delivers steps 1 and 2 and the held review that
opens step 3; steps 4 and 5 wait for the operator's approval of these goldens.
Nothing here runs the `.dl` yet — `csfc` does not parse the `.csf` declarations
(see the pull request body).
