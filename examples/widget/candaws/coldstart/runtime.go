package coldstart

import (
	"context"
	"sync"
	"time"

	"github.com/candacelabs/csf/examples/widget/candaws/fleet"
)

// noticeDepth is how many arrivals and state changes are in flight before a
// sender waits.
const noticeDepth = 64

// Runtime is one Coldstart function runtime running in one process.
//
// It holds channels and nothing else. The routing table, every instance's
// temperature, the backlog and the published view are locals of goroutines
// [Runtime.Run] starts — and the instance goroutines are not started by Run at
// all: they are started by the dispatcher when somebody calls, and they return
// when nobody does.
type Runtime struct {
	// config is the shape and pace NewRuntime validated.
	config Config

	// arrivals is where an invocation reaches the dispatcher.
	arrivals chan invocation

	// states is where an instance reports what it has become.
	states chan stateChange

	// prewarms is the command the card's control emits. The widget changes no
	// state for it; what warming an instance means is the host's, and this is
	// where the host says it.
	prewarms chan struct{}

	// views is the stream the card's declared source resolves to.
	views *fleet.Feed[PoolView]

	// start is a token taken by the first Run.
	start fleet.Once
}

// NewRuntime builds a runtime and starts nothing.
func NewRuntime(config Config) (*Runtime, error) {
	if validationError := config.Validate(); validationError != nil {
		return nil, validationError
	}
	return &Runtime{
		config:   config,
		arrivals: make(chan invocation, noticeDepth),
		states:   make(chan stateChange, noticeDepth),
		prewarms: make(chan struct{}, 1),
		views:    fleet.NewFeed[PoolView](8),
		start:    fleet.NewOnce(),
	}, nil
}

// Config is the configuration this runtime was built from.
func (runtime *Runtime) Config() Config { return runtime.config }

// Run starts the dispatcher, the caller and the feed, and returns when the
// context ends and all of them — including every instance the dispatcher
// spawned — have stopped.
func (runtime *Runtime) Run(ctx context.Context) error {
	if !runtime.start.Take() {
		return fleet.ErrAlreadyRunning
	}

	var crew fleet.Crew
	crew.Go(ctx, runtime.views.Run)
	crew.Go(ctx, runtime.dispatch)
	crew.Go(ctx, runtime.call)

	crew.Wait()
	return nil
}

// Watch is the pool stream the card's declared source resolves to.
func (runtime *Runtime) Watch(ctx context.Context) (<-chan PoolView, error) {
	return runtime.views.Subscribe(ctx)
}

// Prewarm asks the dispatcher to keep one instance permanently warm.
//
// It is what the card's control emits, and it is a method rather than widget
// state because the widget changes nothing when that button is pressed. The
// control the card actually wants is a number, and the document records why it
// cannot have one.
func (runtime *Runtime) Prewarm(ctx context.Context) error {
	select {
	case runtime.prewarms <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// call is the caller: one goroutine minting invocations on a ticker, and one
// short-lived goroutine per invocation waiting on that invocation's own reply
// channel.
//
// The wait is a goroutine of its own rather than the ticker loop blocking,
// because a caller that waited for each answer before making the next call could
// never produce a backlog — and the backlog is half of what this product is
// about. It reads nothing shared. An invocation that is answered returns a value
// on the channel that travelled with it; one that is dropped returns a closed
// channel; one that is neither runs out of patience. All three are that caller's
// own business and none of them is anybody else's memory.
func (runtime *Runtime) call(ctx context.Context) {
	var waiting sync.WaitGroup
	defer waiting.Wait()

	arrivals := time.NewTicker(runtime.config.ArrivalInterval)
	defer arrivals.Stop()

	sequence := uint64(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-arrivals.C:
		}

		sequence++
		call := invocation{Sequence: sequence, Reply: make(chan reply, 1)}
		select {
		case runtime.arrivals <- call:
		case <-ctx.Done():
			return
		}

		waiting.Add(1)
		go func(answered <-chan reply) {
			defer waiting.Done()
			patience := time.NewTimer(runtime.config.CallPatience)
			defer patience.Stop()
			select {
			case <-answered:
			case <-patience.C:
			case <-ctx.Done():
			}
		}(call.Reply)
	}
}

// dispatch is the dispatcher: one goroutine owning the routing table, the
// backlog, the warm floor and every instance goroutine it has started.
//
// On a miss it spawns a goroutine, which pays the start-up budget, reports warm,
// and serves. The reaper closes an idle instance's channel and that goroutine
// returns. Nothing in this engine holds an instance that is not being called,
// which is what "scales to zero" means when the pool is made of goroutines.
func (runtime *Runtime) dispatch(ctx context.Context) {
	// The instance goroutines are this goroutine's own children: it starts them
	// and waits for them, so a runtime whose Run has returned is a runtime with
	// no instance still running.
	var spawned sync.WaitGroup
	defer spawned.Wait()

	table := map[int]handle{}
	var backlog []invocation
	floor := runtime.config.WarmFloor
	next := 0
	sequence := uint64(0)
	served := uint64(0)
	dropped := uint64(0)

	sweeps := time.NewTicker(runtime.config.ReapInterval)
	defer sweeps.Stop()

	// freeze closes an instance's channel, which is the only way an instance
	// goroutine ever ends other than the context.
	freeze := func(id int) {
		live, known := table[id]
		if !known {
			return
		}
		close(live.invocations)
		delete(table, id)
	}
	defer func() {
		for id := range table {
			freeze(id)
		}
	}()

	publish := func() bool {
		sequence++
		return runtime.views.Publish(ctx, PoolView{
			Sequence:        sequence,
			RuntimeName:     runtime.config.RuntimeName,
			WarmInstances:   countWarm(table),
			LiveInstances:   len(table),
			Queued:          len(backlog),
			ColdStartMillis: int(runtime.config.StartupBudget.Milliseconds()),
			DispatcherUp:    len(backlog) < runtime.config.BacklogCeiling,
			Draining:        countIdle(table) > 0,
			WarmFloor:       floor,
			Served:          served,
			Dropped:         dropped,
		})
	}

	// spawn starts one instance goroutine and puts it in the table cold.
	spawn := func() {
		if len(table) >= runtime.config.MaxInstances {
			return
		}
		next++
		id := next
		invocations := make(chan invocation, 1)
		table[id] = handle{invocations: invocations, heat: temperatureWarming}
		sandbox := &instance{
			id:          id,
			startup:     runtime.config.StartupBudget,
			duration:    runtime.config.WorkDuration,
			invocations: invocations,
			states:      runtime.states,
		}
		spawned.Add(1)
		go func() {
			defer spawned.Done()
			sandbox.run(ctx)
		}()
	}

	// drain hands as much of the backlog as there are free instances to take it.
	drain := func() {
		for len(backlog) > 0 {
			id, free := firstFree(table)
			if !free {
				return
			}
			live := table[id]
			live.busy = true
			live.idleSweeps = 0
			table[id] = live
			live.invocations <- backlog[0]
			backlog = backlog[1:]
			served++
		}
	}

	if !publish() {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return

		case call := <-runtime.arrivals:
			backlog = append(backlog, call)
			if _, free := firstFree(table); !free {
				spawn()
			}
			drain()
			// Over the ceiling the dispatcher drops the oldest waiting call
			// rather than growing without bound, and closes its reply channel
			// so the caller learns it rather than waiting out its patience.
			for len(backlog) > runtime.config.BacklogCeiling {
				close(backlog[0].Reply)
				backlog = backlog[1:]
				dropped++
			}
			if !publish() {
				return
			}

		case reported := <-runtime.states:
			live, known := table[reported.ID]
			if !known {
				continue
			}
			live.heat = reported.Temperature
			live.busy = false
			table[reported.ID] = live
			drain()
			if !publish() {
				return
			}

		case <-runtime.prewarms:
			// The premium tier, and the pricing page calls it Serverful.
			if floor < runtime.config.MaxInstances {
				floor++
			}
			if countWarm(table) < floor {
				spawn()
			}
			if !publish() {
				return
			}

		case <-sweeps.C:
			for id, live := range table {
				if live.busy || live.heat == temperatureWarming {
					continue
				}
				live.idleSweeps++
				table[id] = live
			}
			for id, live := range table {
				if live.idleSweeps < runtime.config.IdleSweeps || len(table) <= floor {
					continue
				}
				freeze(id)
			}
			if countWarm(table) < floor {
				spawn()
			}
			if !publish() {
				return
			}
		}
	}
}

// handle is the dispatcher's own record of one instance. It is a value in the
// dispatcher's map and is copied in and out of it, so nothing here is a
// reference anybody else could follow.
type handle struct {
	// invocations is the channel into the instance. The dispatcher is its only
	// writer and its only closer.
	invocations chan invocation

	// heat is what the instance last reported. It is the dispatcher's copy of
	// somebody else's local rather than the local itself, which is why it can
	// only ever be as fresh as the last report.
	heat temperature

	// busy is whether the dispatcher has handed it an invocation it has not
	// reported finishing.
	busy bool

	// idleSweeps is how many reap sweeps it has sat through doing nothing.
	idleSweeps int
}

// firstFree names an instance that has warmed and is not busy, in index order so
// that two runs of the same sequence route the same way.
func firstFree(table map[int]handle) (int, bool) {
	chosen, found := 0, false
	for id, live := range table {
		if live.busy || live.heat == temperatureWarming || live.heat == temperatureCold {
			continue
		}
		if !found || id < chosen {
			chosen, found = id, true
		}
	}
	return chosen, found
}

// countWarm is how many instances have paid their start-up.
func countWarm(table map[int]handle) int {
	warm := 0
	for _, live := range table {
		if live.heat == temperatureWarm || live.heat == temperatureIdle {
			warm++
		}
	}
	return warm
}

// countIdle is how many instances have served and are waiting to be called again
// or reaped. It is what the card draws as draining.
func countIdle(table map[int]handle) int {
	idle := 0
	for _, live := range table {
		if live.heat == temperatureIdle && !live.busy {
			idle++
		}
	}
	return idle
}
