// Package protocol is the boundary every byte crosses in either direction.
//
// Every WebSocket message payload, both ways, is exactly one encoded
// gotthlive.v1.Frame sent as a binary frame. There is no JSON, no text
// framing, and no debug escape hatch. The schema, its refinement predicates,
// and the invariants the predicate grammar cannot express are specified in
// docs/protocol.md.
//
// # Inbound
//
// ParseInbound is the sole entry point for bytes arriving from a client, and
// there is no exported way to obtain a payload that has not passed through
// it. It unmarshals, applies the generated Liquid Proto validator to the
// envelope and then to the matched payload, checks version compatibility,
// walks every enum field against its descriptor, bounds every repeated field,
// and applies the cross-field invariants. A new frame kind cannot skip a step:
// a conformance test walks the payload oneof by protoreflect and asserts every
// member has both a case here and a generated Validate function.
//
// # Outbound
//
// ValidateOutbound re-checks a constructed frame against the same boundary
// immediately before marshalling, on the single write path, and it is not
// optional. Inbound frames are protected because they are parsed; outbound
// frames are constructed, and construction discipline is not a property a
// reviewer can check. Re-checking is what makes "no patch without an origin"
// a type-level fact rather than a convention.
//
// # Layout
//
// Generated code lives in gotthlivepb so that the reproducibility check has a
// clean target and hand-written code is never mixed with generated code. It
// holds the frame schema and generated validators. The canonical annotation
// schema and runtime come from pkg/liquidproto. Generated protocol code is
// committed, because consumers of this module must never need protoc.
//
// # Status
//
// Implemented: ParseInbound, ValidateOutbound, the framer, the limits table,
// the close-code enumeration, and the hand-checked invariants.
package protocol
