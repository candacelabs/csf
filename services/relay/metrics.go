// Copyright 2026 Candace Labs

package relay

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/candacelabs/csf/ipc"
)

const (
	// MetricEnvelopesSent counts envelopes accepted into an inbox, by tier.
	MetricEnvelopesSent = "csf_relay_envelopes_sent_total"
	// MetricEnvelopesDelivered counts envelopes handed to an in-process
	// receiver or acknowledged by a host or network agent, by tier.
	MetricEnvelopesDelivered = "csf_relay_envelopes_delivered_total"
	// MetricTierLabel is the label both counters carry: in_process, host or
	// network, as ipc.Tier names them.
	MetricTierLabel = "tier"
)

// relayMetrics is nil when the binary registered none; every method is then a
// no-op.
type relayMetrics struct {
	sentTotal      *prometheus.CounterVec
	deliveredTotal *prometheus.CounterVec
}

func newRelayMetrics(registerer prometheus.Registerer) (*relayMetrics, error) {
	if registerer == nil {
		return nil, nil
	}
	metrics := &relayMetrics{
		sentTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: MetricEnvelopesSent,
			Help: "Agent envelopes accepted into an inbox, by the widest tier between sender and recipient. Host and network envelopes between agents that could share a runtime are avoidable crossings.",
		}, []string{MetricTierLabel}),
		deliveredTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: MetricEnvelopesDelivered,
			Help: "Agent envelopes handed to an in-process receiver or acknowledged by a host or network agent, by tier. Sent minus delivered is what is still queued.",
		}, []string{MetricTierLabel}),
	}
	for _, collector := range []*prometheus.CounterVec{metrics.sentTotal, metrics.deliveredTotal} {
		if err := registerer.Register(collector); err != nil {
			return nil, fmt.Errorf("relay: register metrics: %w", err)
		}
		// Every tier is exported from the start, so a dashboard reads zero
		// rather than a missing series for a tier nothing has crossed yet.
		for _, tier := range ipc.Tiers {
			collector.WithLabelValues(tier.String())
		}
	}
	return metrics, nil
}

func (metrics *relayMetrics) sent(tier ipc.Tier) {
	if metrics != nil {
		metrics.sentTotal.WithLabelValues(tier.String()).Inc()
	}
}

func (metrics *relayMetrics) delivered(tier ipc.Tier) {
	if metrics != nil {
		metrics.deliveredTotal.WithLabelValues(tier.String()).Inc()
	}
}
