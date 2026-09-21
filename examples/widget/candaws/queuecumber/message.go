package queuecumber

// A message at runtime is a value that is in exactly one of three places: the
// broker's own ready slice, one worker's goroutine stack, or the broker's own
// dead-letter slice. That is the whole reason there is no lock in this package —
// a queue is a hand-off, and a hand-off is a channel.

// messageID identifies one message for its whole life, redeliveries included.
//
// It is minted once and never reused, which is what makes "this is the same
// message, tried again" a statement anything can check rather than an
// impression.
type messageID uint64

// noMessage is the identity no message has.
//
// Identities are minted from one, so zero is free to mean "this worker is
// holding nothing" in the broker's own per-worker table — one array rather than
// a table and a set of booleans that can disagree about the same worker.
const noMessage = messageID(0)

// message is an immutable payload plus a mutable attempt count.
//
// It is copied on every hand-off, so a worker holding one is holding its own
// copy: the broker's ready slice and a worker's stack can never be two views of
// one value.
type message struct {
	// ID is the message's identity.
	ID messageID

	// Attempts is how many times this message has been leased. It only
	// increases, which is the property that makes the attempt ceiling a
	// ceiling rather than a suggestion.
	Attempts int
}

// leaseRequest is a worker asking for work, carrying the channel it wants to be
// answered on.
//
// The reply channel travelling with the request is what makes the ready slice
// unreachable: a worker does not read the queue, it asks and is either handed
// one message or not answered at all.
type leaseRequest struct {
	// Worker is the asking worker's index, which is also how the broker knows
	// how many workers are leasing.
	Worker int

	// Reply is where the broker sends the lease, exactly once, or never.
	//
	// It is the asking worker's own inbox rather than a channel made per
	// request, and it is buffered, so the broker never waits for a worker and a
	// grant made just as that worker gave up waiting is still there when it
	// comes back. A per-request channel would have made that grant unreachable:
	// a lease the broker attributed to a worker that never held the message,
	// aged out unread, and then counted as a redelivery of a message nobody had
	// looked at once.
	Reply chan lease
}

// lease is a time-bounded grant of one message to one worker.
type lease struct {
	// Message is the worker's own copy, with its attempt count already raised.
	Message message

	// Worker is who it was granted to.
	Worker int
}

// ack is a worker reporting that it finished with a message.
//
// There is no nack. A worker that cannot handle a message says nothing, and the
// lease expires — which is not a simplification of at-least-once delivery, it is
// at-least-once delivery: the only thing that removes a message is somebody
// saying they finished with it.
type ack struct {
	// ID names the message the worker was granted.
	ID messageID

	// Worker is who is acknowledging.
	Worker int
}

// pendingLease is one live lease as the expiry goroutine sees it: an identity
// and a tick count, and nothing else.
//
// The expiry goroutine owns these and the broker owns the leases themselves, so
// neither can read the other's. An expiry that fires for a message already
// acknowledged reaches a broker that no longer has it and is ignored, which is
// the one race this design has and the one it is built to absorb.
type pendingLease struct {
	// ID is the leased message.
	ID messageID

	// Remaining is how many sweeps are left before the lease expires. It is a
	// count rather than a deadline so that the expiry goroutine reads no clock
	// of its own: the sweep ticker is the only clock in this half of the
	// engine, and one tick is one unit of visibility.
	Remaining int
}
