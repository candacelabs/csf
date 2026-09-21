package dashbored

import (
	"context"
	"slices"
	"time"

	"github.com/candacelabs/csf/examples/widget/candaws/fleet"
)

// noticeDepth is how many samples and notices are in flight before a sender
// waits. A fan-in's shared channel is the one place backpressure is real, so it
// is generous rather than tuned.
const noticeDepth = 256

// Telemetry is one Dashbored pipeline running in one process: the fleet's own
// console, and a fan-in.
//
// It holds channels and nothing else. Every reservoir, the histogram buckets,
// the firing set and the published view are locals of goroutines [Telemetry.Run]
// starts.
type Telemetry struct {
	// config is the shape and pace NewTelemetry validated.
	config Config

	// ingest is the one shared channel every collector sends on.
	ingest chan sample

	// queries is where a caller asks for a rolled-up answer, carrying its own
	// reply channel. A query is answered from a copy of the buckets, never from
	// the buckets themselves.
	queries chan queryRequest

	// alerts is where a breach leaves the aggregator for the alerter.
	alerts chan breach

	// departures is where a collector says, exactly once, that it has gone.
	// The aggregator counts them down; nothing anywhere holds "how many are
	// left" except the aggregator's own local.
	departures chan int

	// reports is the observer's feed: the aggregator's half and the alerter's
	// half of one published view.
	reports chan telemetryReport

	// retire is closed by Retire to ask every collector to leave.
	retire chan struct{}

	// views is the stream the card's declared source resolves to.
	views *fleet.Feed[MetricsView]

	// retiring and start are start tokens, each takeable once.
	retiring fleet.Once
	start    fleet.Once
}

// queryRequest is one caller asking the aggregator for the rolled-up view,
// carrying the channel it wants to be answered on.
type queryRequest struct {
	// Reply is where the aggregator answers, exactly once.
	Reply chan QueryResult
}

// QueryResult is one answer to a query.
//
// Buckets is a copy: the aggregator is the only reader and writer of its own,
// and a caller holding a slice that aliased them would be a second reader of the
// one thing this engine promises has one.
type QueryResult struct {
	// Buckets is the histogram, in bound order, as a copy.
	Buckets []int

	// Observations is how many samples the buckets are made of.
	Observations int

	// Median is the p50, which is the one number this service surfaces.
	Median float64
}

// NewTelemetry builds a pipeline and starts nothing.
func NewTelemetry(config Config) (*Telemetry, error) {
	if validationError := config.Validate(); validationError != nil {
		return nil, validationError
	}
	return &Telemetry{
		config:     config,
		ingest:     make(chan sample, noticeDepth),
		queries:    make(chan queryRequest),
		alerts:     make(chan breach, noticeDepth),
		departures: make(chan int, maxCollectors),
		reports:    make(chan telemetryReport, noticeDepth),
		retire:     make(chan struct{}),
		views:      fleet.NewFeed[MetricsView](8),
		retiring:   fleet.NewOnce(),
		start:      fleet.NewOnce(),
	}, nil
}

// Config is the configuration this pipeline was built from.
func (telemetry *Telemetry) Config() Config { return telemetry.config }

// Run starts every goroutine and returns when the context ends and all of them
// have stopped. The only error it has is being called twice.
func (telemetry *Telemetry) Run(ctx context.Context) error {
	if !telemetry.start.Take() {
		return fleet.ErrAlreadyRunning
	}

	var crew fleet.Crew
	crew.Go(ctx, telemetry.views.Run)
	crew.Go(ctx, telemetry.observe)
	crew.Go(ctx, telemetry.aggregate)
	crew.Go(ctx, telemetry.alert)
	for index := range telemetry.config.Collectors {
		probe := &collector{
			id:         index,
			series:     seriesName(index),
			interval:   telemetry.config.ScrapeInterval,
			size:       telemetry.config.ReservoirSize,
			ingest:     telemetry.ingest,
			departures: telemetry.departures,
			retire:     telemetry.retire,
			jitter:     fleet.Jitter(telemetry.config.Seed, uint64(index)),
		}
		crew.Go(ctx, probe.run)
	}

	crew.Wait()
	return nil
}

// Watch is the aggregator stream the card's declared source resolves to.
func (telemetry *Telemetry) Watch(ctx context.Context) (<-chan MetricsView, error) {
	return telemetry.views.Subscribe(ctx)
}

// Query asks the aggregator for the rolled-up view, and waits for its answer.
//
// The reply channel is made here and travels with the request, so the answer
// reaches exactly the caller that asked and the histogram never leaves the
// goroutine that owns it.
func (telemetry *Telemetry) Query(ctx context.Context) (QueryResult, error) {
	request := queryRequest{Reply: make(chan QueryResult, 1)}
	select {
	case telemetry.queries <- request:
	case <-ctx.Done():
		return QueryResult{}, ctx.Err()
	}
	select {
	case answer := <-request.Reply:
		return answer, nil
	case <-ctx.Done():
		return QueryResult{}, ctx.Err()
	}
}

// Retire asks every collector to leave.
//
// What happens next is the demonstration: each collector sends exactly one
// departure notice, the aggregator counts them down, and when the count reaches
// zero it reports that ingest has stopped. "Have all the producers finished" is
// a message rather than a shared integer, so there is nothing to lock and
// nothing to get wrong about the last one.
//
// A second call is a no-op rather than a panic: the token can be taken once.
func (telemetry *Telemetry) Retire() {
	if telemetry.retiring.Take() {
		close(telemetry.retire)
	}
}

// aggregate is the aggregator: one goroutine owning every histogram bucket, the
// observation count and how many collectors are left.
//
// Nothing else in the process reads or writes any of them. A query is answered
// from a copy, which is the difference between handing somebody a number and
// handing them a reference to the thing you are still writing to.
func (telemetry *Telemetry) aggregate(ctx context.Context) {
	buckets := make([]int, len(histogramBounds)+1)
	observations := 0
	window := 0
	live := telemetry.config.Collectors

	flushes := time.NewTicker(telemetry.config.FlushInterval)
	defer flushes.Stop()

	tell := func(report telemetryReport) bool {
		select {
		case telemetry.reports <- report:
			return true
		case <-ctx.Done():
			return false
		}
	}

	for {
		select {
		case <-ctx.Done():
			return

		case incoming := <-telemetry.ingest:
			buckets = foldSample(buckets, incoming.Value)
			observations++
			window++
			if incoming.Value < telemetry.config.BreachThreshold {
				continue
			}
			select {
			case telemetry.alerts <- breach{Alert: telemetry.config.AlertName, Value: incoming.Value}:
			case <-ctx.Done():
				return
			}

		case request := <-telemetry.queries:
			request.Reply <- QueryResult{
				Buckets:      slices.Clone(buckets),
				Observations: observations,
				Median:       median(buckets),
			}

		case <-telemetry.departures:
			live--
			if live > 0 {
				continue
			}
			// Every producer has finished, and the count reaching zero is how
			// this goroutine found out. There is nothing left to ingest.
			if !tell(telemetryReport{Kind: reportFlush, CollectorsUp: 0, AggregatorUp: false}) {
				return
			}
			return

		case <-flushes.C:
			rate := int(float64(window) / telemetry.config.FlushInterval.Seconds())
			window = 0
			if !tell(telemetryReport{
				Kind: reportFlush, CollectorsUp: live, SamplesPerSecond: rate,
				Observations: observations, AggregatorUp: true}) {
				return
			}
		}
	}
}

// alert is the alerter: one goroutine owning the firing set and its debounce.
//
// A breach is a message rather than a flag somebody sets, so the alerter is the
// only thing that decides whether an alert is firing, and a silenced alert still
// transitions here — silencing is the card's own flag and this goroutine never
// hears about it.
func (telemetry *Telemetry) alert(ctx context.Context) {
	breaching := 0
	quiet := 0
	firing := ""

	windows := time.NewTicker(telemetry.config.FlushInterval)
	defer windows.Stop()

	tell := func(report telemetryReport) bool {
		select {
		case telemetry.reports <- report:
			return true
		case <-ctx.Done():
			return false
		}
	}

	for {
		select {
		case <-ctx.Done():
			return

		case breached := <-telemetry.alerts:
			breaching++
			quiet = 0
			if firing != "" || breaching < telemetry.config.DebounceWindows {
				continue
			}
			firing = breached.Alert
			if !tell(telemetryReport{Kind: reportAlert, Breaching: true, FiringAlert: firing}) {
				return
			}

		case <-windows.C:
			if breaching > 0 {
				breaching = 0
				quiet = 0
				continue
			}
			quiet++
			if firing == "" || quiet < telemetry.config.DebounceWindows {
				continue
			}
			firing = ""
			if !tell(telemetryReport{Kind: reportAlert, Breaching: false}) {
				return
			}
		}
	}
}

// observe is the published view: one goroutine owning it, and the only place the
// aggregator's half and the alerter's half are ever seen together.
func (telemetry *Telemetry) observe(ctx context.Context) {
	view := MetricsView{
		RetentionDays:    telemetry.config.RetentionDays,
		QueryWindowHours: telemetry.config.QueryWindowHours,
	}
	sequence := uint64(0)

	for {
		select {
		case <-ctx.Done():
			return
		case incoming := <-telemetry.reports:
			view = foldTelemetry(view, incoming)
			sequence++
			view.Sequence = sequence
			if !telemetry.views.Publish(ctx, view) {
				return
			}
		}
	}
}

// seriesName is a collector's neutral series name.
func seriesName(index int) string {
	return "series-" + string(rune('a'+index%26))
}
