# csfc verification in Lean

`CSFCVerifier.lean` is the compiler-verifier stub. `CSFC.Verification.verify`
returns `Result.notImplemented` for every input. It issues no certificate and
contains no compiler-correctness theorem, `sorry`, or custom axiom.

Build with the version in `lean-toolchain`:

```sh
cd csf/compiler/verification
lake build
```

Formal verification is intended to cover the grammar and compiler. The next
implementation must define the accepted grammar and source architecture semantics, the
emitted model semantics, the correspondence to the OCaml compiler output, and
the theorem checked by Lean. Only a completed proof and its audited assumptions
can justify adding a verified result.

The separate [bounded controller proof](../../examples/proof/README.md) checks
its own arithmetic controller model. It does not verify `csfc` or prove runtime
timing, resource cleanup, or physical safety.
