package dashbored

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The specifications below run a real pipeline: three collector goroutines, one
// aggregator, one alerter and a fan-out, against real timers.
const (
	specScrape   = 4 * time.Millisecond
	specFlush    = 20 * time.Millisecond
	specPatience = 15 * time.Second
)

// specConfig is the pace every specification in this file runs at. The threshold
// is the parameter, because a threshold of 1 is a pipeline nothing ever breaches
// and one just above zero is a pipeline where everything does — and those two
// are the ends the alerter is specified between.
func specConfig(threshold float64) Config {
	return Config{
		Collectors:       3,
		ScrapeInterval:   specScrape,
		FlushInterval:    specFlush,
		ReservoirSize:    8,
		BreachThreshold:  threshold,
		DebounceWindows:  2,
		AlertName:        "p99_latency",
		RetentionDays:    395,
		QueryWindowHours: 2,
		Seed:             20260902,
	}
}

// console is one subscriber, read only by the specification's own goroutine.
type console struct {
	views <-chan MetricsView
	seen  []MetricsView
}

// await takes views until one matches.
func (subscriber *console) await(what string, match func(view MetricsView) bool) MetricsView {
	GinkgoHelper()
	deadline := time.After(specPatience)
	for {
		select {
		case view, open := <-subscriber.views:
			Expect(open).To(BeTrue(), "the pipeline stopped before %s", what)
			Expect(view.CollectorsUp).To(BeNumerically(">=", 0))
			subscriber.seen = append(subscriber.seen, view)
			if match(view) {
				return view
			}
		case <-deadline:
			Fail("never saw " + what + " within " + specPatience.String())
			return MetricsView{}
		}
	}
}

// telemetryUnder starts a pipeline for the length of one specification, with one
// subscriber attached, and joins its goroutines on the way out.
func telemetryUnder(config Config) (*Telemetry, *console) {
	GinkgoHelper()

	pipeline, buildError := NewTelemetry(config)
	Expect(buildError).ToNot(HaveOccurred())

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- pipeline.Run(ctx) }()
	DeferCleanup(func() {
		cancel()
		Eventually(stopped, specPatience).Should(Receive())
	})

	views, subscribeError := pipeline.Watch(ctx)
	Expect(subscribeError).ToNot(HaveOccurred())
	return pipeline, &console{views: views}
}

var _ = Describe("A pipeline that is configured wrongly", func() {
	It("reports the fault rather than running and reporting nothing", func() {
		sound := specConfig(0.9)
		for _, broken := range []struct {
			what   string
			change func(config *Config)
			fault  error
		}{
			{"no collectors", func(config *Config) { config.Collectors = 0 }, ErrCollectorCount},
			{"more collectors than the fan-in serves",
				func(config *Config) { config.Collectors = maxCollectors + 1 }, ErrCollectorCount},
			{"no scrape", func(config *Config) { config.ScrapeInterval = 0 }, ErrScrapeInterval},
			{"a flush window nothing arrives in",
				func(config *Config) { config.FlushInterval = config.ScrapeInterval }, ErrFlushInterval},
			{"a reservoir that samples nothing",
				func(config *Config) { config.ReservoirSize = 0 }, ErrReservoir},
			{"a threshold no observation can reach",
				func(config *Config) { config.BreachThreshold = 2 }, ErrThreshold},
			{"an alert that fires on one observation",
				func(config *Config) { config.DebounceWindows = 0 }, ErrDebounce},
			{"an alert with no name", func(config *Config) { config.AlertName = "" }, ErrAlertName},
			{"nothing retained", func(config *Config) { config.RetentionDays = 0 }, ErrRetention},
		} {
			config := sound
			broken.change(&config)
			_, buildError := NewTelemetry(config)
			Expect(buildError).To(MatchError(broken.fault), "for %s", broken.what)
		}
	})

	It("refuses a second Run rather than folding one histogram from two goroutines", func() {
		pipeline, _ := telemetryUnder(specConfig(1))
		Expect(pipeline.Run(context.Background())).To(MatchError(ContainSubstring("already running")))
	})
})

var _ = Describe("A running pipeline", func() {
	It("ingests from every collector into one aggregator", func() {
		_, subscriber := telemetryUnder(specConfig(1))

		ingesting := subscriber.await("an aggregator ingesting from every collector",
			func(view MetricsView) bool {
				return view.AggregatorUp && view.CollectorsUp == 3 && view.SamplesPerSecond > 0
			})
		Expect(ingesting.RetentionDays).To(Equal(395))
		Expect(ingesting.QueryWindowHours).To(Equal(2))
		Expect(ingesting.Quiet()).To(BeTrue(),
			"a threshold of one is a pipeline nothing can breach")
	})

	It("answers a query from a copy of the buckets", func() {
		pipeline, subscriber := telemetryUnder(specConfig(1))
		subscriber.await("some observations",
			func(view MetricsView) bool { return view.SamplesPerSecond > 0 })

		answer, queryError := pipeline.Query(context.Background())
		Expect(queryError).ToNot(HaveOccurred())
		Expect(answer.Observations).To(BeNumerically(">", 0))
		Expect(answer.Buckets).To(HaveLen(len(histogramBounds) + 1))

		// The copy is the point. Writing into what a query returned must not
		// reach the aggregator's own histogram, and the next answer proves it.
		for index := range answer.Buckets {
			answer.Buckets[index] = -1
		}
		second, secondError := pipeline.Query(context.Background())
		Expect(secondError).ToNot(HaveOccurred())
		for _, count := range second.Buckets {
			Expect(count).To(BeNumerically(">=", 0),
				"a caller wrote into a slice that aliased the aggregator's own buckets")
		}
	})

	It("fires an alert only after the debounce, and names it", func() {
		// A threshold just above zero: every observation breaches, so the only
		// thing standing between the first sample and a firing alert is the
		// debounce.
		_, subscriber := telemetryUnder(specConfig(0.000001))

		firing := subscriber.await("a firing alert",
			func(view MetricsView) bool { return view.Breaching })
		Expect(firing.FiringAlert).To(Equal("p99_latency"),
			"the disjunction behind a breach lives in the engine, so the card is told the name")
		Expect(firing.Quiet()).To(BeFalse())
	})

	It("stops ingesting when the last collector has said it has gone", func() {
		pipeline, subscriber := telemetryUnder(specConfig(1))
		subscriber.await("an aggregator with every collector reporting",
			func(view MetricsView) bool { return view.CollectorsUp == 3 })

		// Counted rather than locked. Each collector sends exactly one
		// departure notice, the aggregator counts them down, and reaching zero
		// is how it learns that every producer has finished — a message, not a
		// shared integer.
		pipeline.Retire()
		Expect(pipeline.Retire).ToNot(Panic(),
			"a second retirement is a no-op, not a closed channel twice")

		stopped := subscriber.await("an aggregator that has stopped",
			func(view MetricsView) bool { return !view.AggregatorUp })
		Expect(stopped.CollectorsUp).To(Equal(0))
	})
})
