package queuecumber

import (
	"context"
	"slices"
	"time"

	"github.com/candacelabs/csf/examples/widget/candaws/fleet"
)

// noticeDepth is how many notices are in flight before a sender waits.
//
// It is generous rather than tuned, and it is what keeps the broker from
// blocking on the expiry goroutine while the expiry goroutine is blocked on the
// broker: everything the two say to each other fits, so neither is ever the only
// thing that can unblock the other.
const noticeDepth = 64

// Broker is one Queuecumber broker running in one process.
//
// It holds channels and nothing else. The ready queue, the in-flight table and
// the dead-letter store are locals of the goroutine [Broker.Run] starts, and
// nothing in the process can name them.
type Broker struct {
	// config is the shape and pace NewBroker validated.
	config Config

	// submissions is the producer's tick. It carries no message: the broker
	// mints identities, because an identity minted anywhere else is an
	// identity two goroutines can mint at once.
	submissions chan struct{}

	// requests is where a worker asks for work, carrying its own reply channel.
	requests chan leaseRequest

	// acks is where a worker says it finished.
	acks chan ack

	// timeouts is where the expiry goroutine names a lease that has aged out.
	timeouts chan messageID

	// granted and released are what the broker tells the expiry goroutine: a
	// lease to start ageing, and one that ended before it aged out.
	granted  chan pendingLease
	released chan messageID

	// redrives is the command the card's second control emits. The widget
	// changes no state for it; the host asks the broker, and this is where.
	redrives chan struct{}

	// views is the stream the card's one declared source resolves to.
	views *fleet.Feed[BrokerView]

	// start is a token taken by the first Run.
	start fleet.Once
}

// NewBroker builds a broker and starts nothing.
func NewBroker(config Config) (*Broker, error) {
	if validationError := config.Validate(); validationError != nil {
		return nil, validationError
	}
	return &Broker{
		config:      config,
		submissions: make(chan struct{}, noticeDepth),
		requests:    make(chan leaseRequest, noticeDepth),
		acks:        make(chan ack, noticeDepth),
		timeouts:    make(chan messageID, noticeDepth),
		granted:     make(chan pendingLease, noticeDepth),
		released:    make(chan messageID, noticeDepth),
		redrives:    make(chan struct{}, 1),
		views:       fleet.NewFeed[BrokerView](8),
		start:       fleet.NewOnce(),
	}, nil
}

// Config is the configuration this broker was built from.
func (broker *Broker) Config() Config { return broker.config }

// Run starts every goroutine and returns when the context ends and all of them
// have stopped. The only error it has is being called twice.
func (broker *Broker) Run(ctx context.Context) error {
	if !broker.start.Take() {
		return fleet.ErrAlreadyRunning
	}

	var crew fleet.Crew
	crew.Go(ctx, broker.views.Run)
	crew.Go(ctx, broker.serve)
	crew.Go(ctx, broker.expire)
	crew.Go(ctx, broker.produce)
	for index := range broker.config.Workers {
		handler := &worker{
			id:       index,
			requests: broker.requests,
			replies:  make(chan lease, 1),
			acks:     broker.acks,
			patience: broker.config.SweepInterval,
			duration: broker.config.WorkDuration,
			dropRate: broker.config.DropRate,
			jitter:   fleet.Jitter(broker.config.Seed, uint64(index)),
		}
		crew.Go(ctx, handler.run)
	}

	crew.Wait()
	return nil
}

// Watch is the broker stream the card's declared source resolves to.
func (broker *Broker) Watch(ctx context.Context) (<-chan BrokerView, error) {
	return broker.views.Subscribe(ctx)
}

// Redrive moves everything in the dead-letter store back to the ready queue.
//
// It is what the card's second control asks for, and it is a method rather than
// widget state because the widget changes nothing when that button is pressed:
// the event says what the viewer wants and the host decides what it means, which
// is the same seam a stream's source name is.
func (broker *Broker) Redrive(ctx context.Context) error {
	select {
	case broker.redrives <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// produce is the customer: one goroutine submitting on a ticker.
//
// It carries no payload and mints no identity. A submission is a knock on the
// door, and the broker decides whether there is room.
func (broker *Broker) produce(ctx context.Context) {
	arrivals := time.NewTicker(broker.config.Cadence)
	defer arrivals.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-arrivals.C:
			select {
			case broker.submissions <- struct{}{}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// expire is the visibility timeout: one goroutine owning the age of every live
// lease, and nothing else.
//
// It never sees a message and never sees the broker's table. It is told an
// identity to start ageing and an identity to forget, and it says one thing
// back: this one has been out too long. A timeout that names a message the
// broker no longer has is ignored there, which is the one race in this design
// and the one it absorbs rather than prevents.
func (broker *Broker) expire(ctx context.Context) {
	var pending []pendingLease

	sweeps := time.NewTicker(broker.config.SweepInterval)
	defer sweeps.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case starting := <-broker.granted:
			pending = append(pending, starting)

		case ending := <-broker.released:
			pending = slices.DeleteFunc(pending, func(live pendingLease) bool {
				return live.ID == ending
			})

		case <-sweeps.C:
			// One tick is one unit of visibility. Ageing a count rather than
			// comparing a deadline is what keeps this goroutine free of a clock
			// of its own: the ticker is the only one, and a specification that
			// wants a faster timeout moves the ticker.
			remaining := pending[:0]
			for _, live := range pending {
				live.Remaining--
				if live.Remaining > 0 {
					remaining = append(remaining, live)
					continue
				}
				select {
				case broker.timeouts <- live.ID:
				case <-ctx.Done():
					return
				}
			}
			pending = remaining
		}
	}
}

// serve is the broker: one goroutine owning the ready queue, the in-flight
// table, the dead-letter store and every total the card renders.
//
// Nothing else in the process may touch any of them, which is why they are
// locals here rather than fields anywhere.
func (broker *Broker) serve(ctx context.Context) {
	var ready []message
	leased := map[messageID]lease{}
	var dead []message

	// staff is which workers have ever asked, which is the only way a broker
	// learns its pool size. holding is which message each of them has right
	// now, which is the other half of the same table and the half the ontology
	// puts a cardinality on: a worker holds nought or one, never two. The
	// in-flight table is keyed by message, so it cannot answer that question by
	// itself without a scan, and a scan on every request would make the answer
	// a function of how many messages are out rather than of who is asking.
	var staff [maxWorkers]bool
	var holding [maxWorkers]messageID
	next := messageID(0)
	sequence := uint64(0)
	totals := BrokerView{}

	publish := func() bool {
		sequence++
		view := totals
		view.Sequence = sequence
		view.Depth, view.InFlight, view.DeadLettered = len(ready), len(leased), len(dead)
		view.Accepting = len(ready) < broker.config.Capacity
		view.WorkersUp = 0
		for _, leasing := range staff {
			if leasing {
				view.WorkersUp++
			}
		}
		return broker.views.Publish(ctx, view)
	}

	// The stream opens with a view rather than with a wait, so a subscriber
	// that arrives before the first message has something to render.
	if !publish() {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return

		case <-broker.submissions:
			if len(ready) >= broker.config.Capacity {
				totals.Refused++
				if !publish() {
					return
				}
				continue
			}
			next++
			totals.Submitted++
			ready = append(ready, message{ID: next})
			if !publish() {
				return
			}

		case asking := <-broker.requests:
			staff[asking.Worker] = true
			if holding[asking.Worker] != noMessage {
				// This worker is already holding one. A second grant would put
				// two messages in one pair of hands and would show up on the
				// card as more messages in flight than there are workers to
				// hold them, which is the ontology's "a worker never holds two
				// leases" said in the one place that can enforce it. It is not
				// answered; the lease it has is acknowledged or expires, and
				// it asks again.
				if !publish() {
					return
				}
				continue
			}
			if len(ready) == 0 {
				// Not answered at all. The worker's patience runs out and it
				// asks again, which is cheaper for both than a queue of
				// waiting requests the broker would have to remember.
				if !publish() {
					return
				}
				continue
			}
			taken := ready[0]
			taken.Attempts++
			granted := lease{Message: taken, Worker: asking.Worker}
			select {
			case asking.Reply <- granted:
			default:
				// The worker's inbox still holds a grant it has not taken —
				// its previous lease expired while it was not looking. Nothing
				// is handed over and nothing is counted: the message stays at
				// the head of the queue with the attempt count it arrived
				// with, because an attempt is a message somebody looked at and
				// nobody looked at this one.
				if !publish() {
					return
				}
				continue
			}
			ready = ready[1:]
			if taken.Attempts > 1 {
				totals.Redelivered++
			}
			leased[taken.ID] = granted
			holding[asking.Worker] = taken.ID
			select {
			case broker.granted <- pendingLease{
				ID: taken.ID, Remaining: broker.config.VisibilitySweeps}:
			case <-ctx.Done():
				return
			}
			if !publish() {
				return
			}

		case finished := <-broker.acks:
			live, held := leased[finished.ID]
			if !held || live.Worker != finished.Worker {
				// An acknowledgement for a lease that already expired, or for
				// one that has since been granted to somebody else. The
				// message is not this worker's to finish, and counting it
				// twice would make the conservation law a lie in the
				// flattering direction. The ontology's other half of the same
				// sentence is the worker check: an ack names the lease it was
				// granted, or it names nothing.
				continue
			}
			delete(leased, finished.ID)
			holding[finished.Worker] = noMessage
			totals.Acknowledged++
			select {
			case broker.released <- finished.ID:
			case <-ctx.Done():
				return
			}
			if !publish() {
				return
			}

		case aged := <-broker.timeouts:
			expired, live := leased[aged]
			if !live {
				continue
			}
			delete(leased, aged)
			holding[expired.Worker] = noMessage
			if expired.Message.Attempts >= broker.config.AttemptCeiling {
				// Entry is one-way until a redrive. Every message in the store
				// has been tried the hardest, which is what makes it an add-on.
				dead = append(dead, expired.Message)
			} else {
				ready = append(ready, expired.Message)
			}
			if !publish() {
				return
			}

		case <-broker.redrives:
			for _, exhausted := range dead {
				exhausted.Attempts = 0
				ready = append(ready, exhausted)
			}
			dead = dead[:0]
			if !publish() {
				return
			}
		}
	}
}
