package queuecumber

// BrokerView is what the broker tells anybody, and it is deliberately the shape
// the Queuecumber card's `brokerReport` event carries — plus the three totals
// that make the conservation law checkable from outside.
type BrokerView struct {
	// Sequence is this view's position in the stream, from 1.
	Sequence uint64

	// Accepting is whether the queue has room. A full queue closes the intake,
	// which is the only thing in this engine that ever does.
	Accepting bool

	// Depth is how many messages are ready, InFlight how many are leased, and
	// DeadLettered how many exhausted their attempts.
	//
	// A message is in exactly one of the three at every instant. That is the
	// invariant, and [BrokerView.Conserved] is where it is checkable.
	Depth        int
	InFlight     int
	DeadLettered int

	// WorkersUp is how many distinct workers the broker has been asked for work
	// by. A broker learns its pool size from being asked; nothing tells it.
	WorkersUp int

	// Submitted and Acknowledged are the totals in and out.
	Submitted    uint64
	Acknowledged uint64

	// Redelivered is how many leases expired and put a message back. It is the
	// number the console renders largest, because a redelivery is a fresh
	// transaction.
	Redelivered uint64

	// Refused is how many submissions arrived at a full queue. They are
	// counted rather than queued, because a queue with an unbounded queue is
	// not a queue.
	Refused uint64
}

// Conserved reports whether every message this broker accepted is still
// somewhere it can be named.
//
// Accepted minus acknowledged must equal ready plus leased plus dead-lettered:
// there is no fourth place, and a message that stopped being in one of the three
// without being acknowledged was dropped. It is exported because it is the
// property rather than an implementation detail — a specification asserts it of
// every view the engine ever publishes.
func (view BrokerView) Conserved() bool {
	return int(view.Submitted-view.Acknowledged) == view.Depth+view.InFlight+view.DeadLettered
}
