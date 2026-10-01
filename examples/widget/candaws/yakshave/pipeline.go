package yakshave

import (
	"context"
	"time"

	"github.com/candacelabs/csf/examples/widget/candaws/fleet"
)

// chainDepth is how many artifacts one link of the chain holds before the stage
// behind it has to wait.
//
// It is small on purpose. A pipeline with a deep queue between its stages is a
// pipeline whose picture stops being a picture of anything: the whole point of
// the chain is that an artifact is in exactly one stage.
const chainDepth = 1

// noticeDepth is how many reports and failures are in flight before a stage
// waits to be heard.
//
// It is generous rather than tuned, and it is what keeps the chain from
// deadlocking against its own head: every stage can deposit a failure and carry
// on, so the head is never the only thing that can unblock the chain while
// itself blocked on it.
const noticeDepth = 64

// Pipeline is one Yakshave pipeline running in one process.
//
// It holds channels and nothing else — no stage state, no quota, no published
// view. Each of those belongs to exactly one goroutine started by
// [Pipeline.Run], and this struct is how the rest of the program reaches them.
type Pipeline struct {
	// config is the pace and luck NewPipeline validated.
	config Config

	// links is the chain: one channel into each stage, plus one the last stage
	// forwards its finished artifacts onto.
	links []chan artifact

	// failures is the head's inbox for artifacts a stage did not clear.
	failures chan failure

	// reports is the observer's feed.
	reports chan stageReport

	// charges is the meter's inbox. A caller asks to spend minutes and is
	// answered on the request's own reply channel, which is the only way
	// anything outside the meter goroutine learns the balance.
	charges chan charge

	// runs and quota are the two streams the Yakshave card declares. They are
	// two because the two tickers behind them are two: one bills, one builds,
	// and only one of them can be the tick.
	runs  *fleet.Feed[RunView]
	quota *fleet.Feed[QuotaView]

	// drain is closed by Drain to shut the chain down from the head rather than
	// by cancelling everything at once.
	drain chan struct{}

	// drained is closed by the collector when the far end of the chain closes,
	// which is the proof that a single close at the head reached it: the last
	// stage closes its outbound channel only after its inbound one closed, and
	// so on back to the head.
	drained chan struct{}

	// draining and start are start tokens: each can be taken exactly once.
	draining fleet.Once
	start    fleet.Once
}

// charge is a request to spend minutes from the quota, answered on its own
// reply channel.
//
// The reply channel travels with the request, which is what makes the meter's
// balance unreachable from anywhere else: a caller does not read the quota, it
// asks and is told.
type charge struct {
	// Minutes is what the caller wants to spend.
	Minutes int

	// Queued marks a charge for time spent waiting rather than building. Both
	// come out of the same budget, which is the joke.
	Queued bool

	// Reply is where the meter answers, exactly once.
	Reply chan bool
}

// NewPipeline builds a pipeline and starts nothing.
//
// Every fault in the configuration is reported here, so a *Pipeline that exists
// is one whose pace can actually ship. The goroutines belong to [Pipeline.Run]
// because they belong to the context Run is given.
func NewPipeline(config Config) (*Pipeline, error) {
	if validationError := config.Validate(); validationError != nil {
		return nil, validationError
	}

	pipeline := &Pipeline{
		config:   config,
		links:    make([]chan artifact, stageCount+1),
		failures: make(chan failure, noticeDepth),
		reports:  make(chan stageReport, noticeDepth),
		charges:  make(chan charge),
		runs:     fleet.NewFeed[RunView](8),
		quota:    fleet.NewFeed[QuotaView](8),
		drain:    make(chan struct{}),
		drained:  make(chan struct{}),
		draining: fleet.NewOnce(),
		start:    fleet.NewOnce(),
	}
	for index := range pipeline.links {
		pipeline.links[index] = make(chan artifact, chainDepth)
	}
	return pipeline, nil
}

// Config is the configuration this pipeline was built from.
func (pipeline *Pipeline) Config() Config { return pipeline.config }

// Run starts every goroutine and returns when the context ends and all of them
// have stopped.
//
// It blocks, so a host runs it in a goroutine of its own and a specification can
// wait on its return to know the pipeline is actually gone rather than merely
// asked to go. Cancellation is the caller's own instruction and is not reported
// back as an error; the only error Run has is being called twice.
func (pipeline *Pipeline) Run(ctx context.Context) error {
	if !pipeline.start.Take() {
		return fleet.ErrAlreadyRunning
	}

	var crew fleet.Crew
	crew.Go(ctx, pipeline.runs.Run)
	crew.Go(ctx, pipeline.quota.Run)
	crew.Go(ctx, pipeline.observe)
	crew.Go(ctx, pipeline.meter)
	crew.Go(ctx, pipeline.dispatch)
	crew.Go(ctx, pipeline.collect)
	for index := range stageCount {
		id := stageID(index)
		worker := &stage{
			id:       id,
			duration: pipeline.config.StageDuration,
			rate:     pipeline.config.FailureRate,
			in:       pipeline.links[index],
			out:      pipeline.links[index+1],
			failures: pipeline.failures,
			reports:  pipeline.reports,
			jitter:   fleet.Jitter(pipeline.config.Seed, uint64(index)),
			work:     stageWorks[id],
		}
		crew.Go(ctx, worker.run)
	}

	crew.Wait()
	return nil
}

// Runs is the stream of run views: one per stage report, which is what makes a
// pulse on the card a hand-off that actually crossed a channel.
func (pipeline *Pipeline) Runs(ctx context.Context) (<-chan RunView, error) {
	return pipeline.runs.Subscribe(ctx)
}

// Quota is the other stream: minutes, billed whether or not anything ran.
func (pipeline *Pipeline) Quota(ctx context.Context) (<-chan QuotaView, error) {
	return pipeline.quota.Subscribe(ctx)
}

// Drain shuts the chain down from the head.
//
// It closes the first stage's inbound channel and nothing else, and everything
// that follows is the chain's own doing: each stage finishes the artifact it
// holds, closes its outbound channel, and the next stage sees that and does the
// same. A four-stage pipeline therefore shuts down in order, with no
// coordination, no shared counter and no second signal — which is the whole
// demonstration and is why it is a method rather than a cancelled context.
//
// A second call is a no-op rather than a panic: the token can be taken once.
func (pipeline *Pipeline) Drain() {
	if pipeline.draining.Take() {
		close(pipeline.drain)
	}
}

// Drained is closed once the far end of the chain has closed.
//
// It is the observable half of [Pipeline.Drain]: the last stage closes its
// outbound channel only after its inbound one closed, and so on back to the
// head, so this channel closing means the whole chain shut down in order from
// one close and no other signal. The rest of the engine — the meter, the
// observer and the two feeds — is not part of the chain and stops with the
// context.
func (pipeline *Pipeline) Drained() <-chan struct{} { return pipeline.drained }

// dispatch is the head: the one goroutine that writes into the chain.
//
// It is the only writer, which is what makes closing the head safe — a channel
// with two writers has no moment at which either may close it. It starts runs on
// a ticker, re-injects retries from the failures channel, and asks the meter
// before doing either.
func (pipeline *Pipeline) dispatch(ctx context.Context) {
	defer close(pipeline.links[0])

	starts := time.NewTicker(pipeline.config.Cadence)
	defer starts.Stop()

	// next is the run counter: monotonic, never reused, and this goroutine's
	// own. It is what makes a run identity an identity.
	next := uint64(0)

	for {
		select {
		case <-ctx.Done():
			return
		case <-pipeline.drain:
			return

		case <-starts.C:
			if !pipeline.spend(ctx, charge{Minutes: 1}) {
				continue
			}
			next++
			if !pipeline.inject(ctx, artifact{Run: next}) {
				return
			}

		case failed := <-pipeline.failures:
			if failed.Item.Attempt >= pipeline.config.RetryCeiling {
				continue
			}
			// A retry is billed as queue time, because that is what it is:
			// minutes the customer pays for that produced nothing.
			if !pipeline.spend(ctx, charge{Minutes: 1, Queued: true}) {
				continue
			}
			retried := artifact{Run: failed.Item.Run, Attempt: failed.Item.Attempt + 1}
			if !pipeline.inject(ctx, retried) {
				return
			}
		}
	}
}

// inject puts one artifact at the head of the chain.
func (pipeline *Pipeline) inject(ctx context.Context, item artifact) bool {
	select {
	case pipeline.links[0] <- item:
		return true
	case <-ctx.Done():
		return false
	}
}

// spend asks the meter for minutes and waits for its answer.
//
// The reply channel is made here and travels with the request, so the answer
// reaches exactly the caller that asked and the balance never leaves the meter.
func (pipeline *Pipeline) spend(ctx context.Context, request charge) bool {
	request.Reply = make(chan bool, 1)
	select {
	case pipeline.charges <- request:
	case <-ctx.Done():
		return false
	}
	select {
	case granted := <-request.Reply:
		return granted
	case <-ctx.Done():
		return false
	}
}

// collect drains the end of the chain.
//
// A finished artifact has nowhere to go and somebody has to take it, or the
// deploy stage blocks on its own outbound channel and the chain stops one
// artifact from the end. That somebody is a goroutine rather than a nil channel
// because a nil channel would make the last stage a special case, and the whole
// claim of this engine is that a stage is a stage.
func (pipeline *Pipeline) collect(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, open := <-pipeline.links[stageCount]:
			if !open {
				close(pipeline.drained)
				return
			}
		}
	}
}

// observe is the run view: one goroutine owning the published view and the
// sequence counter, and nothing else in the process may touch either.
func (pipeline *Pipeline) observe(ctx context.Context) {
	view := RunView{Stage: idleStage}
	sequence := uint64(0)

	for {
		select {
		case <-ctx.Done():
			return
		case incoming := <-pipeline.reports:
			view = advanceView(view, incoming)
			sequence++
			view.Sequence = sequence
			if !pipeline.runs.Publish(ctx, view) {
				return
			}
		}
	}
}

// meter is the quota: one goroutine owning the balance and the queued total.
//
// Nothing reads either. A caller sends a [charge] carrying its own reply channel
// and is told yes or no, which is the difference between a budget and a number
// four goroutines are looking at.
func (pipeline *Pipeline) meter(ctx context.Context) {
	remaining := pipeline.config.QuotaMinutes
	queued := 0
	sequence := uint64(0)

	billing := time.NewTicker(pipeline.config.Cadence)
	defer billing.Stop()

	publish := func() bool {
		sequence++
		return pipeline.quota.Publish(ctx, QuotaView{
			Sequence: sequence, QueueMinutes: queued, QuotaMinutes: remaining,
		})
	}

	// The stream opens with a view rather than with a wait, so a subscriber
	// that arrives before the first minute is billed has something to render.
	if !publish() {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return

		case request := <-pipeline.charges:
			granted := remaining >= request.Minutes
			if granted {
				remaining -= request.Minutes
				if request.Queued {
					queued += request.Minutes
				}
			}
			select {
			case request.Reply <- granted:
			case <-ctx.Done():
				return
			}
			if !publish() {
				return
			}

		case <-billing.C:
			if !publish() {
				return
			}
		}
	}
}
