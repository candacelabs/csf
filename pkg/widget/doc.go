// Package widget is the widget SDK: the contract a widget implements, the
// registry a host binary mounts them through, and the interpreter for the small
// Mermaid dialect widgets are declared in.
//
// # Monolithic microservices
//
// A widget is a vertical slice — its own state, its own events, its own live
// region, its own UI — and every widget a host serves runs inside one process.
// There is no per-widget port, container, deployment or connection. What
// separates two widgets is the same thing that separates two goroutines: the Go
// runtime schedules them across cores, a session's goroutine advances one
// widget's reducer at a time, and each effect gets a goroutine of its own at the
// actor boundary. The isolation a fleet of services buys with network calls,
// this buys with a routing table and a slice index.
//
// That is the whole claim behind "monolithic microservices", and it is worth
// stating precisely because the parts of it that are true and the parts that
// are marketing are easy to run together. What holds: independent state,
// independent event namespaces, independent live regions, independent failure
// of an effect, and the scheduler's own parallelism across widgets. What does
// not: a widget cannot be deployed, restarted, scaled or rolled back on its
// own, because there is one binary. A panicking reducer is contained by the
// library's panic budget for that session and by nothing else.
//
// [Registry] is where that cashes out. [Registry.LiveConfig] turns a set of
// registered widgets into one gotth-live configuration — one fragment per
// widget, the union of their event names, and a reducer that routes each event
// to the widget that owns it — which the host hands to live.New and serves.
//
// # The contract
//
// [IWidget] is the widget contract, and its methods are the lifecycle phases of
// docs/ontology.md rather than a shape chosen for convenience: register, mount,
// event, render, effect, unmount, plus the type-independent state projection a
// host reads. This is what a generated widget implements and what P4's fleet is
// written against.
//
// It is generic in the widget's own state type, and everything author-facing
// stays that way. A registry holds widgets of several state types in one
// ordered sequence, which is heterogeneity Go has no other way to express, so
// [Register] erases it — once, behind a generic shell, into an unexported
// adapter nothing outside this package can hold. That one function is the whole
// of where a widget's state type is forgotten and the whole of where it is
// asserted back.
//
// # The dialect
//
// The contract the interpreter implements lives beside it in docs/ and is the
// authority on every question this comment does not settle:
// docs/dialect.md is the surface syntax, docs/ontology.md the typed concepts,
// docs/errors.md the error catalogue, and docs/examples/ four worked documents
// — three that validate and one that does not, whose annotations are this
// package's acceptance test.
//
// # The contract
//
// Interpret parses and validates in one call and returns both an IR value and
// every finding. Three properties hold, and callers may rely on all three:
//
//   - Both passes run to completion. The findings are every finding, never the
//     first, sorted by (line, column, class) so two runs over one document
//     produce byte-identical output. An author who fixes one error and re-runs
//     to discover the next learns that the tool tells them a fraction of the
//     truth, and starts guessing ahead of it.
//   - Every finding is anchored, classified and repairable: it carries a
//     file:line:column position, the identifier of a class in docs/errors.md,
//     a message naming its subject in the present indicative, and one
//     imperative fix naming the exact spelling to write.
//   - Nothing generates before it validates. A returned document is sound only
//     when no finding came with it; generating from an unsound document is
//     generating from a guess.
//
// # What the IR guarantees
//
// A Document is resolved, total, ordered, anchored and closed: every reference
// is a handle rather than a name, no field's absence means "work it out", every
// collection is a sequence in declaration order, every record carries a
// SourceSpan, and nothing in it names a file path, a host, an address or a
// credential. EdgeGeometry, Legend and DirtyProjection are computed rather than
// parsed — they are in the IR so a generator need not re-derive them, and
// absent from the grammar so an author cannot contradict them.
//
// # What this package does not do
//
// It does not generate, render or serve. It does not read a palette: a document
// names seven semantic tokens and the design system owns their values. It does
// not compile the host's connection status into a motion gate — Motion carries
// HostStatusGate so that a generator reads the obligation rather than
// remembering it.
//
// It also does not open a stream. A [Registration] carries the streams a widget
// declared, and the host resolves each source name against the data plane it
// has, because a widget document names no host, no address and no credential by
// construction — which is what makes one publishable.
//
// # Regenerating
//
// gen.sh writes every generated widget in this checkout and, with --check,
// asserts the committed output is byte-identical to a fresh generation. The
// directive below is a discoverability anchor for `go generate`; the script is
// what CI runs, and it must be run inside the toolchain container that carries
// the pinned templ CLI.
//
//go:generate bash gen.sh
package widget
