package fleet

import "context"

// Feed is the fan-out at the end of an engine: one goroutine owning the
// subscriber set and the last view minted, reachable only by sending to it.
//
// V is the engine's own view type and is a type parameter rather than an opaque
// one for the reason the widget SDK gives about its own state: nothing an author
// writes has a reason not to know it. A feed never inspects a view, so the
// constraint is the widest one and the erasure count stays zero.
type Feed[V any] struct {
	// subscriptions is how a caller reaches the feed goroutine to be added to
	// its set. It is unbuffered: a Subscribe that returned before the feed had
	// the request would be a subscriber that misses the next view and cannot
	// tell that it did.
	subscriptions chan feedSubscription[V]

	// published is what an engine sends a new view on.
	published chan V

	// buffer is how far behind one subscriber may fall before it starts missing
	// views, and also the depth of published.
	buffer int
}

// feedSubscription is one caller's feed, as the request to open it.
type feedSubscription[V any] struct {
	// ctx is the subscriber's lifetime. The feed holds it rather than spawning
	// a goroutine per subscriber to watch it: a subscriber whose context ended
	// is dropped at the next view, which costs at most one round.
	ctx context.Context

	// deliveries is the channel the caller reads. The feed owns closing it.
	deliveries chan V
}

// NewFeed builds a feed and starts nothing. The goroutine belongs to [Feed.Run],
// because it belongs to the context Run is given.
//
// A buffer of zero is legal and means every subscriber sees only the view that
// arrives while it is reading, which is almost never what a caller wants; the
// engines here pass a small depth.
func NewFeed[V any](buffer int) *Feed[V] {
	if buffer < 0 {
		buffer = 0
	}
	return &Feed[V]{
		subscriptions: make(chan feedSubscription[V]),
		published:     make(chan V, buffer),
		buffer:        buffer,
	}
}

// Run owns the subscriber set until the context ends, then closes every
// subscriber's channel and returns.
//
// The set, the latest view and the "has there been one" flag are locals rather
// than fields, which is the whole argument: nothing outside this goroutine can
// name them, so there is nothing for a lock to protect and no way to forget one.
func (feed *Feed[V]) Run(ctx context.Context) {
	var subscribers []feedSubscription[V]
	var latest V
	minted := false

	for {
		select {
		case <-ctx.Done():
			for _, subscriber := range subscribers {
				close(subscriber.deliveries)
			}
			return

		case view := <-feed.published:
			latest, minted = view, true
			subscribers = feed.deliver(subscribers, view)

		case request := <-feed.subscriptions:
			subscribers = append(subscribers, request)
			// A subscriber that arrives between two views gets the current one
			// immediately rather than a blank card for up to a round.
			if minted {
				feed.offer(request, latest)
			}
		}
	}
}

// Subscribe returns a channel carrying every view minted from now on, plus the
// current one if there is one. The feed closes it when the feed stops.
func (feed *Feed[V]) Subscribe(ctx context.Context) (<-chan V, error) {
	request := feedSubscription[V]{ctx: ctx, deliveries: make(chan V, feed.buffer)}
	select {
	case feed.subscriptions <- request:
		return request.deliveries, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Publish hands one view to the feed, and reports false when the context ended
// before the feed took it.
//
// It blocks rather than dropping, because the engine is the one producer and a
// view it minted and then discarded is a round nothing can observe. Backpressure
// on subscribers is handled the other way — see [Feed.offer].
func (feed *Feed[V]) Publish(ctx context.Context, view V) bool {
	select {
	case feed.published <- view:
		return true
	case <-ctx.Done():
		return false
	}
}

// deliver hands one view to every live subscriber, and closes and forgets the
// ones whose context has ended.
//
// The filtering is done in place on the slice, which belongs to the feed
// goroutine and is reachable from nowhere else.
func (feed *Feed[V]) deliver(
	subscribers []feedSubscription[V], view V,
) []feedSubscription[V] {
	remaining := subscribers[:0]
	for _, subscriber := range subscribers {
		if subscriber.ctx.Err() != nil {
			close(subscriber.deliveries)
			continue
		}
		feed.offer(subscriber, view)
		remaining = append(remaining, subscriber)
	}
	return remaining
}

// offer hands a view to one subscriber without waiting for it.
//
// A view is a complete picture rather than a delta, so a subscriber that missed
// one has lost nothing it needs. An engine that stalled because a browser
// stopped reading would be a service hostage to its own console.
func (feed *Feed[V]) offer(subscriber feedSubscription[V], view V) {
	select {
	case subscriber.deliveries <- view:
	default:
	}
}
