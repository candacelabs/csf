package main

import (
	"context"
	"fmt"
	"time"

	"github.com/candacelabs/csf/examples/widget/candaws/blobfish"
	"github.com/candacelabs/csf/examples/widget/candaws/coldstart"
	"github.com/candacelabs/csf/examples/widget/candaws/dashbored"
	"github.com/candacelabs/csf/examples/widget/candaws/queuecumber"
	"github.com/candacelabs/csf/examples/widget/candaws/yakshave"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
)

// The range -pace is accepted in, and refused outside rather than passed on.
//
// -pace multiplies every interval in the fleet at once, so a value outside this
// range does not produce a faster or slower fleet — it produces one whose
// intervals have stopped standing in the ratios their engines validate. At zero
// and below, [scale]'s floor turns every interval into the same nanosecond and
// Yakshave reports "the stage duration does not fit the cadence: 1ns across 4
// stages against a 1ns cadence": a configuration error wearing an engine's name
// for a fault the flag could have named. Naming it here is the whole point, and
// it is what `raftdemo` already does for `-nodes`.
//
// The bounds are where the fleet's own defaults run out. Below the lower one,
// Blobfish's 60 ms delay spread — the shortest interval anything here scales —
// falls under 60 µs, and truncation to whole nanoseconds starts to be a
// noticeable fraction of the intervals the engines compare against each other.
// Above the upper one, Coldstart's five-second call patience passes an hour and
// a half, which is not a demo. The engines' own refusals stay exactly where they
// are: they are the backstop for a per-service Config nobody paced, and this is
// the guard for the one knob that paces all five.
const (
	minimumPace = 0.001
	maximumPace = 1000
)

// running is one engine in the fleet: five of these are the whole service line.
type running struct {
	pipeline  *yakshave.Pipeline
	broker    *queuecumber.Broker
	store     *blobfish.Store
	functions *coldstart.Runtime
	metrics   *dashbored.Telemetry
}

// build validates every engine's configuration before starting any of them, so a
// fleet that exists is one where all five can actually do their job.
//
// The seed is offset per service rather than shared, so two engines in one
// process never draw from streams that move in step — which would make the queue
// drop exactly when the pipeline fails, forever, and look like a correlation
// somebody had designed.
func build(seed int64, pace float64, trouble float64) (*running, error) {
	if pace < minimumPace || pace > maximumPace {
		return nil, fmt.Errorf(
			"-pace is out of range: %v, which is not in [%v, %v]",
			pace, minimumPace, maximumPace)
	}
	// A negative -trouble is not a fainter fleet; it is not a probability at
	// all. It becomes zero here rather than at each of the places it is used,
	// so "nothing goes wrong anywhere" is one fleet however it was asked for —
	// the branches below test the value the fleet actually runs at, and the
	// banner prints the same one.
	trouble = effectiveTrouble(trouble)

	pipelineConfig := yakshave.DefaultConfig()
	pipelineConfig.Seed = seed
	pipelineConfig.Cadence = scale(pipelineConfig.Cadence, pace)
	pipelineConfig.StageDuration = scale(pipelineConfig.StageDuration, pace)
	pipelineConfig.FailureRate = clamp(pipelineConfig.FailureRate * trouble)

	brokerConfig := queuecumber.DefaultConfig()
	brokerConfig.Seed = seed + 1
	brokerConfig.Cadence = scale(brokerConfig.Cadence, pace)
	brokerConfig.WorkDuration = scale(brokerConfig.WorkDuration, pace)
	brokerConfig.SweepInterval = scale(brokerConfig.SweepInterval, pace)
	brokerConfig.DropRate = clamp(brokerConfig.DropRate * trouble)

	storeConfig := blobfish.DefaultConfig()
	storeConfig.Seed = seed + 2
	storeConfig.Cadence = scale(storeConfig.Cadence, pace)
	storeConfig.ReplicaDelay = scale(storeConfig.ReplicaDelay, pace)
	storeConfig.DelaySpread = scale(storeConfig.DelaySpread, pace)
	storeConfig.Patience = scale(storeConfig.Patience, pace)
	storeConfig.RepairInterval = scale(storeConfig.RepairInterval, pace)
	if trouble == 0 {
		// Nothing goes wrong anywhere, and for a store that means every zone
		// answers rather than one being eventually consistent.
		storeConfig.SlowZone = -1
	}

	functionsConfig := coldstart.DefaultConfig()
	functionsConfig.ArrivalInterval = scale(functionsConfig.ArrivalInterval, pace)
	functionsConfig.StartupBudget = scale(functionsConfig.StartupBudget, pace)
	functionsConfig.WorkDuration = scale(functionsConfig.WorkDuration, pace)
	functionsConfig.ReapInterval = scale(functionsConfig.ReapInterval, pace)
	functionsConfig.CallPatience = scale(functionsConfig.CallPatience, pace)

	metricsConfig := dashbored.DefaultConfig()
	metricsConfig.Seed = seed + 4
	metricsConfig.ScrapeInterval = scale(metricsConfig.ScrapeInterval, pace)
	metricsConfig.FlushInterval = scale(metricsConfig.FlushInterval, pace)
	if trouble > 0 {
		// A higher trouble setting is a lower threshold, because trouble is a
		// probability everywhere else and a metrics service breaches more often
		// when the bar is lower.
		metricsConfig.BreachThreshold = clampAbove(metricsConfig.BreachThreshold / trouble)
	}

	fleet := &running{}
	var buildError error
	if fleet.pipeline, buildError = yakshave.NewPipeline(pipelineConfig); buildError != nil {
		return nil, buildError
	}
	if fleet.broker, buildError = queuecumber.NewBroker(brokerConfig); buildError != nil {
		return nil, buildError
	}
	if fleet.store, buildError = blobfish.NewStore(storeConfig); buildError != nil {
		return nil, buildError
	}
	if fleet.functions, buildError = coldstart.NewRuntime(functionsConfig); buildError != nil {
		return nil, buildError
	}
	if fleet.metrics, buildError = dashbored.NewTelemetry(metricsConfig); buildError != nil {
		return nil, buildError
	}
	return fleet, nil
}

// start runs every engine against one context and returns a channel carrying the
// first error any of them reported.
func (fleet *running) start(ctx context.Context) <-chan error {
	stopped := make(chan error, 5)
	go func() { stopped <- fleet.pipeline.Run(ctx) }()
	go func() { stopped <- fleet.broker.Run(ctx) }()
	go func() { stopped <- fleet.store.Run(ctx) }()
	go func() { stopped <- fleet.functions.Run(ctx) }()
	go func() { stopped <- fleet.metrics.Run(ctx) }()
	return stopped
}

// The eight effects this host owns: six subscriptions resolving the five
// widgets' declared streams, and two commands. Yakshave declares two streams,
// because its two tickers are two.
//
// Each is a constructor returning a concrete live.Effect[live.AnonymousIdentity], and the source it
// stamps becomes the origin "effect:candaws.<name>" on every patch that effect
// causes. There is no host executor: an effect performs itself.

// streamEffect is the one shape all six subscriptions have — a source name and
// an engine to forward from — written once because six copies of one closure is
// six chances for one of them to name the wrong event.
func streamEffect[V any](
	source string,
	subscribe func(ctx context.Context) (<-chan V, error),
	event string,
	fields func(view V) map[string]string,
) live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: source,
		Run: func(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			return forward(ctx, subscribe, emit, event, fields)
		},
	}
}

// commandEffect is the shape both commands have: a source name and one call
// into an engine.
func commandEffect(source string, invoke func(ctx context.Context) error) live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: source,
		Run: func(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			return invoke(ctx)
		},
	}
}

// sources is what every session subscribes to. One engine per service, one
// subscription per session, which is the right way round: every browser watching
// is watching the same five services rather than five private copies.
func (fleet *running) sources(ctx context.Context, session live.Session[live.AnonymousIdentity]) ([]live.Effect[live.AnonymousIdentity], error) {
	return []live.Effect[live.AnonymousIdentity]{
		streamEffect("candaws.yakshave_runs", fleet.pipeline.Runs,
			yakshave.YakshaveEventRunAdvance, yakshave.RunFields),
		streamEffect("candaws.yakshave_quota", fleet.pipeline.Quota,
			yakshave.YakshaveEventQuotaUpdate, yakshave.QuotaFields),
		streamEffect("candaws.queuecumber_broker", fleet.broker.Watch,
			queuecumber.QueuecumberEventBrokerReport, queuecumber.ReportFields),
		streamEffect("candaws.blobfish_replicas", fleet.store.Watch,
			blobfish.BlobfishEventReplicaReport, blobfish.ReportFields),
		streamEffect("candaws.coldstart_pool", fleet.functions.Watch,
			coldstart.ColdstartEventPoolReport, coldstart.ReportFields),
		streamEffect("candaws.dashbored_scrapes", fleet.metrics.Watch,
			dashbored.DashboredEventScrapeReport, dashbored.ReportFields),
	}, nil
}

// commands wraps the registry's reducer so that the two events which change no
// widget state reach an engine.
//
// The registry never lets a host see a widget's state, and this does not: it
// reads the event's name, hands the transition straight through, and appends an
// effect of its own. A command is a declaration that a viewer can ask for
// something; what it means is exactly this function.
func (fleet *running) commands(inner live.Reducer[widget.HostState, live.AnonymousIdentity]) live.Reducer[widget.HostState, live.AnonymousIdentity] {
	return func(state widget.HostState, event live.Event) (widget.HostState, []live.Effect[live.AnonymousIdentity]) {
		next, effects := inner(state, event)
		switch event.Name {
		case queuecumber.QueuecumberEventRedriveDeadLetters:
			effects = append(effects, commandEffect("candaws.queuecumber_redrive", fleet.broker.Redrive))
		case coldstart.ColdstartEventPrewarm:
			effects = append(effects, commandEffect("candaws.coldstart_prewarm", fleet.functions.Prewarm))
		}
		return next, effects
	}
}

// forward is the whole of the seam between an engine and a card, once: subscribe,
// take views, turn each one into the event its document declared, emit.
//
// It is generic over the view type because there is no reason for it not to be —
// each engine's view is its own type and every one of them is known at the call
// site. Nothing in it decides what a card draws, holds any state, or would look
// different if the engine it subscribed to were a process on another machine.
func forward[V any](
	ctx context.Context,
	subscribe func(ctx context.Context) (<-chan V, error),
	emit live.Emitter,
	event string,
	fields func(view V) map[string]string,
) error {
	views, subscribeError := subscribe(ctx)
	if subscribeError != nil {
		return subscribeError
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case view, open := <-views:
			if !open {
				// The engine stopped. The session outlives it and keeps
				// rendering the last view it had, which is what a stream ending
				// looks like from a browser.
				return nil
			}
			// Event.At is left zero: the actor boundary stamps it, and a value
			// set here is rejected rather than silently replaced.
			if emitError := emit(live.Event{
				Name: event, Fields: live.NewFields(fields(view)),
			}); emitError != nil {
				// The session is saturated or closing. Returning the error is
				// how this effect learns about backpressure rather than having
				// its event vanish.
				return emitError
			}
		}
	}
}

// scale multiplies one interval by the fleet's pace, never down to zero — every
// engine refuses a non-positive interval, and a flag that produced one would turn
// a pacing knob into a configuration error.
//
// The floor is now unreachable through the flag, because [build] refuses a pace
// outside [minimumPace, maximumPace] before any of this runs. It stays because
// it is the honest thing for a helper to do with an interval it cannot scale,
// and because "unreachable" is a property of one caller rather than of this
// function.
func scale(interval time.Duration, pace float64) time.Duration {
	scaled := time.Duration(float64(interval) * pace)
	if scaled <= 0 {
		return time.Nanosecond
	}
	return scaled
}

// effectiveTrouble is what -trouble means once it is inside the range a
// probability multiplier has: negative is zero, and zero is the fleet where
// nothing goes wrong.
//
// It is a function rather than a clamp written twice because [build] and the
// banner have to agree: a banner that printed the flag while the fleet ran on
// something else would be reporting a number nothing in the process used.
func effectiveTrouble(trouble float64) float64 {
	if trouble < 0 {
		return 0
	}
	return trouble
}

// clamp holds a probability inside [0, 1].
func clamp(probability float64) float64 {
	switch {
	case probability < 0:
		return 0
	case probability > 1:
		return 1
	}
	return probability
}

// clampAbove holds a threshold inside (0, 1], which is the range an observation
// can actually reach.
func clampAbove(threshold float64) float64 {
	switch {
	case threshold <= 0:
		return 0.000001
	case threshold > 1:
		return 1
	}
	return threshold
}
