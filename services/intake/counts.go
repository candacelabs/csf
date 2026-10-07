// Copyright 2026 Candace Labs

package intake

import (
	"math"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	intakev1 "github.com/candacelabs/csf/proto/candace/intake/v1"
)

// The receiver's families; each is in services/views's catalog with a panel
// on the CSF dashboard, and the help text is the catalog's definition.
var (
	deliveriesDescription = prometheus.NewDesc("csf_github_deliveries_total",
		"GitHub webhook deliveries this host's receiver answered since it started, by outcome (accepted, duplicate, ignored, refused, failed) and, for a refused one, why.",
		[]string{"outcome", "refusal"}, nil)
	eventsDescription = prometheus.NewDesc("csf_github_events_total",
		"Typed GitHub events this host's receiver recorded on the GitHub event stream since it started, by kind.",
		[]string{"kind"}, nil)
	latencyDescription = prometheus.NewDesc("csf_github_event_latency_seconds",
		"Seconds from when GitHub says the latest event of each kind happened to when this host recorded it as a typed event.",
		[]string{"kind"}, nil)
)

// Counts is the receiver's counters. The key sets are fixed when it is built
// and only the values change, so the maps are read without a lock.
type Counts struct {
	deliveries map[deliveryKey]*atomic.Int64
	events     map[intakev1.EventKind]*atomic.Int64
	latency    map[intakev1.EventKind]*atomic.Uint64
}

var _ prometheus.Collector = (*Counts)(nil)

// NewDeliveryCounts builds the counters at zero: what a receiver starts
// from, and what the catalog spec describes.
func NewDeliveryCounts() *Counts {
	counts := &Counts{
		deliveries: map[deliveryKey]*atomic.Int64{},
		events:     map[intakev1.EventKind]*atomic.Int64{},
		latency:    map[intakev1.EventKind]*atomic.Uint64{},
	}
	for _, outcome := range []Outcome{OutcomeAccepted, OutcomeDuplicate, OutcomeIgnored, OutcomeFailed} {
		counts.deliveries[deliveryKey{outcome: outcome}] = &atomic.Int64{}
	}
	for _, refusal := range []Refusal{RefusedNoSecret, RefusedSignature, RefusedDelivery, RefusedTooLarge} {
		counts.deliveries[deliveryKey{outcome: OutcomeRefused, refusal: refusal}] = &atomic.Int64{}
	}
	for value := range intakev1.EventKind_name {
		kind := intakev1.EventKind(value)
		if kind == intakev1.EventKind_EVENT_KIND_UNSPECIFIED {
			continue
		}
		counts.events[kind] = &atomic.Int64{}
		counts.latency[kind] = &atomic.Uint64{}
	}
	return counts
}

func (counts *Counts) delivery(outcome Outcome, refusal Refusal) {
	if counter, known := counts.deliveries[deliveryKey{outcome: outcome, refusal: refusal}]; known {
		counter.Add(1)
	}
}

func (counts *Counts) event(event *intakev1.Event, now time.Time) {
	if counter, known := counts.events[event.GetKind()]; known {
		counter.Add(1)
		counts.latency[event.GetKind()].Store(math.Float64bits(Latency(event, now).Seconds()))
	}
}

// Deliveries is how many deliveries ended with outcome and refusal.
func (counts *Counts) Deliveries(outcome Outcome, refusal Refusal) int64 {
	if counter, known := counts.deliveries[deliveryKey{outcome: outcome, refusal: refusal}]; known {
		return counter.Load()
	}
	return 0
}

// Events is how many events of kind were recorded.
func (counts *Counts) Events(kind intakev1.EventKind) int64 {
	if counter, known := counts.events[kind]; known {
		return counter.Load()
	}
	return 0
}

// Describe sends the receiver's descriptors.
func (counts *Counts) Describe(descriptions chan<- *prometheus.Desc) {
	descriptions <- deliveriesDescription
	descriptions <- eventsDescription
	descriptions <- latencyDescription
}

// Collect sends every series; a kind never seen reports no latency.
func (counts *Counts) Collect(metrics chan<- prometheus.Metric) {
	for key, counter := range counts.deliveries {
		metrics <- prometheus.MustNewConstMetric(deliveriesDescription, prometheus.CounterValue, float64(counter.Load()), string(key.outcome), string(key.refusal))
	}
	for kind, counter := range counts.events {
		seen := counter.Load()
		metrics <- prometheus.MustNewConstMetric(eventsDescription, prometheus.CounterValue, float64(seen), kindWord(kind))
		if seen > 0 {
			metrics <- prometheus.MustNewConstMetric(latencyDescription, prometheus.GaugeValue, math.Float64frombits(counts.latency[kind].Load()), kindWord(kind))
		}
	}
}
