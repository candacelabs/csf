// Package uigen turns one resolved widget document into the files that make it
// a running widget: a templ view and a Go scaffold implementing the SDK's
// widget contract.
//
// # In memory, then to disk
//
// Generate returns artifacts — a path and its exact bytes — and writes nothing.
// Write is a separate call. That split is what makes the determinism claim
// cheap to check: a spec generates twice and compares two slices, with no
// temporary directory and no filesystem anywhere in the assertion. It is the
// shape pkg/gotth/internal/clientcodec already uses, for the same reason.
//
// Determinism is not incidental here. Everything emitted is derived from the
// IR's ordered sequences, and nothing is derived from a map: the IR is ordered
// precisely so a render is byte-identical for equal state, and a generator that
// reintroduced iteration order would hand that property back.
//
// # What it emits, and what it refuses
//
// Every block of the dialect reaches the output: state, predicates, bindings,
// labels, chrome, roles, channels, placements, a scene of nodes, edges and
// orbits, motion, indicators, controls, events and streams. The three computed
// records reach it too — an edge's geometry becomes its transform, the legend is
// walked rather than re-derived from the channels, and the dirty projection
// becomes the widget's own dirty declaration.
//
// Each generated definition has a default constructor and view, plus NewWidgetAt
// and WidgetViewAt forms that accept a host-assigned region. The region scopes
// the live root, accessible title and animated scene identities; the definition's
// name and event contracts remain shared. Hosts validate region syntax and
// uniqueness before exposing instances through their live configuration.
//
// What is refused is listed by Refusals and is three shapes of one construct: a
// control whose trigger is change, input or submit. A control declares a
// caption, a trigger and an event, and nothing that says what kind of element it
// is — a click is a button and needs nothing more, while those three need a form
// control the dialect cannot describe. Binding one of them to a button would
// emit a binding that can never fire.
//
// Refusing is the whole point of the boundary. A generator that silently
// omitted a construct, or emitted a plausible-looking approximation of one,
// would produce a widget that compiles, renders, and is not the widget the
// author wrote — which is the failure the dialect's validator exists to prevent
// one layer up, and it would be pointless to enforce there and abandon here.
package uigen
