// Copyright 2026 Candace Labs

package intake

import (
	"context"
	"errors"
	"fmt"
	"sync"

	intakev1 "github.com/candacelabs/csf/proto/candace/intake/v1"
)

const (
	// DefaultQueueCapacity is how many accepted events wait for dispatch
	// before Offer blocks the poller.
	DefaultQueueCapacity = 256
	// DefaultDeduplicationMemory is how many recent event identifiers the
	// memory queue remembers. A GitHub feed page holds at most 100 events,
	// so this spans many pages of every feed.
	DefaultDeduplicationMemory = 4096
)

// ErrInvalidEvent reports an event that fails its contract's validation.
var ErrInvalidEvent = errors.New("intake: invalid event")

// IEventQueue holds accepted events between the poller and the dispatcher,
// and is where deduplication happens. A durable implementation (in csfdb) must
// keep these semantics:
//
//   - Offer accepts an event at most once per identifier: offering an
//     identifier it has accepted before reports false and queues nothing.
//   - Offer reports [ErrInvalidEvent] for an event without an identifier.
//   - An Offer that fails — its context ends before the event is queued —
//     leaves no trace, so offering the same event again can succeed.
//   - Take returns accepted events oldest first and blocks until one is
//     available or its context ends.
type IEventQueue interface {
	Offer(ctx context.Context, event *intakev1.Event) (bool, error)
	Take(ctx context.Context) (*intakev1.Event, error)
}

// QueueOption configures a [MemoryEventQueue].
type QueueOption func(configuration *queueConfiguration)

type queueConfiguration struct {
	capacity int
	memory   int
}

// WithQueueCapacity sets how many events wait for dispatch before Offer
// blocks.
func WithQueueCapacity(capacity int) QueueOption {
	return func(configuration *queueConfiguration) { configuration.capacity = capacity }
}

// WithDeduplicationMemory sets how many recent identifiers are remembered.
// An identifier older than that is forgotten and would be accepted again.
func WithDeduplicationMemory(memory int) QueueOption {
	return func(configuration *queueConfiguration) { configuration.memory = memory }
}

// MemoryEventQueue is the in-process [IEventQueue]: a bounded channel of
// pending events and a bounded memory of recent identifiers.
type MemoryEventQueue struct {
	pending chan *intakev1.Event
	memory  int

	// remembering guards seen and order: a leaf critical section (CS-5) — a
	// set membership test and insert, with nothing sequenced through it. The
	// channel send that queues the event happens outside it.
	remembering sync.Mutex
	seen        map[string]struct{}
	order       []string
}

var _ IEventQueue = (*MemoryEventQueue)(nil)

// NewMemoryEventQueue validates its options and returns an empty queue.
func NewMemoryEventQueue(options ...QueueOption) (*MemoryEventQueue, error) {
	configuration := queueConfiguration{capacity: DefaultQueueCapacity, memory: DefaultDeduplicationMemory}
	for _, option := range options {
		if option != nil {
			option(&configuration)
		}
	}
	if configuration.capacity <= 0 {
		return nil, fmt.Errorf("intake: queue capacity must be positive, got %d", configuration.capacity)
	}
	if configuration.memory < configuration.capacity {
		return nil, fmt.Errorf("intake: deduplication memory %d must cover the queue capacity %d", configuration.memory, configuration.capacity)
	}
	return &MemoryEventQueue{
		pending: make(chan *intakev1.Event, configuration.capacity),
		memory:  configuration.memory,
		seen:    make(map[string]struct{}, configuration.memory),
	}, nil
}

// Offer queues event unless its identifier was accepted before; see
// [IEventQueue].
func (queue *MemoryEventQueue) Offer(ctx context.Context, event *intakev1.Event) (bool, error) {
	if event.GetId() == "" {
		return false, fmt.Errorf("%w: an event needs an identifier", ErrInvalidEvent)
	}
	if !queue.remember(event.GetId()) {
		return false, nil
	}
	select {
	case queue.pending <- event:
		return true, nil
	case <-ctx.Done():
		queue.forget(event.GetId())
		return false, ctx.Err()
	}
}

// Take returns the oldest queued event; see [IEventQueue].
func (queue *MemoryEventQueue) Take(ctx context.Context) (*intakev1.Event, error) {
	select {
	case event := <-queue.pending:
		return event, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// remember records identifier and reports whether it was new, forgetting the
// oldest identifier once the memory is full.
func (queue *MemoryEventQueue) remember(identifier string) bool {
	queue.remembering.Lock()
	defer queue.remembering.Unlock()
	if _, seen := queue.seen[identifier]; seen {
		return false
	}
	queue.seen[identifier] = struct{}{}
	queue.order = append(queue.order, identifier)
	if len(queue.order) > queue.memory {
		delete(queue.seen, queue.order[0])
		queue.order = queue.order[1:]
	}
	return true
}

// forget removes an identifier whose event was never queued.
func (queue *MemoryEventQueue) forget(identifier string) {
	queue.remembering.Lock()
	defer queue.remembering.Unlock()
	delete(queue.seen, identifier)
	for index, remembered := range queue.order {
		if remembered == identifier {
			queue.order = append(queue.order[:index], queue.order[index+1:]...)
			return
		}
	}
}
