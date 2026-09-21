package queuecumber

import (
	"context"
	"math/rand/v2"
	"time"
)

// worker is one handler: one goroutine, its own random stream, and at most one
// message in hand at any instant.
//
// It never holds two leases, and it never reads the broker's queue. Everything
// it learns arrives on its own inbox, handed over with every request it makes.
// The "at most one" is not left to this side to keep, either: the broker knows
// which message each worker is holding and does not answer a second ask.
type worker struct {
	// id is this worker's index, and the only thing the broker knows it by.
	id int

	// requests is the broker's inbox for lease requests.
	requests chan<- leaseRequest

	// replies is this worker's own inbox for leases: one channel for its whole
	// life, buffered by one, handed over with every request it makes.
	//
	// Per worker rather than per request, because a grant that lands just as
	// this worker's patience runs out has to go somewhere it can still be read.
	// In a channel made for that one request it would be unreachable, and the
	// message would sit leased to a worker that was not holding it until the
	// visibility timeout put it back.
	replies chan lease

	// acks is where a worker says it finished with a message.
	acks chan<- ack

	// patience is how long a worker waits for an answer before asking again.
	// The broker answers or does not answer; a worker that waited forever for
	// an empty queue would be a worker that never notices it filled up.
	patience time.Duration

	// duration is how long the worker holds a message.
	duration time.Duration

	// dropRate is how often this worker finishes and says nothing.
	dropRate float64

	// jitter is this worker's own random stream, seeded from the broker's seed
	// and the worker's index.
	jitter *rand.Rand
}

// run is the worker's whole life: ask, wait, work, acknowledge or not, repeat.
//
// The "or not" is the product. A worker that drops a message does not report a
// failure, does not nack and does not tell anybody: it simply stops holding the
// message, the lease ages out, and the broker makes it ready again with its
// attempt count raised. At-least-once delivery is that sentence and nothing more.
func (handler *worker) run(ctx context.Context) {
	for {
		granted, taken := handler.claim(ctx)
		if !taken {
			if ctx.Err() != nil {
				return
			}
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(handler.duration):
		}

		if handler.jitter.Float64() < handler.dropRate {
			continue
		}

		select {
		case handler.acks <- ack{ID: granted.Message.ID, Worker: handler.id}:
		case <-ctx.Done():
			return
		}
	}
}

// claim looks in this worker's own inbox, and if it is empty asks for one
// message and waits the worker's patience for an answer.
//
// The inbox is looked at first because a grant made after this worker gave up
// waiting is sitting in it. Taking it is the difference between a lease the
// broker attributed to somebody and a lease somebody is holding: the message
// gets worked, acknowledged, and billed for exactly one look. The broker
// answers into the inbox without ever waiting, so a worker that has already
// given up still costs the broker nothing.
func (handler *worker) claim(ctx context.Context) (lease, bool) {
	select {
	case waiting := <-handler.replies:
		return waiting, true
	default:
	}

	select {
	case handler.requests <- leaseRequest{Worker: handler.id, Reply: handler.replies}:
	case <-ctx.Done():
		return lease{}, false
	}

	select {
	case granted := <-handler.replies:
		return granted, true
	case <-time.After(handler.patience):
		return lease{}, false
	case <-ctx.Done():
		return lease{}, false
	}
}
