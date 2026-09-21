# Rust conformance interpreter

This optional executable independently evaluates numeric-profile-v1 postfix
programs. The Go runtime remains the default in-process composition. This is a
test backend, not a real-time service or an implementation of activation,
observation freshness, fallback, physical dynamics, or a controller compiler.

From the repository root:

```sh
cargo test --locked --manifest-path csf/examples/rust/Cargo.toml
cargo clippy --locked --manifest-path csf/examples/rust/Cargo.toml --all-targets -- -D warnings
cargo build --locked --manifest-path csf/examples/rust/Cargo.toml
csf/examples/rust/target/debug/brain-spine-conformance < requests.jsonl
```

Rust 1.91 or newer is required. `Cargo.lock` pins dependencies. There is no
`protoc`, system-package installation, container, or running service requirement.

## Generated contract

The sole message owner is
`proto/candace/brainspine/v1/brainspine.proto`. The build script uses pinned
`protox` and `prost-build` to generate Rust message types and a descriptor set in
Cargo's output directory. `prost-reflect` consumes that descriptor for ProtoJSON.
There are no handwritten JSON DTOs or checked-in generated copies to drift.
Building after a schema change regenerates both projections; `cargo test` checks
their use at the actual JSON boundary. Liquid Proto annotations remain part of
the source contract; the interpreter independently enforces the numeric and
stack semantics below.

## JSON Lines interface

Each input line is the canonical ProtoJSON `RuntimeRequest` with
`kind: "REQUEST_KIND_EVALUATE"`, a `program`, and an `observation`. For example,
send this as one line:

```json
{"kind":"REQUEST_KIND_EVALUATE","program":{"schemaVersion":1,"steering":[{"opcode":"OPCODE_INPUT"},{"opcode":"OPCODE_SCALE","value":"-500"}],"acceleration":[{"opcode":"OPCODE_CONSTANT","value":"250"}]},"observation":{"features":["333","0","0","0"]}}
```

The reply is ProtoJSON `RuntimeResponse`:

```json
{"action":{"steering":"-166","acceleration":"250"}}
```

ProtoJSON emits 64-bit integers as strings and omits default-valued fields. Each
success has `action` and an empty/default `error`; each rejection has a nonempty
`error` and no `action`. Error text is diagnostic, not a stable comparison key.
Conformance comparisons should compare acceptance and decoded actions. Epoch and
sequence remain zero, matching Go's pure `EVALUATE`; the stateful Go `STEP` path
owns those fields. Other request kinds are rejected. No output goes to stdout
except responses, and each response is flushed immediately.

The CLI accepts at most 65,536 bytes per input line, including its newline.
Malformed JSON, unknown fields, unknown enum values, duplicate message fields,
and invalid programs return errors. Duplicate detection traverses nested
messages and recognizes both ProtoJSON spellings of the same field through its
descriptor number. Ordinary request errors do not stop subsequent requests. An oversized
line returns one error and closes the input stream to bound allocation and avoid
draining an unbounded input. EOF terminates normally.

## Arithmetic and admission

- Each output has 1–128 instructions; the reconstructed expression has at most
  16 node levels (root depth 0 through leaf depth 15).
- The observation has exactly four integer features, each within ±10,000.
- Constants and clamp bounds are within ±1,000,000,000; lower ≤ upper.
- `CONSTANT` and `INPUT` push; `ADD` consumes two values; `SCALE` and `CLAMP`
  consume one. Every output must leave exactly one value.
- `ADD` saturates at ±1,000,000,000. `SCALE` multiplies by a coefficient within
  ±1,000,000, divides by 1,000 with truncation toward zero, then saturates. The
  maximum product is 10^15, safely within `i64` in debug and release builds.
- `CLAMP` clips to its validated bounds. Final steering and acceleration clip
  independently to ±1,000. Nonzero operands unused by an opcode are rejected.

The tests cover intermediate saturation before cancellation, negative division,
both output channels, numeric extrema, invalid operands, stack underflow and
leftovers, exact depth/instruction boundaries, ProtoJSON errors, and executable
stream behavior. Agreement with Go is differential evidence; it does not prove
either toolchain or implementation correct.
