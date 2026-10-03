// Copyright 2026 Candace Labs

// Package inproc is the home of the in-process io tier: communication between
// goroutines of one process. Queue is its first primitive: the one typed FIFO
// an in-process store is built on, so no service hand-rolls a queue.
package inproc

import (
	"context"
	"errors"
	"sync"
)

// ErrQueueClosed reports a Push after Close, or a Pop on a closed queue that
// has drained.
var ErrQueueClosed = errors.New("inproc: queue is closed")

// Queue is an unbounded FIFO of T shared by any number of producers and
// consumers. Push never blocks; Pop blocks until an item arrives, the
// context ends or the queue is closed and drained. Close stops new pushes
// and lets consumers drain what was queued: the items already accepted are
// delivered before Pop reports ErrQueueClosed.
//
// The lock guards one datum, the backlog and its closed flag, and every
// section under it is a single step; nobody waits under it. Waiting happens
// outside the lock on the wake channel, which is why the lock is a leaf
// (house rule CS-5) rather than a protocol.
type Queue[T any] struct {
	// guard is the leaf critical section over items and closed.
	guard  sync.Mutex
	items  []T
	closed bool
	// wake carries at most one pending wake-up. A consumer that took an item
	// while more remain re-arms it, so no consumer waits beside a non-empty
	// backlog.
	wake chan struct{}
}

// NewQueue returns an empty, open queue.
func NewQueue[T any]() *Queue[T] {
	return &Queue[T]{wake: make(chan struct{}, 1)}
}

// Push appends item and wakes one waiting consumer. It reports ErrQueueClosed
// after Close.
func (queue *Queue[T]) Push(item T) error {
	queue.guard.Lock()
	if queue.closed {
		queue.guard.Unlock()
		return ErrQueueClosed
	}
	queue.items = append(queue.items, item)
	queue.guard.Unlock()
	queue.signal()
	return nil
}

// Pop removes and returns the oldest item. It waits for one while the queue
// is open and empty; it returns ctx's cause when ctx ends first, and
// ErrQueueClosed once the queue is closed and empty.
func (queue *Queue[T]) Pop(ctx context.Context) (T, error) {
	for {
		item, state := queue.take()
		switch state {
		case took:
			return item, nil
		case drained:
			var zero T
			return zero, ErrQueueClosed
		}
		select {
		case <-queue.wake:
		case <-ctx.Done():
			var zero T
			return zero, context.Cause(ctx)
		}
	}
}

// takeState is what one attempt to take an item found.
type takeState int

const (
	// empty: nothing queued and the queue is open; wait.
	empty takeState = iota
	// took: an item was removed.
	took
	// drained: the queue is closed and nothing remains.
	drained
)

// take is the single-step section: one item out, or the state that explains
// why not.
func (queue *Queue[T]) take() (T, takeState) {
	queue.guard.Lock()
	defer queue.guard.Unlock()
	if len(queue.items) == 0 {
		var zero T
		if queue.closed {
			// The next waiting consumer must learn the same thing.
			queue.signal()
			return zero, drained
		}
		return zero, empty
	}
	item := queue.items[0]
	var zero T
	queue.items[0] = zero
	queue.items = queue.items[1:]
	if len(queue.items) > 0 || queue.closed {
		// Another consumer may be waiting: hand it the next item, or the
		// closed state, without a push to wake it.
		queue.signal()
	}
	return item, took
}

// Len is the number of queued items not yet popped.
func (queue *Queue[T]) Len() int {
	queue.guard.Lock()
	defer queue.guard.Unlock()
	return len(queue.items)
}

// Close refuses further pushes and wakes waiting consumers; queued items stay
// poppable until drained. Close is idempotent.
func (queue *Queue[T]) Close() {
	queue.guard.Lock()
	queue.closed = true
	queue.guard.Unlock()
	queue.signal()
}

// signal arms the one pending wake-up without blocking.
func (queue *Queue[T]) signal() {
	select {
	case queue.wake <- struct{}{}:
	default:
	}
}
