// Package clientcodec generates the browser runtime's protobuf codec, its
// predicate manifest, and the cross-runtime golden vectors, from the same
// FileDescriptorSet that drives the Go refinement generator.
//
// # Why generated
//
// The bugs this code attracts are a wrong field number, a wrong wire type, or
// a field somebody forgot — and none of them is visible in review. Reading the
// descriptors makes the client and the server incapable of disagreeing about
// the wire, and makes "add a field" a regeneration rather than a transcription
// (docs/protocol.md §10.2).
//
// # What it emits
//
//	client/codec.gen.js             the codec: schema table, encoder, decoder
//	client/predicates.manifest.txt  every predicate, and who enforces it
//	client/test/golden.json         Go-encoded vectors for the JS round-trip
//
// All three are committed. Consumers of this module never run this package,
// and the browser never sees a code generator.
//
// # A schema table, not straight-line code
//
// The codec is a compact schema string plus one generic encoder and one
// generic decoder that interpret it. Straight-line per-field code would be
// bigger and would grow linearly with the schema; a table grows by one line
// per field and compresses, which is what a 12,288-byte gzip ceiling
// (PRD NFR-2) actually rewards. It is still generated code: every field
// number, wire kind and length bound in the table comes from the descriptors,
// which is the property docs/protocol.md §10.2 asks for.
//
// # Predicate enforcement is directional
//
// docs/protocol.md §10.3 fixes the line: the client enforces length bounds on
// decode, because the decoder has already read a length prefix and the check
// costs two comparisons; it enforces nothing else, because an RE2 engine and a
// predicate evaluator are not in the byte budget. That asymmetry is emitted as
// a manifest a reviewer can read rather than left as an unwritten assumption.
//
// # Determinism
//
// Regenerating twice must be byte-identical: descriptors are walked in
// declaration order, nothing is ranged over a map, and no clock, hostname or
// path enters the output. A spec asserts it.
package clientcodec
