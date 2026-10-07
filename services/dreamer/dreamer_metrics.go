// Copyright 2026 Candace Labs

package dreamer

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/candacelabs/csf/services/ouroboros"
)

// The families the dreamer exports, each a panel of dashboard.json.
const (
	MetricReady     = "csf_dreamer_ready_slices"
	MetricTarget    = "csf_dreamer_target_slices"
	MetricGenerated = "csf_dreamer_slices_generated"
	MetricFactor    = "csf_dreamer_struggle_factor_per_week"
	MetricRate      = "csf_dreamer_struggle_rate_per_1k_tool_calls"
	MetricExplore   = "csf_dreamer_explore_weight"

	labelSource    = "source"
	labelPartition = "partition"
	labelBound     = "bound"
	boundValue     = "value"
	boundLow       = "low"
	boundHigh      = "high"
)

var (
	readyDesc     = prometheus.NewDesc(MetricReady, "Slices in the dispatcher's ready frontier at the dreamer's latest pass.", nil, nil)
	targetDesc    = prometheus.NewDesc(MetricTarget, "The ready frontier the dispatcher's admission can use until the next pass, as the latest pass derived it.", nil, nil)
	generatedDesc = prometheus.NewDesc(MetricGenerated, "Slices the dreamer has added, by the source of their work.", []string{labelSource}, nil)
	factorDesc    = prometheus.NewDesc(MetricFactor, "Weekly compounding factor of the struggle rate with its 95% interval, on all runs, the in-sample runs the dreamer chooses work from, and the held-out runs it never reads.", []string{labelPartition, labelBound}, nil)
	rateDesc      = prometheus.NewDesc(MetricRate, "Struggle episodes per 1,000 tool calls in the latest complete week with the 95% Garwood interval, by partition.", []string{labelPartition, labelBound}, nil)
	exploreDesc   = prometheus.NewDesc(MetricExplore, "The strategy's weight on struggle classes no earlier work touched.", nil, nil)
)

// Collector exports the dreamer's latest snapshot as the families above.
type Collector struct{ dreamer *Dreamer }

var _ prometheus.Collector = (*Collector)(nil)

// Collector is the dreamer's exporter: register it on the binary's metrics
// registry.
func (dreamer *Dreamer) Collector() *Collector { return &Collector{dreamer: dreamer} }

// Describe sends every family's descriptor.
func (*Collector) Describe(descriptions chan<- *prometheus.Desc) {
	for _, description := range []*prometheus.Desc{readyDesc, targetDesc, generatedDesc, factorDesc, rateDesc, exploreDesc} {
		descriptions <- description
	}
}

// Collect sends the latest snapshot's samples.
func (exporter *Collector) Collect(metrics chan<- prometheus.Metric) {
	snapshot := exporter.dreamer.latest.Load()
	if snapshot == nil {
		return
	}
	metrics <- prometheus.MustNewConstMetric(exploreDesc, prometheus.GaugeValue, snapshot.Explore)
	for source, count := range snapshot.Generated {
		metrics <- prometheus.MustNewConstMetric(generatedDesc, prometheus.CounterValue, float64(count), string(source))
	}
	for _, record := range snapshot.Decisions {
		if record.Kind == RecordDecision && record.Decision.Outcome != OutcomePaused && record.Decision.Outcome != OutcomeDispatcherPaused {
			metrics <- prometheus.MustNewConstMetric(readyDesc, prometheus.GaugeValue, float64(record.Decision.Target.Ready))
			metrics <- prometheus.MustNewConstMetric(targetDesc, prometheus.GaugeValue, float64(record.Decision.Target.Target))
			break
		}
	}
	if snapshot.Strategy == nil {
		return
	}
	for _, partition := range []Partition{snapshot.Strategy.All, snapshot.Strategy.InSample, snapshot.Strategy.HeldOut} {
		bounds(metrics, factorDesc, partition.Name, partition.Factor.Factor, partition.Factor.Low, partition.Factor.High)
		if week, found := latestComplete(partition.Weeks, snapshot.Strategy.Week); found {
			bounds(metrics, rateDesc, partition.Name, week.PerK, week.Low, week.High)
		}
	}
}

// bounds emits a value and its interval, each only when measured.
func bounds(metrics chan<- prometheus.Metric, description *prometheus.Desc, partition string, value *float64, low *float64, high *float64) {
	for bound, measured := range map[string]*float64{boundValue: value, boundLow: low, boundHigh: high} {
		if measured != nil {
			metrics <- prometheus.MustNewConstMetric(description, prometheus.GaugeValue, *measured, partition, bound)
		}
	}
}

// latestComplete is the newest week before current with a rate.
func latestComplete(weeks []ouroboros.Rate, current string) (ouroboros.Rate, bool) {
	for index := len(weeks) - 1; index >= 0; index-- {
		if weeks[index].Day < current && weeks[index].PerK != nil {
			return weeks[index], true
		}
	}
	return ouroboros.Rate{}, false
}
