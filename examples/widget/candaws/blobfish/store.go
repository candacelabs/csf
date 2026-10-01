package blobfish

import (
	"context"
	"time"

	"github.com/candacelabs/csf/examples/widget/candaws/fleet"
)

// noticeDepth is how many acknowledgements and reports are in flight before a
// sender waits. It is generous rather than tuned: a late acknowledgement from a
// zone the quorum stopped waiting for has to have somewhere to land, or the zone
// blocks on a coordinator that has moved on.
const noticeDepth = 64

// Store is one Blobfish store running in one process.
//
// It holds channels and nothing else. The bucket's key map, each zone's
// generation, the coordinator's in-flight write and the published view are all
// locals of goroutines [Store.Run] starts.
type Store struct {
	// config is the shape and pace NewStore validated.
	config Config

	// puts and gets are the bucket's requests, each carrying its own reply
	// channel. Nothing reads a result from anywhere else.
	puts chan writeRequest
	gets chan readRequest

	// inboxes is one channel per zone. The coordinator writes to them and so
	// does the repairer; neither closes them, which is what lets a channel have
	// two writers at all.
	inboxes []chan replicaOp

	// acks is the one shared channel every zone answers on. Collecting on one
	// channel rather than three is what makes "stop at the quorum" a `select`
	// that stopped counting rather than a table of who has answered.
	acks chan replicaAck

	// reports is the observer's feed: the coordinator's half and the repairer's
	// half of one published view.
	reports chan storeReport

	// views is the stream the card's declared source resolves to.
	views *fleet.Feed[StoreView]

	// start is a token taken by the first Run.
	start fleet.Once
}

// writeRequest is one put, carrying the channel it wants to be answered on.
type writeRequest struct {
	// Reply is where the coordinator answers, exactly once.
	Reply chan writeResult
}

// writeResult is what a put came back with.
type writeResult struct {
	// Generation is the generation the coordinator minted for this write.
	Generation uint64

	// Acknowledged is how many zones answered before the coordinator stopped
	// counting. It is at least the quorum on a durable write and fewer on one
	// that did not reach it.
	Acknowledged int

	// Durable is whether the quorum was reached.
	Durable bool
}

// readRequest is one get, carrying its own reply channel.
type readRequest struct {
	// Reply is where the coordinator answers, exactly once.
	Reply chan readResult
}

// readResult is what a get came back with.
type readResult struct {
	// Answered is how many zones answered, and Served is whether that reached
	// the read quorum.
	Answered int
	Served   bool
}

// reportKind names which half of the view a report carries.
type reportKind uint8

const (
	// reportWrite is the coordinator's half: a generation and a quorum.
	reportWrite reportKind = iota

	// reportRepair is the repairer's half: how many zones are behind.
	reportRepair
)

// storeReport is one goroutine telling the observer what it knows. Neither half
// can see the other's numbers, which is why the observer exists at all.
type storeReport struct {
	// Kind is which half this is.
	Kind reportKind

	// Generation, Acks, Writable and Readable are the coordinator's half.
	Generation uint64
	Acks       int
	Objects    int
	Writable   bool
	Readable   bool

	// Lagging is the repairer's half.
	Lagging int
}

// NewStore builds a store and starts nothing.
func NewStore(config Config) (*Store, error) {
	if validationError := config.Validate(); validationError != nil {
		return nil, validationError
	}

	store := &Store{
		config:  config,
		puts:    make(chan writeRequest, noticeDepth),
		gets:    make(chan readRequest, noticeDepth),
		inboxes: make([]chan replicaOp, zoneCount),
		acks:    make(chan replicaAck, noticeDepth),
		reports: make(chan storeReport, noticeDepth),
		views:   fleet.NewFeed[StoreView](8),
		start:   fleet.NewOnce(),
	}
	for zone := range store.inboxes {
		store.inboxes[zone] = make(chan replicaOp, noticeDepth)
	}
	return store, nil
}

// Config is the configuration this store was built from.
func (store *Store) Config() Config { return store.config }

// Run starts every goroutine and returns when the context ends and all of them
// have stopped. The only error it has is being called twice.
func (store *Store) Run(ctx context.Context) error {
	if !store.start.Take() {
		return fleet.ErrAlreadyRunning
	}

	var crew fleet.Crew
	crew.Go(ctx, store.views.Run)
	crew.Go(ctx, store.observe)
	crew.Go(ctx, store.coordinate)
	crew.Go(ctx, store.repair)
	crew.Go(ctx, store.bucket)
	for zone := range zoneCount {
		member := &replica{
			id:     zone,
			inbox:  store.inboxes[zone],
			acks:   store.acks,
			delay:  store.config.zoneDelay(zone),
			spread: store.config.DelaySpread,
			jitter: fleet.Jitter(store.config.Seed, uint64(zone)),
		}
		crew.Go(ctx, member.run)
	}

	crew.Wait()
	return nil
}

// Watch is the store stream the card's declared source resolves to.
func (store *Store) Watch(ctx context.Context) (<-chan StoreView, error) {
	return store.views.Subscribe(ctx)
}

// bucket is the bucket: one goroutine owning the key-to-generation map that no
// other goroutine can reach.
//
// It puts on a cadence and reads on the same one, and everything it learns
// arrives on a reply channel it made itself. It is the only thing in this engine
// that knows how many objects exist.
func (store *Store) bucket(ctx context.Context) {
	keys := map[uint64]uint64{}
	next := uint64(0)

	cadence := time.NewTicker(store.config.Cadence)
	defer cadence.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-cadence.C:
		}

		written := writeRequest{Reply: make(chan writeResult, 1)}
		select {
		case store.puts <- written:
		case <-ctx.Done():
			return
		}
		var result writeResult
		select {
		case result = <-written.Reply:
		case <-ctx.Done():
			return
		}
		if result.Durable {
			next++
			keys[next] = result.Generation
		}

		read := readRequest{Reply: make(chan readResult, 1)}
		select {
		case store.gets <- read:
		case <-ctx.Done():
			return
		}
		var served readResult
		select {
		case served = <-read.Reply:
		case <-ctx.Done():
			return
		}

		select {
		case store.reports <- storeReport{
			Kind: reportWrite, Generation: result.Generation, Acks: result.Acknowledged,
			Objects: len(keys), Writable: result.Durable, Readable: served.Served}:
		case <-ctx.Done():
			return
		}
	}
}

// coordinate is the write and read path: one goroutine owning the generation
// counter and the collection of acknowledgements.
//
// It fans a write out to every zone and collects on one shared channel until W
// have answered, then replies and stops waiting. The third acknowledgement
// arrives later, or never. That is not a simplification of eventual
// consistency; it is eventual consistency, made out of a select that stopped
// counting.
func (store *Store) coordinate(ctx context.Context) {
	generation := uint64(0)

	for {
		select {
		case <-ctx.Done():
			return

		case <-store.acks:
			// A zone answering about a write that has already been replied to.
			// Draining it here is what keeps a slow zone from filling the
			// shared channel and blocking on a coordinator that has moved on.

		case written := <-store.puts:
			generation++
			if !store.fan(ctx, replicaOp{Kind: opWrite, Generation: generation}) {
				return
			}
			collected, gathered := store.collect(ctx, generation, opWrite)
			if !gathered {
				return
			}
			written.Reply <- writeResult{
				Generation: generation, Acknowledged: collected, Durable: collected >= quorum}

		case read := <-store.gets:
			if !store.fan(ctx, replicaOp{Kind: opRead, Generation: generation}) {
				return
			}
			answered, gathered := store.collect(ctx, generation, opRead)
			if !gathered {
				return
			}
			read.Reply <- readResult{Answered: answered, Served: answered >= quorum}
		}
	}
}

// fan sends one operation to every zone.
func (store *Store) fan(ctx context.Context, op replicaOp) bool {
	for _, inbox := range store.inboxes {
		select {
		case inbox <- op:
		case <-ctx.Done():
			return false
		}
	}
	return true
}

// collect counts acknowledgements of one generation until the quorum is reached,
// the patience runs out, or the context ends.
//
// It never un-replies and never waits past the quorum, which is the invariant
// the fleet document states of a write quorum: the coordinator replies on
// reaching it, and what arrives afterwards is somebody else's problem.
func (store *Store) collect(ctx context.Context, generation uint64, kind opKind) (int, bool) {
	patience := time.NewTimer(store.config.Patience)
	defer patience.Stop()

	counted := 0
	for counted < quorum {
		select {
		case <-ctx.Done():
			return counted, false
		case <-patience.C:
			return counted, true
		case answered := <-store.acks:
			if answered.Generation == generation && answered.Kind == kind {
				counted++
			}
		}
	}
	return counted, true
}

// repair is the anti-entropy sweep: one goroutine that asks every zone what
// generation it is on, and writes the highest into whichever is behind.
//
// It asks on a reply channel it made itself rather than reading a zone's
// counter, because a zone's counter is a local of a goroutine that is not this
// one. A zone that does not answer inside the sweep is simply not repaired this
// time round, which is what "eventually" is made of.
func (store *Store) repair(ctx context.Context) {
	sweeps := time.NewTicker(store.config.RepairInterval)
	defer sweeps.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sweeps.C:
		}

		answers := make(chan generationReport, zoneCount)
		if !store.fan(ctx, replicaOp{Kind: opProbe, Reply: answers}) {
			return
		}

		// One patience for the whole sweep, sized to a slow zone rather than to
		// a fast one: the zone this exists to repair is the zone that takes
		// longest to answer, so a sweep paced to the fast ones would never see
		// the one it is for.
		reported := map[int]uint64{}
		deadline := time.NewTimer(store.sweepPatience())
		gathering := true
		for gathering && len(reported) < zoneCount {
			select {
			case <-ctx.Done():
				deadline.Stop()
				return
			case <-deadline.C:
				gathering = false
			case answer := <-answers:
				reported[answer.Zone] = answer.Generation
			}
		}
		deadline.Stop()

		// A sweep that heard from nobody has learned nothing, and says nothing.
		// Reporting every zone behind because the sweep was too short would be
		// the repairer describing its own patience rather than the store.
		if len(reported) == 0 {
			continue
		}

		highest := uint64(0)
		for _, generation := range reported {
			if generation > highest {
				highest = generation
			}
		}

		// A zone that did not answer inside the sweep is behind by the only
		// definition this goroutine has: it could not say otherwise. It gets
		// the repair anyway, and its own rule refuses a generation that is not
		// greater than the one it already has — so a repair sent to a zone that
		// was merely slow costs nothing and changes nothing.
		behind := 0
		for zone := range zoneCount {
			generation, answered := reported[zone]
			if answered && generation >= highest {
				continue
			}
			behind++
			select {
			case store.inboxes[zone] <- replicaOp{Kind: opRepair, Generation: highest}:
			case <-ctx.Done():
				return
			}
		}

		select {
		case store.reports <- storeReport{Kind: reportRepair, Lagging: behind}:
		case <-ctx.Done():
			return
		}
	}
}

// sweepPatience is how long the repairer waits for every zone to answer a probe.
func (store *Store) sweepPatience() time.Duration {
	slowest := store.config.zoneDelay(store.config.SlowZone) + store.config.DelaySpread
	return 2 * slowest
}

// observe is the published view: one goroutine owning it, and the only place the
// coordinator's half and the repairer's half are ever seen together.
func (store *Store) observe(ctx context.Context) {
	view := StoreView{StorageClass: store.config.StorageClass}
	sequence := uint64(0)

	for {
		select {
		case <-ctx.Done():
			return
		case incoming := <-store.reports:
			view = foldReport(view, incoming)
			sequence++
			view.Sequence = sequence
			if !store.views.Publish(ctx, view) {
				return
			}
		}
	}
}
