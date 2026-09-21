package coldstart

import (
	"context"
	"time"
)

// A function instance at runtime is a goroutine that exists only while somebody
// is calling it. Its temperature is a local of that goroutine, and a frozen
// instance is not an instance in a cold state — it is a goroutine that has
// returned. Goroutine lifecycle is the product, made mechanical rather than
// illustrated.

// temperature is how ready an instance is to serve.
type temperature uint8

const (
	// temperatureCold is an instance that does not exist yet.
	temperatureCold temperature = iota

	// temperatureWarming is a goroutine paying the start-up budget.
	temperatureWarming

	// temperatureWarm is an instance that can serve now.
	temperatureWarm

	// temperatureIdle is one that has served and is waiting to be called
	// again, or reaped.
	temperatureIdle
)

// String names each temperature, so a failure about one says something.
func (heat temperature) String() string {
	if name, named := temperatureNames[heat]; named {
		return name
	}
	return "temperature " + string(rune('0'+int(heat)))
}

// temperatureNames is the vocabulary, and the only place one is spelled.
var temperatureNames = map[temperature]string{
	temperatureCold:    "cold",
	temperatureWarming: "warming",
	temperatureWarm:    "warm",
	temperatureIdle:    "idle",
}

// lifecycleEvent is one thing that can happen to an instance.
type lifecycleEvent uint8

const (
	// eventSpawn is the dispatcher starting a goroutine for it.
	eventSpawn lifecycleEvent = iota

	// eventWarmed is the start-up budget having been paid.
	eventWarmed

	// eventInvoked is an invocation arriving.
	eventInvoked

	// eventIdled is one having been answered.
	eventIdled

	// eventFrozen is the reaper closing the instance's channel, after which
	// its goroutine returns and there is no instance.
	eventFrozen
)

// temperatureRule is what one lifecycle event does to a temperature: a pure
// function from the current temperature to the next one and whether the move is
// legal at all.
//
// The five events are a family of one signature, so they are a registry rather
// than a switch: "temperature moves cold to warming to warm to idle and never
// skips warming" is then a property somebody can enumerate rather than one
// stated in a comment above a switch nobody re-reads.
type temperatureRule func(current temperature) (temperature, bool)

// temperatureRules is the table, one entry per event.
var temperatureRules = map[lifecycleEvent]temperatureRule{
	eventSpawn: func(current temperature) (temperature, bool) {
		return temperatureWarming, current == temperatureCold
	},
	eventWarmed: func(current temperature) (temperature, bool) {
		// The one rule that makes the ladder a ladder: an instance reaches warm
		// only from warming, so nothing can skip the start-up budget.
		return temperatureWarm, current == temperatureWarming
	},
	eventInvoked: func(current temperature) (temperature, bool) {
		return temperatureWarm, current == temperatureWarm || current == temperatureIdle
	},
	eventIdled: func(current temperature) (temperature, bool) {
		return temperatureIdle, current == temperatureWarm
	},
	eventFrozen: func(current temperature) (temperature, bool) {
		return temperatureCold, current != temperatureCold
	},
}

// advance applies one lifecycle event, and reports whether it was legal.
//
// An illegal move leaves the temperature where it was. That is deliberate: an
// instance that could be dragged sideways by a message arriving out of order
// would be an instance whose reported temperature is a rumour.
func advance(current temperature, happened lifecycleEvent) (temperature, bool) {
	rule, known := temperatureRules[happened]
	if !known {
		return current, false
	}
	next, legal := rule(current)
	if !legal {
		return current, false
	}
	return next, true
}

// invocation is one call, carrying the channel its answer comes back on.
//
// That channel is what makes the round trip a round trip: the caller blocks in a
// select on reply-or-context and nothing shared carries the answer back. Exactly
// one value is ever sent on it and it is closed afterwards, so a caller whose
// invocation was dropped learns that from the close rather than by waiting
// forever.
type invocation struct {
	// Sequence is the call's identity, monotonic and never reused.
	Sequence uint64

	// Reply is where the answer arrives, exactly once, before the channel is
	// closed.
	Reply chan reply
}

// reply is one invocation's answer.
type reply struct {
	// Sequence names the invocation this answers.
	Sequence uint64

	// Instance is which instance served it.
	Instance int

	// ColdStartMillis is what the platform spent getting ready before this
	// instance could serve anything. It is billed.
	ColdStartMillis int
}

// stateChange is an instance telling the dispatcher what it has become. The
// dispatcher never asks: asking would mean reading a temperature that is a local
// of a goroutine that is not the dispatcher.
type stateChange struct {
	// ID is the reporting instance.
	ID int

	// Temperature is what it has become.
	Temperature temperature
}

// instance is one function instance: one goroutine, one inbound channel, one
// temperature nothing else can name.
type instance struct {
	// id is this instance's index in the dispatcher's table.
	id int

	// startup is the budget this instance pays before it can serve anything.
	startup time.Duration

	// duration is how long one invocation takes once it is warm.
	duration time.Duration

	// invocations is the dispatcher's channel into this instance. The
	// dispatcher is its only writer and its only closer, and closing it is how
	// this goroutine is reaped.
	invocations <-chan invocation

	// states is where this instance reports what it has become.
	states chan<- stateChange
}

// run is the instance's whole life: pay the start-up, say so, serve until the
// dispatcher closes the channel, and return.
//
// Returning is what "frozen" means. There is no frozen state to be in.
func (sandbox *instance) run(ctx context.Context) {
	heat, _ := advance(temperatureCold, eventSpawn)

	select {
	case <-ctx.Done():
		return
	case <-time.After(sandbox.startup):
	}

	heat, _ = advance(heat, eventWarmed)
	if !sandbox.tell(ctx, heat) {
		return
	}

	for {
		var call invocation
		select {
		case <-ctx.Done():
			return
		case taken, open := <-sandbox.invocations:
			if !open {
				return
			}
			call = taken
		}

		heat, _ = advance(heat, eventInvoked)
		select {
		case <-ctx.Done():
			return
		case <-time.After(sandbox.duration):
		}

		// Exactly one value, then the close. The channel is this invocation's
		// own and is read by exactly one caller, so neither can block.
		call.Reply <- reply{
			Sequence:        call.Sequence,
			Instance:        sandbox.id,
			ColdStartMillis: int(sandbox.startup.Milliseconds()),
		}
		close(call.Reply)

		heat, _ = advance(heat, eventIdled)
		if !sandbox.tell(ctx, heat) {
			return
		}
	}
}

// tell reports this instance's temperature, and answers false when the context
// ended mid-send.
func (sandbox *instance) tell(ctx context.Context, heat temperature) bool {
	select {
	case sandbox.states <- stateChange{ID: sandbox.id, Temperature: heat}:
		return true
	case <-ctx.Done():
		return false
	}
}
