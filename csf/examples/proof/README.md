# Checked arithmetic compiler

This small Lean 4 project formalizes the arithmetic slice of
[`brainspine.proto`](../../../proto/candace/brainspine/v1/brainspine.proto).
It proves a concrete source-expression to postfix-instruction compiler correct.
It does not prove physical safety or verify the native Go/Rust implementations.

Run from the repository root:

```sh
bash csf/examples/proof/check.sh
```

The script requires Linux x86_64, `curl`, `sha256sum`, `tar`, `zstd`, and
`python3`. It downloads an official Lean release into this directory's ignored
`.cache/`, verifies its SHA256 on every invocation, checks the executable's
version and source commit, and runs Lean with `--trust=0`, two threads, a
2048 MiB memory limit, and warnings treated as errors. There is no host install,
Elan setup, Mathlib dependency, or container/service mutation. The first run
downloads about 554 MiB; subsequent runs reuse the local archive and toolchain.

## Exact semantics

- The typed source AST has constants, four observation slots, addition, integer
  scaling, and clamping. A `Fin 4` index excludes invalid slots by construction.
- Values are mathematical `Int`s. Constants, inputs, and every arithmetic
  operation saturate to `[-1000000000, 1000000000]`.
- `scale coefficient x` multiplies first, divides by 1000 **toward zero**, then
  saturates. A sign split implements truncation explicitly; it does not use
  Lean's signed Euclidean division directly on a negative product.
- Clamp is `max lower (min upper value)`, followed by saturation. Native
  correspondence assumes the schema/admission layer has checked `lower ≤ upper`.
- Postfix addition compiles the left operand, then the right operand, then
  `ADD`. The instruction consumes right/left from the stack in that order.
- Stack underflow returns `none`. Final execution requires exactly one stack
  value and applies the spine-owned actuator clamp `[-1000, 1000]`.

The formal compiler handles every value of its typed mathematical AST. The
canonical wire schema has additional validation obligations, including opcode,
arity, scalar bounds, and observation length. Parsing and discharging those
obligations are outside this theorem.

## Checked statements

[`BrainSpine.lean`](BrainSpine.lean) contains these machine-checked theorems:

| Theorem | Guarantee |
|---|---|
| `compile_correct` | For every expression, inputs, and existing stack, executing its compiled instructions yields `some (evaluate expression :: stack)`. |
| `evaluate_bounds` | Every source expression evaluates within the numeric saturation bound. |
| `compiled_actuator_correct` | Executing compiled instructions from an empty stack and applying the actuator wrapper agrees exactly with the wrapped source evaluator. |
| `compiled_actuator_bounds` | Every compiled expression produces an actuator value in `[-1000, 1000]`. |

The file also checks concrete negative-division, saturation, stack-underflow,
and surplus-stack examples with kernel-checked `decide` proofs. It does not use
`native_decide`, `sorry`, custom axioms, or a generic unused safety theorem.

The script audits the dependencies of all four named theorems. It admits only
Lean's standard `propext`, `Classical.choice`, and `Quot.sound` axioms; it rejects
`sorryAx`, compiler-trust axioms, custom axioms, and missing audit output. This
is a standard-axiom proof, not an axiom-free claim.

## Toolchain provenance and observed run

The checked pin is `leanprover/lean4:v4.34.0`, released on
2026-09-14 at 14:04:36 UTC. On 2026-09-16 the
[official release API](https://api.github.com/repos/leanprover/lean4/releases/tags/v4.34.0)
reported these values for the
[Linux x86_64 release archive](https://github.com/leanprover/lean4/releases/download/v4.34.0/lean-4.34.0-linux.tar.zst):

```text
size: 580367391 bytes
SHA256: caaa98356098c85dc0fcbbd28e1ec66f39eb6551829972b752ff20e1286b646b
Lean source commit: 293d5d0c0c3f3dded4688b3ccd6a33939ac5102b
```

The checker writes its own output for the source it is given. Historical private
run receipts are not part of this public example. Run it for the revision you use.

## Trust and delivery boundary

The proof concerns this Lean model and compiler. Agreement between that model
and the canonical wire semantics is a reviewed translation boundary. Agreement
with the handwritten Go and Rust evaluators requires the prototype's separate
conformance checks; this project does not establish their equivalence by proof.
Those native languages, compilers, runtimes, integer representations, JSON or
protobuf parsers, simulator conversion, timing, and physical behavior remain
outside the theorem. In particular, finite-width multiplication must be shown
not to overflow under the admission constraints.

The Lean kernel, official release build, imported standard library, standard
axioms, operating system, and execution hardware remain trusted. The official
archive hash pins the downloaded bytes; it is not an independent reproducible
build attestation. Actuator clamping proves a numeric range only. It says nothing
about collision avoidance, stability, usefulness, or safe controller switching.
