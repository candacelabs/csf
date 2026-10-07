// Copyright 2026 Candace Labs

package githubtools

import "github.com/prometheus/client_golang/prometheus"

// CallCollector exports the GitHub measurement, read afresh from the GitHub
// log at each scrape.
type CallCollector struct {
	state         string
	calls         *prometheus.Desc
	rateLimit     *prometheus.Desc
	rateRemaining *prometheus.Desc
}

// NewCallCollector builds the collector over the GitHub log under state.
func NewCallCollector(state string) *CallCollector {
	return &CallCollector{
		state:         state,
		calls:         prometheus.NewDesc(callsMetric, "GitHub calls made through CSF's GitHub tools, by operation, actor (operator or virtual_session) and outcome.", []string{labelOperation, labelActor, labelOutcome}, nil),
		rateLimit:     prometheus.NewDesc(rateLimitMetric, "GitHub's core rate limit per hour, as the latest call that reported it saw.", nil, nil),
		rateRemaining: prometheus.NewDesc(rateRemainingMetric, "Requests left in GitHub's current core rate-limit window, as the latest call that reported it saw.", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (collector *CallCollector) Describe(output chan<- *prometheus.Desc) {
	output <- collector.calls
	output <- collector.rateLimit
	output <- collector.rateRemaining
}

// Collect implements prometheus.Collector.
func (collector *CallCollector) Collect(output chan<- prometheus.Metric) {
	counts, err := CountCalls(collector.state)
	if err != nil {
		output <- prometheus.NewInvalidMetric(collector.calls, err)
		return
	}
	for key, count := range counts.Calls {
		output <- prometheus.MustNewConstMetric(collector.calls, prometheus.CounterValue, float64(count), key.Operation, string(key.Actor), key.Outcome)
	}
	if counts.RateLimit > 0 {
		output <- prometheus.MustNewConstMetric(collector.rateLimit, prometheus.GaugeValue, float64(counts.RateLimit))
		output <- prometheus.MustNewConstMetric(collector.rateRemaining, prometheus.GaugeValue, float64(counts.RateRemaining))
	}
}
