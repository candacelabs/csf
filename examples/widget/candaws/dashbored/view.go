package dashbored

// histogramBounds are the upper edges of the aggregator's buckets, in order.
//
// The last bucket is everything above the last bound, so a histogram of N bounds
// has N+1 buckets. Observations are in [0, 1), which is what a collector's own
// random stream produces.
var histogramBounds = []float64{0.1, 0.25, 0.5, 0.75, 0.9, 0.99}

// reportKind names which half of the view a report carries.
type reportKind uint8

const (
	// reportFlush is the aggregator's half: a rate, a collector count and
	// whether it is still ingesting.
	reportFlush reportKind = iota

	// reportAlert is the alerter's half: whether anything is firing and what.
	reportAlert
)

// telemetryReport is one goroutine telling the observer what it knows. Neither
// half can see the other's numbers, which is why the observer exists.
type telemetryReport struct {
	// Kind is which half this is.
	Kind reportKind

	// CollectorsUp, SamplesPerSecond, Observations and AggregatorUp are the
	// aggregator's half.
	CollectorsUp     int
	SamplesPerSecond int
	Observations     int
	AggregatorUp     bool

	// Breaching and FiringAlert are the alerter's half.
	Breaching   bool
	FiringAlert string
}

// MetricsView is what the pipeline tells anybody, and it is deliberately the
// shape the Dashbored card's `scrapeReport` event carries.
type MetricsView struct {
	// Sequence is this view's position in the stream, from 1.
	Sequence uint64

	// FiringAlert is the name of what is firing, or empty when nothing is. It
	// is the fleet's third text field, and it exists because the disjunction
	// behind a breach — p99 or error rate — lives in the engine, so the card
	// cannot say which fired without being told the name.
	FiringAlert string

	// CollectorsUp is how many collectors the aggregator has not had a
	// departure notice from.
	CollectorsUp int

	// SamplesPerSecond is the ingest rate over the last flush window.
	SamplesPerSecond int

	// Observations is how many samples the histogram is made of.
	Observations int

	// RetentionDays is how long the service says it keeps your data, and
	// QueryWindowHours how much of it you may ask about. They never change, and
	// the difference between them is the joke rendered as data.
	RetentionDays    int
	QueryWindowHours int

	// AggregatorUp is whether the aggregator is still ingesting. It goes false
	// exactly once, when the last collector's departure notice arrives.
	AggregatorUp bool

	// Breaching is whether the alerter has anything firing.
	Breaching bool
}

// foldTelemetry merges one half of the picture into the published view.
//
// It is a pure function, which is what lets the merge be specified without
// starting a goroutine: the aggregator cannot see whether an alert is firing and
// the alerter cannot see the ingest rate, and every rule about what the card
// shows is a rule about this function.
func foldTelemetry(view MetricsView, incoming telemetryReport) MetricsView {
	next := view
	switch incoming.Kind {
	case reportFlush:
		next.CollectorsUp = incoming.CollectorsUp
		next.SamplesPerSecond = incoming.SamplesPerSecond
		next.Observations = incoming.Observations
		next.AggregatorUp = incoming.AggregatorUp
	case reportAlert:
		next.Breaching = incoming.Breaching
		next.FiringAlert = incoming.FiringAlert
	}
	return next
}

// Quiet reports whether nothing is wrong, which is the only thing a metrics
// service ever wants to say. It is the `forbids`-only predicate the card
// declares, and this is the same statement on the engine's side.
func (view MetricsView) Quiet() bool { return !view.Breaching }

// foldSample puts one observation in its bucket.
//
// It is a pure function of the buckets and the value, and it returns the slice
// it was given rather than a copy: the caller is the aggregator goroutine, which
// owns it, and a copy per sample would be a copy per sample.
func foldSample(buckets []int, value float64) []int {
	for index, bound := range histogramBounds {
		if value < bound {
			buckets[index]++
			return buckets
		}
	}
	buckets[len(histogramBounds)]++
	return buckets
}

// median is the p50 of a histogram: the midpoint of the bucket the middle
// observation falls in.
//
// It is the one number this service surfaces, and it is approximate by
// construction — which is what a histogram is. An empty histogram has no median
// and reports zero rather than pretending.
func median(buckets []int) float64 {
	total := 0
	for _, count := range buckets {
		total += count
	}
	if total == 0 {
		return 0
	}

	seen := 0
	for index, count := range buckets {
		seen += count
		if seen*2 < total {
			continue
		}
		return bucketMidpoint(index)
	}
	return bucketMidpoint(len(buckets) - 1)
}

// bucketMidpoint is the middle of one bucket's range.
func bucketMidpoint(index int) float64 {
	lower := 0.0
	if index > 0 {
		lower = histogramBounds[index-1]
	}
	if index >= len(histogramBounds) {
		return lower
	}
	return (lower + histogramBounds[index]) / 2
}
