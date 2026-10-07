package session

import "time"

// Event is one input to a reducer, already past the refinement boundary and
// past authorization.
//
// ID and At are stamped at the actor boundary, never read by a reducer from a
// clock or a random source: that is what makes an event log replay to a
// byte-identical result.
type Event struct {
	// ID is the server-minted causal root. It is zero for the transitions the
	// server started on its own, where the origin source names the cause.
	ID uint64
	// ClientRef is the client's own correlation handle, echoed back so the
	// browser can match a patch to the interaction that caused it.
	ClientRef uint64
	// SeenServerSeq is the causation edge: the sequence number of the last
	// patch the user had applied when they acted.
	SeenServerSeq uint64

	// Name is the event name the application registered, such as
	// "cart.add". An unregistered name never reaches here: it is refused at
	// ingress and counted.
	Name string

	// FragmentID is the live region the interaction happened in, empty when
	// the server started the transition itself.
	FragmentID string

	// At is stamped at the actor boundary, not read by the reducer from a
	// clock. That is what lets an event log replay to a byte-identical result.
	At time.Time

	// Fields are the form values the interaction carried, in the order they
	// arrived.
	Fields []Field

	// Contributing lists events in this session whose state changes this
	// event carries. It is only ever set on an event an effect emits, where
	// the application is the only party that knows the edge — the library
	// knows which event scheduled an effect, but not which event caused the
	// value an asynchronous fan-out is delivering.
	Contributing []uint64
}
