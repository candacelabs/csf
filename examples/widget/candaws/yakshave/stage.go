package yakshave

import (
	"context"
	"math/rand/v2"
	"time"
)

// A stage at runtime is a goroutine owning one inbound channel and its own last
// result. An artifact is a value moving down a chain of channels, owned by
// exactly one stage at a time — which is why nothing in this package locks
// anything: a pipeline is a hand-off, and a hand-off is a channel.

// stageID names one stage of the chain, and its order in it.
type stageID int

const (
	stageCheckout stageID = iota
	stageBuild
	stageTest
	stageDeploy

	// stageCount is the length of the chain. It is derived from the constants
	// above by being the one after the last, so a fifth stage moves it.
	stageCount = int(stageDeploy) + 1
)

// idleStage is what the observer reports when no stage holds an artifact.
const idleStage = "idle"

// String is the name the card interpolates into its stage line. A stage outside
// the chain prints as a number rather than as a plausible name, so a failure
// naming it says something.
func (id stageID) String() string {
	if name, named := stageNames[id]; named {
		return name
	}
	return "stage " + string(rune('0'+int(id)))
}

// stageNames is the chain's vocabulary, and the only place a stage's name is
// spelled.
var stageNames = map[stageID]string{
	stageCheckout: "checkout",
	stageBuild:    "build",
	stageTest:     "test",
	stageDeploy:   "deploy",
}

// stageWork is what one stage does to the artifact it was handed: a pure
// function from the artifact, one draw of that stage's own random stream and the
// configured failure rate, to whether the stage cleared.
//
// It is a function value rather than a method because the four stages are a
// family of one signature, and a family of one signature is a registry: a fifth
// stage is an entry in [stageWorks] rather than a case in a switch somebody has
// to remember to extend.
type stageWork func(item artifact, draw float64, rate float64) bool

// stageWorks is the chain, one entry per stage.
//
// Each stage weights the configured failure rate by how much of a build's
// trouble it is actually responsible for, which is the joke made mechanical:
// the build stage fails three times as often as the checkout, and the test stage
// is kinder to a retry than to a first attempt, which is the whole excuse for
// retrying a red build automatically.
//
// Every weight is at least one, and that is a property rather than a
// coincidence: a failure rate of 1 must be a pipeline that never ships, so a
// draw — which is in [0, 1) — can never reach a threshold of 1. A rate of 0 is
// the other end, and every stage clears.
var stageWorks = map[stageID]stageWork{
	stageCheckout: func(item artifact, draw float64, rate float64) bool {
		return draw >= rate
	},
	stageBuild: func(item artifact, draw float64, rate float64) bool {
		return draw >= rate*3
	},
	stageTest: func(item artifact, draw float64, rate float64) bool {
		if item.Attempt > 0 {
			return draw >= rate
		}
		return draw >= rate*2
	},
	stageDeploy: func(item artifact, draw float64, rate float64) bool {
		return draw >= rate
	},
}

// artifact is one run's value as it moves down the chain.
//
// It is copied on every hand-off, which is what makes "owned by exactly one
// goroutine at every instant" a property of the type rather than a rule somebody
// follows: a stage that kept its copy would be keeping a copy.
type artifact struct {
	// Run is the run's identity: monotonic, never reused.
	Run uint64

	// Attempt is which try this is, from zero.
	Attempt int

	// Cleared records the stages this artifact has passed on this attempt.
	Cleared [stageCount]bool
}

// stageReport is one stage telling the observer what it is doing. A stage is
// never asked: asking would mean reading a stage's locals from another
// goroutine, which is the thing this package does not do.
type stageReport struct {
	// Run and Stage name the artifact and the stage reporting on it.
	Run   uint64
	Stage stageID

	// Attempt is the artifact's retry count, which is what the card's retry
	// stat reads.
	Attempt int

	// Busy is true on the report a stage sends when it takes an artifact and
	// false on the one it sends when it has finished with it.
	Busy bool

	// Cleared is meaningful only on a finished report, and is this stage's own
	// last result.
	Cleared bool
}

// failure is a stage handing an artifact back to the head for another go.
type failure struct {
	// Item is the artifact as it was when the stage failed it.
	Item artifact

	// Stage is where it failed, which is what the head charges the retry to.
	Stage stageID
}

// stage is one link of the chain: one goroutine, one inbound channel, one
// outbound channel, and two locals nothing else can see.
type stage struct {
	// id is this stage's place in the chain.
	id stageID

	// duration is how long this stage takes to do its work. Every minute of it
	// is billed, including the ones spent waiting.
	duration time.Duration

	// rate is the configured failure rate this stage weights.
	rate float64

	// in is where artifacts arrive. Closing it is how the chain shuts down.
	in <-chan artifact

	// out is the next stage's inbox. This goroutine is its only writer and
	// closes it exactly once, only after in closed.
	out chan<- artifact

	// failures is the head's inbox for artifacts that did not clear.
	failures chan<- failure

	// reports is the observer's feed.
	reports chan<- stageReport

	// jitter is this stage's own random stream, seeded from the pipeline's seed
	// and the stage's index.
	jitter *rand.Rand

	// work is this stage's own rule, taken from the registry once.
	work stageWork
}

// run is the stage's whole life: take one artifact, work it, forward it or fail
// it, repeat until the inbound channel closes or the context ends.
//
// The deferred close is the demonstration. A single close at the head drains the
// chain in order — every stage finishes what it holds, closes its own outbound
// channel, and the next stage sees that and does the same — so shutting a
// four-stage pipeline down is one channel operation and no coordination at all.
func (worker *stage) run(ctx context.Context) {
	defer close(worker.out)

	// busy and lastCleared are this goroutine's own. Nothing in the package can
	// name them, which is why there is nothing for a lock to protect.
	busy := false
	lastCleared := true

	for {
		var item artifact
		select {
		case <-ctx.Done():
			return
		case taken, open := <-worker.in:
			if !open {
				return
			}
			item = taken
		}

		busy = true
		if !worker.tell(ctx, stageReport{
			Run: item.Run, Stage: worker.id, Attempt: item.Attempt, Busy: busy,
		}) {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(worker.duration):
		}

		lastCleared = worker.work(item, worker.jitter.Float64(), worker.rate)
		busy = false
		if !worker.tell(ctx, stageReport{
			Run: item.Run, Stage: worker.id, Attempt: item.Attempt,
			Busy: busy, Cleared: lastCleared,
		}) {
			return
		}

		if !lastCleared {
			select {
			case worker.failures <- failure{Item: item, Stage: worker.id}:
			case <-ctx.Done():
				return
			}
			continue
		}

		item.Cleared[worker.id] = true
		select {
		case worker.out <- item:
		case <-ctx.Done():
			return
		}
	}
}

// tell sends one report, and answers false when the context ended mid-send.
func (worker *stage) tell(ctx context.Context, report stageReport) bool {
	select {
	case worker.reports <- report:
		return true
	case <-ctx.Done():
		return false
	}
}
