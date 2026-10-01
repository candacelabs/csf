package coldstart

// PoolView is what the dispatcher tells anybody, and it is deliberately the
// shape the Coldstart card's `poolReport` event carries — plus the three numbers
// that make the pool's own invariants checkable from outside.
type PoolView struct {
	// Sequence is this view's position in the stream, from 1.
	Sequence uint64

	// RuntimeName is the runtime the card interpolates into a stat line.
	RuntimeName string

	// WarmInstances is how many instances have paid their start-up and can
	// serve now.
	WarmInstances int

	// LiveInstances is how many instance goroutines exist, warming ones
	// included. The warm count never exceeds it, which is what makes
	// [PoolView.Sound] a property rather than a coincidence.
	LiveInstances int

	// Queued is how many invocations are waiting on a start-up.
	Queued int

	// ColdStartMillis is what the platform spends getting ready, and is billed.
	ColdStartMillis int

	// DispatcherUp is whether the dispatcher is still accepting. It stops when
	// the backlog reaches its ceiling, which is the only thing that closes it.
	DispatcherUp bool

	// Draining is whether any instance has served and is now waiting to be
	// called again or reaped.
	Draining bool

	// WarmFloor is how many instances survive every sweep. Zero is what
	// "scales to zero" means; the prewarm control raises it.
	WarmFloor int

	// Served and Dropped are the totals out. A dropped invocation had its reply
	// channel closed rather than being left to wait, so the two together are
	// every invocation the dispatcher ever accepted.
	Served  uint64
	Dropped uint64
}

// Sound reports whether this view describes a pool that could exist.
//
// The warm count never exceeds the live count — an instance cannot have paid a
// start-up it was never spawned for — and a warm floor of zero is legal and is
// the whole of what scaling to zero means. It is exported because it is the
// property rather than an implementation detail: a specification asserts it of
// every view the engine publishes.
func (view PoolView) Sound() bool {
	return view.WarmInstances >= 0 &&
		view.WarmInstances <= view.LiveInstances &&
		view.WarmFloor >= 0
}

// ScaledToZero reports whether no instance goroutine exists at all. It is the
// state the product is named after, and the one a warm floor prevents.
func (view PoolView) ScaledToZero() bool { return view.LiveInstances == 0 }
