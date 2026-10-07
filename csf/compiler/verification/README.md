# csfc verification in Lean

`CSFCVerifier.lean` is the decision-tree proof emitted by `csfc` from the
question-tree declaration in
[`question_tree.csf`](question_tree.csf). It is never hand-written: the emitter
`decision_codegen` renders the declaration into Lean inductive types, and the
golden test `//csf/compiler/architecture:decision_test` holds the committed file
to that output byte for byte. Any change to the emitter or the declaration that
changes the projection fails there before a human edits a theorem by hand.

The same declaration projects four further files beside the proof, each held by
the same golden test and drift-gated by `tools/check-generated.sh`:

- `question_kind_enum.txt` — each question as an enum of its options, so
  `enum tier { in_process; kernel; ipc; net; }` is the tier question;
- `question_directory.txt` — the directory tree the declaration names, the root
  question as the top-level directory and each option a child;
- `question_cli_layer.txt` — every question a CLI layer and every option a verb,
  so the CLI reads `csf <area> <verb>` layer by layer;
- `question_jev_prompt.txt` — each question's text as the criterion and its
  option definitions as the alternatives, `criterion :: a | b | c`.

Together the CSF language, the decision tree, the directory tree and the CLI
are one declaration.

The file states and proves the three invariants the compiler preserves over the
decision tree it turns into a shell:

- `at_most_16` — no question offers more than 16 options, proved by `decide`;
- `one_way` — each leaf option names exactly one template, a functional
  relation (rests on `propext`);
- `composes` — every allowed path through the tree compiles to a shell, typing
  preservation over allowed pairs.

`sorry` is zero and the only axioms are `propext`, `Classical.choice`, and
`Quot.sound`.

Build with the version in `lean-toolchain`:

```sh
cd csf/compiler/verification
./check.sh
```

The check downloads the pinned Lean 4.34.0 archive (SHA256-verified, cached in
`.cache/`), runs `lake build`, then compiles the emitted file with
`--trust=0 -DwarningAsError=true` and audits that the three theorems carry no
`sorry` and no custom axiom.

The separate [bounded controller proof](../../examples/proof/README.md) checks
its own arithmetic controller model. It does not verify `csfc` or prove [runtime](../../docs/generated/ontology_cgen.md#term-runtime)
timing, resource cleanup, or physical safety.
